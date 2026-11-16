package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		return nil
	})}
	body := []byte(`{"action":"synchronize","number":5,"repository":{"full_name":"acme/app"},"pull_request":{"head":{"sha":"0123456789abcdef0123456789abcdef01234567"},"base":{"sha":"abcdef0123456789abcdef0123456789abcdef01"}}}`)
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
}
