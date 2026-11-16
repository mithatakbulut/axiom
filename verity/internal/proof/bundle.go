package proof

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"verity/internal/domain"
	vigilbridge "verity/internal/integrations/vigil"
	"verity/internal/verification"
)

type AuditEvent struct {
	ID           string            `json:"id"`
	WorkflowID   string            `json:"workflow_id"`
	Sequence     int64             `json:"sequence"`
	Type         string            `json:"type"`
	CandidateSHA string            `json:"candidate_sha"`
	Actor        string            `json:"actor"`
	At           time.Time         `json:"at"`
	Details      map[string]string `json:"details,omitempty"`
}

func (e AuditEvent) Validate() error {
	if e.ID == "" || e.WorkflowID == "" || e.Sequence < 1 || e.Type == "" || e.Actor == "" || e.At.IsZero() || (e.CandidateSHA != "" && !validSHA(e.CandidateSHA, 40, 64)) {
		return errors.New("invalid proof audit event")
	}
	return nil
}

type Bundle struct {
	SchemaVersion int                 `json:"schema_version"`
	WorkflowID    string              `json:"workflow_id"`
	CandidateSHA  string              `json:"candidate_sha"`
	Contract      json.RawMessage     `json:"contract,omitempty"`
	Verification  verification.Run    `json:"verification"`
	Evidence      []Evidence          `json:"evidence"`
	Artifacts     []ArtifactReference `json:"artifacts"`
	Audit         []AuditEvent        `json:"audit"`
	VigilLinks    []vigilbridge.Link  `json:"vigil_links,omitempty"`
}
type ArtifactReference struct {
	EvidenceID string `json:"evidence_id"`
	Digest     string `json:"sha256"`
	Size       int64  `json:"size"`
}

func (b Bundle) Validate() error {
	if b.SchemaVersion != 1 || b.WorkflowID == "" || !validSHA(b.CandidateSHA, 40, 64) || b.Verification.WorkflowID != b.WorkflowID || b.Verification.CandidateSHA != b.CandidateSHA {
		return errors.New("proof bundle identity mismatch")
	}
	if err := b.Verification.Validate(); err != nil {
		return err
	}
	seenAttempts := map[string]bool{}
	for _, link := range b.VigilLinks {
		if err := link.Validate(); err != nil {
			return err
		}
		if link.CandidateSHA == "" || link.VigilVersion == "" || seenAttempts[link.AttemptID] {
			return errors.New("Vigil reference is incomplete or duplicated")
		}
		seenAttempts[link.AttemptID] = true
		if link.WorkflowID != b.WorkflowID || link.CandidateSHA != b.CandidateSHA {
			return errors.New("Vigil reference is bound to another workflow or candidate")
		}
	}
	seen := map[string]Evidence{}
	for _, e := range b.Evidence {
		if err := e.Validate(); err != nil {
			return err
		}
		if e.WorkflowID != b.WorkflowID || e.CandidateSHA != b.CandidateSHA {
			return errors.New("evidence is bound to another workflow or candidate")
		}
		if _, ok := seen[e.ID]; ok {
			return errors.New("duplicate evidence id")
		}
		seen[e.ID] = e
	}
	for _, c := range b.Verification.Checks {
		if c.Required && c.Outcome == verification.Passed && len(c.EvidenceIDs) == 0 {
			return errors.New("required passing check is missing evidence")
		}
		for _, id := range c.EvidenceIDs {
			if _, ok := seen[id]; !ok {
				return errors.New("verification references missing evidence")
			}
		}
	}
	artifactIDs := make(map[string]bool, len(b.Artifacts))
	for _, a := range b.Artifacts {
		e, ok := seen[a.EvidenceID]
		if !ok || artifactIDs[a.EvidenceID] || e.ContentSHA256 != a.Digest || e.Size != a.Size {
			return errors.New("artifact reference does not match evidence")
		}
		artifactIDs[a.EvidenceID] = true
	}
	if len(artifactIDs) != len(seen) {
		return errors.New("evidence is missing an artifact reference")
	}
	return nil
}
func (b Bundle) Marshal() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	sort.Slice(b.Evidence, func(i, j int) bool { return b.Evidence[i].ID < b.Evidence[j].ID })
	sort.Slice(b.Artifacts, func(i, j int) bool { return b.Artifacts[i].EvidenceID < b.Artifacts[j].EvidenceID })
	sort.Slice(b.Audit, func(i, j int) bool { return b.Audit[i].Sequence < b.Audit[j].Sequence })
	sort.Slice(b.VigilLinks, func(i, j int) bool { return b.VigilLinks[i].AttemptID < b.VigilLinks[j].AttemptID })
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	err := enc.Encode(b)
	return out.Bytes(), err
}
func (b Bundle) VerifyArtifacts(ctx context.Context, s ArtifactStore) error {
	if err := b.Validate(); err != nil {
		return err
	}
	for _, a := range b.Artifacts {
		data, err := s.Get(ctx, a.Digest)
		if err != nil {
			return err
		}
		if int64(len(data)) != a.Size || sha256Hex(data) != a.Digest {
			return ErrDigestMismatch
		}
	}
	return nil
}

// Capture stores content before its provenance record; callers may then attach
// the returned evidence ID to a verification observation.
func Capture(ctx context.Context, store ArtifactStore, id, workflow, candidate, kind, source string, at time.Time, data []byte) (Evidence, error) {
	e, err := NewEvidence(id, workflow, candidate, kind, source, at, data)
	if err != nil {
		return Evidence{}, err
	}
	digest, err := store.Put(ctx, data)
	if err != nil {
		return Evidence{}, err
	}
	if digest != e.ContentSHA256 {
		return Evidence{}, ErrDigestMismatch
	}
	return e, nil
}

type EvidenceRepository interface {
	SaveEvidence(context.Context, Evidence) error
}

// AttachCheckEvidence stores each check's captured output and binds its record
// to the verification observation before the run is persisted.
func AttachCheckEvidence(ctx context.Context, run verification.Run, artifacts ArtifactStore, records EvidenceRepository, now time.Time) (verification.Run, error) {
	for i := range run.Checks {
		c := &run.Checks[i]
		if !c.Required {
			continue
		}
		id, err := domain.NewID("evt")
		if err != nil {
			return verification.Run{}, err
		}
		e, err := Capture(ctx, artifacts, string(id), run.WorkflowID, run.CandidateSHA, "check-output", "verification:"+c.ID, now, []byte(c.Output))
		if err != nil {
			return verification.Run{}, err
		}
		if err = records.SaveEvidence(ctx, e); err != nil {
			return verification.Run{}, err
		}
		c.EvidenceIDs = append(c.EvidenceIDs, e.ID)
	}
	return run, nil
}
