// Package preview contains provider-neutral preview contracts and adapters.
package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"verity/internal/lifecycle"
)

var ErrPermission = errors.New("preview provider permission denied")

type Deployment struct {
	ID        string
	URL       string
	SourceSHA string
	State     string
}
type Deployer interface {
	Deploy(context.Context, string, string) (Deployment, error)
}
type URLResolver interface {
	Resolve(context.Context, string) (string, error)
}

type Netlify struct {
	Token, SiteID, BaseURL string
	BranchForSHA           func(string) (string, error)
	HTTP                   *http.Client
}

func (n Netlify) Deploy(ctx context.Context, sha, branch string) (Deployment, error) {
	if !fullSHA(sha) || n.Token == "" || n.SiteID == "" {
		return Deployment{}, errors.New("Netlify requires token, site id, and full source SHA")
	}
	if n.BranchForSHA == nil {
		return Deployment{}, errors.New("Netlify adapter requires a SHA-to-branch resolver")
	}
	actual, err := n.BranchForSHA(sha)
	if err != nil {
		return Deployment{}, err
	}
	if actual == "" || actual != branch {
		return Deployment{}, errors.New("branch does not resolve to requested candidate SHA")
	}
	base := strings.TrimRight(n.BaseURL, "/")
	if base == "" {
		base = "https://api.netlify.com/api/v1"
	}
	hc := n.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	body, _ := json.Marshal(map[string]string{"branch": branch})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/sites/"+url.PathEscape(n.SiteID)+"/builds", bytes.NewReader(body))
	if err != nil {
		return Deployment{}, err
	}
	req.Header.Set("Authorization", "Bearer "+n.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return Deployment{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Deployment{}, err
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return Deployment{}, ErrPermission
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Deployment{}, fmt.Errorf("Netlify API %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var x struct {
		ID     string `json:"id"`
		URL    string `json:"deploy_url"`
		Commit string `json:"commit_ref"`
		State  string `json:"state"`
	}
	if err = json.Unmarshal(data, &x); err != nil {
		return Deployment{}, err
	}
	if x.ID == "" || x.URL == "" || x.Commit != sha {
		return Deployment{}, errors.New("Netlify deployment response does not match requested source SHA")
	}
	return Deployment{ID: x.ID, URL: x.URL, SourceSHA: x.Commit, State: x.State}, nil
}

// DeployCandidate adapts Netlify to lifecycle's candidate-pinned preview contract.
func (n Netlify) DeployCandidate(ctx context.Context, c lifecycle.Candidate) (lifecycle.Preview, error) {
	if n.BranchForSHA == nil {
		return lifecycle.Preview{}, errors.New("Netlify adapter requires a SHA-to-branch resolver")
	}
	branch, err := n.BranchForSHA(c.SHA)
	if err != nil {
		return lifecycle.Preview{}, err
	}
	d, err := n.Deploy(ctx, c.SHA, branch)
	if err != nil {
		return lifecycle.Preview{}, err
	}
	resolved, err := (AllowlistedURLResolver{}).Resolve(ctx, d.URL)
	if err != nil {
		return lifecycle.Preview{}, err
	}
	return lifecycle.Preview{ID: d.ID, URL: resolved, SourceSHA: d.SourceSHA}, nil
}

// Resolve accepts a provider URL only when it is an absolute HTTPS URL and,
// when configured, belongs to an explicitly allowed host.
type AllowlistedURLResolver struct{ Hosts []string }

func (r AllowlistedURLResolver) Resolve(_ context.Context, raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return "", errors.New("preview URL must be an absolute HTTPS URL")
	}
	if len(r.Hosts) > 0 {
		ok := false
		for _, h := range r.Hosts {
			if strings.EqualFold(u.Hostname(), h) {
				ok = true
				break
			}
		}
		if !ok {
			return "", errors.New("preview URL host is not allowed")
		}
	}
	return u.String(), nil
}
func fullSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
