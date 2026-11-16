package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"verity/internal/process/control"
)

type DashboardTask struct {
	ID    string            `json:"id"`
	Title string            `json:"title"`
	State control.TaskState `json:"state"`
}
type DashboardRun struct {
	ID        string                `json:"id"`
	Title     string                `json:"title"`
	State     control.WorkflowState `json:"state"`
	CreatedAt time.Time             `json:"created_at"`
	Tasks     []DashboardTask       `json:"tasks"`
}
type DashboardSummary struct {
	SchemaVersion int            `json:"schema_version"`
	ProjectID     string         `json:"project_id"`
	ProjectName   string         `json:"project_name"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Runs          []DashboardRun `json:"runs"`
}

// Dashboard reads the schema version and all runs from one SQLite snapshot.
func Dashboard(ctx context.Context, path, projectID, projectName string) (DashboardSummary, error) {
	out := DashboardSummary{SchemaVersion: 1, ProjectID: projectID, ProjectName: projectName, Runs: []DashboardRun{}}
	abs, err := filepath.Abs(path)
	if err != nil {
		return out, err
	}
	uri := (&url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&_busy_timeout=5000"}).String()
	db, err := sql.Open("sqlite3", uri)
	if err != nil {
		return out, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version); err != nil {
		return out, fmt.Errorf("read database schema: %w", err)
	}
	if version < 1 || version > 7 {
		return out, fmt.Errorf("unsupported database schema version %d", version)
	}
	rows, err := tx.QueryContext(ctx, "SELECT body FROM process_runs ORDER BY id")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		var run control.Run
		if err = rows.Scan(&body); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(body), &run); err != nil {
			return out, fmt.Errorf("invalid run record: %w", err)
		}
		item := DashboardRun{ID: run.ID, Title: run.Definition.Name, State: run.State, CreatedAt: run.CreatedAt, Tasks: []DashboardTask{}}
		for _, task := range run.Tasks {
			item.Tasks = append(item.Tasks, DashboardTask{ID: task.ID, Title: task.Title, State: task.State})
		}
		out.Runs = append(out.Runs, item)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.GeneratedAt = time.Now().UTC()
	return out, nil
}
