package recorder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordDedupAndPrivacy(t *testing.T) {
	payload := `{"hook_event_name":"PostToolUse","session_id":"session-1","turn_id":"turn-1","tool_name":"exec","tool_response":"password=verysecret","tool_input":{"token":"sk-1234567890secret"},"transcript_path":"/private/secret"}`
	e, err := Parse(strings.NewReader(payload), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(t.TempDir(), "vigil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for i := 0; i < 2; i++ {
		if err = s.Record(context.Background(), e, 30); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Check(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("count %d: %v", n, err)
	}
	var data string
	if err = s.db.QueryRow("select data from events").Scan(&data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(data, "secret") || strings.Contains(data, "transcript") {
		t.Fatalf("private data stored: %s", data)
	}
}
func TestInvalidAndSupported(t *testing.T) {
	for _, v := range []string{`{`, `null`, `{"session_id":"x","hook_event_name":"unknown"}`, `{"hook_event_name":"Stop"}`, `{} {}`, strings.Repeat("x", MaxPayload+1)} {
		if _, err := Parse(strings.NewReader(v), time.Now()); err == nil {
			t.Fatalf("accepted %q", v[:min(len(v), 30)])
		}
	}
	for typ := range supported {
		_, err := Parse(strings.NewReader(fmt.Sprintf(`{"session_id":"s","hook_event_name":%q}`, typ)), time.Now())
		if err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
}
func TestRetention(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "vigil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now()
	for i, age := range []int{31, 0} {
		e, err := Parse(strings.NewReader(fmt.Sprintf(`{"session_id":"s","hook_event_name":"Stop","turn_id":"%d"}`, i)), now.Add(-time.Duration(age)*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Record(context.Background(), e, 30); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Check(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("count %d: %v", n, err)
	}
}
func TestUnavailableStorage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(p, "db")); err == nil {
		t.Fatal("expected error")
	}
}

func TestSessionObservationsRespectAttemptTimeWindow(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "observation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Now().UTC().Truncate(time.Second)
	for i, at := range []time.Time{start.Add(-time.Second), start.Add(time.Second), start.Add(3 * time.Second)} {
		payload := fmt.Sprintf(`{"hook_event_name":"UserPromptSubmit","session_id":"session-c","turn_id":"turn-%d"}`, i)
		e, err := Parse(strings.NewReader(payload), at)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Record(context.Background(), e, 30); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.SessionObservations(context.Background(), "session-c", start, start.Add(2*time.Second))
	if err != nil || len(rows) != 1 || rows[0].SessionID != "session-c" || rows[0].TurnID != "turn-1" || rows[0].SchemaVersion != 1 {
		t.Fatalf("observations=%+v err=%v", rows, err)
	}
}

func TestSummaryCorrelatesAndPreservesUnknowns(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now().UTC()
	fixtures := []struct {
		payload string
		at      time.Time
	}{
		{`{"session_id":"s","turn_id":"t","hook_event_name":"PreToolUse","tool_use_id":"call1","tool_name":"Bash"}`, now},
		{`{"session_id":"s","turn_id":"t","hook_event_name":"PostToolUse","tool_use_id":"call1","tool_response":{"exit_code":2,"output":"password=hunter2"}}`, now.Add(1500 * time.Millisecond)},
		{`{"session_id":"s","turn_id":"t","hook_event_name":"PostModelRequest","status":"failed","error":"secret token","token_usage":{"input_tokens":42,"output_tokens":5,"invented":"secret"}}`, now.Add(2 * time.Second)},
		{`{"session_id":"s","turn_id":"t","hook_event_name":"SubagentStart","agent_id":"a","agent_type":"worker"}`, now.Add(3 * time.Second)},
	}
	for _, f := range fixtures {
		e, err := Parse(strings.NewReader(f.payload), f.at)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(e.Data), "hunter2") || strings.Contains(string(e.Data), "secret") {
			t.Fatalf("secret persisted: %s", e.Data)
		}
		if err = s.Record(context.Background(), e, 30); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := s.Summary(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Turns != 1 || summary.ToolFailures != 1 || summary.ModelFailures != 1 || summary.TokenUsage["input_tokens"] != 42 || summary.ToolDurationsMS["t/call1"] != 1500 || summary.EventCounts["SubagentStart"] != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if _, err = s.Summary(context.Background(), "missing"); err == nil {
		t.Fatal("missing session accepted")
	}
}
func TestSensitiveMetadataOmitted(t *testing.T) {
	e, err := Parse(strings.NewReader(`{"session_id":"s","hook_event_name":"SessionStart","cwd":"/home/user/token/abc","source":"password=hunter2","tool_name":"sk-123456789secret"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if e.Cwd != "" || strings.Contains(string(e.Data), "hunter2") || strings.Contains(string(e.Data), "sk-") {
		t.Fatalf("sensitive metadata: %+v", e)
	}
}

func TestBashExitOnly(t *testing.T) {
	e, err := Parse(strings.NewReader(`{"session_id":"s","hook_event_name":"PostToolUse","tool_name":"Bash","tool_response":"Chunk ID: x\nProcess exited with code 7\nOutput:\npassword=hunter2"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(e.Data), `"exit_code":7`) || !strings.Contains(string(e.Data), "tool-exit-7") || strings.Contains(string(e.Data), "hunter2") {
		t.Fatalf("data: %s", e.Data)
	}
}

func TestNullHookMetadataRemainsUnknown(t *testing.T) {
	for _, payload := range []string{
		`{"session_id":"s","hook_event_name":"PostToolUse","duration_ms":null,"is_error":null,"tool_response":{"exit_code":null,"isError":null}}`,
		`{"session_id":"s","hook_event_name":"PostModelRequest","token_usage":{"total_tokens":null}}`,
	} {
		e, err := Parse(strings.NewReader(payload), time.Now())
		if err != nil || string(e.Data) != "{}" {
			t.Fatalf("null metadata became an observation: %s %v", e.Data, err)
		}
	}
}

func TestOpenRestrictsExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("database permissions = %o", info.Mode().Perm())
	}
}
