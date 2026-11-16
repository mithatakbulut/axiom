package control

import (
	"context"
	"errors"
	"testing"
	"time"
)

type succeedAll struct{}

type cancelOnApproval struct{ cancel context.CancelFunc }

func (a cancelOnApproval) Run(context.Context, Task) (bool, error) { return true, nil }
func (a cancelOnApproval) Approve(context.Context, Task) (bool, error) {
	a.cancel()
	return true, nil
}

func TestFakeRunnerCancellationDuringApproval(t *testing.T) {
	r, err := NewRun("run", fixture(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = RunWithActivities(ctx, &r, cancelOnApproval{cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if r.State != Waiting {
		t.Fatalf("run state = %s, want waiting for approval", r.State)
	}
}

func (succeedAll) Run(context.Context, Task) (bool, error)     { return true, nil }
func (succeedAll) Approve(context.Context, Task) (bool, error) { return true, nil }

func TestFakeRunnerCompletesWorkflow(t *testing.T) {
	r, err := NewRun("run", fixture(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = RunWithActivities(context.Background(), &r, succeedAll{}); err != nil {
		t.Fatal(err)
	}
	if r.State != Succeeded {
		t.Fatalf("run state=%s", r.State)
	}
}

func fixture() Definition {
	return Definition{SchemaVersion: 1, ID: "feature-v1", Name: "feature", Version: 1, MaxTasks: 8, MaxAttempts: 3, MaxTransitions: 20, Tasks: []Task{{ID: "a", Title: "A", MaxAttempts: 2}, {ID: "b", Title: "B", DependsOn: []string{"a"}, MaxAttempts: 2}, {ID: "gate", Title: "Approval", DependsOn: []string{"b"}, Approval: true, MaxAttempts: 1}}}
}
func TestDAGAndSchedulingRespectDependencies(t *testing.T) {
	d := fixture()
	r, e := NewRun("run", d, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Start(); e != nil {
		t.Fatal(e)
	}
	got, e := r.Schedule()
	if e != nil || len(got) != 1 || got[0] != "a" {
		t.Fatalf("schedule=%v err=%v", got, e)
	}
	if e = r.BeginTask("b"); e == nil {
		t.Fatal("dependent task ran early")
	}
	if e = r.BeginTask("a"); e != nil {
		t.Fatal(e)
	}
	if e = r.FinishTask("a", true); e != nil {
		t.Fatal(e)
	}
	got, e = r.Schedule()
	if e != nil || len(got) != 1 || got[0] != "b" {
		t.Fatalf("schedule=%v err=%v", got, e)
	}
	if e = r.BeginTask("b"); e != nil {
		t.Fatal(e)
	}
	_ = r.FinishTask("b", true)
	got, e = r.Schedule()
	if e != nil || len(got) != 1 || got[0] != "gate" || r.State != Waiting {
		t.Fatalf("approval scheduling: %v %s %v", got, r.State, e)
	}
	if e = r.Approve("gate", true); e != nil {
		t.Fatal(e)
	}
	if r.State != Succeeded {
		t.Fatalf("approval should complete final node, state=%s", r.State)
	}
}
func TestCycleAndRetryBound(t *testing.T) {
	d := fixture()
	d.Tasks[0].DependsOn = []string{"b"}
	if d.Validate() == nil {
		t.Fatal("cycle accepted")
	}
	d = fixture()
	r, _ := NewRun("run", d, time.Now())
	_ = r.Start()
	_, _ = r.Schedule()
	for i := 0; i < 2; i++ {
		if e := r.BeginTask("a"); e != nil {
			t.Fatal(e)
		}
		if e := r.FinishTask("a", false); e != nil {
			t.Fatal(e)
		}
	}
	if r.Tasks[0].Attempts != 2 || r.State != Failed {
		t.Fatalf("unbounded retry: %+v", r)
	}
}
func TestInvalidTransitionsAndBoundedScheduler(t *testing.T) {
	r, _ := NewRun("run", fixture(), time.Now())
	if r.Pause() == nil {
		t.Fatal("paused from ready")
	}
	r.Definition.MaxTransitions = 1
	_ = r.Start()
	if _, e := r.Schedule(); e == nil {
		t.Fatal("scheduler exceeded transition budget")
	}
}
