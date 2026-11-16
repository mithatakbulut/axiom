package recorder

import (
	"context"
	"encoding/json"
	"time"
)

type Usage struct {
	Session                   string           `json:"session_id"`
	Task                      string           `json:"task_id,omitempty"`
	ModelRequests             int              `json:"observed_model_requests"`
	RequestsByModel           map[string]int   `json:"requests_by_model"`
	ModelsWithUsage           int              `json:"requests_with_token_usage"`
	Tokens                    map[string]int64 `json:"observed_tokens"`
	EstimatedAPICost          *float64         `json:"estimated_api_cost"`
	Subagents                 int              `json:"observed_subagent_starts"`
	ActiveSubagents           int              `json:"observed_active_subagents"`
	PeakSubagents             int              `json:"observed_peak_subagents"`
	UnspecifiedSubagentModels int              `json:"subagent_starts_without_model"`
	AdvisorCalls              int              `json:"advisor_calls"`
	TurnAdvisorCalls          int              `json:"turn_advisor_calls"`
	RunningAdvisors           int              `json:"running_advisors"`
	RuntimeSeconds            int64            `json:"observed_runtime_seconds"`
	SessionSeconds            int64            `json:"observed_session_seconds"`
	LongCommands              int              `json:"observed_long_commands"`
}

func (s *Store) Usage(ctx context.Context, session, turn, task, since string, now time.Time, long time.Duration) (Usage, error) {
	u := Usage{Session: session, Task: task, RequestsByModel: map[string]int{}}
	if err := s.db.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM escalations WHERE session_id=? AND task_id=?),
 (SELECT count(*) FROM escalations WHERE session_id=? AND turn_id=?),
 (SELECT count(*) FROM escalations WHERE state='running' AND created_at>?)`,
		session, task, session, turn, stamp(now.Add(-45*time.Second))).Scan(&u.AdvisorCalls, &u.TurnAdvisorCalls, &u.RunningAdvisors); err != nil {
		return u, err
	}
	var boundary string
	if task != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(min(created_at),'') FROM tasks WHERE session_id=? AND created_at>?`, session, since).Scan(&boundary); err != nil {
			return u, err
		}
	} else if turn != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(min(created_at),'') FROM tasks WHERE session_id=?`, session).Scan(&boundary); err != nil {
			return u, err
		}
	}
	runtimeEnd := now
	if boundary != "" {
		var err error
		runtimeEnd, err = time.Parse(time.RFC3339Nano, boundary)
		if err != nil {
			return u, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT turn_id,event_type,timestamp,model,agent_id,data FROM events WHERE session_id=? ORDER BY timestamp,event_id`, session)
	if err != nil {
		return u, err
	}
	defer func() { _ = rows.Close() }()
	active := map[string]bool{}
	started := map[string]time.Time{}
	unmatched := 0
	var starts, ends int
	var first, last, sessionStart, sessionLast time.Time
	ended, sessionEnded := false, false
	for rows.Next() {
		var turnID, typ, at, model, agent, raw string
		if err = rows.Scan(&turnID, &typ, &at, &model, &agent, &raw); err != nil {
			return u, err
		}
		instant, parseErr := time.Parse(time.RFC3339Nano, at)
		if parseErr != nil {
			return u, parseErr
		}
		if sessionStart.IsZero() {
			sessionStart = instant
		}
		sessionLast = instant
		switch typ {
		case "SessionEnd":
			sessionEnded = true
		case "SessionStart":
			sessionEnded = false
		}
		if at < since || (boundary != "" && at >= boundary) {
			continue
		}
		if first.IsZero() {
			first = instant
		}
		last = instant
		if typ == "SessionEnd" {
			ended = true
		}
		if typ == "SessionStart" {
			ended = false
		}
		var d struct {
			ToolID   string           `json:"tool_use_id"`
			Duration int64            `json:"duration_ms"`
			Tokens   map[string]int64 `json:"token_usage"`
		}
		if err = json.Unmarshal([]byte(raw), &d); err != nil {
			return u, err
		}
		switch typ {
		case "PreModelRequest":
			starts++
		case "PostModelRequest":
			ends++
			if model == "" {
				model = "unknown"
			}
			u.RequestsByModel[model]++
			if len(d.Tokens) > 0 {
				u.ModelsWithUsage++
				if u.Tokens == nil {
					u.Tokens = map[string]int64{}
				}
				for k, v := range d.Tokens {
					u.Tokens[k] += v
				}
			}
		case "SubagentStart":
			u.Subagents++
			if model == "" {
				u.UnspecifiedSubagentModels++
			}
			if agent == "" {
				unmatched++
			} else {
				active[agent] = true
			}
			u.PeakSubagents = max(u.PeakSubagents, len(active)+unmatched)
		case "SubagentStop":
			if agent == "" {
				unmatched = max(0, unmatched-1)
			} else {
				delete(active, agent)
			}
		case "PreToolUse":
			if d.ToolID != "" {
				started[turnID+"/"+d.ToolID] = instant
			}
		case "PostToolUse":
			key := turnID + "/" + d.ToolID
			start := started[key]
			if d.Duration >= long.Milliseconds() || (!start.IsZero() && instant.Sub(start) >= long) {
				u.LongCommands++
			}
			delete(started, key)
		}
	}
	if err = rows.Err(); err != nil {
		return u, err
	}
	for _, start := range started {
		if !ended && boundary == "" && now.Sub(start) >= long {
			u.LongCommands++
		}
	}
	u.ActiveSubagents = len(active) + unmatched
	if ended || boundary != "" {
		u.ActiveSubagents = 0
	}
	u.ModelRequests = max(starts, ends)
	if missing := u.ModelRequests - ends; missing > 0 {
		u.RequestsByModel["unknown"] += missing
	}
	if !first.IsZero() {
		if since != "" {
			first, _ = time.Parse(time.RFC3339Nano, since)
		}
		if !ended {
			last = maxTime(last, runtimeEnd)
		}
		u.RuntimeSeconds = max(0, int64(last.Sub(first)/time.Second))
	}
	if !sessionStart.IsZero() {
		if !sessionEnded {
			sessionLast = maxTime(sessionLast, now)
		}
		u.SessionSeconds = max(0, int64(sessionLast.Sub(sessionStart)/time.Second))
	}
	return u, nil
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
