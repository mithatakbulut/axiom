package vigil

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
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
