package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"verity/internal/process/control"
)

func TestDashboardReadOnlyAndWAL(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	writer, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	run := control.Run{ID: "run", Definition: control.Definition{Name: "title"}, State: control.Running, CreatedAt: time.Now(), Tasks: []control.Task{{ID: "a", Title: "A", State: control.TaskRunning, Description: "secret"}, {ID: "b", Title: "B", State: control.TaskWaiting}}}
	if err := writer.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	got, err := Dashboard(ctx, path, "project", "Project")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Runs) != 1 || len(got.Runs[0].Tasks) != 2 || got.Runs[0].Tasks[1].State != control.TaskWaiting {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "secret") {
		t.Fatal("private field leaked")
	}
}
func TestDashboardMissingDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if _, err := Dashboard(context.Background(), path, "p", "p"); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("database created: %v", err)
	}
}
func TestDashboardRejectsFutureSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec("INSERT INTO schema_migrations VALUES(999,CURRENT_TIMESTAMP)"); err != nil {
		t.Fatal(err)
	}
	if _, err := Dashboard(ctx, path, "p", "p"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected schema error: %v", err)
	}
}

func TestDashboardConcurrentWALWriter(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "concurrent.sqlite")
	writer, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	run := control.Run{ID: "run", Definition: control.Definition{Name: "parallel"}, State: control.Ready, CreatedAt: time.Now(), Tasks: []control.Task{{ID: "one", Title: "one", State: control.TaskReady}, {ID: "two", Title: "two", State: control.TaskReady}}}
	if err := writer.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 30; i++ {
			run.Transitions = i + 1
			if i%2 == 0 {
				run.State = control.Running
				run.Tasks[0].State = control.TaskRunning
				run.Tasks[1].State = control.TaskRunning
			} else {
				run.State = control.Ready
				run.Tasks[0].State = control.TaskReady
				run.Tasks[1].State = control.TaskReady
			}
			if err := writer.UpdateRun(ctx, run, i); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 30; i++ {
		got, err := Dashboard(ctx, path, "p", "p")
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Runs) != 1 || len(got.Runs[0].Tasks) != 2 || got.Runs[0].Tasks[0].State != got.Runs[0].Tasks[1].State {
			t.Fatalf("inconsistent snapshot: %+v", got)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
