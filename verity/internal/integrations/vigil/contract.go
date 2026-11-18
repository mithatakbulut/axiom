// Package vigil implements Verity's optional, read-only bridge to Vigil's
// documented stdio MCP surface.
package vigil

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"verity/internal/candidate"
)

const SchemaVersion = 2

var ErrIncompatible = errors.New("Vigil MCP interface is incompatible")

type ExecutionStatus string

const (
	ExecutionCompleted ExecutionStatus = "execution_completed"
	ExecutionFailed    ExecutionStatus = "execution_failed"
	ExecutionBlocked   ExecutionStatus = "execution_blocked"
	CandidateReady     ExecutionStatus = "candidate_ready"
	VerificationPassed ExecutionStatus = "verification_passed"
	WorkflowAccepted   ExecutionStatus = "workflow_accepted"
)

// Link is the versioned bridge record. Verity's final two lifecycle states
// are intentionally unrepresentable as Vigil outcomes.
type Link struct {
	SchemaVersion          int                `json:"schema_version"`
	WorkflowID             string             `json:"workflow_id"`
	TaskID                 string             `json:"task_id"`
	AttemptID              string             `json:"attempt_id"`
	CorrelationID          string             `json:"correlation_id"`
	Candidate              candidate.Identity `json:"candidate,omitempty"`
	BaseSHA                string             `json:"base_sha"`
	CandidateSHA           string             `json:"candidate_sha,omitempty"`
	RuntimeID              string             `json:"runtime_id,omitempty"`
	SessionID              string             `json:"session_id,omitempty"`
	TurnID                 string             `json:"turn_id,omitempty"`
	Status                 ExecutionStatus    `json:"status"`
	Signals                []string           `json:"signals,omitempty"`
	EventRefs              []string           `json:"event_refs,omitempty"`
	AdvisorAction          string             `json:"advisor_action,omitempty"`
	BudgetWarnings         []string           `json:"budget_warnings,omitempty"`
	ObservedModelRequests  int                `json:"observed_model_requests,omitempty"`
	ObservedRuntimeSeconds int64              `json:"observed_runtime_seconds,omitempty"`
	VigilVersion           string             `json:"vigil_version,omitempty"`
	Summary                string             `json:"summary,omitempty"`
	CapturedAt             time.Time          `json:"captured_at"`
}

func (l Link) Validate() error {
	if (l.SchemaVersion != 1 && l.SchemaVersion != SchemaVersion) || l.WorkflowID == "" || l.TaskID == "" || l.AttemptID == "" || l.CorrelationID == "" || l.SessionID == "" || !validSHA(l.BaseSHA) || (l.CandidateSHA != "" && !validSHA(l.CandidateSHA)) || l.CapturedAt.IsZero() {
		return errors.New("invalid Vigil bridge record")
	}
	if l.SchemaVersion == SchemaVersion && (l.Candidate.IsZero() || l.Candidate.Validate() != nil || l.Candidate.ID != l.CandidateSHA || (l.Candidate.Kind == candidate.GitIndex && l.Candidate.BaseRevision != l.BaseSHA)) {
		return errors.New("version 2 Vigil link requires a matching typed candidate")
	}
	switch l.Status {
	case ExecutionCompleted, ExecutionFailed, ExecutionBlocked, CandidateReady:
	default:
		return errors.New("invalid Vigil execution status")
	}
	return nil
}

// Snapshot is a bounded normalized subset of Vigil's published MCP results.
// Raw event payloads and full session telemetry are never copied.
type Snapshot struct {
	VigilVersion           string    `json:"vigil_version"`
	SessionID              string    `json:"session_id"`
	Signals                []string  `json:"signals,omitempty"`
	AdvisorAction          string    `json:"advisor_action,omitempty"`
	BudgetWarnings         []string  `json:"budget_warnings,omitempty"`
	ObservedModelRequests  int       `json:"observed_model_requests"`
	ObservedRuntimeSeconds int64     `json:"observed_runtime_seconds"`
	CapturedAt             time.Time `json:"captured_at"`
}

func (s Snapshot) Attach(l Link) (Link, error) {
	if s.SessionID == "" || l.SessionID != "" && l.SessionID != s.SessionID || s.VigilVersion == "" || s.CapturedAt.IsZero() {
		return Link{}, errors.New("Vigil snapshot does not match bridge link")
	}
	l.SessionID = s.SessionID
	l.VigilVersion = s.VigilVersion
	l.Signals = unique(append(l.Signals, s.Signals...))
	l.BudgetWarnings = unique(append(l.BudgetWarnings, s.BudgetWarnings...))
	l.AdvisorAction = s.AdvisorAction
	l.ObservedModelRequests = s.ObservedModelRequests
	l.ObservedRuntimeSeconds = s.ObservedRuntimeSeconds
	l.CapturedAt = s.CapturedAt
	if err := l.Validate(); err != nil {
		return Link{}, err
	}
	return l, nil
}

func normalizeProgress(raw json.RawMessage) []string {
	var p struct {
		Decision string   `json:"decision"`
		Signals  []string `json:"signals"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	out := append([]string(nil), p.Signals...)
	if p.Decision == "escalate_candidate" {
		out = append(out, "progress_escalation_candidate")
	}
	return unique(out)
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func validSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
