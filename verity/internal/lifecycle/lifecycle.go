// Package lifecycle connects candidate execution, independent verification,
// preview checks, proof capture, and explicit release gates.
package lifecycle

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"verity/internal/domain"
	"verity/internal/proof"
	"verity/internal/verification"
)

var ErrGate = errors.New("lifecycle release gate rejected progression")

type Candidate struct {
	SHA        string
	Kind       verification.CandidateKind
	Base       string
	Repository string
	Output     string
}
type Implementer interface {
	Implement(context.Context, string, int) (Candidate, error)
}
type Reviewer interface {
	Review(context.Context, Candidate) (approved bool, findings string, err error)
}

// HumanReview records a separate human decision on the candidate and review findings.
type HumanReview interface {
	Decide(context.Context, Candidate, string) (approved bool, actor string, err error)
}
type Preview struct {
	ID        string
	URL       string
	SourceSHA string
}
type PreviewDeployer interface {
	Deploy(context.Context, Candidate) (Preview, error)
	Remove(context.Context, Preview) error
}
type PreviewSmoke interface {
	Verify(context.Context, Preview) ([]byte, error)
}
type PostDeployVerifier interface {
	Verify(context.Context, string) error
}
type Approval interface {
	Approve(context.Context, string, string) (bool, error)
}
type EvidenceRepository interface {
	proof.EvidenceRepository
	Evidence(context.Context, string, string) ([]proof.Evidence, error)
}

type Config struct {
	WorkflowID, TaskID, RunID string
	Repository                string
	Profile                   verification.Profile
	MaxCorrections            int
	ProductionEnabled         bool // must be explicitly set to enable production deploy
}
type Result struct {
	Candidate          Candidate
	Verification       verification.Run
	Bundle             proof.Bundle
	Preview            Preview
	ProductionDeployed bool
	CorrectionAttempts int
}
type Service struct {
	Implementer  Implementer
	Verification verification.Engine
	Reviewer     Reviewer
	HumanReview  HumanReview
	Preview      PreviewDeployer
	Smoke        PreviewSmoke
	PostDeploy   PostDeployVerifier
	Approval     Approval
	Artifacts    proof.ArtifactStore
	Evidence     EvidenceRepository
	Now          func() time.Time
}

// Run drives one feature from requirement to a candidate-bound proof bundle.
// Corrections are bounded, and preview deployment is always associated with
// the exact verified candidate SHA.
func (s Service) Run(ctx context.Context, cfg Config, requirement string, manifest verification.Manifest, planApproved, releaseApproved bool) (Result, error) {
	var out Result
	if s.Implementer == nil || s.Reviewer == nil || s.Preview == nil || s.Smoke == nil || s.Artifacts == nil || s.Evidence == nil || s.Approval == nil {
		return out, errors.New("lifecycle dependencies are required")
	}
	if cfg.WorkflowID == "" || cfg.RunID == "" || cfg.Repository == "" || requirement == "" || cfg.MaxCorrections < 0 || cfg.MaxCorrections > 10 {
		return out, errors.New("invalid lifecycle configuration")
	}
	if !planApproved {
		return out, verification.ErrNotApproved
	}
	if manifest.Review && s.HumanReview == nil {
		return out, errors.New("human review decision is required")
	}
	now := s.Now
	if now == nil {
		now = time.Now
	}
	for attempt := 1; attempt <= cfg.MaxCorrections+1; attempt++ {
		candidate, err := s.Implementer.Implement(ctx, requirement, attempt)
		if err != nil {
			return out, err
		}
		if len(candidate.SHA) != 40 && len(candidate.SHA) != 64 {
			return out, verification.ErrInvalidCandidate
		}
		if _, err = hex.DecodeString(candidate.SHA); err != nil {
			return out, verification.ErrInvalidCandidate
		}
		if candidate.Kind == "" {
			return out, verification.ErrInvalidCandidateKind
		}
		engine := s.Verification
		engine.CandidateKind = candidate.Kind
		engine.CandidateBase = candidate.Base
		engine.CandidateRepository = candidate.Repository
		if engine.CandidateRepository == "" {
			engine.CandidateRepository = cfg.Repository
		}
		run, err := engine.Verify(ctx, cfg.Repository, cfg.RunID, cfg.WorkflowID, cfg.TaskID, manifest, cfg.Profile, candidate.SHA, true)
		if err != nil {
			return out, err
		}
		out.Candidate, out.Verification, out.CorrectionAttempts = candidate, run, attempt-1
		if run.State == verification.StatePass || run.State == verification.StateNeedsHuman {
			approved, findings, reviewErr := s.Reviewer.Review(ctx, candidate)
			if reviewErr != nil {
				return out, reviewErr
			}
			run.Review = &verification.ReviewResult{Completed: true, Findings: []string{findings}}
			if !approved {
				run.State = verification.StateFail
				out.Verification = run
				if attempt <= cfg.MaxCorrections {
					continue
				}
				return out, ErrGate
			}
			if manifest.Review {
				decision, actor, decisionErr := s.HumanReview.Decide(ctx, candidate, findings)
				if decisionErr != nil {
					return out, decisionErr
				}
				if strings.TrimSpace(actor) == "" {
					return out, errors.New("human review actor is required")
				}
				run.Review.HumanApproved, run.Review.HumanActor = &decision, actor
			}
			run.State = verification.Evaluate(run, manifest.Review)
			out.Verification = run
			break
		}
		if run.State == verification.StateStale {
			return out, ErrGate
		}
		if attempt > cfg.MaxCorrections {
			return out, ErrGate
		}
	}
	if out.Verification.State != verification.StatePass {
		return out, ErrGate
	}
	preview, err := s.Preview.Deploy(ctx, out.Candidate)
	if err != nil {
		return out, err
	}
	if preview.SourceSHA != out.Candidate.SHA || preview.ID == "" || preview.URL == "" {
		_ = s.Preview.Remove(ctx, preview)
		return out, errors.New("preview source SHA does not match verified candidate")
	}
	out.Preview = preview
	smoke, err := s.Smoke.Verify(ctx, preview)
	if err != nil {
		_ = s.Preview.Remove(ctx, preview)
		return out, err
	}
	if len(smoke) == 0 {
		_ = s.Preview.Remove(ctx, preview)
		return out, errors.New("preview smoke verification produced no evidence")
	}
	if s.PostDeploy != nil {
		if err = s.PostDeploy.Verify(ctx, preview.SourceSHA); err != nil {
			_ = s.Preview.Remove(ctx, preview)
			return out, err
		}
	}
	out.Verification, err = proof.AttachCheckEvidence(ctx, out.Verification, s.Artifacts, s.Evidence, now().UTC())
	if err != nil {
		return out, err
	}
	id, err := domain.NewID("evt")
	if err != nil {
		return out, err
	}
	e, err := proof.Capture(ctx, s.Artifacts, string(id), cfg.WorkflowID, preview.SourceSHA, "preview-smoke", "preview:"+preview.ID, now().UTC(), smoke)
	if err != nil {
		return out, err
	}
	if err = s.Evidence.SaveEvidence(ctx, e); err != nil {
		return out, err
	}
	// The preview observation is part of the candidate-bound verification record.
	out.Verification.Checks = append(out.Verification.Checks, verification.CheckResult{ID: "preview-smoke", Name: "preview smoke", Outcome: verification.Passed, Required: true, StartedAt: now().UTC(), FinishedAt: now().UTC(), EvidenceIDs: []string{e.ID}})
	if !releaseApproved {
		return out, ErrGate
	}
	approved, err := s.Approval.Approve(ctx, cfg.WorkflowID, out.Candidate.SHA)
	if err != nil {
		return out, err
	}
	if !approved {
		return out, ErrGate
	}
	// Production is deliberately absent from the adapter surface. Enabling this
	// flag only records approval; a future provider phase must add deployment.
	if cfg.ProductionEnabled {
		return out, errors.New("production deployment adapter is not implemented")
	}
	evidence, err := s.Evidence.Evidence(ctx, cfg.WorkflowID, out.Candidate.SHA)
	if err != nil {
		return out, err
	}
	refs := make([]proof.ArtifactReference, 0, len(evidence))
	for _, item := range evidence {
		refs = append(refs, proof.ArtifactReference{EvidenceID: item.ID, Digest: item.ContentSHA256, Size: item.Size})
	}
	out.Bundle = proof.Bundle{SchemaVersion: proof.CurrentBundleSchemaVersion, WorkflowID: cfg.WorkflowID, CandidateKind: out.Verification.CandidateKind, Candidate: out.Verification.Candidate, CandidateSHA: out.Candidate.SHA, CandidateBase: out.Verification.CandidateBase, CandidateRepository: out.Verification.CandidateRepository, TrustLevel: out.Verification.TrustLevel, ManifestSHA256: out.Verification.ManifestSHA256, IsolationPolicy: out.Verification.IsolationPolicy, ContainerImage: out.Verification.ContainerImage, Verification: out.Verification, Evidence: evidence, Artifacts: refs}
	if err = out.Bundle.Validate(); err != nil {
		return out, err
	}
	return out, nil
}
