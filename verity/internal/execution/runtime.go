package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type FakeRuntime struct {
	Provider string
	Outcome  Outcome
}

func (f FakeRuntime) Capabilities(context.Context) (Capabilities, error) {
	name := f.Provider
	if name == "" {
		name = "fake"
	}
	return Capabilities{Provider: name, Cancellation: true, WorkspaceRequired: true}, nil
}

// Execute is deterministic: it reports the base revision as the candidate and never edits files.
func (f FakeRuntime) Execute(ctx context.Context, req Request) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	started := time.Now().UTC()
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	outcome := f.Outcome
	if outcome == "" {
		outcome = Succeeded
	}
	result := Result{Outcome: outcome, StartedAt: started, FinishedAt: time.Now().UTC(), Summary: "deterministic fake runtime; no repository changes"}
	if outcome == Succeeded {
		result.CandidateSHA = req.BaseSHA
		result.Candidate = req.BaseCandidate
	}
	return result, nil
}

type ManualRuntime struct{ Pending func(Request) error }

func (m ManualRuntime) Capabilities(context.Context) (Capabilities, error) {
	return Capabilities{Provider: "manual", Manual: true, Cancellation: true, WorkspaceRequired: true}, nil
}

// Execute creates a handoff request; a human must return a separately validated result.
func (m ManualRuntime) Execute(ctx context.Context, req Request) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	if m.Pending == nil {
		return Result{}, fmt.Errorf("manual runtime requires a handoff sink")
	}
	if err := m.Pending(req); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	now := time.Now().UTC()
	return Result{Outcome: Blocked, StartedAt: now, FinishedAt: now, Summary: "manual handoff created; candidate result is pending"}, nil
}

// Tracker retains attempts in process memory; persistence is intentionally left to the execution store milestone.
type Tracker struct {
	mu       sync.Mutex
	attempts []Attempt
}

// DurableAttemptStore must commit invocation intent before Execute is called.
type DurableAttemptStore interface {
	CreateExecutionAttempt(context.Context, DurableAttempt) error
	RecordExecutionResult(context.Context, string, Result) error
	MarkExecutionUnknown(context.Context, string, string, time.Time) error
}

// RunDurable persists intent before runtime invocation and records observed
// results. A returned provider error is UNKNOWN because the external effect
// may have occurred despite the missing result.
func RunDurable(ctx context.Context, provider AgentRuntime, req Request, store DurableAttemptStore) (Result, error) {
	if store == nil {
		return Result{}, fmt.Errorf("nil durable attempt store")
	}
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	if req.BaseCandidate.IsZero() {
		return Result{}, fmt.Errorf("%w: durable execution requires a typed base candidate", ErrInvalid)
	}
	if provider == nil {
		return Result{}, fmt.Errorf("nil runtime")
	}
	caps, err := provider.Capabilities(ctx)
	if err != nil {
		return Result{}, err
	}
	if caps.Provider == "" {
		return Result{}, fmt.Errorf("runtime returned empty provider name")
	}
	created := time.Now().UTC()
	if err = store.CreateExecutionAttempt(ctx, DurableAttempt{SchemaVersion: CurrentAttemptSchemaVersion, Request: req, Provider: caps.Provider, CreatedAt: created, State: AttemptRunning}); err != nil {
		return Result{}, err
	}
	result, err := Run(ctx, provider, req, nil)
	if err != nil {
		markErr := store.MarkExecutionUnknown(ctx, req.ID, "runtime returned an error without an authoritative result", time.Now().UTC())
		return Result{}, errors.Join(err, markErr)
	}
	if err = store.RecordExecutionResult(ctx, req.ID, result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (t *Tracker) Record(a Attempt) error {
	if a.Provider == "" {
		return ErrInvalid
	}
	if err := a.Request.Validate(); err != nil {
		return err
	}
	if err := a.Result.Validate(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.attempts = append(t.attempts, a)
	return nil
}
func (t *Tracker) List() []Attempt {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Attempt(nil), t.attempts...)
}

// Run bounds execution by the request timeout and records the result when the provider returns one.
func Run(ctx context.Context, provider AgentRuntime, req Request, tracker *Tracker) (Result, error) {
	if err := req.Validate(); err != nil {
		return Result{}, err
	}
	if provider == nil {
		return Result{}, fmt.Errorf("nil runtime")
	}
	caps, err := provider.Capabilities(ctx)
	if err != nil {
		return Result{}, err
	}
	if caps.Provider == "" {
		return Result{}, fmt.Errorf("runtime returned empty provider name")
	}
	execCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()
	started := time.Now().UTC()
	result, runErr := provider.Execute(execCtx, req)
	if runErr != nil {
		if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(execCtx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			finished := time.Now().UTC()
			result = Result{Outcome: TimedOut, StartedAt: started, FinishedAt: finished, Summary: "runtime exceeded execution timeout"}
		} else if errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			finished := time.Now().UTC()
			result = Result{Outcome: Cancelled, StartedAt: started, FinishedAt: finished, Summary: "execution cancelled"}
		} else {
			return Result{}, runErr
		}
		if tracker != nil {
			if err := tracker.Record(Attempt{Request: req, Result: result, Provider: caps.Provider}); err != nil {
				return Result{}, err
			}
		}
		return result, nil
	}
	if err := result.Validate(); err != nil {
		return Result{}, err
	}
	if !req.BaseCandidate.IsZero() && result.Outcome == Succeeded && result.Candidate.IsZero() {
		return Result{}, fmt.Errorf("%w: typed execution result omitted candidate identity", ErrInvalid)
	}
	if tracker != nil {
		if err := tracker.Record(Attempt{Request: req, Result: result, Provider: caps.Provider}); err != nil {
			return Result{}, err
		}
	}
	return result, nil
}
