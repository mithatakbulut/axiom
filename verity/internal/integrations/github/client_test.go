package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

func TestPublishCheckUsesExactSHAAndUpdatesExisting(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	calls := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method == http.MethodGet && !strings.Contains(r.URL.Path, "/commits/"+sha+"/check-runs") {
			t.Errorf("missing SHA path: %s", r.URL.Path)
		}
		if r.Method == http.MethodGet {
			return response(r, 200, `{"check_runs":[{"id":12,"name":"Verity","external_id":"wf-1"}]}`), nil
		}
		if r.Method != http.MethodPatch || !strings.HasSuffix(r.URL.Path, "/12") {
			t.Errorf("unexpected update %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["head_sha"] != sha {
			t.Errorf("wrong SHA: %v", body["head_sha"])
		}
		return response(r, 200, `{}`), nil
	})
	c := Client{BaseURL: "https://example.test", Token: "fake", HTTP: &http.Client{Transport: transport}}
	err := c.PublishCheck(context.Background(), "acme", "app", Status{SHA: sha, Name: "Verity", ExternalID: "wf-1", State: "completed", Conclusion: "success", Summary: "PASS", Title: "Passed"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("requests: %d", calls)
	}
}
func TestPermissionFailureIsVisible(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusForbidden, `{"message":"forbidden"}`), nil
	})
	c := Client{BaseURL: "https://example.test", Token: "fake", HTTP: &http.Client{Transport: transport}}
	err := c.PublishCheck(context.Background(), "o", "r", Status{SHA: strings.Repeat("a", 40), Name: "Verity", ExternalID: "x", State: "queued"})
	if err == nil || !strings.Contains(err.Error(), ErrPermission.Error()) {
		t.Fatalf("expected visible permission error, got %v", err)
	}
}
