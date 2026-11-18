package sqlite

import "context"

// migrateManualTriggerAudit stores the authenticated actor independently of
// the mutable worker queue and makes the trigger record append-only.
func (s *Store) migrateManualTriggerAudit(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=10`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `
CREATE TABLE github_manual_triggers(delivery_id TEXT PRIMARY KEY REFERENCES github_webhook_events(delivery_id), actor TEXT NOT NULL, repository TEXT NOT NULL, pull_number INTEGER NOT NULL, head_sha TEXT NOT NULL, workflow_id TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL);
CREATE TRIGGER github_manual_triggers_no_update BEFORE UPDATE ON github_manual_triggers BEGIN SELECT RAISE(ABORT, 'GitHub manual trigger records are append-only'); END;
CREATE TRIGGER github_manual_triggers_no_delete BEFORE DELETE ON github_manual_triggers BEGIN SELECT RAISE(ABORT, 'GitHub manual trigger records are append-only'); END;
INSERT INTO schema_migrations(version,applied_at) VALUES(10,CURRENT_TIMESTAMP);`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
