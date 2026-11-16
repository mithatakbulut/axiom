package recorder

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const migration3 = `
CREATE INDEX events_turn_time ON events(session_id,turn_id,timestamp);
CREATE INDEX events_type_time ON events(session_id,event_type,timestamp);
CREATE TABLE progress_snapshots(session_id TEXT NOT NULL, turn_id TEXT NOT NULL, recorded_at TEXT NOT NULL, data TEXT NOT NULL, PRIMARY KEY(session_id,turn_id));
CREATE TABLE escalations(id INTEGER PRIMARY KEY, session_id TEXT NOT NULL, task_id TEXT NOT NULL, turn_id TEXT NOT NULL, model TEXT NOT NULL, created_at TEXT NOT NULL, state TEXT NOT NULL, automatic INTEGER NOT NULL DEFAULT 0, detail TEXT NOT NULL DEFAULT '');
CREATE INDEX escalations_session_task ON escalations(session_id,task_id,turn_id);
CREATE INDEX escalations_time ON escalations(created_at);
CREATE TABLE resource_snapshots(session_id TEXT PRIMARY KEY, recorded_at TEXT NOT NULL, data TEXT NOT NULL);
CREATE TABLE tasks(session_id TEXT NOT NULL, task_id TEXT NOT NULL, title TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(session_id,task_id));
CREATE TABLE requirements(session_id TEXT NOT NULL, task_id TEXT NOT NULL, requirement_id TEXT NOT NULL, description TEXT NOT NULL, PRIMARY KEY(session_id,task_id,requirement_id), FOREIGN KEY(session_id,task_id) REFERENCES tasks ON DELETE CASCADE);
CREATE TABLE evidence(id INTEGER PRIMARY KEY, session_id TEXT NOT NULL, task_id TEXT NOT NULL, requirement_id TEXT NOT NULL, kind TEXT NOT NULL, source TEXT NOT NULL, result TEXT NOT NULL, state TEXT NOT NULL, event_id TEXT REFERENCES events ON DELETE SET NULL, recorded_at TEXT NOT NULL, FOREIGN KEY(session_id,task_id,requirement_id) REFERENCES requirements ON DELETE CASCADE);
CREATE INDEX evidence_requirement ON evidence(session_id,task_id,requirement_id,id);
INSERT INTO schema_migrations VALUES(3,strftime('%Y-%m-%dT%H:%M:%fZ','now'));
`

// Preserve only the fields used to validate a linked event; raw tool output is never stored.
const migration4 = `
ALTER TABLE evidence ADD COLUMN event_summary TEXT;
UPDATE evidence SET event_summary=(SELECT json_object('event_id',events.event_id,'timestamp',events.timestamp,'exit_code',json_extract(events.data,'$.exit_code'),'is_error',json_extract(events.data,'$.is_error'),'test_passed',json_extract(events.data,'$.test_passed'),'test_failed',json_extract(events.data,'$.test_failed')) FROM events WHERE events.event_id=evidence.event_id) WHERE event_id IS NOT NULL;
UPDATE evidence SET state='evidenced' WHERE state='verified' AND kind IN ('unit_test','integration_test','e2e_test') AND event_summary IS NULL;
INSERT INTO schema_migrations VALUES(4,strftime('%Y-%m-%dT%H:%M:%fZ','now'));
`

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

func (s *Store) SaveProgress(ctx context.Context, session, turn string, now time.Time, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO progress_snapshots VALUES(?,?,?,?) ON CONFLICT(session_id,turn_id) DO UPDATE SET recorded_at=excluded.recorded_at,data=excluded.data WHERE excluded.recorded_at>=progress_snapshots.recorded_at`, session, turn, stamp(now), string(raw))
	return err
}
func (s *Store) LastProgress(ctx context.Context, session string) (json.RawMessage, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM progress_snapshots WHERE (?='' OR session_id=?) ORDER BY recorded_at DESC LIMIT 1`, session, session).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return json.RawMessage(raw), err
}

type Reservation struct {
	Session, Task, Turn, Model, DefaultMode string
	Automatic                               bool
	Approved                                bool
	RequireApproval                         bool
	Shadow                                  bool
	PerTurn, PerTask, Parallel              int
	Cooldown                                time.Duration
	Lease                                   time.Duration
}
type Escalation struct {
	ID      int64  `json:"id"`
	Session string `json:"session_id"`
	Task    string `json:"task_id,omitempty"`
	Turn    string `json:"turn_id"`
	Model   string `json:"model"`
	At      string `json:"recorded_at"`
	State   string `json:"state"`
	Detail  string `json:"detail,omitempty"`
}

// One write statement both checks policy and reserves a slot. SQLite serializes
// competing writers, even when hooks execute in separate processes.
func (s *Store) Reserve(ctx context.Context, q Reservation, now time.Time) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
 INSERT INTO escalations(session_id,task_id,turn_id,model,created_at,state,automatic)
 SELECT ?,?,?,?,?,'running',?
 WHERE COALESCE((SELECT value FROM settings WHERE key='paused'),'false')='false'
 AND (CASE WHEN ? THEN COALESCE((SELECT value FROM settings WHERE key='mode'),?)='autonomous' AND NOT ? AND NOT ?
      ELSE COALESCE((SELECT value FROM settings WHERE key='mode'),?) IN ('assist','autonomous') AND (? OR (COALESCE((SELECT value FROM settings WHERE key='mode'),?)='autonomous' AND NOT ?)) END)
 AND (NOT ? OR NOT EXISTS(SELECT 1 FROM escalations WHERE session_id=? AND turn_id=? AND automatic=1))
 AND (SELECT count(*) FROM escalations WHERE session_id=? AND turn_id=?) < ?
 AND (SELECT count(*) FROM escalations WHERE session_id=? AND task_id=?) < ?
 AND NOT EXISTS(SELECT 1 FROM escalations WHERE session_id=? AND created_at>?)
 AND (SELECT count(*) FROM escalations WHERE state='running' AND created_at>?) < ?
 RETURNING id`, q.Session, q.Task, q.Turn, q.Model, stamp(now), q.Automatic,
		q.Automatic, q.DefaultMode, q.RequireApproval, q.Shadow, q.DefaultMode, q.Approved, q.DefaultMode, q.RequireApproval,
		q.Automatic, q.Session, q.Turn, q.Session, q.Turn, q.PerTurn, q.Session, q.Task, q.PerTask, q.Session, stamp(now.Add(-q.Cooldown)), stamp(now.Add(-q.Lease)), q.Parallel).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}
func (s *Store) FinishAdvisor(ctx context.Context, id int64, state, detail string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE escalations SET state=?, detail=? WHERE id=? AND state='running'`, state, detail, id)
	return err
}
func (s *Store) LastAdvisor(ctx context.Context, session string) (*Escalation, error) {
	var e Escalation
	err := s.db.QueryRowContext(ctx, `SELECT id,session_id,task_id,turn_id,model,created_at,state,detail FROM escalations WHERE (?='' OR session_id=?) ORDER BY id DESC LIMIT 1`, session, session).Scan(&e.ID, &e.Session, &e.Task, &e.Turn, &e.Model, &e.At, &e.State, &e.Detail)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &e, err
}

func (s *Store) TurnObservedAt(ctx context.Context, session, turn string) (time.Time, error) {
	var at sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT min(timestamp) FROM events WHERE session_id=? AND turn_id=?`, session, turn).Scan(&at)
	if err != nil {
		return time.Time{}, err
	}
	if !at.Valid {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, at.String)
}

func (s *Store) SaveResources(ctx context.Context, session string, now time.Time, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO resource_snapshots VALUES(?,?,?) ON CONFLICT(session_id) DO UPDATE SET recorded_at=excluded.recorded_at,data=excluded.data WHERE excluded.recorded_at>=resource_snapshots.recorded_at`, session, stamp(now), string(raw))
	return err
}
