package recorder

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Summary reports only what hooks actually observed. Unmatched lifecycle events are retained as counts.
type Summary struct {
	SessionID       string           `json:"session_id"`
	FirstSeen       string           `json:"first_seen"`
	LastSeen        string           `json:"last_seen"`
	EventCounts     map[string]int   `json:"event_counts"`
	Turns           int              `json:"turns"`
	ToolFailures    int              `json:"tool_failures"`
	ModelFailures   int              `json:"model_failures"`
	TokenUsage      map[string]int64 `json:"token_usage"`
	ToolDurationsMS map[string]int64 `json:"tool_durations_ms"`
}

func (s *Store) Summary(ctx context.Context, session string) (Summary, error) {
	out := Summary{SessionID: session, EventCounts: map[string]int{}, TokenUsage: map[string]int64{}, ToolDurationsMS: map[string]int64{}}
	rows, err := s.db.QueryContext(ctx, `SELECT turn_id,event_type,timestamp,data FROM events WHERE session_id=? ORDER BY timestamp,event_id`, session)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	turns := map[string]bool{}
	starts := map[string]time.Time{}
	ends := map[string]time.Time{}
	for rows.Next() {
		var turn, typ, stamp, raw string
		if err = rows.Scan(&turn, &typ, &stamp, &raw); err != nil {
			return out, err
		}
		if out.FirstSeen == "" {
			out.FirstSeen = stamp
		}
		out.LastSeen = stamp
		out.EventCounts[typ]++
		if turn != "" {
			turns[turn] = true
		}
		var data struct {
			ToolID   string           `json:"tool_use_id"`
			ExitCode *int             `json:"exit_code"`
			IsError  bool             `json:"is_error"`
			Status   string           `json:"status"`
			Usage    map[string]int64 `json:"token_usage"`
		}
		if err = json.Unmarshal([]byte(raw), &data); err != nil {
			return out, err
		}
		for k, v := range data.Usage {
			out.TokenUsage[k] += v
		}
		if typ == "PostModelRequest" && data.Status == "failed" {
			out.ModelFailures++
		}
		if typ == "PostToolUse" && (data.IsError || (data.ExitCode != nil && *data.ExitCode != 0)) {
			out.ToolFailures++
		}
		if data.ToolID != "" {
			t, e := time.Parse(time.RFC3339Nano, stamp)
			if e == nil {
				key := turn + "/" + data.ToolID
				if typ == "PreToolUse" {
					starts[key] = t
				}
				if typ == "PostToolUse" {
					ends[key] = t
				}
			}
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if out.FirstSeen == "" {
		return out, sql.ErrNoRows
	}
	out.Turns = len(turns)
	for id, start := range starts {
		if end, ok := ends[id]; ok && !end.Before(start) {
			out.ToolDurationsMS[id] = end.Sub(start).Milliseconds()
		}
	}
	return out, nil
}
