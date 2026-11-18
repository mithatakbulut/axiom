// Package verification defines candidate-bound verification planning and results.
package verification

import (
	"encoding/hex"
	"strings"
	"time"

	"verity/internal/candidate"
)

type Outcome string

const (
	Passed   Outcome = "PASS"
	Failed   Outcome = "FAIL"
	Blocked  Outcome = "BLOCKED"
	Skipped  Outcome = "SKIPPED"
	TimedOut Outcome = "TIMED_OUT"
)

// State is the aggregate decision for a verification run.
type State string

type CandidateKind = candidate.Kind

const CurrentRunSchemaVersion = 2

const (
	CandidateGitCommit     CandidateKind = candidate.GitCommit
	CandidateGitIndex      CandidateKind = candidate.GitIndex
	CandidateContentSource CandidateKind = candidate.ContentSource
)

const (
	StatePass       State = "PASS"
	StateFail       State = "FAIL"
	StateBlocked    State = "BLOCKED"
	StateNeedsHuman State = "NEEDS_HUMAN"
	StateStale      State = "STALE"
	StatePartial    State = "PARTIAL"
	StateCancelled  State = "CANCELLED"
)

// CheckResult is an observation from a check runner, never an approval decision.
type CheckResult struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	Outcome            Outcome             `json:"outcome"`
	Required           bool                `json:"required"`
	StartedAt          time.Time           `json:"started_at"`
	FinishedAt         time.Time           `json:"finished_at"`
	ExitCode           *int                `json:"exit_code,omitempty"`
	Output             string              `json:"output,omitempty"`
	EvidenceIDs        []string            `json:"evidence_ids,omitempty"`
	Diagnostic         string              `json:"diagnostic,omitempty"`
	CriteriaIDs        []string            `json:"criteria_ids,omitempty"`
	CredentialBindings []PlannedCredential `json:"credential_bindings,omitempty"`
	NetworkAllowlist   []string            `json:"network_allowlist,omitempty"`
}

// Run binds check observations to one immutable source revision.
type Run struct {
	ID                  string             `json:"id"`
	SchemaVersion       int                `json:"schema_version"`
	WorkflowID          string             `json:"workflow_id"`
	TaskID              string             `json:"task_id,omitempty"`
	Candidate           candidate.Identity `json:"candidate,omitempty"`
	CandidateKind       CandidateKind      `json:"candidate_kind,omitempty"`
	CandidateSHA        string             `json:"candidate_sha"`
	CandidateBase       string             `json:"candidate_base,omitempty"`
	CandidateRepository string             `json:"candidate_repository,omitempty"`
	TrustLevel          string             `json:"trust_level,omitempty"`
	ManifestSHA256      string             `json:"manifest_sha256"`
	IsolationPolicy     IsolationPolicy    `json:"isolation_policy,omitempty"`
	ContainerImage      string             `json:"container_image,omitempty"`
	NetworkAllowlist    []string           `json:"network_allowlist,omitempty"`
	Profile             Profile            `json:"profile,omitempty"`
	State               State              `json:"state,omitempty"`
	StartedAt           time.Time          `json:"started_at"`
	FinishedAt          time.Time          `json:"finished_at,omitempty"`
	Checks              []CheckResult      `json:"checks"`
	Criteria            []Criterion        `json:"criteria,omitempty"`
	Review              *ReviewResult      `json:"review,omitempty"`
}

func (r Run) Validate() error {
	if r.ID == "" || r.WorkflowID == "" {
		return ErrMissingIdentity
	}
	if r.SchemaVersion != 1 && r.SchemaVersion != CurrentRunSchemaVersion {
		return ErrUnsupportedSchema
	}
	if !validRevision(r.CandidateSHA) {
		return ErrInvalidCandidate
	}
	if r.CandidateKind != "" && r.CandidateKind != CandidateGitCommit && r.CandidateKind != CandidateGitIndex && r.CandidateKind != CandidateContentSource {
		return ErrInvalidCandidateKind
	}
	if !r.Candidate.IsZero() {
		if err := r.Candidate.Validate(); err != nil || r.Candidate.Kind != r.CandidateKind || r.Candidate.ID != r.CandidateSHA || r.Candidate.Repository != r.CandidateRepository || r.Candidate.BaseRevision != r.CandidateBase {
			return ErrInvalidCandidateKind
		}
	}
	if r.SchemaVersion >= CurrentRunSchemaVersion && r.Candidate.IsZero() {
		return ErrInvalidCandidateKind
	}
	if r.CandidateKind != "" && r.CandidateRepository == "" {
		return ErrInvalidCandidateKind
	}
	if r.CandidateKind != "" && r.IsolationPolicy != IsolationLocalProcess && r.IsolationPolicy != IsolationRootlessContainer {
		return ErrInvalidCandidateKind
	}
	if r.IsolationPolicy == IsolationRootlessContainer && !validContainerImage(r.ContainerImage) {
		return ErrInvalidCandidateKind
	}
	if r.TrustLevel != "" && r.TrustLevel != TrustTrusted && r.TrustLevel != TrustUntrusted {
		return ErrInvalidCandidateKind
	}
	if r.TrustLevel == TrustUntrusted && r.IsolationPolicy != IsolationRootlessContainer {
		return ErrInvalidCandidateKind
	}
	if len(r.NetworkAllowlist) > 0 && (r.TrustLevel != TrustUntrusted || r.IsolationPolicy != IsolationRootlessContainer) {
		return ErrInvalidManifest
	}
	seenDestinations := map[string]bool{}
	for _, destination := range r.NetworkAllowlist {
		if !validNetworkDestination(destination) || seenDestinations[destination] {
			return ErrInvalidManifest
		}
		seenDestinations[destination] = true
	}
	if r.CandidateKind == CandidateGitIndex && !validRevision(r.CandidateBase) {
		return ErrInvalidCandidateKind
	}
	if r.ManifestSHA256 != "" {
		if len(r.ManifestSHA256) != 64 {
			return ErrInvalidManifest
		}
		if _, err := hex.DecodeString(r.ManifestSHA256); err != nil {
			return ErrInvalidManifest
		}
	}
	seen := map[string]bool{}
	runDestinations := map[string]bool{}
	for _, destination := range r.NetworkAllowlist {
		runDestinations[destination] = true
	}
	for _, c := range r.Checks {
		if c.ID == "" || c.Name == "" || !validOutcome(c.Outcome) || seen[c.ID] {
			return ErrInvalidCheck
		}
		if len(c.CredentialBindings) > 0 && (r.TrustLevel != TrustUntrusted || r.IsolationPolicy != IsolationRootlessContainer) {
			return ErrInvalidManifest
		}
		if len(c.CredentialBindings) > 0 && len(c.NetworkAllowlist) != 1 {
			return ErrInvalidManifest
		}
		checkDestinations := map[string]bool{}
		for _, destination := range c.NetworkAllowlist {
			if !validNetworkDestination(destination) || !runDestinations[destination] || checkDestinations[destination] {
				return ErrInvalidManifest
			}
			checkDestinations[destination] = true
		}
		credentialNames := map[string]bool{}
		for _, binding := range c.CredentialBindings {
			if !validEnvironmentName(binding.Name) || !strings.HasPrefix(binding.Name, "VERITY_TEST_") || !validEnvironmentName(binding.SourceEnv) || !strings.HasPrefix(binding.SourceEnv, "VERITY_TEST_") || !validEnvironmentName(binding.ExpiresAtEnv) || !strings.HasPrefix(binding.ExpiresAtEnv, "VERITY_TEST_") || binding.Name == binding.SourceEnv || binding.Name == binding.ExpiresAtEnv || binding.SourceEnv == binding.ExpiresAtEnv || binding.TTL < time.Second || binding.TTL > 600*time.Second || credentialNames[binding.Name] {
				return ErrInvalidManifest
			}
			credentialNames[binding.Name] = true
		}
		seen[c.ID] = true
	}
	return nil
}

func validRevision(v string) bool {
	if len(v) != 40 && len(v) != 64 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}
func validOutcome(o Outcome) bool {
	return o == Passed || o == Failed || o == Blocked || o == Skipped || o == TimedOut
}
