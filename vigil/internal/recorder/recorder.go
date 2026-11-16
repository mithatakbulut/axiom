package recorder

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const MaxPayload = 256 * 1024

var supported = map[string]bool{"SessionStart": true, "UserPromptSubmit": true, "PreToolUse": true, "PostToolUse": true, "PreModelRequest": true, "PostModelRequest": true, "SubagentStart": true, "SubagentStop": true, "PreCompact": true, "PostCompact": true, "Stop": true, "SessionEnd": true, "PermissionRequest": true, "Interrupt": true}
var testCounts = regexp.MustCompile(`(?m)^Tests: +([0-9]+) passed, +([0-9]+) failed\r?$`)

var failureLine = regexp.MustCompile(`(?im)^[ \t]*(?:--- FAIL: [A-Za-z0-9_./-]+|(?:[A-Za-z]*Error|panic):[^\r\n]{1,240})`)
var volatileError = regexp.MustCompile(`0x[0-9a-fA-F]+|[0-9]+|/[^\s:]+/`)
var transientError = regexp.MustCompile(`(?im)^(?:error: )?(?:temporary failure in name resolution|connection timed out|connection reset by peer|dial tcp [^\r\n]+: i/o timeout)\s*$`)

var bashExit = regexp.MustCompile(`(?m)^Process exited with code ([0-9]{1,3})\r?$`)

var safeValue = regexp.MustCompile(`^[a-zA-Z0-9_.:/@ -]{1,256}$`)
var secret = regexp.MustCompile(`(?i)(sk[-_][a-z0-9_-]{8,}|(?:token|password|secret|api[_-]?key|bearer|credential)[=:_/ -]+\S+|gh[pousr]_[a-z0-9]+|github_pat_[a-z0-9_]+|AKIA[A-Z0-9]{16})`)

type Event struct {
	ID         string
	SessionID  string
	TurnID     string
	Type       string
	Timestamp  time.Time
	Model      string
	AgentID    string
	Cwd        string
	Data       json.RawMessage
	ProjectDir string // Hook working directory; never persisted.
}

func clean(s string) string {
	if !safeValue.MatchString(s) || secret.MatchString(s) {
		return ""
	}
	return s
}
func stringField(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return clean(s)
}

// Parse accepts only explicitly selected metadata. Prompts, inputs, responses and transcripts are never stored.
func Parse(r io.Reader, now time.Time) (Event, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxPayload+1))
	if err != nil {
		return Event{}, err
	}
	if len(b) > MaxPayload {
		return Event{}, errors.New("payload too large")
	}
	var m map[string]json.RawMessage
	d := json.NewDecoder(bytes.NewReader(b))
	if err = d.Decode(&m); err != nil || m == nil {
		return Event{}, errors.New("invalid JSON object")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return Event{}, errors.New("trailing JSON")
	}
	typ := stringField(m, "hook_event_name")
	session := stringField(m, "session_id")
	if !supported[typ] || session == "" {
		return Event{}, errors.New("unsupported event or missing session")
	}
	// Hash the exact payload only for deduplication; the payload itself is discarded.
	sum := sha256.Sum256(b)
	data := map[string]any{}
	for _, k := range []string{"tool_name", "tool_use_id", "agent_type"} {
		if v := stringField(m, k); v != "" {
			data[k] = v
		}
	}
	// Retain equality only, never command text. Correlation requires tool_use_id.
	if typ == "PreToolUse" {
		var input struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(m["tool_input"], &input) == nil && input.Command != "" && len(input.Command) <= 16384 {
			sum := sha256.Sum256([]byte(strings.TrimSpace(input.Command)))
			data["command_fingerprint"] = hex.EncodeToString(sum[:])
		}
	}
	// Enumerated fields are accepted only from documented values; arbitrary error text is never stored.
	for key, values := range map[string]string{"source": " startup resume clear compact fork ", "reason": " other ", "trigger": " manual auto ", "permission_mode": " default acceptEdits plan dontAsk bypassPermissions ", "status": " completed failed cancelled "} {
		v := stringField(m, key)
		if v != "" && strings.Contains(values, " "+v+" ") {
			data[key] = v
		}
	}
	for _, k := range []string{"stop_hook_active", "is_error"} {
		var v bool
		if decodePresent(m[k], &v) {
			data[k] = v
		}
	}
	for _, k := range []string{"duration_ms", "exit_code"} {
		var v int64
		if decodePresent(m[k], &v) && v >= 0 && v <= 1000000000 {
			data[k] = v
		}
	}
	if typ == "PostModelRequest" {
		var usage map[string]json.RawMessage
		if json.Unmarshal(m["token_usage"], &usage) == nil {
			counts := map[string]int64{}
			for _, k := range []string{"input_tokens", "cached_input_tokens", "output_tokens", "reasoning_output_tokens", "total_tokens"} {
				var n int64
				if decodePresent(usage[k], &n) && n >= 0 && n <= 1000000000000 {
					counts[k] = n
				}
			}
			if len(counts) > 0 {
				data["token_usage"] = counts
			}
		}
		if data["status"] == "failed" {
			data["error_summary"] = "model request failed"
			data["error_fingerprint"] = "model-request-failed"
		}
	}
	if typ == "PostToolUse" {
		var response map[string]json.RawMessage
		if json.Unmarshal(m["tool_response"], &response) == nil {
			var code int64
			if decodePresent(response["exit_code"], &code) && code >= 0 && code <= 255 {
				data["exit_code"] = code
			}
			var failed bool
			if decodePresent(response["isError"], &failed) {
				data["is_error"] = failed
			}
		}
		// The CLI's Bash result may be a text envelope. Inspect only its exit-code line.
		if tool := stringField(m, "tool_name"); tool == "Bash" || tool == "functions.exec_command" || tool == "exec_command" {

			var responseText string
			if json.Unmarshal(m["tool_response"], &responseText) == nil {
				if transientError.MatchString(responseText) {
					data["transient_failure"] = true
				}
				if match := failureLine.FindString(responseText); match != "" {
					normalized := strings.Join(strings.Fields(volatileError.ReplaceAllString(strings.ToLower(match), "#")), " ")
					hash := sha256.Sum256([]byte(normalized))
					data["failure_signature"] = "error-" + hex.EncodeToString(hash[:8])
				}
				matches := testCounts.FindAllStringSubmatch(responseText, 2)
				if len(matches) == 1 {
					match := matches[0]
					passed, e1 := strconv.Atoi(match[1])
					failed, e2 := strconv.Atoi(match[2])
					if e1 == nil && e2 == nil && passed <= 1000000 && failed <= 1000000 {
						data["test_passed"] = passed
						data["test_failed"] = failed
					}
				}
				if match := bashExit.FindStringSubmatch(responseText); len(match) == 2 {
					n, err := strconv.ParseInt(match[1], 10, 64)
					if err == nil && n <= 255 {
						data["exit_code"] = n
					}
				}
			}
		}
		if code, ok := data["exit_code"].(int64); ok && code != 0 {
			data["error_summary"] = "tool exited unsuccessfully"
			data["error_fingerprint"] = "tool-exit-" + strconv.FormatInt(code, 10)
		} else if data["is_error"] == true {
			data["error_summary"] = "tool reported an error"
			data["error_fingerprint"] = "tool-reported-error"
		}
	}
	if data["error_fingerprint"] != nil && data["failure_signature"] != nil {
		data["error_fingerprint"] = data["failure_signature"]
	}
	delete(data, "failure_signature")
	var project string
	if typ == "Stop" {
		_ = json.Unmarshal(m["cwd"], &project)
		if !filepath.IsAbs(project) || len(project) > 4096 {
			project = ""
		}
	}
	raw, _ := json.Marshal(data)
	return Event{ID: hex.EncodeToString(sum[:]), SessionID: session, TurnID: stringField(m, "turn_id"), Type: typ, Timestamp: now.UTC(), Model: stringField(m, "model"), AgentID: stringField(m, "agent_id"), Cwd: "", Data: raw, ProjectDir: project}, nil
}

func decodePresent(raw json.RawMessage, value any) bool {
	return !bytes.Equal(raw, []byte("null")) && json.Unmarshal(raw, value) == nil
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Set permissions before SQLite opens the file, including for new databases.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err = os.Chmod(path+suffix, 0600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path, RawQuery: "_busy_timeout=1000&_foreign_keys=on&_journal_mode=WAL"}).String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	if err = s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version > 4 {
		return errors.New("database schema is newer than this version")
	}
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS events (event_id TEXT PRIMARY KEY, schema_version INTEGER NOT NULL, session_id TEXT NOT NULL, turn_id TEXT NOT NULL, event_type TEXT NOT NULL, timestamp TEXT NOT NULL, model TEXT NOT NULL, agent_id TEXT NOT NULL, cwd TEXT NOT NULL, data TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS events_session_time ON events(session_id, timestamp);
 CREATE INDEX IF NOT EXISTS events_timestamp ON events(timestamp);
 INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(1, strftime('%Y-%m-%dT%H:%M:%fZ','now'));`)
	if err != nil {
		return err
	}
	if version < 2 {
		if _, err = tx.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
        INSERT INTO schema_migrations(version,applied_at) VALUES(2, strftime('%Y-%m-%dT%H:%M:%fZ','now'));`); err != nil {
			return err
		}
	}
	if version < 3 {
		if _, err = tx.Exec(migration3); err != nil {
			return err
		}
	}
	if version < 4 {
		if _, err = tx.Exec(migration4); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Record(ctx context.Context, e Event, retention int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO events VALUES(?,1,?,?,?,?,?,?,?,?)`, e.ID, e.SessionID, e.TurnID, e.Type, e.Timestamp.Format("2006-01-02T15:04:05.000000000Z"), e.Model, e.AgentID, e.Cwd, string(e.Data))
	if err != nil {
		return err
	}
	cutoff := stamp(e.Timestamp.AddDate(0, 0, -retention))
	for _, query := range []string{
		"DELETE FROM events WHERE timestamp < ?",
		"DELETE FROM progress_snapshots WHERE recorded_at < ?",
		"DELETE FROM escalations WHERE created_at < ?",
		"DELETE FROM resource_snapshots WHERE recorded_at < ?",
	} {
		if _, err = tx.ExecContext(ctx, query, cutoff); err != nil {
			return err
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Check(ctx context.Context) (int, error) {
	var result string
	if err := s.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return 0, err
	}
	if result != "ok" {
		return 0, fmt.Errorf("integrity check: %s", result)
	}
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM events").Scan(&count)
	return count, err
}
