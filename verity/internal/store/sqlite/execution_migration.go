package sqlite

import "context"

// migrateExecutionScope preserves attempt and event history while changing the
// attempt sequence from globally scoped task IDs to workflow-local task IDs.
func (s *Store) migrateExecutionScope(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=7`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `
CREATE TABLE execution_attempts_v7(id TEXT PRIMARY KEY,workflow_id TEXT NOT NULL,task_id TEXT NOT NULL,attempt INTEGER NOT NULL,provider TEXT NOT NULL,body TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(workflow_id,task_id,attempt));
INSERT INTO execution_attempts_v7 SELECT * FROM execution_attempts;
CREATE TABLE execution_attempt_events_v7(id INTEGER PRIMARY KEY AUTOINCREMENT,attempt_id TEXT NOT NULL REFERENCES execution_attempts_v7(id),kind TEXT NOT NULL,state TEXT NOT NULL,payload TEXT NOT NULL,actor TEXT NOT NULL,reason TEXT NOT NULL,created_at TEXT NOT NULL);
INSERT INTO execution_attempt_events_v7 SELECT * FROM execution_attempt_events;
DROP TABLE execution_attempt_events;
DROP TABLE execution_attempts;
ALTER TABLE execution_attempts_v7 RENAME TO execution_attempts;
ALTER TABLE execution_attempt_events_v7 RENAME TO execution_attempt_events;
CREATE INDEX execution_attempt_events_latest ON execution_attempt_events(attempt_id,id);
CREATE TRIGGER execution_attempt_events_no_update BEFORE UPDATE ON execution_attempt_events BEGIN SELECT RAISE(ABORT, 'execution attempt events are append-only'); END;
CREATE TRIGGER execution_attempt_events_no_delete BEFORE DELETE ON execution_attempt_events BEGIN SELECT RAISE(ABORT, 'execution attempt events are append-only'); END;
INSERT INTO schema_migrations(version,applied_at) VALUES(7,CURRENT_TIMESTAMP);`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
