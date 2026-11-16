package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"verity/internal/domain"
	"verity/internal/process/workflow"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct{ db *sql.DB }

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err = s.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func CheckReadOnly(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro&_foreign_keys=on")
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}
func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL); CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL, name TEXT NOT NULL, root TEXT NOT NULL, created_at TEXT NOT NULL); CREATE TABLE IF NOT EXISTS workflows(id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), schema_version INTEGER NOT NULL, name TEXT NOT NULL, state TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL); CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL REFERENCES workflows(id), schema_version INTEGER NOT NULL, body TEXT NOT NULL); CREATE TABLE IF NOT EXISTS candidates(id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL REFERENCES workflows(id), schema_version INTEGER NOT NULL, revision TEXT NOT NULL, created_at TEXT NOT NULL); CREATE TABLE IF NOT EXISTS policies(id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL, body TEXT NOT NULL); CREATE TABLE IF NOT EXISTS workflow_events(id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL REFERENCES workflows(id), schema_version INTEGER NOT NULL, sequence INTEGER NOT NULL, from_state TEXT NOT NULL, to_state TEXT NOT NULL, event TEXT NOT NULL, correlation_id TEXT NOT NULL, actor TEXT NOT NULL, reason TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(workflow_id, sequence)); CREATE TRIGGER IF NOT EXISTS workflow_events_no_update BEFORE UPDATE ON workflow_events BEGIN SELECT RAISE(ABORT, 'workflow events are append-only'); END; CREATE TRIGGER IF NOT EXISTS workflow_events_no_delete BEFORE DELETE ON workflow_events BEGIN SELECT RAISE(ABORT, 'workflow events are append-only'); END; CREATE TABLE IF NOT EXISTS process_definitions(id TEXT NOT NULL, version INTEGER NOT NULL, body TEXT NOT NULL, PRIMARY KEY(id,version)); CREATE TABLE IF NOT EXISTS process_runs(id TEXT PRIMARY KEY, definition_id TEXT NOT NULL, definition_version INTEGER NOT NULL, body TEXT NOT NULL); CREATE TABLE IF NOT EXISTS scheduler_leases(run_id TEXT PRIMARY KEY REFERENCES process_runs(id), owner TEXT NOT NULL, expires_at TEXT NOT NULL); CREATE TABLE IF NOT EXISTS proof_evidence(id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL, candidate_sha TEXT NOT NULL, body TEXT NOT NULL); CREATE TABLE IF NOT EXISTS proof_audit(workflow_id TEXT NOT NULL, sequence INTEGER NOT NULL, body TEXT NOT NULL, PRIMARY KEY(workflow_id,sequence)); CREATE TABLE IF NOT EXISTS verification_runs(id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL, candidate_sha TEXT NOT NULL, body TEXT NOT NULL); CREATE TABLE IF NOT EXISTS github_webhook_events(delivery_id TEXT PRIMARY KEY, event_type TEXT NOT NULL, received_at TEXT NOT NULL); CREATE TABLE IF NOT EXISTS github_pull_workflows(repository TEXT NOT NULL, pull_number INTEGER NOT NULL, head_sha TEXT NOT NULL, workflow_id TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(repository,pull_number,head_sha)); CREATE TABLE IF NOT EXISTS vigil_links(attempt_id TEXT PRIMARY KEY, workflow_id TEXT NOT NULL, candidate_sha TEXT NOT NULL, body TEXT NOT NULL); CREATE TRIGGER IF NOT EXISTS vigil_links_no_update BEFORE UPDATE ON vigil_links BEGIN SELECT RAISE(ABORT, 'Vigil links are append-only'); END; CREATE TRIGGER IF NOT EXISTS vigil_links_no_delete BEFORE DELETE ON vigil_links BEGIN SELECT RAISE(ABORT, 'Vigil links are append-only'); END; CREATE TRIGGER IF NOT EXISTS proof_audit_no_update BEFORE UPDATE ON proof_audit BEGIN SELECT RAISE(ABORT, 'proof audit is append-only'); END; CREATE TRIGGER IF NOT EXISTS proof_audit_no_delete BEFORE DELETE ON proof_audit BEGIN SELECT RAISE(ABORT, 'proof audit is append-only'); END; INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(1,CURRENT_TIMESTAMP); INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(2,CURRENT_TIMESTAMP); INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(3,CURRENT_TIMESTAMP); INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(4,CURRENT_TIMESTAMP); INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(5,CURRENT_TIMESTAMP);`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS execution_attempts(id TEXT PRIMARY KEY,workflow_id TEXT NOT NULL,task_id TEXT NOT NULL,attempt INTEGER NOT NULL,provider TEXT NOT NULL,body TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(task_id,attempt)); CREATE TABLE IF NOT EXISTS execution_attempt_events(id INTEGER PRIMARY KEY AUTOINCREMENT,attempt_id TEXT NOT NULL REFERENCES execution_attempts(id),kind TEXT NOT NULL,state TEXT NOT NULL,payload TEXT NOT NULL,actor TEXT NOT NULL,reason TEXT NOT NULL,created_at TEXT NOT NULL); CREATE INDEX IF NOT EXISTS execution_attempt_events_latest ON execution_attempt_events(attempt_id,id); CREATE TRIGGER IF NOT EXISTS execution_attempt_events_no_update BEFORE UPDATE ON execution_attempt_events BEGIN SELECT RAISE(ABORT, 'execution attempt events are append-only'); END; CREATE TRIGGER IF NOT EXISTS execution_attempt_events_no_delete BEFORE DELETE ON execution_attempt_events BEGIN SELECT RAISE(ABORT, 'execution attempt events are append-only'); END; INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(6,CURRENT_TIMESTAMP);`)
	if err != nil {
		return err
	}
	return s.migrateExecutionScope(ctx)
}
func (s *Store) CreateProject(ctx context.Context, p domain.Project) error {
	if err := p.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO projects VALUES(?,?,?,?,?)", p.ID, p.SchemaVersion, p.Name, p.Root, p.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) GetProject(ctx context.Context, id domain.ID) (domain.Project, error) {
	var p domain.Project
	var created string
	err := s.db.QueryRowContext(ctx, "SELECT id,schema_version,name,root,created_at FROM projects WHERE id=?", id).Scan(&p.ID, &p.SchemaVersion, &p.Name, &p.Root, &created)
	if err != nil {
		return p, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return p, nil
}
func (s *Store) CreateWorkflow(ctx context.Context, w domain.Workflow) error {
	if err := w.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO workflows VALUES(?,?,?,?,?,?,?)", w.ID, w.ProjectID, w.SchemaVersion, w.Name, w.State, w.CreatedAt.UTC().Format(time.RFC3339Nano), w.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) GetWorkflow(ctx context.Context, id domain.ID) (domain.Workflow, error) {
	var w domain.Workflow
	var c, u string
	err := s.db.QueryRowContext(ctx, "SELECT id,project_id,schema_version,name,state,created_at,updated_at FROM workflows WHERE id=?", id).Scan(&w.ID, &w.ProjectID, &w.SchemaVersion, &w.Name, &w.State, &c, &u)
	if err != nil {
		return w, err
	}
	w.CreatedAt, _ = time.Parse(time.RFC3339Nano, c)
	w.UpdatedAt, _ = time.Parse(time.RFC3339Nano, u)
	return w, nil
}
func (s *Store) Transition(ctx context.Context, id domain.ID, event workflow.Event, correlationID, actor, reason string) (domain.WorkflowEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.WorkflowEvent{}, err
	}
	defer tx.Rollback()
	var state workflow.State
	err = tx.QueryRowContext(ctx, "SELECT state FROM workflows WHERE id=?", id).Scan(&state)
	if err != nil {
		return domain.WorkflowEvent{}, err
	}
	next, err := workflow.Transition(state, event)
	if err != nil {
		return domain.WorkflowEvent{}, err
	}
	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, "UPDATE workflows SET state=?,updated_at=? WHERE id=? AND state=?", next, now.Format(time.RFC3339Nano), id, state)
	if err != nil {
		return domain.WorkflowEvent{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return domain.WorkflowEvent{}, fmt.Errorf("concurrent workflow update")
	}
	var seq int64
	_ = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0)+1 FROM workflow_events WHERE workflow_id=?", id).Scan(&seq)
	eid, err := domain.NewID("evt")
	if err != nil {
		return domain.WorkflowEvent{}, err
	}
	e := domain.WorkflowEvent{ID: eid, WorkflowID: id, SchemaVersion: domain.SchemaVersion, Sequence: seq, From: state, To: next, Event: event, CorrelationID: correlationID, Actor: actor, Reason: reason, CreatedAt: now}
	_, err = tx.ExecContext(ctx, "INSERT INTO workflow_events VALUES(?,?,?,?,?,?,?,?,?,?,?)", e.ID, e.WorkflowID, e.SchemaVersion, e.Sequence, e.From, e.To, e.Event, e.CorrelationID, e.Actor, e.Reason, now.Format(time.RFC3339Nano))
	if err != nil {
		return domain.WorkflowEvent{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.WorkflowEvent{}, err
	}
	return e, nil
}
func (s *Store) Events(ctx context.Context, id domain.ID) ([]domain.WorkflowEvent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,workflow_id,schema_version,sequence,from_state,to_state,event,correlation_id,actor,reason,created_at FROM workflow_events WHERE workflow_id=? ORDER BY sequence", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.WorkflowEvent{}
	for rows.Next() {
		var e domain.WorkflowEvent
		var t string
		if err = rows.Scan(&e.ID, &e.WorkflowID, &e.SchemaVersion, &e.Sequence, &e.From, &e.To, &e.Event, &e.CorrelationID, &e.Actor, &e.Reason, &t); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, t)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) CreateTask(ctx context.Context, t domain.Task) error {
	if err := t.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO tasks VALUES(?,?,?,?)", t.ID, t.WorkflowID, t.SchemaVersion, string(b))
	return err
}
