// Package execution defines provider-neutral bounded task execution contracts.
package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"verity/internal/candidate"
)

var ErrInvalid = errors.New("invalid execution record")

const CurrentAttemptSchemaVersion = 2

type Outcome string

const (
	Succeeded Outcome = "SUCCEEDED"
	Failed    Outcome = "FAILED"
	TimedOut  Outcome = "TIMED_OUT"
	Cancelled Outcome = "CANCELLED"
	Blocked   Outcome = "BLOCKED"
	Unknown   Outcome = "UNKNOWN"
)

type Request struct {
	ID, WorkflowID, TaskID, Workspace string
	BaseCandidate                     candidate.Identity `json:"base_candidate,omitempty"`
	Attempt                           int
	BaseSHA                           string
	Timeout                           time.Duration
	Instruction                       string
}

type Result struct {
	Outcome               Outcome
	CandidateSHA          string
	Candidate             candidate.Identity `json:"candidate,omitempty"`
	SessionID             string             `json:"session_id,omitempty"`
	Summary               string
	StartedAt, FinishedAt time.Time
}

func (r Request) Validate() error {
	if r.ID == "" || r.WorkflowID == "" || r.TaskID == "" || r.Workspace == "" || r.Attempt < 1 || !validSHA(r.BaseSHA) || r.Timeout <= 0 {
		return ErrInvalid
	}
	if !r.BaseCandidate.IsZero() {
		if r.BaseCandidate.Validate() != nil || r.BaseCandidate.ID != r.BaseSHA {
			return fmt.Errorf("%w: base candidate identity does not match base SHA", ErrInvalid)
		}
	}
	return nil
}

func (r Result) Validate() error {
	switch r.Outcome {
	case Succeeded, Failed, TimedOut, Cancelled, Blocked, Unknown:
	default:
		return fmt.Errorf("%w: unknown outcome", ErrInvalid)
	}
	if r.StartedAt.IsZero() || r.FinishedAt.Before(r.StartedAt) {
		return ErrInvalid
	}
	if r.CandidateSHA != "" && !validSHA(r.CandidateSHA) {
		return ErrInvalid
	}
	if !r.Candidate.IsZero() {
		if r.Candidate.Validate() != nil || r.Candidate.ID != r.CandidateSHA {
			return fmt.Errorf("%w: typed candidate identity does not match candidate SHA", ErrInvalid)
		}
	}
	if r.Outcome == Succeeded && r.CandidateSHA == "" {
		return fmt.Errorf("%w: success needs candidate SHA", ErrInvalid)
	}
	return nil
}

type Capabilities struct {
	Provider          string
	Manual            bool
	Cancellation      bool
	WorkspaceRequired bool
}

// AgentRuntime must honor context cancellation and return an observed outcome.
type AgentRuntime interface {
	Capabilities(context.Context) (Capabilities, error)
	Execute(context.Context, Request) (Result, error)
}

// Attempt is the immutable record of one bounded provider call.
type Attempt struct {
	Request  Request
	Result   Result
	Provider string
}

type AttemptState string

const (
	AttemptRunning   AttemptState = "RUNNING"
	AttemptUnknown   AttemptState = "UNKNOWN"
	AttemptFinished  AttemptState = "FINISHED"
	AttemptAbandoned AttemptState = "ABANDONED"
)

// DurableAttempt is the persisted invocation intent. State and Result are
// projections of append-only store events and are not provider assertions.
type DurableAttempt struct {
	SchemaVersion int          `json:"schema_version"`
	Request       Request      `json:"request"`
	Provider      string       `json:"provider"`
	CreatedAt     time.Time    `json:"created_at"`
	State         AttemptState `json:"state"`
	Result        *Result      `json:"result,omitempty"`
}

type Reconciliation struct {
	Outcome      Outcome            `json:"outcome"`
	CandidateSHA string             `json:"candidate_sha,omitempty"`
	Candidate    candidate.Identity `json:"candidate,omitempty"`
	Actor        string             `json:"actor"`
	Reason       string             `json:"reason"`
	EvidenceRef  string             `json:"evidence_ref,omitempty"`
	At           time.Time          `json:"at"`
}

func (r Reconciliation) Validate() error {
	if r.Actor == "" || r.Reason == "" || r.At.IsZero() {
		return ErrInvalid
	}
	switch r.Outcome {
	case Succeeded, Failed, TimedOut, Cancelled, Blocked:
	default:
		return ErrInvalid
	}
	if r.CandidateSHA != "" && !validSHA(r.CandidateSHA) {
		return ErrInvalid
	}
	if !r.Candidate.IsZero() && (r.Candidate.Validate() != nil || r.Candidate.ID != r.CandidateSHA) {
		return fmt.Errorf("%w: typed reconciliation candidate does not match candidate SHA", ErrInvalid)
	}
	if r.Outcome == Succeeded && r.CandidateSHA == "" {
		return ErrInvalid
	}
	return nil
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
