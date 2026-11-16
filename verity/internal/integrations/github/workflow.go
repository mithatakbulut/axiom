package github

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"verity/internal/process/control"
)

// PullWorkflowStore atomically records a delivery, its PR association and run.
type PullWorkflowStore interface {
	CreatePullWorkflowForDelivery(context.Context, string, string, string, int, string, control.Run) error
}

// PullWorkflowHandler starts local workflow records for an explicitly selected repository.
// It does not schedule tasks, fetch code, or publish GitHub status.
type PullWorkflowHandler struct {
	Repository string
	Store      PullWorkflowStore
	Now        func() time.Time
}

func (h PullWorkflowHandler) HandleGitHubEvent(context.Context, Event) error {
	return fmt.Errorf("pull workflow handler requires atomic delivery handling")
}

func (h PullWorkflowHandler) HandleGitHubEventAtomic(ctx context.Context, e Event) error {
	if h.Store == nil || h.Repository == "" || e.Repository != h.Repository || e.Type != "pull_request" || e.DeliveryID == "" || e.Number <= 0 || !fullSHA(e.HeadSHA) {
		return fmt.Errorf("unsupported or unconfigured GitHub event")
	}
	switch e.Action {
	case "opened", "reopened", "synchronize", "ready_for_review":
	default:
		return fmt.Errorf("pull request action does not start a workflow")
	}
	now := time.Now().UTC()
	if h.Now != nil {
		now = h.Now().UTC()
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", e.Repository, e.Number, e.HeadSHA)))
	id := fmt.Sprintf("github-%x", digest)
	definition := control.Template("feature", id+"-definition")
	run, err := control.NewRun(id, definition, now)
	if err != nil {
		return err
	}
	return h.Store.CreatePullWorkflowForDelivery(ctx, e.DeliveryID, e.Type, e.Repository, e.Number, e.HeadSHA, run)
}
