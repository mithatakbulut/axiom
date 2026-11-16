// Package verification defines candidate-bound verification planning and results.
package verification

import (
	"encoding/hex"
	"time"
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
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Outcome     Outcome   `json:"outcome"`
	Required    bool      `json:"required"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	ExitCode    *int      `json:"exit_code,omitempty"`
	Output      string    `json:"output,omitempty"`
	EvidenceIDs []string  `json:"evidence_ids,omitempty"`
	Diagnostic  string    `json:"diagnostic,omitempty"`
	CriteriaIDs []string  `json:"criteria_ids,omitempty"`
}

// Run binds check observations to one immutable source revision.
type Run struct {
	ID            string        `json:"id"`
	SchemaVersion int           `json:"schema_version"`
	WorkflowID    string        `json:"workflow_id"`
	TaskID        string        `json:"task_id,omitempty"`
	CandidateSHA  string        `json:"candidate_sha"`
	Profile       Profile       `json:"profile,omitempty"`
	State         State         `json:"state,omitempty"`
	StartedAt     time.Time     `json:"started_at"`
	FinishedAt    time.Time     `json:"finished_at,omitempty"`
	Checks        []CheckResult `json:"checks"`
	Criteria      []Criterion   `json:"criteria,omitempty"`
	Review        *ReviewResult `json:"review,omitempty"`
}

func (r Run) Validate() error {
	if r.ID == "" || r.WorkflowID == "" {
		return ErrMissingIdentity
	}
	if r.SchemaVersion != 1 {
		return ErrUnsupportedSchema
	}
	if !validRevision(r.CandidateSHA) {
		return ErrInvalidCandidate
	}
	seen := map[string]bool{}
	for _, c := range r.Checks {
		if c.ID == "" || c.Name == "" || !validOutcome(c.Outcome) || seen[c.ID] {
			return ErrInvalidCheck
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
