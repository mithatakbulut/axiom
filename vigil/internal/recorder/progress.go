package recorder

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"vigil/internal/config"
)

// Progress is an observation of retained events, not an intervention decision.
type Progress struct {
	TurnID           string      `json:"turn_id,omitempty"`
	DistinctAttempts int         `json:"distinct_attempts"`
	Confidence       float64     `json:"confidence"`
	Recommendation   string      `json:"recommendation"`
	LastTest         *TestCounts `json:"last_test,omitempty"`
	SessionID        string      `json:"session_id"`
	Events           int         `json:"events"`
	RepeatedErrors   []string    `json:"repeated_errors"`
	RepeatedCommands int         `json:"repeated_commands"`
	TestTrend        string      `json:"test_trend"`
	Git              *GitSignal  `json:"git"`
	Decision         string      `json:"decision"`
	Signals          []string    `json:"signals"`
}
type TestCounts struct {
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}
type GitSignal struct {
	ChangedFiles int `json:"changed_files"`
	Added        int `json:"added"`
	Removed      int `json:"removed"`
}

// GitProgress returns nil when repository information cannot be observed.
func GitProgress(ctx context.Context, dir string) *GitSignal {
	if dir == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/git", "-C", dir, "rev-parse", "--show-toplevel")
	if err := cmd.Run(); err != nil {
		return nil
	}
	cmd = exec.CommandContext(ctx, "/usr/bin/git", "-c", "core.fsmonitor=false", "-C", dir, "diff", "--no-ext-diff", "--no-textconv", "--numstat", "HEAD", "--")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err = cmd.Start(); err != nil {
		_ = pipe.Close()
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(pipe, 512*1024+1))
	if err != nil || len(b) > 512*1024 {
		cancel()
	}
	if cmd.Wait() != nil || err != nil || len(b) > 512*1024 {
		return nil
	}
	g := &GitSignal{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var a, d int
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			return nil
		}
		g.ChangedFiles++
		if fields[0] != "-" {
			if _, err := fmt.Sscan(fields[0], &a); err != nil {
				return nil
			}
			g.Added += a
		}
		if fields[1] != "-" {
			if _, err := fmt.Sscan(fields[1], &d); err != nil {
				return nil
			}
			g.Removed += d
		}
	}
	return g
}

func (s *Store) Progress(ctx context.Context, session, dir string) (Progress, error) {
	return s.ProgressFor(ctx, session, "", dir, config.Progress{RepeatedErrorThreshold: 2, MinimumDistinctAttempts: 2, IgnoreImprovingTests: true})
}

func (s *Store) ProgressFor(ctx context.Context, session, turn, dir string, policy config.Progress) (Progress, error) {
	p := Progress{TurnID: turn, SessionID: session, TestTrend: "unknown", Decision: "continue", RepeatedErrors: []string{}, Signals: []string{}}
	query := `SELECT event_type,data FROM events WHERE session_id=? AND turn_id=? ORDER BY timestamp,event_id`
	args := []any{session, turn}
	if turn == "" {
		query = `SELECT event_type,data FROM events WHERE session_id=? AND timestamp>=COALESCE((SELECT max(timestamp) FROM events WHERE session_id=? AND event_type='UserPromptSubmit'),'') ORDER BY timestamp,event_id`
		args[1] = session
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return p, err
	}
	defer func() { _ = rows.Close() }()
	errors := map[string]int{}
	commands := map[string]int{}
	pending := map[string]string{}
	attempts := map[string]bool{}
	transient := false
	lastFailed := -1
	lastPassed := -1
	approvals := map[string]bool{}
	unknownApproval := false
	for rows.Next() {
		var typ, raw string
		if err = rows.Scan(&typ, &raw); err != nil {
			return p, err
		}
		p.Events++
		var d struct {
			Transient bool   `json:"transient_failure"`
			ToolID    string `json:"tool_use_id"`
			Command   string `json:"command_fingerprint"`
			ExitCode  *int   `json:"exit_code"`
			Error     string `json:"error_fingerprint"`
			Failed    *int   `json:"test_failed"`
			Passed    *int   `json:"test_passed"`
		}
		if err = json.Unmarshal([]byte(raw), &d); err != nil {
			return p, err
		}
		if typ == "UserPromptSubmit" {
			errors, commands, pending, attempts = map[string]int{}, map[string]int{}, map[string]string{}, map[string]bool{}
			lastFailed, lastPassed = -1, -1
			p.TestTrend, p.LastTest = "unknown", nil
			transient = false
			approvals, unknownApproval = map[string]bool{}, false
		}
		if typ == "PermissionRequest" {
			if d.ToolID == "" {
				unknownApproval = true
			} else {
				approvals[d.ToolID] = true
			}
		}
		if typ == "PostToolUse" && d.ToolID != "" {
			delete(approvals, d.ToolID)
		}
		if typ == "PreToolUse" && d.ToolID != "" {
			pending[d.ToolID] = d.Command
		}
		if typ == "PostToolUse" {
			if d.Transient {
				transient = true
			}
			if d.Error != "" && !d.Transient {
				errors[d.Error]++
				if d.ToolID != "" {
					key := pending[d.ToolID]
					if key == "" {
						key = "tool:" + d.ToolID
					}
					attempts[key] = true
				}
				if c := pending[d.ToolID]; c != "" {
					commands[c]++
				}
			}
			// Test runners often return exit code zero even when their summary
			// reports failed tests. Treat that result as a failed attempt too.
			if d.Failed != nil && *d.Failed > 0 {
				errors[fmt.Sprintf("tests-failed:%d", *d.Failed)]++
				if d.ToolID != "" {
					key := pending[d.ToolID]
					if key == "" {
						key = "tool:" + d.ToolID
					}
					attempts[key] = true
					if command := pending[d.ToolID]; command != "" {
						commands[command]++
					}
				}
			}
			if d.ExitCode != nil && *d.ExitCode == 0 && d.Error == "" && (d.Failed == nil || *d.Failed == 0) {
				if cmd := pending[d.ToolID]; cmd != "" {
					delete(commands, cmd)
				}
				if d.Passed != nil && *d.Passed > 0 && d.Failed != nil && *d.Failed == 0 {
					errors = map[string]int{}
					commands = map[string]int{}
					attempts = map[string]bool{}
				}
			}
			delete(pending, d.ToolID)
		}
		if typ == "PostModelRequest" && d.Error != "" {
			errors[d.Error]++
		}
		if d.Failed != nil && d.Passed != nil {
			if lastFailed >= 0 {
				switch {
				case *d.Failed < lastFailed || (*d.Failed == lastFailed && *d.Passed > lastPassed):
					p.TestTrend = "improving"
				case *d.Failed > lastFailed || (*d.Failed == lastFailed && *d.Passed < lastPassed):
					p.TestTrend = "worsening"
				default:
					p.TestTrend = "unchanged"
				}
			}
			p.LastTest = &TestCounts{Passed: *d.Passed, Failed: *d.Failed}
			lastFailed = *d.Failed
			lastPassed = *d.Passed
		}
	}
	if err = rows.Err(); err != nil {
		return p, err
	}
	if p.Events == 0 {
		return p, sql.ErrNoRows
	}
	for k, n := range errors {
		if n >= max(2, policy.RepeatedErrorThreshold) {
			p.RepeatedErrors = append(p.RepeatedErrors, k)
		}
	}
	sort.Strings(p.RepeatedErrors)
	for _, n := range commands {
		if n >= max(2, policy.RepeatedErrorThreshold) {
			p.RepeatedCommands++
		}
	}
	if len(p.RepeatedErrors) > 0 {
		p.Signals = append(p.Signals, "same_error_repeated")
	}
	if p.RepeatedCommands > 0 {
		p.Signals = append(p.Signals, "same_command_failed_repeatedly")
	}
	approval := unknownApproval || len(approvals) > 0
	if approval {
		p.Signals = append(p.Signals, "approval_pending")
	}
	if p.TestTrend == "improving" {
		p.Signals = append(p.Signals, "tests_improving")
	}
	p.DistinctAttempts = len(attempts)
	if transient {
		p.Signals = append(p.Signals, "transient_failure_observed")
	}
	if len(pending) > 0 {
		p.Signals = append(p.Signals, "commands_in_flight")
	}
	if p.DistinctAttempts >= max(2, policy.MinimumDistinctAttempts) {
		p.Signals = append(p.Signals, "distinct_attempts")
	}
	p.Recommendation = "keep_working"
	if approval {
		p.Recommendation = "wait_for_approval"
	}
	if !approval && len(pending) == 0 && (!policy.IgnoreImprovingTests || p.TestTrend != "improving") {
		switch {
		case p.RepeatedCommands > 0:
			p.Confidence = 0.8
		case len(p.RepeatedErrors) > 0 && p.DistinctAttempts >= max(2, policy.MinimumDistinctAttempts):
			p.Confidence = 0.65
		}
		if p.Confidence > 0 {
			p.Decision = "escalate_candidate"
			p.Recommendation = "consult_advisor"
		}
	}
	p.Git = GitProgress(ctx, dir)
	return p, nil
}
