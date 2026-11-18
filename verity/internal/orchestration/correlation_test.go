package orchestration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"verity/internal/candidate"
	"verity/internal/execution"
	"verity/internal/store/sqlite"
)

func TestVigilCorrelationFailureIsBestEffort(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "verity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	start := time.Now().UTC().Add(-time.Second)
	end := start.Add(500 * time.Millisecond)
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	identity := candidate.Identity{SchemaVersion: 1, Kind: candidate.ContentSource, ID: digest, CapturedAt: start, Provenance: "test"}
	attempt := execution.DurableAttempt{
		Request: execution.Request{ID: "attempt-1", TaskID: "task-1", BaseSHA: "0123456789abcdef0123456789abcdef01234567"},
		Result:  &execution.Result{Outcome: execution.Succeeded, CandidateSHA: digest, Candidate: identity, SessionID: "session-1", StartedAt: start, FinishedAt: end},
	}

	t.Run("Vigil unavailable", func(t *testing.T) {
		t.Setenv("VERITY_VIGIL_BINARY", filepath.Join(t.TempDir(), "missing-vigil"))
		persistVigilCorrelation(ctx, store, "workflow-1", "task-1", attempt, digest)
		links, err := store.VigilLinks(ctx, "workflow-1", digest)
		if err != nil || len(links) != 0 {
			t.Fatalf("unavailable Vigil produced links=%#v err=%v", links, err)
		}
	})

	t.Run("incomplete observation", func(t *testing.T) {
		binary := filepath.Join(t.TempDir(), "mcp")
		script := `#!/bin/sh
read a
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"serverInfo":{"name":"vigil","version":"0.1.0"}}}'
read b
printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"vigil_correlate_session"}]}}'
read c
printf '%s\n' '{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"[{}]"}]}}'
`
		if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("VERITY_VIGIL_BINARY", binary)
		persistVigilCorrelation(ctx, store, "workflow-2", "task-1", attempt, digest)
		links, err := store.VigilLinks(ctx, "workflow-2", digest)
		if err != nil || len(links) != 0 {
			t.Fatalf("incomplete observation produced links=%#v err=%v", links, err)
		}
	})
}
