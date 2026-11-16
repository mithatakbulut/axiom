package verification

import (
	"encoding/hex"
	"testing"
)

func TestRunRequiresImmutableCandidateAndKnownOutcomes(t *testing.T) {
	sha := hex.EncodeToString(make([]byte, 20))
	run := Run{
		ID: "run-1", SchemaVersion: 1, WorkflowID: "wf-1", CandidateSHA: sha,
		Checks: []CheckResult{{ID: "check-1", Name: "unit", Outcome: Passed}},
	}
	if err := run.Validate(); err != nil {
		t.Fatalf("valid run rejected: %v", err)
	}
	run.CandidateSHA = "main"
	if err := run.Validate(); err != ErrInvalidCandidate {
		t.Fatalf("mutable candidate error = %v, want %v", err, ErrInvalidCandidate)
	}
}
