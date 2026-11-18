package sqlite

import "context"

// migratePullJobHistory allows authorized manual runs to be recorded for an
// exact PR head while preserving delivery uniqueness and all prior jobs.
func (s *Store) migratePullJobHistory(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=9`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `
CREATE TABLE github_pull_jobs_v9(workflow_id TEXT PRIMARY KEY, repository TEXT NOT NULL, pull_number INTEGER NOT NULL, head_sha TEXT NOT NULL, delivery_id TEXT NOT NULL UNIQUE, payload TEXT NOT NULL, state TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, started_at TEXT, next_attempt TEXT NOT NULL, last_error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
INSERT INTO github_pull_jobs_v9 SELECT workflow_id,repository,pull_number,head_sha,delivery_id,payload,state,attempts,started_at,next_attempt,last_error,created_at,updated_at FROM github_pull_jobs;
DROP TABLE github_pull_jobs;
ALTER TABLE github_pull_jobs_v9 RENAME TO github_pull_jobs;
CREATE INDEX github_pull_jobs_ready ON github_pull_jobs(state,next_attempt,created_at);
INSERT INTO schema_migrations(version,applied_at) VALUES(9,CURRENT_TIMESTAMP);`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
