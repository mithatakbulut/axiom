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

func TestPullRequestHeadReadsExactPullRequest(t *testing.T) {
	sha := strings.Repeat("a", 40)
	client := Client{BaseURL: "https://api.example.test", Token: "test", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/app/pulls/12" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return response(r, 200, `{"head":{"sha":"`+sha+`"}}`), nil
	})}}
	got, err := client.PullRequestHead(context.Background(), "acme", "app", 12)
	if err != nil || got != sha {
		t.Fatalf("head=(%q,%v)", got, err)
	}
}

func TestAuthorizePullRequestRetriggerRequiresWritePermissionAndResolvesLiveHead(t *testing.T) {
	sha := strings.Repeat("d", 40)
	for _, tc := range []struct {
		name       string
		permission string
		wantErr    bool
	}{{"write", "write", false}, {"maintain", "maintain", false}, {"admin", "admin", false}, {"read", "read", true}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			client := Client{BaseURL: "https://api.example.test", Token: "token", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.URL.Path)
				switch r.URL.Path {
				case "/user":
					return response(r, http.StatusOK, `{"login":"maintainer"}`), nil
				case "/repos/acme/app/collaborators/maintainer/permission":
					return response(r, http.StatusOK, `{"permission":"`+tc.permission+`"}`), nil
				case "/repos/acme/app/pulls/12":
					return response(r, http.StatusOK, `{"head":{"sha":"`+sha+`"}}`), nil
				default:
					t.Errorf("unexpected API path %s", r.URL.Path)
					return response(r, http.StatusNotFound, `{}`), nil
				}
			})}}
			actor, head, err := client.AuthorizePullRequestRetrigger(context.Background(), "acme", "app", 12)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), ErrPermission.Error()) {
					t.Fatalf("expected permission rejection, got actor=%q head=%q err=%v", actor, head, err)
				}
				if len(calls) != 2 {
					t.Fatalf("unauthorized trigger resolved PR head: %v", calls)
				}
				return
			}
			if err != nil || actor != "maintainer" || head != sha {
				t.Fatalf("authorization=(%q,%q,%v)", actor, head, err)
			}
			if len(calls) != 3 || calls[2] != "/repos/acme/app/pulls/12" {
				t.Fatalf("API calls: %v", calls)
			}
		})
	}
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

func TestRequestRetriesTransientServerFailure(t *testing.T) {
	calls := 0
	client := Client{BaseURL: "https://api.example.test", Token: "test", MaxRetries: 1, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(r, http.StatusServiceUnavailable, `{"message":"try again"}`), nil
		}
		return response(r, http.StatusOK, `{"head":{"sha":"`+strings.Repeat("a", 40)+`"}}`), nil
	})}}
	sha, err := client.PullRequestHead(context.Background(), "acme", "app", 7)
	if err != nil || !fullSHA(sha) {
		t.Fatalf("retried request=(%q,%v)", sha, err)
	}
	if calls != 2 {
		t.Fatalf("transient failure caused %d calls, want 2", calls)
	}
}

func TestRateLimitFailuresRemainVisible(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		headers http.Header
	}{
		{"rate-limit remaining exhausted", http.StatusForbidden, http.Header{"X-Ratelimit-Remaining": []string{"0"}}},
		{"too many requests", http.StatusTooManyRequests, make(http.Header)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := Client{BaseURL: "https://api.example.test", Token: "test", MaxRetries: 0, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				resp := response(r, tc.status, `{"message":"rate limited"}`)
				resp.Header = tc.headers.Clone()
				return resp, nil
			})}}
			_, err := client.PullRequestHead(context.Background(), "acme", "app", 7)
			if err != ErrRateLimit {
				t.Fatalf("rate limit error = %v, want ErrRateLimit", err)
			}
		})
	}
}
