package handoff

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"verity/internal/execution"
)

func TestWriteHandoff(t *testing.T) {
	req := execution.Request{ID: "exec", WorkflowID: "wf", TaskID: "task", Workspace: "/tmp/ws", Attempt: 1, BaseSHA: "0123456789abcdef0123456789abcdef01234567", Timeout: time.Minute}
	p, err := New(req, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "handoff.json")
	if err := Write(path, p); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Package
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Request.BaseSHA != req.BaseSHA || got.SchemaVersion != 1 {
		t.Fatalf("bad handoff: %+v", got)
	}
}
