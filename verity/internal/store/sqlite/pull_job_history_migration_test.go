package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"verity/internal/process/control"
)

func TestPullJobHistoryMigrationAndManualRetrigger(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at TEXT NOT NULL);
INSERT INTO schema_migrations(version,applied_at) VALUES(1,'now'),(2,'now'),(3,'now'),(4,'now'),(5,'now'),(6,'now'),(7,'now'),(8,'now');
CREATE TABLE github_pull_jobs(workflow_id TEXT PRIMARY KEY,repository TEXT NOT NULL,pull_number INTEGER NOT NULL,head_sha TEXT NOT NULL,delivery_id TEXT NOT NULL UNIQUE,payload TEXT NOT NULL,state TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0,started_at TEXT,next_attempt TEXT NOT NULL,last_error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(repository,pull_number,head_sha));
CREATE INDEX github_pull_jobs_ready ON github_pull_jobs(state,next_attempt,created_at);
INSERT INTO github_pull_jobs VALUES('old-run','acme/app',5,?,'old-delivery','{}','COMPLETED',1,NULL,'2026-01-01T00:00:00Z','','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');`, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	old, err := store.PullJob(ctx, "old-run")
	if err != nil || old.State != "COMPLETED" || old.HeadSHA != strings.Repeat("a", 40) {
		t.Fatalf("migration lost old pull job: %#v %v", old, err)
	}

	now := time.Now().UTC()
	sha := strings.Repeat("a", 40)
	definition := control.Definition{SchemaVersion: 1, ID: "manual-definition", Name: "manual", Version: 1, Tasks: []control.Task{{ID: "verify", Title: "Verify", MaxAttempts: 1}}, MaxTasks: 1, MaxAttempts: 1, MaxTransitions: 8}
	run, err := control.NewRun("manual-run", definition, now)
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"trigger":"manual","actor":"maintainer"}`
	if err = store.CreateManualPullWorkflowJob(ctx, "manual-delivery", "maintainer", "acme/app", 5, sha, payload, run); err != nil {
		t.Fatal(err)
	}
	if err = store.CreateManualPullWorkflowJob(ctx, "manual-delivery", "maintainer", "acme/app", 5, sha, payload, run); err != nil {
		t.Fatalf("duplicate manual delivery was not idempotent: %v", err)
	}
	if _, err = store.PullJob(ctx, "manual-run"); err != nil {
		t.Fatal(err)
	}
	links, err := store.PullWorkflows(ctx, "acme/app")
	if err != nil || len(links) != 1 || links[0].ManualActor != "maintainer" || links[0].WorkflowID != "manual-run" {
		t.Fatalf("manual trigger association=%#v err=%v", links, err)
	}
	if _, err = store.db.ExecContext(ctx, "UPDATE github_manual_triggers SET actor='tampered'"); err == nil {
		t.Fatal("manual trigger audit record was mutable")
	}
	parallel, err := control.NewRun("parallel-run", control.Definition{SchemaVersion: 1, ID: "parallel-definition", Name: "parallel", Version: 1, Tasks: []control.Task{{ID: "verify", Title: "Verify", MaxAttempts: 1}}, MaxTasks: 1, MaxAttempts: 1, MaxTransitions: 8}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateManualPullWorkflowJob(ctx, "parallel-delivery", "maintainer", "acme/app", 5, sha, payload, parallel); err == nil {
		t.Fatal("parallel manual retrigger was accepted while the prior job was pending")
	}
}
