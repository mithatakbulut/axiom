package control

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vigil/internal/recorder"
)

type EvalCase struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Observed string `json:"observed"`
}
type Evaluation struct {
	Total  int        `json:"total"`
	Passed int        `json:"passed"`
	Cases  []EvalCase `json:"cases"`
}

func (s Service) Eval(ctx context.Context) (Evaluation, error) {
	dir, err := os.MkdirTemp("", "vigil-eval-*")
	if err != nil {
		return Evaluation{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	return evaluate(ctx, dir, s)
}
func evaluate(ctx context.Context, dir string, svc Service) (Evaluation, error) {
	var out Evaluation
	failure := func(id string) string { return `"tool_use_id":"` + id + `","exit_code":1` }
	test := func(id, text string) string {
		return failure(id) + `,"tool_name":"Bash","tool_response":` + fmt.Sprintf("%q", text)
	}
	type fixture struct {
		name, expected string
		events         []string
	}
	fixtures := []fixture{
		{"first_failure", "continue", []string{"PostToolUse:" + failure("a")}},
		{"distinct_failed_attempts", "escalate_candidate", []string{"PostToolUse:" + failure("a"), "PostToolUse:" + failure("b")}},
		{"identical_command_loop", "escalate_candidate", []string{`PreToolUse:"tool_use_id":"a","tool_input":{"command":"go test ./..."}`, "PostToolUse:" + failure("a"), `PreToolUse:"tool_use_id":"b","tool_input":{"command":"go test ./..."}`, "PostToolUse:" + failure("b")}},
		{"improving_tests", "continue", []string{"PostToolUse:" + test("a", "Tests: 2 passed, 3 failed"), "PostToolUse:" + test("b", "Tests: 3 passed, 2 failed")}},
		{"pending_approval", "continue", []string{"PostToolUse:" + failure("a"), "PostToolUse:" + failure("b"), "PermissionRequest:"}},
		{"in_flight_command", "continue", []string{"PostToolUse:" + failure("a"), "PostToolUse:" + failure("b"), `PreToolUse:"tool_use_id":"c","tool_input":{"command":"go test ./..."}`}},
		{"transient_network", "continue", []string{"PostToolUse:" + test("a", "connection timed out"), "PostToolUse:" + test("b", "connection timed out")}},
		{"missing_usage", "continue", []string{`PostModelRequest:"token_usage":null`}},
		{"fresh_prompt", "continue", []string{"PostToolUse:" + failure("a"), "PostToolUse:" + failure("b"), "UserPromptSubmit:"}},
		{"clean_git_without_failure", "continue", []string{"UserPromptSubmit:"}},
	}
	db, err := recorder.Open(filepath.Join(dir, "eval.db"))
	if err != nil {
		return out, err
	}
	defer func() { _ = db.Close() }()
	for i, tc := range fixtures {
		session := fmt.Sprintf("eval-%d", i)
		for j, line := range tc.events {
			kind, rest, _ := strings.Cut(line, ":")
			if rest != "" {
				rest = "," + rest
			}
			raw := fmt.Sprintf(`{"hook_event_name":%q,"session_id":%q,"turn_id":"turn"%s}`, kind, session, rest)
			e, err := recorder.Parse(strings.NewReader(raw), time.Unix(int64(j), 0))
			if err != nil {
				return out, err
			}
			if err = db.Record(ctx, e, 30); err != nil {
				return out, err
			}
		}
		p, err := db.ProgressFor(ctx, session, "turn", "", svc.Config.Progress)
		if err != nil {
			return out, err
		}
		out.Cases = append(out.Cases, EvalCase{tc.name, tc.expected, p.Decision})
		if tc.expected == p.Decision {
			out.Passed++
		}
	}
	out.Total = len(out.Cases)
	return out, nil
}
