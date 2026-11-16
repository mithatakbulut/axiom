package execution

import (
	"context"
	"testing"
	"time"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func request() Request {
	return Request{ID: "exec-1", WorkflowID: "wf-1", TaskID: "task-1", Workspace: "/tmp/ws", Attempt: 1, BaseSHA: sha, Timeout: time.Second}
}

func TestFakeRuntimeRecordsCandidateButNotAcceptance(t *testing.T) {
	tracker := &Tracker{}
	r, err := Run(context.Background(), FakeRuntime{}, request(), tracker)
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != Succeeded || r.CandidateSHA != sha {
		t.Fatalf("unexpected result: %+v", r)
	}
	if got := tracker.List(); len(got) != 1 || got[0].Provider != "fake" {
		t.Fatalf("attempt not recorded: %+v", got)
	}
}

func TestManualRuntimeCreatesHandoffAndRemainsBlocked(t *testing.T) {
	called := false
	r, err := Run(context.Background(), ManualRuntime{Pending: func(Request) error { called = true; return nil }}, request(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !called || r.Outcome != Blocked || r.CandidateSHA != "" {
		t.Fatalf("manual result incorrectly accepted: called=%v result=%+v", called, r)
	}
}

func TestRunHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tracker := &Tracker{}
	r, err := Run(ctx, FakeRuntime{}, request(), tracker)
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != Cancelled || len(tracker.List()) != 1 {
		t.Fatalf("cancellation was not recorded: result=%+v attempts=%+v", r, tracker.List())
	}
}

type waitingRuntime struct{}

func (waitingRuntime) Capabilities(context.Context) (Capabilities, error) {
	return Capabilities{Provider: "waiter"}, nil
}
func (waitingRuntime) Execute(ctx context.Context, _ Request) (Result, error) {
	<-ctx.Done()
	return Result{}, ctx.Err()
}

func TestRunConvertsTimeoutToTimedOutAttempt(t *testing.T) {
	req := request()
	req.Timeout = 10 * time.Millisecond
	tracker := &Tracker{}
	r, err := Run(context.Background(), waitingRuntime{}, req, tracker)
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != TimedOut || len(tracker.List()) != 1 {
		t.Fatalf("timeout was not recorded: result=%+v attempts=%+v", r, tracker.List())
	}
}
