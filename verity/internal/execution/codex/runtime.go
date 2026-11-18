// Package codex runs explicitly requested, bounded Codex CLI jobs.
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"verity/internal/execution"
	"verity/internal/verification"
)

type Runtime struct {
	Binary       string
	Model        string
	SnapshotRoot string
	SourcePaths  []string
}

func (r Runtime) Capabilities(context.Context) (execution.Capabilities, error) {
	return execution.Capabilities{Provider: "codex-cli", Cancellation: true, WorkspaceRequired: true}, nil
}

type boundedOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	const limit = 8 << 20
	n := len(p)
	if b.Len()+n > limit {
		b.overflow = true
		return n, nil
	}
	_, err := b.Buffer.Write(p)
	return n, err
}

func (r Runtime) Execute(ctx context.Context, req execution.Request) (execution.Result, error) {
	if err := req.Validate(); err != nil {
		return execution.Result{}, err
	}
	if r.Binary == "" || r.SnapshotRoot == "" || len(r.SourcePaths) == 0 || strings.TrimSpace(req.Instruction) == "" {
		return execution.Result{}, errors.New("Codex runtime requires a binary, instruction and explicit snapshot paths")
	}
	started := time.Now().UTC()
	args := []string{"exec", "--json", "--skip-git-repo-check", "--sandbox", "workspace-write", "-C", req.Workspace}
	if r.Model != "" {
		args = append(args, "--model", r.Model)
	}
	args = append(args, "-")
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	cmd.Stdin = strings.NewReader(req.Instruction)
	var stdout, stderr boundedOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		// Killing the CLI does not prove that every spawned process stopped or
		// that its workspace writes have ceased. Require reconciliation.
		return execution.Result{Outcome: execution.Unknown, StartedAt: started, FinishedAt: time.Now().UTC(), Summary: "Codex execution was interrupted; outcome requires reconciliation"}, nil
	}
	if stdout.overflow || stderr.overflow {
		return execution.Result{}, errors.New("Codex output exceeded the event limit")
	}
	var thread string
	var completed, failed bool
	var failureSummary string
	var reportedSummary string
	for _, line := range bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n")) {
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Error    struct {
				Message string `json:"message"`
			} `json:"error"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if len(line) == 0 {
			continue
		}
		if err := json.Unmarshal(line, &event); err != nil {
			return execution.Result{}, errors.New("Codex returned invalid event JSON")
		}
		switch event.Type {
		case "thread.started":
			thread = event.ThreadID
		case "turn.completed":
			completed = true
		case "item.completed":
			if event.Item.Type == "agent_message" {
				reportedSummary = event.Item.Text
			}
		case "turn.failed":
			failed = true
			var detail struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(event.Error.Message), &detail) == nil && detail.Error.Code == "model_not_found" {
				failureSummary = "; configured model is unavailable"
			}
		}
	}
	if thread == "" || completed == failed {
		return execution.Result{}, errors.New("Codex did not return an unambiguous terminal event")
	}
	if failed {
		return execution.Result{Outcome: execution.Failed, StartedAt: started, FinishedAt: time.Now().UTC(), Summary: "Codex turn failed" + failureSummary + "; thread " + thread}, nil
	}
	if runErr != nil {
		return execution.Result{}, fmt.Errorf("Codex process failed after its terminal event: %w", runErr)
	}
	digest, err := verification.CreateContentSnapshot(ctx, req.Workspace, r.SnapshotRoot, r.SourcePaths)
	if err != nil {
		return execution.Result{}, fmt.Errorf("capture Codex output: %w", err)
	}
	summary := "Codex turn completed; verification remains required; thread " + thread
	if len(reportedSummary) > 4096 {
		reportedSummary = reportedSummary[:4096]
	}
	if reportedSummary != "" {
		summary += "; provider report: " + reportedSummary
	}
	return execution.Result{Outcome: execution.Succeeded, CandidateSHA: digest, StartedAt: started, FinishedAt: time.Now().UTC(), Summary: summary}, nil
}
