package vigil

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"verity/internal/candidate"
)

func TestLinkCannotRepresentVerificationOrAcceptance(t *testing.T) {
	base := "0123456789abcdef0123456789abcdef01234567"
	l := Link{SchemaVersion: 1, WorkflowID: "wf", TaskID: "task", AttemptID: "att", CorrelationID: "corr", BaseSHA: base, SessionID: "session", Status: CandidateReady, CapturedAt: time.Now()}
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
	l.Status = VerificationPassed
	if err := l.Validate(); err == nil {
		t.Fatal("Vigil must not report Verity verification")
	}
	l.Status = WorkflowAccepted
	if err := l.Validate(); err == nil {
		t.Fatal("Vigil must not accept a workflow")
	}
}

func TestVersionTwoLinkRequiresTypedCandidate(t *testing.T) {
	now := time.Now().UTC()
	base := "0123456789abcdef0123456789abcdef01234567"
	digest := strings.Repeat("a", 64)
	identity := candidate.Identity{SchemaVersion: 1, Kind: candidate.ContentSource, ID: digest, CapturedAt: now, Provenance: "codex-cli:selected-source-snapshot"}
	link := Link{SchemaVersion: SchemaVersion, WorkflowID: "wf", TaskID: "task", AttemptID: "attempt", CorrelationID: "attempt", Candidate: identity, BaseSHA: base, CandidateSHA: digest, SessionID: "session", Status: CandidateReady, CapturedAt: now}
	if err := link.Validate(); err != nil {
		t.Fatalf("valid typed integration link rejected: %v", err)
	}
	link.Candidate = candidate.Identity{}
	if err := link.Validate(); err == nil {
		t.Fatal("accepted version 2 integration link without typed candidate")
	}
}

func TestMCPClientCorrelateSessionUsesVersionedPublicTool(t *testing.T) {
	start := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	response := `{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"[{\"schema_version\":1,\"session_id\":\"session-1\",\"turn_id\":\"turn-1\",\"observed_at\":\"2026-11-18T00:00:30Z\"}]"}]}}`
	bin := filepath.Join(t.TempDir(), "mcp-fixture")
	script := `#!/bin/sh
read a
printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"serverInfo":{"name":"vigil","version":"0.1.0"}}}'
read b
printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"vigil_correlate_session"}]}}'
read c
printf '%s\n' '` + response + `'
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := (MCPClient{Command: bin}).CorrelateSession(context.Background(), "session-1", start, end)
	if err != nil || len(got) != 1 || got[0].SessionID != "session-1" || got[0].TurnID != "turn-1" {
		t.Fatalf("correlation=%+v err=%v", got, err)
	}
}

func TestNormalizeProgress(t *testing.T) {
	got := normalizeProgress(json.RawMessage(`{"decision":"escalate_candidate","signals":["repeat_failure","repeat_failure"]}`))
	if len(got) != 2 || got[0] != "repeat_failure" || got[1] != "progress_escalation_candidate" {
		t.Fatalf("unexpected normalized signals: %#v", got)
	}
}

func TestSnapshotAttachesOnlyToMatchingSession(t *testing.T) {
	l := Link{SchemaVersion: 1, WorkflowID: "wf", TaskID: "task", AttemptID: "att", CorrelationID: "corr", BaseSHA: "0123456789abcdef0123456789abcdef01234567", CandidateSHA: "1123456789abcdef0123456789abcdef01234567", SessionID: "s1", Status: CandidateReady, CapturedAt: time.Now()}
	s := Snapshot{VigilVersion: "0.1.0", SessionID: "s1", Signals: []string{"stuck_candidate"}, BudgetWarnings: []string{"runtime_limit"}, CapturedAt: time.Now()}
	got, err := s.Attach(l)
	if err != nil {
		t.Fatal(err)
	}
	if got.VigilVersion != "0.1.0" || len(got.Signals) != 1 || len(got.BudgetWarnings) != 1 {
		t.Fatalf("unexpected link: %+v", got)
	}
	s.SessionID = "other"
	if _, err = s.Attach(l); err == nil {
		t.Fatal("mismatched session attached")
	}
}

func TestMCPProcess(t *testing.T) {
	if os.Getenv("VERITY_MCP_HELPER") != "1" {
		return
	}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			version := os.Getenv("VERITY_MCP_VERSION")
			if version == "" {
				version = "0.1.0"
			}
			result = map[string]any{"serverInfo": map[string]string{"name": "vigil", "version": version}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]string{"name": "vigil_explain"}, map[string]string{"name": "vigil_budget"}}}
		case "tools/call":
			value := `{"latest_progress":{"decision":"escalate_candidate","signals":["repeat_failure"]},"latest_advisor":null}`
			if req.Params.Name == "vigil_budget" {
				value = `{"warnings":["runtime_limit"]}`
			}
			result = map[string]any{"content": []any{map[string]string{"text": value}}}
		}
		if enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}) != nil {
			return
		}
	}
}

func TestInspectRejectsUnknownVigilVersion(t *testing.T) {
	t.Setenv("VERITY_MCP_HELPER", "1")
	t.Setenv("VERITY_MCP_VERSION", "0.2.0")
	_, err := (MCPClient{Command: os.Args[0], Args: []string{"-test.run=^TestMCPProcess$"}}).Inspect(context.Background(), "session-1", "")
	if !errors.Is(err, ErrIncompatible) {
		t.Fatalf("expected incompatibility, got %v", err)
	}
}

func TestInspectUsesPublishedMCPContract(t *testing.T) {
	t.Setenv("VERITY_MCP_HELPER", "1")
	s, err := (MCPClient{Command: os.Args[0], Args: []string{"-test.run=^TestMCPProcess$"}}).Inspect(context.Background(), "session-1", "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	if s.SessionID != "session-1" || s.VigilVersion != "0.1.0" || len(s.Signals) != 2 {
		t.Fatalf("unexpected snapshot: %+v", s)
	}
	if err = ValidateResult(s); err != nil {
		t.Fatal(err)
	}
}
