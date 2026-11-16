package recorder

import (
	"context"
	"database/sql"
	"encoding/json"
)

type HistoryEvent struct {
	ID        string          `json:"event_id"`
	Session   string          `json:"session_id"`
	Turn      string          `json:"turn_id,omitempty"`
	Type      string          `json:"event_type"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

func (s *Store) History(ctx context.Context, limit int) ([]HistoryEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT event_id,session_id,turn_id,event_type,timestamp,data FROM events ORDER BY timestamp DESC,event_id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []HistoryEvent{}
	for rows.Next() {
		var e HistoryEvent
		var raw string
		if err = rows.Scan(&e.ID, &e.Session, &e.Turn, &e.Type, &e.Timestamp, &raw); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(raw)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Setting(ctx context.Context, key, fallback string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return fallback, nil
	}
	return v, err
}
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
