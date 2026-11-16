package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookProcess(t *testing.T) {
	if os.Getenv("VIGIL_TEST_HELPER") != "1" {
		return
	}
	os.Exit(run(os.Args[3:]))
}
func TestHookIntegration(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "Vigil's config.yaml")
	db := filepath.Join(dir, "events.db")
	if err := os.WriteFile(cfg, []byte("version: 1\nmode: observe\nstorage:\n  path: "+db+"\n  retention_days: 30\n"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(input string, args ...string) (string, string, error) {
		a := append([]string{"-test.run=^TestHookProcess$", "--"}, args...)
		c := exec.Command(os.Args[0], a...)
		c.Env = append(os.Environ(), "VIGIL_TEST_HELPER=1")
		c.Stdin = strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		c.Stdout = &stdout
		c.Stderr = &stderr
		err := c.Run()
		return stdout.String(), stderr.String(), err
	}
	payload := `{"session_id":"integration","turn_id":"t","hook_event_name":"PostToolUse","tool_use_id":"id","tool_response":{"exit_code":1,"output":"private value"}}`
	for i := 0; i < 2; i++ {
		out, _, err := invoke(payload, "hook", "--config", cfg)
		if err != nil || out != "" {
			t.Fatalf("hook: %q %v", out, err)
		}
	}
	out, _, err := invoke("", "summary", "--session", "integration", "--config", cfg)
	if err != nil {
		t.Fatal(err)
	}
	var summary struct {
		EventCounts  map[string]int `json:"event_counts"`
		ToolFailures int            `json:"tool_failures"`
	}
	if err = json.Unmarshal([]byte(out), &summary); err != nil || summary.EventCounts["PostToolUse"] != 1 || summary.ToolFailures != 1 {
		t.Fatalf("summary: %s %v", out, err)
	}
	if _, _, err = invoke("", "pause", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	if _, _, err = invoke("", "mode", "assist", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	out, _, err = invoke("", "mcp", "--config", cfg)
	if err != nil || out != "" {
		t.Fatalf("empty MCP: %q %v", out, err)
	}
	out, _, err = invoke(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vigil_status","arguments":{}}}`+"\n", "mcp", "--config", cfg)
	if err != nil || !strings.Contains(out, `\"paused\":true`) || !strings.Contains(out, `\"mode\":\"assist\"`) {
		t.Fatalf("MCP status: %s %v", out, err)
	}
	out, _, err = invoke("", "history", "--config", cfg)
	if err != nil || !strings.Contains(out, "PostToolUse") || strings.Contains(out, "private value") {
		t.Fatalf("history: %s %v", out, err)
	}
	if _, _, err = invoke("", "resume", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	secondFailure := strings.Replace(payload, `"tool_use_id":"id"`, `"tool_use_id":"second"`, 1)
	if _, _, err = invoke(secondFailure, "hook", "--config", cfg); err != nil {
		t.Fatal(err)
	}
	stop := `{"session_id":"integration","turn_id":"t","hook_event_name":"Stop","stop_hook_active":false}`
	out, _, err = invoke(stop, "hook", "--config", cfg)
	var notice struct {
		SystemMessage string `json:"systemMessage"`
	}
	if err != nil || json.Unmarshal([]byte(out), &notice) != nil {
		t.Fatalf("Assist Stop: %q %v", out, err)
	}
	want := `--config '` + dir + `/Vigil'"'"'s config.yaml'`
	if !strings.Contains(notice.SystemMessage, want) {
		t.Fatalf("Assist Stop should use the active config: %q", notice.SystemMessage)
	}
	if _, _, err = invoke("{", "hook", "--config", cfg); err != nil {
		t.Fatalf("malformed hook blocked: %v", err)
	}
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	badStorage := filepath.Join(dir, "bad-storage.yaml")
	if err := os.WriteFile(badStorage, []byte("storage:\n  path: "+filepath.Join(blocked, "db")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := invoke(payload, "hook", "--config", badStorage)
	if err != nil || !strings.Contains(stderr, "event storage unavailable") {
		t.Fatalf("storage failure: %q %v", stderr, err)
	}
	bad := filepath.Join(dir, "missing", "config.yaml")
	if _, _, err = invoke(payload, "hook", "--config", bad); err != nil {
		t.Fatalf("storage/config failure blocked: %v", err)
	}
}
