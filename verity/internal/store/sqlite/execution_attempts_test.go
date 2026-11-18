package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"verity/internal/candidate"
	"verity/internal/execution"
)

type resultWriteFailureStore struct{ *Store }

func (s resultWriteFailureStore) RecordExecutionResult(context.Context, string, execution.Result) error {
	return errors.New("simulated process loss before result persistence")
}

type resultAfterSideEffectRuntime struct{ calls int }

func (r *resultAfterSideEffectRuntime) Capabilities(ctx context.Context) (execution.Capabilities, error) {
	return execution.FakeRuntime{}.Capabilities(ctx)
}

func (r *resultAfterSideEffectRuntime) Execute(ctx context.Context, req execution.Request) (execution.Result, error) {
	r.calls++ // Represents a provider-side effect that may have completed.
	return execution.FakeRuntime{}.Execute(ctx, req)
}

func TestAttemptScopeAndV6Migration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`CREATE TABLE execution_attempts(id TEXT PRIMARY KEY,workflow_id TEXT NOT NULL,task_id TEXT NOT NULL,attempt INTEGER NOT NULL,provider TEXT NOT NULL,body TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(task_id,attempt));
CREATE TABLE execution_attempt_events(id INTEGER PRIMARY KEY AUTOINCREMENT,attempt_id TEXT NOT NULL REFERENCES execution_attempts(id),kind TEXT NOT NULL,state TEXT NOT NULL,payload TEXT NOT NULL,actor TEXT NOT NULL,reason TEXT NOT NULL,created_at TEXT NOT NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	prior := execution.DurableAttempt{Request: execution.Request{ID: "old", WorkflowID: "legacy", TaskID: "implement", Workspace: "/w", Attempt: 1, BaseSHA: "0123456789abcdef0123456789abcdef01234567", Timeout: time.Minute}, Provider: "fake", CreatedAt: time.Now().UTC(), State: execution.AttemptRunning}
	body, err := json.Marshal(prior)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = old.Exec(`INSERT INTO execution_attempts VALUES(?,?,?,?,?,?,?); INSERT INTO execution_attempt_events(attempt_id,kind,state,payload,actor,reason,created_at) VALUES('old','STARTED','RUNNING','','runtime','intent','2026-01-01')`, "old", "legacy", "implement", 1, "fake", string(body), "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	old.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.ExecutionAttempt(ctx, "old"); err != nil || got.Request.WorkflowID != "legacy" || got.State != execution.AttemptRunning {
		t.Fatalf("migration lost history: %+v %v", got, err)
	}
	req := execution.Request{ID: "one", WorkflowID: "wf1", TaskID: "implement", Workspace: "/w", Attempt: 1, BaseSHA: "0123456789abcdef0123456789abcdef01234567", BaseCandidate: testCommitCandidate("0123456789abcdef0123456789abcdef01234567"), Timeout: time.Minute}
	if err = s.CreateExecutionAttempt(ctx, execution.DurableAttempt{Request: req, Provider: "fake"}); err != nil {
		t.Fatal(err)
	}
	next := req
	next.ID = "two"
	next.Attempt = 2
	if err = s.CreateExecutionAttempt(ctx, execution.DurableAttempt{Request: next, Provider: "fake"}); err == nil {
		t.Fatal("running attempt allowed retry")
	}
	next.WorkflowID = "wf2"
	next.Attempt = 1
	if err = s.CreateExecutionAttempt(ctx, execution.DurableAttempt{Request: next, Provider: "fake"}); err != nil {
		t.Fatalf("independent workflow: %v", err)
	}
	if _, err = s.RecoverRunningAttempts(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateExecutionAttempt(ctx, execution.DurableAttempt{Request: execution.Request{ID: "three", WorkflowID: "wf1", TaskID: "implement", Workspace: "/w", Attempt: 2, BaseSHA: req.BaseSHA, BaseCandidate: req.BaseCandidate, Timeout: time.Minute}, Provider: "fake"}); err == nil {
		t.Fatal("unknown attempt allowed retry")
	}
}

func TestExecutionAttemptRecoveryAndReconciliation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "attempts.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	req := execution.Request{ID: "exec-1", WorkflowID: "wf-1", TaskID: "task-1", Workspace: "/workspace", Attempt: 1, BaseSHA: "0123456789abcdef0123456789abcdef01234567", BaseCandidate: testCommitCandidate("0123456789abcdef0123456789abcdef01234567"), Timeout: time.Minute}
	if err = s.CreateExecutionAttempt(ctx, execution.DurableAttempt{Request: req, Provider: "manual", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ids, err := s.RecoverRunningAttempts(ctx, now.Add(time.Second))
	if err != nil || len(ids) != 1 || ids[0] != req.ID {
		t.Fatalf("recovery %v %v", ids, err)
	}
	ids, err = s.RecoverRunningAttempts(ctx, now.Add(2*time.Second))
	if err != nil || len(ids) != 0 {
		t.Fatalf("repeat recovery %v %v", ids, err)
	}
	a, err := s.ExecutionAttempt(ctx, req.ID)
	if err != nil || a.State != execution.AttemptUnknown {
		t.Fatalf("attempt %+v %v", a, err)
	}
	if err = s.RecordExecutionResult(ctx, req.ID, execution.Result{Outcome: execution.Failed, StartedAt: now, FinishedAt: now.Add(time.Second)}); err == nil {
		t.Fatal("late result accepted after recovery")
	}
	if err = s.CreateExecutionAttempt(ctx, execution.DurableAttempt{Request: req, Provider: "manual", CreatedAt: now}); err == nil {
		t.Fatal("reused task attempt number accepted")
	}
	reconSHA := "1123456789abcdef0123456789abcdef01234567"
	recon := execution.Reconciliation{Outcome: execution.Succeeded, CandidateSHA: reconSHA, Candidate: testCommitCandidate(reconSHA), Actor: "operator", Reason: "confirmed from runtime output", EvidenceRef: "ticket:123", At: now.Add(3 * time.Second)}
	if err = s.ReconcileExecutionAttempt(ctx, req.ID, recon); err != nil {
		t.Fatal(err)
	}
	a, err = s.ExecutionAttempt(ctx, req.ID)
	if err != nil || a.State != execution.AttemptFinished || a.Result == nil || a.Result.CandidateSHA != recon.CandidateSHA {
		t.Fatalf("reconciled %+v %v", a, err)
	}
	if err = s.ReconcileExecutionAttempt(ctx, req.ID, recon); err != nil {
		t.Fatalf("identical reconciliation: %v", err)
	}
	conflicting := recon
	conflicting.Reason = "different decision"
	if err = s.ReconcileExecutionAttempt(ctx, req.ID, conflicting); err == nil {
		t.Fatal("conflicting reconciliation accepted")
	}
	if _, err = s.db.ExecContext(ctx, "UPDATE execution_attempt_events SET reason='mutated'"); err == nil {
		t.Fatal("event update allowed")
	}
}

func TestExecutionResultIdempotentAndConflictRejected(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	identity := candidate.Identity{SchemaVersion: 1, Kind: candidate.GitCommit, ID: "0123456789abcdef0123456789abcdef01234567", Repository: "file:///repo", CapturedAt: now, Provenance: "git:HEAD"}
	req := execution.Request{ID: "exec", WorkflowID: "wf", TaskID: "task", Workspace: "/w", BaseCandidate: identity, Attempt: 1, BaseSHA: identity.ID, Timeout: time.Minute}
	if err = s.CreateExecutionAttempt(ctx, execution.DurableAttempt{Request: req, Provider: "fake", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	res := execution.Result{Outcome: execution.Succeeded, CandidateSHA: req.BaseSHA, Candidate: identity, StartedAt: now, FinishedAt: now.Add(time.Second)}
	if err = s.RecordExecutionResult(ctx, req.ID, res); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordExecutionResult(ctx, req.ID, res); err != nil {
		t.Fatalf("identical result: %v", err)
	}
	stored, err := s.ExecutionAttempt(ctx, req.ID)
	if err != nil || stored.Request.BaseCandidate != identity || stored.Result == nil || stored.Result.Candidate != identity {
		t.Fatalf("typed candidate did not survive durable persistence: %+v err=%v", stored, err)
	}
	res.Summary = "conflict"
	if err = s.RecordExecutionResult(ctx, req.ID, res); err == nil {
		t.Fatal("conflicting result accepted")
	}
}

func TestRunDurablePersistsIntentBeforeResult(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	req := execution.Request{ID: "durable", WorkflowID: "wf", TaskID: "task", Workspace: "/workspace", Attempt: 1, BaseSHA: "0123456789abcdef0123456789abcdef01234567", BaseCandidate: testCommitCandidate("0123456789abcdef0123456789abcdef01234567"), Timeout: time.Second}
	result, err := execution.RunDurable(ctx, execution.FakeRuntime{}, req, s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ExecutionAttempt(ctx, req.ID)
	if err != nil || got.State != execution.AttemptFinished || got.Result == nil || got.Result.CandidateSHA != result.CandidateSHA {
		t.Fatalf("durable execution %+v %v", got, err)
	}
}

func TestCrashAfterProviderReturnsBeforeResultPersistenceRecoversUnknown(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "attempts.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	req := execution.Request{
		ID: "crash-after-provider", WorkflowID: "wf-crash", TaskID: "task", Workspace: "/workspace",
		Attempt: 1, BaseSHA: sha, BaseCandidate: testCommitCandidate(sha), Timeout: time.Second,
	}
	runtime := &resultAfterSideEffectRuntime{}
	if _, err = execution.RunDurable(ctx, runtime, req, resultWriteFailureStore{Store: store}); err == nil {
		t.Fatal("expected injected result persistence failure")
	}
	if runtime.calls != 1 {
		t.Fatalf("provider calls = %d, want exactly one", runtime.calls)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ids, err := store.RecoverRunningAttempts(ctx, time.Now().UTC())
	if err != nil || len(ids) != 1 || ids[0] != req.ID {
		t.Fatalf("recovered attempts = %v, err=%v", ids, err)
	}
	got, err := store.ExecutionAttempt(ctx, req.ID)
	if err != nil || got.State != execution.AttemptUnknown || got.Result != nil {
		t.Fatalf("post-crash attempt = %+v, err=%v; want UNKNOWN without a result", got, err)
	}
	if err = store.CreateExecutionAttempt(ctx, execution.DurableAttempt{
		Request: execution.Request{ID: "unsafe-retry", WorkflowID: req.WorkflowID, TaskID: req.TaskID, Workspace: req.Workspace,
			Attempt: 2, BaseSHA: sha, BaseCandidate: req.BaseCandidate, Timeout: req.Timeout},
		Provider: "fake", CreatedAt: time.Now().UTC(), State: execution.AttemptRunning,
	}); err == nil {
		t.Fatal("uncertain provider execution allowed automatic retry")
	}
	if runtime.calls != 1 {
		t.Fatalf("provider was reinvoked after recovery; calls=%d", runtime.calls)
	}
}

func testCommitCandidate(id string) candidate.Identity {
	return candidate.Identity{SchemaVersion: 1, Kind: candidate.GitCommit, ID: id, Repository: "file:///repo", CapturedAt: time.Now().UTC(), Provenance: "git:HEAD"}
}
