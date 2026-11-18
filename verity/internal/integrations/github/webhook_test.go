package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"verity/internal/store/sqlite"
)

type eventFunc func(context.Context, Event) error

func (f eventFunc) HandleGitHubEvent(c context.Context, e Event) error { return f(c, e) }
func signature(secret, body []byte) string {
	h := hmac.New(sha256.New, secret)
	h.Write(body)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}
func TestWebhookValidatesAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	secret := []byte("test-secret")
	calls := 0
	w := Webhook{Secret: secret, Store: store, Handler: eventFunc(func(_ context.Context, e Event) error {
		calls++
		if e.HeadSHA != "0123456789abcdef0123456789abcdef01234567" {
			t.Fatalf("unexpected sha: %s", e.HeadSHA)
		}
		if e.InstallationID != 77 {
			t.Fatalf("installation ID=%d want 77", e.InstallationID)
		}
		return nil
	})}
	body := []byte(`{"action":"synchronize","number":5,"repository":{"full_name":"acme/app"},"installation":{"id":77},"pull_request":{"head":{"sha":"0123456789abcdef0123456789abcdef01234567"},"base":{"sha":"abcdef0123456789abcdef0123456789abcdef01"}}}`)
	serve := func(sig string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
		r.Header.Set("X-Hub-Signature-256", sig)
		r.Header.Set("X-GitHub-Delivery", "delivery-1")
		r.Header.Set("X-GitHub-Event", "pull_request")
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out
	}
	bad := serve("sha256=00")
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature status %d", bad.Code)
	}
	if got := serve(signature(secret, body)); got.Code != http.StatusAccepted {
		t.Fatalf("first status %d: %s", got.Code, got.Body)
	}
	if got := serve(signature(secret, body)); got.Code != http.StatusAccepted {
		t.Fatalf("duplicate status %d", got.Code)
	}
	if calls != 1 {
		t.Fatalf("handler called %d times", calls)
	}
	// Delivery claims survive a process restart.
	if ok, err := store.ClaimWebhookDelivery(ctx, "delivery-1", "pull_request"); err != nil || ok {
		t.Fatalf("persisted duplicate claim=(%v,%v)", ok, err)
	}
}

func TestPullWebhookCreatesRevisionBoundWorkflowAtomically(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	secret := []byte("test-secret")
	w := Webhook{Secret: secret, Handler: PullWorkflowHandler{Repository: "acme/app", Store: store}}
	send := func(id, sha, repo string) int {
		body := []byte(fmt.Sprintf(`{"action":"synchronize","number":5,"repository":{"full_name":%q},"pull_request":{"head":{"sha":%q}}}`, repo, sha))
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
		r.Header.Set("X-Hub-Signature-256", signature(secret, body))
		r.Header.Set("X-GitHub-Delivery", id)
		r.Header.Set("X-GitHub-Event", "pull_request")
		out := httptest.NewRecorder()
		w.ServeHTTP(out, r)
		return out.Code
	}
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, tc := range []struct {
		id, sha, repo string
		want          int
	}{{"d1", a, "acme/app", 202}, {"d1", a, "acme/app", 202}, {"d2", a, "acme/app", 202}, {"d3", b, "acme/app", 202}, {"d4", a, "other/app", 503}} {
		if got := send(tc.id, tc.sha, tc.repo); got != tc.want {
			t.Fatalf("%s: got %d", tc.id, got)
		}
	}
	runs, err := store.ListRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs: %+v", runs)
	}
	for _, run := range runs {
		if run.State != "READY" {
			t.Fatalf("unexpected workflow state: %s", run.State)
		}
	}
	if ok, err := store.WebhookDeliveryExists(ctx, "d4"); err != nil || ok {
		t.Fatalf("rejected delivery recorded: %v %v", ok, err)
	}
	links, err := store.PullWorkflows(ctx, "acme/app")
	if err != nil || len(links) != 2 {
		t.Fatalf("pull workflows: %#v %v", links, err)
	}
	for _, link := range links {
		job, err := store.PullJob(ctx, link.WorkflowID)
		wantState := "PENDING"
		if link.HeadSHA == a {
			wantState = "SUPERSEDED"
		}
		if err != nil || job.State != wantState || job.HeadSHA != link.HeadSHA || job.Payload == "" {
			t.Fatalf("durable queue job: %#v %v", job, err)
		}
	}
	claimed, err := store.ClaimNextPullJob(ctx, time.Now().UTC(), time.Minute, 3)
	if err != nil || claimed.State != "RUNNING" || claimed.Attempts != 1 {
		t.Fatalf("claim queued pull job: %#v %v", claimed, err)
	}
	if err := store.RetryPullJob(ctx, claimed.WorkflowID, errors.New("temporary worker failure"), time.Now().Add(-time.Second), 3); err != nil {
		t.Fatal(err)
	}
	retry, err := store.ClaimNextPullJob(ctx, time.Now().UTC(), time.Minute, 3)
	if err != nil || retry.WorkflowID != claimed.WorkflowID || retry.Attempts != 2 {
		t.Fatalf("retry durable pull job: %#v %v", retry, err)
	}
	if err := store.ClosePullWorkflowForDelivery(ctx, "close-delivery", "acme/app", 5); err != nil {
		t.Fatal(err)
	}
	closed, err := store.PullJob(ctx, retry.WorkflowID)
	if err != nil || closed.State != "CANCELLED" {
		t.Fatalf("closed PR should cancel leased work: %#v %v", closed, err)
	}
}

func TestPullWorkflowRejectsUnexpectedInstallationBeforeQueueing(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	event := Event{DeliveryID: "wrong-install", Type: "pull_request", Action: "opened", Repository: "acme/app", InstallationID: 78, Number: 9, HeadSHA: strings.Repeat("a", 40)}
	handler := PullWorkflowHandler{Repository: "acme/app", InstallationID: 77, Store: store}
	if err = handler.HandleGitHubEventAtomic(ctx, event); err == nil {
		t.Fatal("unexpected installation was accepted")
	}
	if exists, err := store.WebhookDeliveryExists(ctx, "wrong-install"); err != nil || exists {
		t.Fatalf("rejected delivery was persisted: exists=%t err=%v", exists, err)
	}
	runs, err := store.ListRuns(ctx)
	if err != nil || len(runs) != 0 {
		t.Fatalf("unexpected workflow created: %#v err=%v", runs, err)
	}
}

func TestManualPullRetriggerIsActorBoundAndIdempotent(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, t.TempDir()+"/db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := PullWorkflowHandler{Repository: "acme/app", Store: store, Now: func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) }}
	sha := strings.Repeat("c", 40)
	runID, err := handler.ManualRetrigger(ctx, "manual-delivery-1", "maintainer", 12, sha)
	if err != nil || runID == "" {
		t.Fatalf("manual trigger=(%q,%v)", runID, err)
	}
	duplicate, err := handler.ManualRetrigger(ctx, "manual-delivery-1", "maintainer", 12, sha)
	if err != nil || duplicate != runID {
		t.Fatalf("duplicate trigger=(%q,%v), want %q", duplicate, err, runID)
	}
	if _, err = handler.ManualRetrigger(ctx, "manual-delivery-1", "different-actor", 12, sha); err == nil {
		t.Fatal("delivery ID reuse with another actor was accepted")
	}
	job, err := store.PullJob(ctx, runID)
	if err != nil || job.State != "PENDING" || job.HeadSHA != sha || !strings.Contains(job.Payload, `"actor":"maintainer"`) {
		t.Fatalf("manual job=%#v err=%v", job, err)
	}
	if _, err = handler.ManualRetrigger(ctx, "manual-delivery-2", "maintainer", 12, sha); err == nil || !strings.Contains(err.Error(), "PENDING") {
		t.Fatalf("parallel manual trigger should be rejected, got %v", err)
	}
}
