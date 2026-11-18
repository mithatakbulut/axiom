package github

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"verity/internal/process/control"
)

// PullWorkflowStore atomically records a delivery, its PR association and run.
type PullWorkflowStore interface {
	CreatePullWorkflowForDelivery(context.Context, string, string, string, int, string, control.Run) error
}
type pullWorkflowJobStore interface {
	CreatePullWorkflowJobForDelivery(context.Context, string, string, string, int, string, string, control.Run) error
}
type pullLifecycleStore interface {
	ClosePullWorkflowForDelivery(context.Context, string, string, int) error
}
type manualPullWorkflowStore interface {
	CreateManualPullWorkflowJob(context.Context, string, string, string, int, string, string, control.Run) error
}

// PullWorkflowHandler starts local workflow records for an explicitly selected repository.
// It does not schedule tasks, fetch code, or publish GitHub status.
type PullWorkflowHandler struct {
	Repository     string
	InstallationID int64
	Store          PullWorkflowStore
	Now            func() time.Time
}

func (h PullWorkflowHandler) HandleGitHubEvent(context.Context, Event) error {
	return fmt.Errorf("pull workflow handler requires atomic delivery handling")
}

func (h PullWorkflowHandler) HandleGitHubEventAtomic(ctx context.Context, e Event) error {
	if h.Store == nil || h.Repository == "" || e.Repository != h.Repository || e.Type != "pull_request" || e.DeliveryID == "" || e.Number <= 0 || !fullSHA(e.HeadSHA) {
		return fmt.Errorf("unsupported or unconfigured GitHub event")
	}
	if h.InstallationID > 0 && e.InstallationID != h.InstallationID {
		return fmt.Errorf("GitHub event installation does not match configured installation")
	}
	switch e.Action {
	case "opened", "reopened", "synchronize", "ready_for_review":
	case "closed":
		lifecycle, ok := h.Store.(pullLifecycleStore)
		if !ok {
			return fmt.Errorf("pull workflow store does not support PR lifecycle updates")
		}
		return lifecycle.ClosePullWorkflowForDelivery(ctx, e.DeliveryID, e.Repository, e.Number)
	default:
		return fmt.Errorf("pull request action does not start a workflow")
	}
	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", e.Repository, e.Number, e.HeadSHA)))
	id := fmt.Sprintf("github-%x", digest)
	definition := control.Definition{
		SchemaVersion: 1, ID: id + "-definition", Name: "GitHub pull request verification", Version: 1,
		Tasks:    []control.Task{{ID: "verify", Title: "Verify pull request candidate", MaxAttempts: 1}},
		MaxTasks: 1, MaxAttempts: 1, MaxTransitions: 8,
	}
	run, err := control.NewRun(id, definition, now)
	if err != nil {
		return err
	}
	if queue, ok := h.Store.(pullWorkflowJobStore); ok {
		payload, err := json.Marshal(e)
		if err != nil {
			return err
		}
		return queue.CreatePullWorkflowJobForDelivery(ctx, e.DeliveryID, e.Type, e.Repository, e.Number, e.HeadSHA, string(payload), run)
	}
	return h.Store.CreatePullWorkflowForDelivery(ctx, e.DeliveryID, e.Type, e.Repository, e.Number, e.HeadSHA, run)
}

// ManualRetrigger queues one authorized operator request for the caller's
// already-resolved live head SHA. The caller must authenticate actor and head
// through GitHub before invoking this method.
func (h PullWorkflowHandler) ManualRetrigger(ctx context.Context, delivery, actor string, number int, sha string) (string, error) {
	store, ok := h.Store.(manualPullWorkflowStore)
	if !ok || h.Repository == "" || delivery == "" || actor == "" || number <= 0 || !fullSHA(sha) {
		return "", fmt.Errorf("manual retrigger requires an atomic store, repository, authorized actor, pull number, delivery id, and full head SHA")
	}
	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("manual\x00%s\x00%d\x00%s\x00%s", h.Repository, number, sha, delivery)))
	id := fmt.Sprintf("github-manual-%x", digest)
	definition := control.Definition{
		SchemaVersion: 1, ID: id + "-definition", Name: "GitHub pull request manual verification", Version: 1,
		Tasks:    []control.Task{{ID: "verify", Title: "Verify pull request candidate", MaxAttempts: 1}},
		MaxTasks: 1, MaxAttempts: 1, MaxTransitions: 8,
	}
	run, err := control.NewRun(id, definition, now)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Trigger    string `json:"trigger"`
		Actor      string `json:"actor"`
		Repository string `json:"repository"`
		Number     int    `json:"number"`
		HeadSHA    string `json:"head_sha"`
		Delivery   string `json:"delivery_id"`
	}{"manual", actor, h.Repository, number, sha, delivery})
	if err != nil {
		return "", err
	}
	if err = store.CreateManualPullWorkflowJob(ctx, delivery, actor, h.Repository, number, sha, string(payload), run); err != nil {
		return "", err
	}
	return id, nil
}
