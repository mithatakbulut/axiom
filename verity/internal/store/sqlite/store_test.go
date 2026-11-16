package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"verity/internal/domain"
	"verity/internal/process/workflow"
	"verity/internal/proof"
)

func TestMigrateCreateTransitionRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "verity.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	pid, _ := domain.NewID("prj")
	wid, _ := domain.NewID("wf")
	if err = s.CreateProject(ctx, domain.Project{ID: pid, SchemaVersion: 1, Name: "demo", Root: ".", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateWorkflow(ctx, domain.Workflow{ID: wid, ProjectID: pid, SchemaVersion: 1, Name: "change", State: workflow.Draft, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Transition(ctx, wid, workflow.ContractApproved, "corr-1", "user", "approved"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Transition(ctx, wid, workflow.PostDeployPassed, "corr-2", "user", ""); err == nil {
		t.Fatal("invalid transition accepted")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, err := s.GetWorkflow(ctx, wid)
	if err != nil {
		t.Fatal(err)
	}
	if w.State != workflow.Ready {
		t.Fatalf("state after restart=%s", w.State)
	}
	events, err := s.Events(ctx, wid)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Sequence != 1 {
		t.Fatalf("events=%+v", events)
	}
	if _, err = s.db.ExecContext(ctx, "DELETE FROM workflow_events WHERE id=?", events[0].ID); err == nil {
		t.Fatal("append-only event was deleted")
	}
}

func BenchmarkSQLiteSaveEvidence(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(b.TempDir(), "benchmark.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	sha := "0000000000000000000000000000000000000000"
	content := []byte("benchmark evidence payload")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e, err := proof.NewEvidence(fmt.Sprintf("ev-%d", i), "wf-benchmark", sha, "check-log", "benchmark", time.Now(), content)
		if err != nil {
			b.Fatal(err)
		}
		if err = s.SaveEvidence(ctx, e); err != nil {
			b.Fatal(err)
		}
	}
}
