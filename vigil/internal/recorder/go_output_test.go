package recorder

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const liveGoFailure = "--- FAIL: TestResolve (0.00s)\n    --- FAIL: TestResolve/escaped-slash (0.00s)\n        resolve_test.go:9: wrong URI\nFAIL\nexit status 1\nFAIL\tadvisoracceptance\t0.182s\n"

func TestGoTestFailureWithoutExitMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, tool, output string
		failed             bool
	}{
		{"observed CLI output", "Bash", liveGoFailure, true},
		{"recognized shell tool", "exec_command", liveGoFailure, true},
		{"test failure despite zero shell exit", "Bash", liveGoFailure, true},
		{"conflicting passing summary", "Bash", "Tests: 4 passed, 0 failed\n" + liveGoFailure, true},
		{"failure line without package result", "Bash", "--- FAIL: TestResolve (0.00s)\n", false},
		{"package result without failure line", "Bash", "FAIL\tadvisoracceptance\t0.182s\n", false},
		{"unrecognized tool", "Read", liveGoFailure, false},
		{"passing output", "Bash", "--- PASS: TestResolve (0.00s)\nPASS\nok\tadvisoracceptance\t0.182s\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{"session_id": "s", "hook_event_name": "PostToolUse", "tool_name": tc.tool, "tool_response": tc.output + "password=hunter2\n"}
			if tc.name == "test failure despite zero shell exit" {
				payload["exit_code"] = 0
			}
			raw, _ := json.Marshal(payload)
			e, err := Parse(strings.NewReader(string(raw)), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			var data struct {
				ErrorFingerprint string `json:"error_fingerprint"`
				ExitCode         *int   `json:"exit_code"`
				TestFailed       *int   `json:"test_failed"`
			}
			if err := json.Unmarshal(e.Data, &data); err != nil {
				t.Fatal(err)
			}
			if (data.ErrorFingerprint != "") != tc.failed {
				t.Fatalf("failure observation=%q, want failed=%v", data.ErrorFingerprint, tc.failed)
			}
			if tc.name != "test failure despite zero shell exit" && data.ExitCode != nil {
				t.Fatalf("invented exit status: %s", e.Data)
			}
			if data.TestFailed != nil || strings.Contains(string(e.Data), "hunter2") || strings.Contains(string(e.Data), "wrong URI") {
				t.Fatalf("invented counts or retained output: %s", e.Data)
			}
		})
	}
}

func TestProgressGoFailuresWithoutExitMetadata(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "vigil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		for _, kind := range []string{"PreToolUse", "PostToolUse"} {
			payload := map[string]any{"session_id": "s", "turn_id": "t", "hook_event_name": kind, "tool_name": "Bash", "tool_use_id": fmt.Sprintf("call-%d", i)}
			if kind == "PreToolUse" {
				payload["tool_input"] = map[string]string{"command": fmt.Sprintf("go test -count=1 -run TestResolve -v=%t", i == 1)}
			} else {
				payload["tool_response"] = liveGoFailure
			}
			raw, _ := json.Marshal(payload)
			e, err := Parse(strings.NewReader(string(raw)), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Record(ctx, e, 30); err != nil {
				t.Fatal(err)
			}
		}
	}
	p, err := db.Progress(ctx, "s", "")
	if err != nil || p.Decision != "escalate_candidate" || p.DistinctAttempts != 2 {
		t.Fatalf("progress=%+v err=%v", p, err)
	}
}
