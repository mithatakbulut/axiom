package recorder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProgressFixtures(t *testing.T) {
	cases := []struct{ name, extra, decision, trend string }{
		{"repeated", "", "escalate_candidate", "unknown"},
		{"improving", `{"hook_event_name":"PostToolUse","session_id":"s","tool_name":"Bash","tool_response":"Tests: 3 passed, 2 failed"}` + "\n" + `{"hook_event_name":"PostToolUse","session_id":"s","tool_name":"Bash","tool_response":"Tests: 4 passed, 1 failed"}`, "continue", "improving"},
		{"approval", `{"hook_event_name":"PermissionRequest","session_id":"s"}`, "continue", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "events.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			payloads := []string{}
			for i := 0; i < 2; i++ {
				payloads = append(payloads, fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s","tool_use_id":"%d","tool_input":{"command":"go test ./..."}}`, i), fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":"s","tool_use_id":"%d","exit_code":1}`, i))
			}
			if tc.extra != "" {
				payloads = append(payloads, strings.Split(tc.extra, "\n")...)
			}
			for i, payload := range payloads {
				e, err := Parse(strings.NewReader(payload), time.Unix(int64(i+1), 0))
				if err != nil {
					t.Fatal(err)
				}
				if err = s.Record(context.Background(), e, 30000); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Progress(context.Background(), "s", "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Decision != tc.decision || got.TestTrend != tc.trend || got.RepeatedCommands != 1 || len(got.RepeatedErrors) != 1 || got.Git != nil {
				t.Fatalf("unexpected: %+v", got)
			}
		})
	}
}

func TestFailedTestSummariesAreRepeatedFailuresDespiteZeroExit(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	stamp := int64(1)
	for i := 0; i < 3; i++ {
		for _, payload := range []string{
			fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s","tool_use_id":"%d","tool_input":{"command":"go test ./..."}}`, i),
			fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":"s","tool_name":"Bash","tool_use_id":"%d","exit_code":0,"tool_response":"Tests: 8 passed, 2 failed"}`, i),
		} {
			e, err := Parse(strings.NewReader(payload), time.Unix(stamp, 0))
			stamp++
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Record(context.Background(), e, 30000); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := s.Progress(context.Background(), "s", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != "escalate_candidate" || got.RepeatedCommands != 1 || got.LastTest == nil || got.LastTest.Failed != 2 {
		t.Fatalf("failed test summaries did not create repeated failure signal: %+v", got)
	}
}

func TestProgressKeepsUnresolvedApprovals(t *testing.T) {
	for _, tc := range []struct {
		name, requests, completion, want string
	}{
		{"multiple requests", `{"hook_event_name":"PermissionRequest","session_id":"s","tool_use_id":"a"}` + "\n" + `{"hook_event_name":"PermissionRequest","session_id":"s","tool_use_id":"b"}`, `{"hook_event_name":"PostToolUse","session_id":"s","tool_use_id":"b","exit_code":0}`, "continue"},
		{"unknown request", `{"hook_event_name":"PermissionRequest","session_id":"s"}`, `{"hook_event_name":"PostToolUse","session_id":"s","tool_use_id":"other","exit_code":0}`, "continue"},
		{"resolved request", `{"hook_event_name":"PermissionRequest","session_id":"s","tool_use_id":"a"}`, `{"hook_event_name":"PostToolUse","session_id":"s","tool_use_id":"a","exit_code":0}`, "escalate_candidate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "events.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			payloads := []string{}
			for i := 0; i < 2; i++ {
				payloads = append(payloads, fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s","tool_use_id":"fail%d","tool_input":{"command":"go test ./..."}}`, i), fmt.Sprintf(`{"hook_event_name":"PostToolUse","session_id":"s","tool_use_id":"fail%d","exit_code":1}`, i))
			}
			payloads = append(payloads, strings.Split(tc.requests, "\n")...)
			payloads = append(payloads, tc.completion)
			for i, payload := range payloads {
				e, err := Parse(strings.NewReader(payload), time.Unix(int64(i+1), 0))
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Record(context.Background(), e, 30000); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.Progress(context.Background(), "s", "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Decision != tc.want {
				t.Fatalf("decision = %s, want %s: %+v", got.Decision, tc.want, got)
			}
		})
	}
}

func TestProgressWaitsForNonCommandTool(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	payloads := []string{
		`{"hook_event_name":"PreToolUse","session_id":"s","tool_use_id":"a","tool_input":{"command":"go test"}}`,
		`{"hook_event_name":"PostToolUse","session_id":"s","tool_use_id":"a","exit_code":1}`,
		`{"hook_event_name":"PreToolUse","session_id":"s","tool_use_id":"b","tool_input":{"command":"go test"}}`,
		`{"hook_event_name":"PostToolUse","session_id":"s","tool_use_id":"b","exit_code":1}`,
		`{"hook_event_name":"PreToolUse","session_id":"s","tool_use_id":"c","tool_name":"Read"}`,
		`{"hook_event_name":"PostToolUse","session_id":"s","tool_use_id":"c","tool_name":"Read"}`,
	}
	for i, payload := range payloads {
		e, err := Parse(strings.NewReader(payload), time.Unix(int64(i+1), 0))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Record(context.Background(), e, 30000); err != nil {
			t.Fatal(err)
		}
		if i >= 4 {
			got, err := s.Progress(context.Background(), "s", "")
			if err != nil {
				t.Fatal(err)
			}
			want := "continue"
			if i == 5 {
				want = "escalate_candidate"
			}
			if got.Decision != want {
				t.Fatalf("step %d: decision = %s, want %s: %+v", i, got.Decision, want, got)
			}
		}
	}
}
func TestGitProgress(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	if GitProgress(context.Background(), dir) != nil {
		t.Fatal("non-repository should be unknown")
	}
	git("init", "-q")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "test")
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "file")
	git("commit", "-qm", "initial")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	g := GitProgress(context.Background(), dir)
	if g == nil || g.ChangedFiles != 1 || g.Added != 1 || g.Removed != 0 {
		t.Fatalf("git: %+v", g)
	}
}
