package control

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vigil/internal/advisor"
	"vigil/internal/config"
	"vigil/internal/recorder"
)

func TestModesAndStop(t *testing.T) {
	ctx := context.Background()
	c, _ := config.Default()
	dir := t.TempDir()
	c.Storage.Path = filepath.Join(dir, "db")
	c.Models.Advisor = "test-model"
	db, err := recorder.Open(c.Storage.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	path := filepath.Join(dir, "codex")
	script := `#!/bin/sh
case "$1" in
 --version) echo codex ;;
 app-server)
  read -r init
  echo '{"id":1,"result":{}}'
  read -r ack
  read -r req
  echo '{"id":2,"result":{"data":[{"model":"test-model","supportedReasoningEfforts":[{"reasoningEffort":"high"}]}]}}'
  ;;
 exec)
  /bin/cat >/dev/null
  echo '{"type":"item.completed","item":{"type":"agent_message","text":"{\"diagnosis\":\"fixture diagnosis\",\"next_steps\":[\"check fixture\"],\"validation\":[\"test fixture\"]}"}}'
  echo '{"type":"turn.completed"}'
  ;;
esac
`
	if err = os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	svc := Service{Config: c, Store: db, Advisor: advisor.Runner{Binary: path}}
	event := func(session, turn, kind, extra string) recorder.Event {
		t.Helper()
		raw := fmt.Sprintf(`{"session_id":%q,"turn_id":%q,"hook_event_name":%q,"cwd":%q,%s}`, session, turn, kind, dir, extra)
		e, err := recorder.Parse(strings.NewReader(raw), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Record(ctx, e, 30); err != nil {
			t.Fatal(err)
		}
		return e
	}
	seed := func(session, turn string) recorder.Event {
		event(session, turn, "PostToolUse", `"tool_use_id":"one","exit_code":1`)
		event(session, turn, "PostToolUse", `"tool_use_id":"two","exit_code":1`)
		return event(session, turn, "Stop", `"stop_hook_active":false`)
	}
	stop := seed("s", "observe")
	out, err := svc.OnStop(ctx, stop)
	if err != nil || out != (StopResult{}) {
		t.Fatalf("observe: %+v %v", out, err)
	}
	exp, err := svc.Explain(ctx, "s")
	if err != nil || !strings.Contains(string(exp.Progress), "escalate_candidate") || exp.Advisor != nil {
		t.Fatalf("explain: %+v %v", exp, err)
	}
	if err = svc.SetMode(ctx, "assist"); err != nil {
		t.Fatal(err)
	}
	stop = seed("s", "assist")
	out, err = svc.OnStop(ctx, stop)
	if err != nil || !strings.Contains(out.SystemMessage, "explicitly approve") || out.Decision != "" {
		t.Fatalf("assist: %+v %v", out, err)
	}
	if _, err = svc.RequestAdvisor(ctx, "s", "assist", dir, false, false); err == nil {
		t.Fatal("assist ran without approval")
	}
	if _, err = svc.RequestAdvisor(ctx, "s", "assist", dir, false, true); err != nil {
		t.Fatal(err)
	}
	if err = svc.SetMode(ctx, "autonomous"); err != nil {
		t.Fatal(err)
	}
	svc.Config.Escalation.RequireApproval = false
	stop = seed("auto", "autonomous")
	out, err = svc.OnStop(ctx, stop)
	if err != nil || out != (StopResult{}) {
		t.Fatalf("shadow: %+v %v", out, err)
	}
	svc.Config.Escalation.Shadow = false
	if err = svc.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	out, err = svc.OnStop(ctx, stop)
	if err != nil || out != (StopResult{}) {
		t.Fatalf("paused: %+v %v", out, err)
	}
	if err = svc.SetPaused(ctx, false); err != nil {
		t.Fatal(err)
	}
	out, err = svc.OnStop(ctx, stop)
	if err != nil || out.Decision != "block" || !strings.Contains(out.Reason, "fixture diagnosis") {
		t.Fatalf("autonomous: %+v %v", out, err)
	}
	out, err = svc.OnStop(ctx, event("auto", "autonomous", "Stop", `"stop_hook_active":true`))
	if err != nil || out != (StopResult{}) {
		t.Fatalf("recursion: %+v %v", out, err)
	}
	out, err = svc.OnStop(ctx, stop)
	if err != nil || out.Decision != "" {
		t.Fatalf("repeat stop: %+v %v", out, err)
	}
	svc.Advisor.Binary = filepath.Join(dir, "missing")
	out, err = svc.OnStop(ctx, seed("failure", "turn"))
	if err != nil || out.Decision != "" || out.SystemMessage == "" {
		t.Fatalf("failed advisor should fail open: %+v %v", out, err)
	}
	exp, err = svc.Explain(ctx, "failure")
	if err != nil || exp.Advisor == nil || exp.Advisor.State != "failed" {
		t.Fatalf("failed advisor not recorded: %+v %v", exp, err)
	}
}

func TestEval(t *testing.T) {
	c, _ := config.Default()
	e, err := (Service{Config: c}).Eval(context.Background())
	if err != nil || e.Total == 0 || e.Passed != e.Total {
		t.Fatalf("eval: %+v %v", e, err)
	}
}

func TestBudgetKeepsOldTurnWithOriginalTask(t *testing.T) {
	ctx := context.Background()
	c, _ := config.Default()
	db, err := recorder.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	start := time.Now().Add(-2 * time.Minute)
	for i, id := range []string{"first", "second"} {
		at := start.Add(time.Duration(i) * time.Minute)
		contract := recorder.Contract{ID: id, Title: id, Requirements: []recorder.Requirement{{ID: "test", Description: "Pass tests"}}}
		if err = db.CreateTask(ctx, "s", contract, at); err != nil {
			t.Fatal(err)
		}
		raw := fmt.Sprintf(`{"session_id":"s","turn_id":%q,"model":"fixture","hook_event_name":"PostModelRequest","token_usage":{"input_tokens":%d}}`, id, i+1)
		e, err := recorder.Parse(strings.NewReader(raw), at.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Record(ctx, e, 30); err != nil {
			t.Fatal(err)
		}
	}
	svc := Service{Config: c, Store: db}
	old, err := svc.Budget(ctx, "s", "first", time.Now())
	if err != nil || old.Usage.Task != "first" || old.Usage.ModelRequests != 1 || old.Usage.Tokens["input_tokens"] != 1 || old.Usage.RuntimeSeconds != 60 {
		t.Fatalf("old task budget: %+v %v", old, err)
	}
	current, err := svc.Budget(ctx, "s", "", time.Now())
	if err != nil || current.Usage.Task != "second" || current.Usage.Tokens["input_tokens"] != 2 || current.Usage.EstimatedAPICost != nil {
		t.Fatalf("current task budget: %+v %v", current, err)
	}
}
