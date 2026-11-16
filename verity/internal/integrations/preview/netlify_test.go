package preview

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type previewRoundTrip func(*http.Request) (*http.Response, error)

func (f previewRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func previewResponse(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}
func TestNetlifyBindsDeploymentToCandidateSHA(t *testing.T) {
	sha := strings.Repeat("a", 40)
	transport := previewRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("unexpected request")
		}
		return previewResponse(r, `{"id":"d-1","deploy_url":"https://preview.example.netlify.app","commit_ref":"`+sha+`","state":"ready"}`), nil
	})
	n := Netlify{Token: "secret", SiteID: "site", BaseURL: "https://api.test", BranchForSHA: func(got string) (string, error) {
		if got != sha {
			t.Fatalf("wrong SHA %s", got)
		}
		return "feature", nil
	}, HTTP: &http.Client{Transport: transport}}
	d, err := n.Deploy(context.Background(), sha, "feature")
	if err != nil {
		t.Fatal(err)
	}
	if d.SourceSHA != sha || d.ID != "d-1" {
		t.Fatalf("bad deployment: %+v", d)
	}
}
func TestNetlifyRejectsMismatchedSource(t *testing.T) {
	sha := strings.Repeat("a", 40)
	transport := previewRoundTrip(func(r *http.Request) (*http.Response, error) {
		return previewResponse(r, `{"id":"d","deploy_url":"https://x.example","commit_ref":"`+strings.Repeat("b", 40)+`"}`), nil
	})
	n := Netlify{Token: "x", SiteID: "s", BaseURL: "https://api.test", BranchForSHA: func(string) (string, error) { return "branch", nil }, HTTP: &http.Client{Transport: transport}}
	if _, err := n.Deploy(context.Background(), sha, "branch"); err == nil {
		t.Fatal("expected SHA mismatch")
	}
}
func TestPreviewURLResolverRequiresHTTPSAndAllowedHost(t *testing.T) {
	r := AllowlistedURLResolver{Hosts: []string{"preview.example"}}
	if _, err := r.Resolve(context.Background(), "https://evil.example"); err == nil {
		t.Fatal("accepted unlisted host")
	}
	if _, err := r.Resolve(context.Background(), "http://preview.example"); err == nil {
		t.Fatal("accepted HTTP URL")
	}
	if got, err := r.Resolve(context.Background(), "https://preview.example/path"); err != nil || got != "https://preview.example/path" {
		t.Fatalf("resolve %q, %v", got, err)
	}
}
