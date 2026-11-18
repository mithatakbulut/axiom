package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"vigil/internal/config"
	"vigil/internal/control"
	"vigil/internal/recorder"
)

func TestServerControlAndPersistence(t *testing.T) {
	c, _ := config.Default()
	c.Storage.Path = filepath.Join(t.TempDir(), "vigil.db")
	s, err := recorder.Open(c.Storage.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"vigil_pause","arguments":{}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"vigil_set_mode","arguments":{"mode":"assist"}}}
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"vigil_status","arguments":{}}}
{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"vigil_budget","arguments":{"session_id":"fixture"}}}
{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"vigil_eval","arguments":{}}}
{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"vigil_set_mode","arguments":{"mode":"invalid"}}}
`)
	var out bytes.Buffer
	svc := control.Service{Config: c, Store: s}
	if err = Serve(context.Background(), input, &out, svc); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 8 {
		t.Fatalf("responses: %s", out.String())
	}
	var last map[string]any
	if err = json.Unmarshal([]byte(lines[7]), &last); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lines[7], `"isError":true`) {
		t.Fatal(lines[7])
	}
	if !strings.Contains(lines[5], `\"observed_tokens\":null`) || !strings.Contains(lines[6], `\"passed\":10`) {
		t.Fatalf("budget or eval: %s %s", lines[5], lines[6])
	}
	st, err := svc.Status(context.Background())
	if err != nil || !st.Paused || st.Mode != "assist" || st.InterventionAvailable {
		t.Fatalf("status %+v: %v", st, err)
	}
}

func TestCorrelateToolIsVersionedAndReturnsOnlySessionAssociation(t *testing.T) {
	c, _ := config.Default()
	s, err := recorder.Open(filepath.Join(t.TempDir(), "vigil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e, err := recorder.Parse(strings.NewReader(`{"hook_event_name":"UserPromptSubmit","session_id":"session-1","turn_id":"turn-1"}`), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Record(context.Background(), e, 30); err != nil {
		t.Fatal(err)
	}
	start, end := e.Timestamp.Add(-time.Second), e.Timestamp.Add(time.Second)
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"vigil_correlate_session","arguments":{"schema_version":1,"session_id":"session-1","not_before":"` + start.Format(time.RFC3339Nano) + `","not_after":"` + end.Format(time.RFC3339Nano) + `"}}}` + "\n")
	var out bytes.Buffer
	if err = Serve(context.Background(), input, &out, control.Service{Config: c, Store: s}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `session-1`) || !strings.Contains(out.String(), `turn-1`) {
		t.Fatalf("unexpected correlation response: %s", out.String())
	}
}
