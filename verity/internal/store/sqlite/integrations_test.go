package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"verity/internal/process/control"
)

func TestGitHubDeliveryAndPullWorkflowIdempotency(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, t.TempDir()+"/integration.db")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if ok, e := s.ClaimWebhookDelivery(ctx, "d1", "pull_request"); e != nil || !ok {
		t.Fatalf("first claim %v %v", ok, e)
	}
	if ok, e := s.ClaimWebhookDelivery(ctx, "d1", "pull_request"); e != nil || ok {
		t.Fatalf("duplicate claim %v %v", ok, e)
	}
	sha := strings.Repeat("a", 40)
	wf, e := s.AssociatePullWorkflow(ctx, "acme/app", 7, sha, "wf_first")
	if e != nil || wf != "wf_first" {
		t.Fatalf("first association %q %v", wf, e)
	}
	wf, e = s.AssociatePullWorkflow(ctx, "acme/app", 7, sha, "wf_duplicate")
	if e != nil || wf != "wf_first" {
		t.Fatalf("duplicate association %q %v", wf, e)
	}
	wf, e = s.AssociatePullWorkflow(ctx, "acme/app", 7, strings.Repeat("b", 40), "wf_next")
	if e != nil || wf != "wf_next" {
		t.Fatalf("new SHA association %q %v", wf, e)
	}
}

func TestPullWorkflowTransactionRollsBackDeliveryOnConflict(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d := control.Template("feature", "def")
	run, err := control.NewRun("existing", d, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	err = s.CreatePullWorkflowForDelivery(ctx, "delivery", "pull_request", "acme/app", 1, strings.Repeat("a", 40), run)
	if err == nil {
		t.Fatal("expected conflicting run to fail")
	}
	if ok, err := s.WebhookDeliveryExists(ctx, "delivery"); err != nil || ok {
		t.Fatalf("delivery survived rollback: %v %v", ok, err)
	}
}
