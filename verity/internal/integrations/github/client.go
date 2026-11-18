package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrPermission = errors.New("GitHub permission denied")
var ErrRateLimit = errors.New("GitHub rate limit exceeded")

type Client struct {
	BaseURL, Token string
	TokenSource    interface {
		AccessToken(context.Context) (string, error)
	}
	HTTP       *http.Client
	MaxRetries int
}

func (c Client) AccessToken(ctx context.Context) (string, error) {
	if c.TokenSource != nil {
		return c.TokenSource.AccessToken(ctx)
	}
	if c.Token == "" {
		return "", errors.New("GitHub token is required")
	}
	return c.Token, nil
}

func (c Client) request(ctx context.Context, method, path string, body any, out any) error {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	token, err := c.AccessToken(ctx)
	if err != nil {
		return err
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	var payload []byte
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	tries := c.MaxRetries
	if tries < 0 {
		tries = 0
	}
	if tries > 5 {
		tries = 5
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out != nil && len(data) > 0 {
				return json.Unmarshal(data, out)
			}
			return nil
		}
		if (resp.StatusCode == 403 && resp.Header.Get("X-RateLimit-Remaining") == "0") || resp.StatusCode == 429 {
			if attempt < tries {
				if !wait(ctx, retryDelay(resp.Header)) {
					return ctx.Err()
				}
				continue
			}
			return ErrRateLimit
		}
		if resp.StatusCode == 403 || resp.StatusCode == 401 {
			return fmt.Errorf("%w: %s", ErrPermission, compact(data))
		}
		if resp.StatusCode >= 500 && attempt < tries {
			if !wait(ctx, time.Duration(attempt+1)*200*time.Millisecond) {
				return ctx.Err()
			}
			continue
		}
		return fmt.Errorf("GitHub API %s %s: %s", resp.Status, path, compact(data))
	}
}
func compact(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
func retryDelay(h http.Header) time.Duration {
	if n, e := strconv.Atoi(h.Get("Retry-After")); e == nil && n > 0 && n < 60 {
		return time.Duration(n) * time.Second
	}
	return time.Second
}
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

type Status struct {
	SHA, Name, Title, Summary, DetailsURL, ExternalID, Conclusion string
	State                                                         string
	StartedAt, CompletedAt                                        time.Time
}

func (s Status) validate() error {
	if !fullSHA(s.SHA) || s.Name == "" || s.ExternalID == "" {
		return errors.New("status requires full SHA, name, and external id")
	}
	switch s.State {
	case "queued", "in_progress", "completed":
	default:
		return errors.New("invalid check state")
	}
	return nil
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

// PublishCheck creates or updates Verity's named check run for the exact SHA.
func (c Client) PublishCheck(ctx context.Context, owner, repo string, s Status) error {
	if owner == "" || repo == "" {
		return errors.New("owner and repository are required")
	}
	if err := s.validate(); err != nil {
		return err
	}
	prefix := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/check-runs"
	var listed struct {
		CheckRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			ExternalID string `json:"external_id"`
		} `json:"check_runs"`
	}
	listPath := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/commits/" + url.PathEscape(s.SHA) + "/check-runs?per_page=100&check_name=" + url.QueryEscape(s.Name) + "&filter=all"
	if err := c.request(ctx, http.MethodGet, listPath, nil, &listed); err != nil {
		return err
	}
	endpoint, method := prefix, http.MethodPost
	for _, run := range listed.CheckRuns {
		if run.Name == s.Name && run.ExternalID == s.ExternalID {
			endpoint += "/" + strconv.FormatInt(run.ID, 10)
			method = http.MethodPatch
			break
		}
	}
	payload := map[string]any{"name": s.Name, "head_sha": s.SHA, "external_id": s.ExternalID, "status": s.State, "output": map[string]string{"title": s.Title, "summary": s.Summary}}
	if s.DetailsURL != "" {
		payload["details_url"] = s.DetailsURL
	}
	if !s.StartedAt.IsZero() {
		payload["started_at"] = s.StartedAt.UTC().Format(time.RFC3339)
	}
	if s.State == "completed" {
		if !validConclusion(s.Conclusion) {
			return errors.New("completed check requires an explicit valid conclusion")
		}
		payload["conclusion"] = s.Conclusion
		if s.CompletedAt.IsZero() {
			s.CompletedAt = time.Now().UTC()
		}
		payload["completed_at"] = s.CompletedAt.UTC().Format(time.RFC3339)
	}
	return c.request(ctx, method, endpoint, payload, nil)
}
func validConclusion(s string) bool {
	switch s {
	case "success", "failure", "neutral", "cancelled", "skipped", "timed_out", "action_required":
		return true
	}
	return false
}

const commentMarker = "<!-- verity-verification-summary -->"

type CommentSummary struct {
	SHA, State, EvidenceURL string
	PullNumber              int
}

func (c Client) PublishPRSummary(ctx context.Context, owner, repo string, s CommentSummary) error {
	if owner == "" || repo == "" || s.PullNumber <= 0 || !fullSHA(s.SHA) {
		return errors.New("PR summary requires repository, pull number, and full SHA")
	}
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/issues/" + strconv.Itoa(s.PullNumber) + "/comments"
	body := commentMarker + "\n**Verity verification: " + s.State + "** for `" + s.SHA + "`."
	if s.EvidenceURL != "" {
		body += " [Evidence](" + s.EvidenceURL + ")"
	}
	for page := 1; page <= 10; page++ {
		var comments []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		}
		if err := c.request(ctx, http.MethodGet, base+"?per_page=100&page="+strconv.Itoa(page), nil, &comments); err != nil {
			return err
		}
		for _, cm := range comments {
			if strings.Contains(cm.Body, commentMarker) {
				return c.request(ctx, http.MethodPatch, base+"/"+strconv.FormatInt(cm.ID, 10), map[string]string{"body": body}, nil)
			}
		}
		if len(comments) < 100 {
			break
		}
	}
	return c.request(ctx, http.MethodPost, base, map[string]string{"body": body}, nil)
}

// PullRequestHead returns GitHub's current head for the requested PR. It is
// used immediately before publishing a final PR-facing result.
func (c Client) PullRequestHead(ctx context.Context, owner, repo string, number int) (string, error) {
	if owner == "" || repo == "" || number <= 0 {
		return "", errors.New("repository and pull number are required")
	}
	var response struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/pulls/" + strconv.Itoa(number)
	if err := c.request(ctx, http.MethodGet, path, nil, &response); err != nil {
		return "", err
	}
	if !fullSHA(response.Head.SHA) {
		return "", errors.New("GitHub returned an invalid pull request head")
	}
	return response.Head.SHA, nil
}

// AuthorizePullRequestRetrigger resolves the authenticated actor and requires
// write-capable repository permission before returning the live PR head.
func (c Client) AuthorizePullRequestRetrigger(ctx context.Context, owner, repo string, number int) (string, string, error) {
	if owner == "" || repo == "" || number <= 0 {
		return "", "", errors.New("repository and pull number are required")
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := c.request(ctx, http.MethodGet, "/user", nil, &user); err != nil {
		return "", "", err
	}
	if user.Login == "" {
		return "", "", errors.New("GitHub returned an empty authenticated username")
	}
	var permission struct {
		Permission string `json:"permission"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/collaborators/" + url.PathEscape(user.Login) + "/permission"
	if err := c.request(ctx, http.MethodGet, path, nil, &permission); err != nil {
		return "", "", err
	}
	switch permission.Permission {
	case "write", "maintain", "admin":
	default:
		return "", "", fmt.Errorf("%w: user %q lacks write permission for %s/%s", ErrPermission, user.Login, owner, repo)
	}
	head, err := c.PullRequestHead(ctx, owner, repo, number)
	if err != nil {
		return "", "", err
	}
	return user.Login, head, nil
}

type ActionRun struct {
	ID         int64  `json:"id"`
	HeadSHA    string `json:"head_sha"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
}
type ActionArtifact struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	ArchiveDownloadURL string `json:"archive_download_url"`
	Expired            bool   `json:"expired"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size_in_bytes"`
}

func (c Client) WorkflowRuns(ctx context.Context, owner, repo, sha string) ([]ActionRun, error) {
	if !fullSHA(sha) {
		return nil, errors.New("workflow runs require a full SHA")
	}
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/actions/runs?per_page=100&head_sha=" + url.QueryEscape(sha)
	all := []ActionRun{}
	for page := 1; page <= 10; page++ {
		var x struct {
			Runs []ActionRun `json:"workflow_runs"`
		}
		if e := c.request(ctx, http.MethodGet, base+"&page="+strconv.Itoa(page), nil, &x); e != nil {
			return nil, e
		}
		all = append(all, x.Runs...)
		if len(x.Runs) < 100 {
			break
		}
	}
	return all, nil
}
func (c Client) WorkflowArtifacts(ctx context.Context, owner, repo string, runID int64) ([]ActionArtifact, error) {
	if runID <= 0 {
		return nil, errors.New("invalid workflow run id")
	}
	base := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/artifacts?per_page=100", url.PathEscape(owner), url.PathEscape(repo), runID)
	all := []ActionArtifact{}
	for page := 1; page <= 10; page++ {
		var x struct {
			Artifacts []ActionArtifact `json:"artifacts"`
		}
		if e := c.request(ctx, http.MethodGet, base+"&page="+strconv.Itoa(page), nil, &x); e != nil {
			return nil, e
		}
		all = append(all, x.Artifacts...)
		if len(x.Artifacts) < 100 {
			break
		}
	}
	return all, nil
}

// DownloadWorkflowArtifact returns a bounded archive whose digest is checked
// against GitHub's metadata when that metadata includes a digest.
func (c Client) DownloadWorkflowArtifact(ctx context.Context, owner, repo string, a ActionArtifact) ([]byte, error) {
	if owner == "" || repo == "" || a.ID <= 0 || a.Expired || a.Size < 0 || a.Size > 50<<20 {
		return nil, errors.New("invalid, expired, or oversized workflow artifact")
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	if c.Token == "" {
		return nil, errors.New("GitHub token is required")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	path := fmt.Sprintf("%s/repos/%s/%s/actions/artifacts/%d/zip", base, url.PathEscape(owner), url.PathEscape(repo), a.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, ErrPermission
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub artifact download: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (50<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 50<<20 {
		return nil, errors.New("GitHub artifact exceeds 50 MiB download limit")
	}
	if a.Size > 0 && int64(len(data)) != a.Size {
		return nil, errors.New("GitHub artifact size does not match metadata")
	}
	if a.Digest != "" {
		want := strings.TrimPrefix(a.Digest, "sha256:")
		sum := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), want) {
			return nil, errors.New("GitHub artifact digest does not match metadata")
		}
	}
	return data, nil
}
