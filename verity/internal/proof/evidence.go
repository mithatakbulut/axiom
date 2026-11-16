// Package proof records delivery evidence with content integrity and provenance.
package proof

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type Evidence struct {
	ID            string    `json:"id"`
	SchemaVersion int       `json:"schema_version"`
	WorkflowID    string    `json:"workflow_id"`
	CandidateSHA  string    `json:"candidate_sha"`
	Kind          string    `json:"kind"`
	Source        string    `json:"source"`
	CapturedAt    time.Time `json:"captured_at"`
	ContentSHA256 string    `json:"content_sha256"`
	Size          int64     `json:"size"`
}

var ErrInvalidEvidence = errors.New("evidence is missing required provenance or integrity data")

func NewEvidence(id, workflowID, candidateSHA, kind, source string, capturedAt time.Time, content []byte) (Evidence, error) {
	e := Evidence{ID: id, SchemaVersion: 1, WorkflowID: workflowID, CandidateSHA: strings.ToLower(candidateSHA), Kind: kind, Source: source, CapturedAt: capturedAt.UTC(), ContentSHA256: sha256Hex(content), Size: int64(len(content))}
	if err := e.Validate(); err != nil {
		return Evidence{}, err
	}
	return e, nil
}
func (e Evidence) Validate() error {
	if e.ID == "" || e.WorkflowID == "" || !validSHA(e.CandidateSHA, 40, 64) || e.Kind == "" || e.Source == "" || e.CapturedAt.IsZero() || !validSHA(e.ContentSHA256, 64) || e.SchemaVersion != 1 || e.Size < 0 {
		return ErrInvalidEvidence
	}
	return nil
}
func sha256Hex(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func validSHA(v string, lengths ...int) bool {
	for _, n := range lengths {
		if len(v) == n {
			if _, err := hex.DecodeString(v); err == nil {
				return true
			}
		}
	}
	return false
}
