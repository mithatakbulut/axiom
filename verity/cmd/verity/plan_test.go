package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"verity/internal/candidate"
	"verity/internal/execution"
	"verity/internal/process/control"
	"verity/internal/proof"
	"verity/internal/store/sqlite"
	"verity/internal/verification"
)

func TestPlanCreateAndShow(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	manifest := "version: 1\ncommands:\n  check:\n    command: ['true']\n    required: true\n    timeout_seconds: 5\n    criteria: [pass, works]\ncriteria:\n  - id: pass\n    description: Pass\n  - id: works\n    description: Works\n"
	if err := os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "plan.json")
	plan := `{"schema_version":1,"name":"Example","version":1,"goal":"Deliver","acceptance_criteria":["Pass"],"source_paths":["src"],"max_tasks":2,"max_attempts":1,"max_transitions":32,"tasks":[{"id":"one","title":"First","description":"Implement","acceptance_criteria":["Works"],"source_paths":["src"],"timeout_seconds":60,"max_attempts":1}]}`
	if err := os.WriteFile(path, []byte(plan), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, "--config", cfg, "plan", "create", path)
	if err != nil {
		t.Fatal(err)
	}
	var run struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &run); err != nil {
		t.Fatal(err)
	}
	if run.ID == "" || run.State != "READY" {
		t.Fatalf("unexpected run: %s", out)
	}
	out, err = invoke(t, "--config", cfg, "plan", "show", run.ID)
	if err != nil || !strings.Contains(out, `"goal":"Deliver"`) {
		t.Fatalf("show: %s %v", out, err)
	}
	invalid := strings.Replace(plan, `"id":"one"`, `"id":"one","depends_on":["missing"]`, 1)
	if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(t, "--config", cfg, "plan", "create", path); err == nil {
		t.Fatal("missing dependency accepted")
	}
}

func TestPlanDrivesThreeStepsAndHonorsApproval(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	if err := os.WriteFile(filepath.Join(root, "src"), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\ncommands:\n  check:\n    command: ['true']\n    required: true\n    timeout_seconds: 5\n    criteria: [works]\ncriteria:\n  - id: works\n    description: Works\n"
	if err := os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	task := func(id string, deps []string, approval bool) control.Task {
		return control.Task{ID: id, Title: id, Description: id, AcceptanceCriteria: []string{"Works"}, SourcePaths: []string{"src"}, TimeoutSeconds: 60, MaxAttempts: 1, DependsOn: deps, Approval: approval}
	}
	d := control.Definition{SchemaVersion: 1, Name: "Example", Version: 1, Goal: "Goal", AcceptanceCriteria: []string{"Works"}, SourcePaths: []string{"src"}, MaxTasks: 4, MaxAttempts: 1, MaxTransitions: 64, Tasks: []control.Task{task("one", nil, false), task("two", []string{"one"}, false), task("gate", []string{"two"}, true), task("three", []string{"gate"}, false)}}
	data, _ := json.Marshal(d)
	path := filepath.Join(root, "plan.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, "--config", cfg, "plan", "create", path)
	if err != nil {
		t.Fatal(err)
	}
	var created control.Run
	if err = json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(root, ".verity", "verity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	binDir := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\nprintf 'call\\n' >> \"$PLAN_TEST_CALLS\"\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"fixture\"}' '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"finished\"}}' '{\"type\":\"turn.completed\"}'\n"
	if err = os.WriteFile(filepath.Join(binDir, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	callsPath := filepath.Join(root, "calls")
	t.Setenv("PLAN_TEST_CALLS", callsPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err = invoke(t, "--config", cfg, "plan", "start", created.ID); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetRun(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != control.Waiting || got.Tasks[0].State != control.TaskCompleted || got.Tasks[1].State != control.TaskCompleted || got.Tasks[3].State == control.TaskCompleted {
		t.Fatalf("unexpected before approval: %+v", got)
	}
	// Simulate a restart after task two's proof was saved, before its state transition.
	before := got.Transitions
	got.Transitions++
	got.State = control.Running
	got.Tasks[1].State = control.TaskRunning
	got.Tasks[2].State = control.Pending
	if err = db.UpdateRun(ctx, got, before); err != nil {
		t.Fatal(err)
	}
	if _, err = invoke(t, "--config", cfg, "plan", "resume", created.ID); err != nil {
		t.Fatal(err)
	}
	interim, err := os.ReadFile(callsPath)
	if err != nil || strings.Count(string(interim), "call\n") != 2 {
		t.Fatalf("verified task ran again: %q %v", interim, err)
	}
	if _, err = invoke(t, "--config", cfg, "plan", "approve", created.ID, "gate", "reviewer", "reviewed"); err != nil {
		t.Fatal(err)
	}
	changedManifest := "version: 1\ncommands:\n  check:\n    command: ['false']\n    required: true\n    timeout_seconds: 5\n    criteria: [works]\ncriteria:\n  - id: works\n    description: Works\n"
	if err = os.WriteFile(filepath.Join(root, ".verity", "verification.yaml"), []byte(changedManifest), 0600); err != nil {
		t.Fatal(err)
	}
	attempt, err := db.LatestExecutionAttempt(ctx, created.ID, "one")
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := db.Evidence(ctx, created.ID, attempt.Result.CandidateSHA)
	if err != nil || len(evidence) == 0 {
		t.Fatalf("evidence: %v", err)
	}
	artifacts := proof.LocalArtifactStore{Root: filepath.Join(root, ".verity", "artifacts")}
	content, err := artifacts.Get(ctx, evidence[0].ContentSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err = artifacts.Delete(ctx, evidence[0].ContentSHA256); err != nil {
		t.Fatal(err)
	}
	checks, err := db.TaskVerifications(ctx, created.ID, "one")
	if err != nil || len(checks) == 0 {
		t.Fatalf("checks: %v", err)
	}
	if err = planEvidence(ctx, db, filepath.Join(root, ".verity"), checks[0]); err == nil {
		t.Fatal("missing evidence accepted")
	}
	if _, err = artifacts.Put(ctx, content); err != nil {
		t.Fatal(err)
	}
	if _, err = invoke(t, "--config", cfg, "plan", "resume", created.ID); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil || strings.Count(string(calls), "call\n") != 3 {
		t.Fatalf("CLI calls: %q %v", calls, err)
	}
	got, err = db.GetRun(ctx, created.ID)
	if err != nil || got.State != control.Succeeded || got.Tasks[2].ApprovalActor != "reviewer" {
		t.Fatalf("unexpected completion: %+v %v", got, err)
	}
}

type countingPlanRuntime struct {
	calls   int
	outcome execution.Outcome
}

func (r *countingPlanRuntime) Capabilities(ctx context.Context) (execution.Capabilities, error) {
	return execution.FakeRuntime{}.Capabilities(ctx)
}
func (r *countingPlanRuntime) Execute(ctx context.Context, req execution.Request) (execution.Result, error) {
	r.calls++
	return (execution.FakeRuntime{Outcome: r.outcome}).Execute(ctx, req)
}

func TestPlanDoesNotRepeatUnknownOrFailedVerification(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		outcome       execution.Outcome
	}{{"provider timeout with unknown outcome", "true", execution.Unknown}, {"crash after intent before provider invocation", "true", execution.Unknown}, {"verification failure", "false", execution.Succeeded}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "src"), []byte("data"), 0600); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, ".verity")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			manifest := "version: 1\ncommands:\n  check:\n    command: ['" + tc.command + "']\n    required: true\n    timeout_seconds: 5\n    criteria: [works]\ncriteria:\n  - id: works\n    description: Works\n"
			d := control.Definition{SchemaVersion: 1, ID: "p", Name: "Plan", Version: 1, Goal: "Goal", AcceptanceCriteria: []string{"Works"}, SourcePaths: []string{"src"}, VerificationManifest: manifest, MaxTasks: 2, MaxAttempts: 2, MaxTransitions: 32, Tasks: []control.Task{{ID: "one", Title: "One", Description: "Do", AcceptanceCriteria: []string{"Works"}, SourcePaths: []string{"src"}, TimeoutSeconds: 60, MaxAttempts: 2}, {ID: "_plan_verify", Title: "Verify", Description: "Verify", AcceptanceCriteria: []string{"Works"}, SourcePaths: []string{"src"}, TimeoutSeconds: 60, MaxAttempts: 1, DependsOn: []string{"one"}}}}
			ctx := context.Background()
			db, err := sqlite.Open(ctx, filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			run, err := control.NewRun("p", d, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err = run.Start(); err != nil {
				t.Fatal(err)
			}
			if _, err = run.ScheduleOne(); err != nil {
				t.Fatal(err)
			}
			if err = run.BeginTask("one"); err != nil {
				t.Fatal(err)
			}
			if err = db.CreatePlan(ctx, d, run); err != nil {
				t.Fatal(err)
			}
			if tc.name == "crash after intent before provider invocation" {
				base, err := verification.CreateContentSnapshot(ctx, root, filepath.Join(dir, "snapshots"), []string{"src"})
				if err != nil {
					t.Fatal(err)
				}
				baseCandidate := candidate.Identity{SchemaVersion: 1, Kind: candidate.ContentSource, ID: base, Repository: root, CapturedAt: time.Now().UTC(), Provenance: "verity-plan:test-base"}
				req := execution.Request{ID: "interrupted", WorkflowID: "p", TaskID: "one", Workspace: root, Attempt: 1, BaseSHA: base, BaseCandidate: baseCandidate, Timeout: time.Minute, Instruction: "work"}
				if err = db.CreateExecutionAttempt(ctx, execution.DurableAttempt{Request: req, Provider: "fake", CreatedAt: time.Now(), State: execution.AttemptRunning}); err != nil {
					t.Fatal(err)
				}
			}
			runtime := &countingPlanRuntime{outcome: tc.outcome}
			if err = planDrive(ctx, root, dir, "", db, "p", runtime); err == nil {
				t.Fatal("expected blocked execution")
			}
			if tc.name == "crash after intent before provider invocation" {
				attempt, err := db.LatestExecutionAttempt(ctx, "p", "one")
				if err != nil || attempt.State != execution.AttemptUnknown || attempt.Result != nil {
					t.Fatalf("in-flight intent was not preserved as UNKNOWN without a fabricated result: %+v %v", attempt, err)
				}
			}
			if err = planDrive(ctx, root, dir, "", db, "p", runtime); err == nil {
				t.Fatal("expected blocked resume")
			}
			expectedCalls := 1
			if tc.name == "crash after intent before provider invocation" {
				expectedCalls = 0
			}
			if runtime.calls != expectedCalls {
				t.Fatalf("attempt repeated %d times", runtime.calls)
			}
			got, err := db.GetRun(ctx, "p")
			if err != nil || got.State == control.Succeeded || got.Tasks[0].State == control.TaskCompleted {
				t.Fatalf("premature completion %+v %v", got, err)
			}
		})
	}
}

type stalePassPlanRuntime struct {
	db         *sqlite.Store
	repository string
}

func (r stalePassPlanRuntime) Capabilities(ctx context.Context) (execution.Capabilities, error) {
	return execution.FakeRuntime{}.Capabilities(ctx)
}

func (r stalePassPlanRuntime) Execute(ctx context.Context, req execution.Request) (execution.Result, error) {
	staleSHA := strings.Repeat("b", 64)
	stale := verification.Run{
		ID: "stale-pass", SchemaVersion: verification.CurrentRunSchemaVersion,
		WorkflowID: req.WorkflowID, TaskID: req.TaskID,
		Candidate: candidate.Identity{
			SchemaVersion: 1, Kind: candidate.ContentSource, ID: staleSHA,
			Repository: r.repository, CapturedAt: time.Now().UTC(), Provenance: "test:stale-candidate",
		},
		CandidateKind: verification.CandidateContentSource, CandidateSHA: staleSHA,
		CandidateRepository: r.repository, IsolationPolicy: verification.IsolationLocalProcess,
		TrustLevel: verification.TrustTrusted, Profile: verification.Standard,
		State: verification.StatePass, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
		Checks: []verification.CheckResult{{ID: "check", Name: "check", Required: true, Outcome: verification.Passed, EvidenceIDs: []string{"unbound-old-evidence"}}},
	}
	if err := r.db.SaveVerification(ctx, stale); err != nil {
		return execution.Result{}, err
	}
	return execution.FakeRuntime{}.Execute(ctx, req)
}

func TestPlanDoesNotAcceptPassingVerificationForOlderCandidate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "src"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, ".verity")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\ncommands:\n  check:\n    command: ['false']\n    required: true\n    timeout_seconds: 5\n    criteria: [works]\ncriteria:\n  - id: works\n    description: Works\n"
	d := control.Definition{
		SchemaVersion: 1, ID: "stale-plan", Name: "Stale candidate", Version: 1,
		Goal: "Verify candidate identity", AcceptanceCriteria: []string{"Works"},
		SourcePaths: []string{"src"}, VerificationManifest: manifest,
		MaxTasks: 2, MaxAttempts: 1, MaxTransitions: 32,
		Tasks: []control.Task{
			{ID: "one", Title: "Implement", Description: "Make change", AcceptanceCriteria: []string{"Works"}, SourcePaths: []string{"src"}, TimeoutSeconds: 60, MaxAttempts: 1},
			{ID: "_plan_verify", Title: "Finish verification", Description: "Verify final plan", AcceptanceCriteria: []string{"Works"}, SourcePaths: []string{"src"}, TimeoutSeconds: 60, MaxAttempts: 1, DependsOn: []string{"one"}},
		},
	}
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(dataDir, "verity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run, err := control.NewRun(d.ID, d, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err = run.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err = run.ScheduleOne(); err != nil {
		t.Fatal(err)
	}
	if err = run.BeginTask("one"); err != nil {
		t.Fatal(err)
	}
	if err = db.CreatePlan(ctx, d, run); err != nil {
		t.Fatal(err)
	}
	runtime := stalePassPlanRuntime{db: db, repository: root}
	if err = planDrive(ctx, root, dataDir, "", db, run.ID, runtime); err == nil {
		t.Fatal("old passing verification hid the current candidate's failing check")
	}
	current, err := db.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State == control.Succeeded || current.Tasks[0].State == control.TaskCompleted {
		t.Fatalf("stale verification completed the task: %+v", current)
	}
	attempt, err := db.LatestExecutionAttempt(ctx, run.ID, "one")
	if err != nil {
		t.Fatal(err)
	}
	verifications, err := db.TaskVerifications(ctx, run.ID, "one")
	if err != nil || len(verifications) != 2 {
		t.Fatalf("verification records = %d, %v", len(verifications), err)
	}
	foundStale, foundCurrentFailure := false, false
	for _, v := range verifications {
		if v.CandidateSHA == strings.Repeat("b", 64) && v.State == verification.StatePass {
			foundStale = true
		}
		if v.CandidateSHA == attempt.Result.CandidateSHA && v.State == verification.StateFail {
			foundCurrentFailure = true
		}
	}
	if !foundStale || !foundCurrentFailure {
		t.Fatalf("stale/current decisions not preserved: %+v; candidate=%s", verifications, attempt.Result.CandidateSHA)
	}
}

func TestPlanVerificationReviewRequiresRecordedHuman(t *testing.T) {
	root := t.TempDir()
	if _, err := invoke(t, "init", root); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, ".verity", "config.yaml")
	dir := filepath.Join(root, ".verity")
	if err := os.WriteFile(filepath.Join(root, "src"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\nreview: true\ncommands:\n  check:\n    command: ['true']\n    required: true\n    timeout_seconds: 5\n    criteria: [works]\ncriteria:\n  - id: works\n    description: Works\n"
	if err := os.WriteFile(filepath.Join(dir, "verification.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	d := control.Definition{SchemaVersion: 1, Name: "Plan", Version: 1, Goal: "Goal", AcceptanceCriteria: []string{"Works"}, SourcePaths: []string{"src"}, MaxTasks: 1, MaxAttempts: 1, MaxTransitions: 32, Tasks: []control.Task{{ID: "one", Title: "One", Description: "Do", AcceptanceCriteria: []string{"Works"}, SourcePaths: []string{"src"}, TimeoutSeconds: 60, MaxAttempts: 1}}}
	data, _ := json.Marshal(d)
	path := filepath.Join(root, "plan.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := invoke(t, "--config", cfg, "plan", "create", path)
	if err != nil {
		t.Fatal(err)
	}
	var run control.Run
	if err = json.Unmarshal([]byte(out), &run); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(dir, "verity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	before := run.Transitions
	if err = run.Start(); err != nil {
		t.Fatal(err)
	}
	if err = db.UpdateRun(ctx, run, before); err != nil {
		t.Fatal(err)
	}
	runtime := &countingPlanRuntime{}
	for _, task := range []string{"one", "_plan_verify"} {
		if err = planDrive(ctx, root, dir, "", db, run.ID, runtime); err == nil {
			t.Fatal("review gate bypassed")
		}
		checks, err := db.TaskVerifications(ctx, run.ID, task)
		if err != nil || len(checks) != 1 || checks[0].State != verification.StateNeedsHuman {
			t.Fatalf("missing review: %+v %v", checks, err)
		}
		if _, err = invoke(t, "--config", cfg, "plan", "review", run.ID, checks[0].ID, "reviewer", "reviewed evidence"); err != nil {
			t.Fatal(err)
		}
	}
	if err = planDrive(ctx, root, dir, "", db, run.ID, runtime); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetRun(ctx, run.ID)
	if err != nil || got.State != control.Succeeded || runtime.calls != 1 {
		t.Fatalf("unexpected result: %+v calls=%d %v", got, runtime.calls, err)
	}
}
