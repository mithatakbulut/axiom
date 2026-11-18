package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"verity/internal/execution"
)

func TestRuntimeCapturesCompletedTurn(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source"), []byte("contents"), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"fixture-thread\"}' '{\"type\":\"turn.completed\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r := Runtime{Binary: bin, SnapshotRoot: t.TempDir(), SourcePaths: []string{"source"}}
	req := execution.Request{ID: "attempt", WorkflowID: "workflow", TaskID: "task", Workspace: root, Attempt: 1, BaseSHA: strings.Repeat("a", 64), Timeout: time.Minute, Instruction: "Update the source"}
	result, err := r.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != execution.Succeeded || len(result.CandidateSHA) != 64 || result.Candidate.ID != result.CandidateSHA || result.Candidate.Kind != "content_snapshot" || !strings.Contains(result.Summary, "fixture-thread") || result.SessionID != "fixture-thread" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("typed runtime result is invalid: %v", err)
	}
}

func TestRuntimeRequiresTerminalEvent(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"fixture\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r := Runtime{Binary: bin, SnapshotRoot: t.TempDir(), SourcePaths: []string{"source"}}
	req := execution.Request{ID: "attempt", WorkflowID: "workflow", TaskID: "task", Workspace: root, Attempt: 1, BaseSHA: strings.Repeat("a", 64), Timeout: time.Minute, Instruction: "work"}
	if _, err := r.Execute(context.Background(), req); err == nil {
		t.Fatal("missing terminal event accepted")
	}
}

func TestRuntimeTreatsInterruptedProcessAsUnknown(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r := Runtime{Binary: bin, SnapshotRoot: t.TempDir(), SourcePaths: []string{"source"}}
	req := execution.Request{ID: "attempt", WorkflowID: "workflow", TaskID: "task", Workspace: root, Attempt: 1, BaseSHA: strings.Repeat("a", 64), Timeout: time.Minute, Instruction: "work"}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, err := r.Execute(ctx, req)
	if err != nil || result.Outcome != execution.Unknown {
		t.Fatalf("expected UNKNOWN, got %+v: %v", result, err)
	}
}
