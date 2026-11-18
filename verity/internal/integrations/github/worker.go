package github

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"verity/internal/process/control"
	"verity/internal/proof"
	"verity/internal/store/sqlite"
	"verity/internal/verification"
)

type PullJobStore interface {
	ClaimNextPullJobForRepository(context.Context, time.Time, time.Duration, int, string) (sqlite.PullJob, error)
	PullJob(context.Context, string) (sqlite.PullJob, error)
	CompletePullJob(context.Context, string, time.Time) error
	RetryPullJob(context.Context, string, error, time.Time, int) error
	GetRun(context.Context, string) (control.Run, error)
	UpdateRun(context.Context, control.Run, int) error
	Verification(context.Context, string) (verification.Run, error)
	SaveVerification(context.Context, verification.Run) error
	Evidence(context.Context, string, string) ([]proof.Evidence, error)
	SaveEvidence(context.Context, proof.Evidence) error
	ProofAudit(context.Context, string) ([]proof.AuditEvent, error)
}

type PullWorker struct {
	Store       PullJobStore
	Repository  string
	GitBaseURL  string
	GitHub      Client
	Manifest    verification.Manifest // Loaded from trusted operator configuration, never from the candidate.
	Artifacts   proof.ArtifactStore
	Snapshotter verification.Snapshotter
	Now         func() time.Time
	Lease       time.Duration
	MaxAttempts int
	RetryDelay  time.Duration
	OutputLimit int
	verifier    pullRequestVerifier
}

type pullRequestVerifier interface {
	Verify(context.Context, string, string, string, verification.Manifest, string, string, string) (verification.Run, error)
}

type rootlessPullRequestVerifier struct {
	Snapshotter verification.Snapshotter
	OutputLimit int
}

func (v rootlessPullRequestVerifier) Verify(ctx context.Context, root, verificationID, workflowID string, manifest verification.Manifest, candidateSHA, baseRevision, repository string) (verification.Run, error) {
	engine := verification.Engine{
		Runner:        verification.ContainerRunner{Snapshotter: v.Snapshotter, OutputLimit: v.OutputLimit},
		CandidateKind: verification.CandidateGitCommit, CandidateBase: baseRevision, CandidateRepository: repository,
	}
	return engine.Verify(ctx, root, verificationID, workflowID, "verify", manifest, verification.Standard, candidateSHA, true)
}

// RunOnce claims and processes at most one durable pull request job.
func (w PullWorker) RunOnce(ctx context.Context) error {
	if w.Store == nil || w.Artifacts == nil || w.Repository == "" {
		return errors.New("GitHub worker store, repository scope, and artifact store are required")
	}
	if err := w.Manifest.Validate(); err != nil {
		return fmt.Errorf("trusted GitHub verification policy: %w", err)
	}
	if w.Manifest.TrustLevel != verification.TrustUntrusted || w.Manifest.Isolation != verification.IsolationRootlessContainer {
		return errors.New("GitHub pull request worker requires an untrusted rootless-container policy")
	}
	now := w.now()
	lease, attempts := w.Lease, w.MaxAttempts
	if lease <= 0 {
		lease = 20 * time.Minute
	}
	if attempts <= 0 {
		attempts = 3
	}
	job, err := w.Store.ClaimNextPullJobForRepository(ctx, now, lease, attempts, w.Repository)
	if err != nil {
		return err
	}
	if err = w.process(ctx, job); err != nil {
		delay := w.RetryDelay
		if delay <= 0 {
			delay = time.Duration(job.Attempts) * time.Minute
		}
		return errors.Join(err, w.Store.RetryPullJob(ctx, job.WorkflowID, err, w.now().Add(delay), attempts))
	}
	return w.Store.CompletePullJob(ctx, job.WorkflowID, w.now())
}

func (w PullWorker) process(ctx context.Context, job sqlite.PullJob) error {
	parts := strings.Split(job.Repository, "/")
	if !validRepositoryName(job.Repository) || len(parts) != 2 || !fullSHA(job.HeadSHA) || job.Number <= 0 {
		return errors.New("invalid persisted pull request job")
	}
	current, err := w.Store.PullJob(ctx, job.WorkflowID)
	if err != nil {
		return err
	}
	if current.State != "RUNNING" {
		return fmt.Errorf("pull request job is %s", current.State)
	}
	if current.HeadSHA != job.HeadSHA {
		return errors.New("pull request job candidate changed")
	}
	var event Event
	if err = json.Unmarshal([]byte(job.Payload), &event); err != nil || event.Repository != job.Repository || event.Number != job.Number || event.HeadSHA != job.HeadSHA {
		return errors.New("persisted pull request event does not match its durable job")
	}
	var gitToken string
	if source, ok := w.GitHub.TokenSource.(interface {
		AccessTokenForInstallation(context.Context, int64) (string, error)
	}); ok {
		if event.Type == "pull_request" && event.InstallationID <= 0 {
			return errors.New("GitHub App pull request event has no installation ID")
		}
		gitToken, err = source.AccessTokenForInstallation(ctx, event.InstallationID)
	} else {
		gitToken, err = w.GitHub.AccessToken(ctx)
	}
	if err != nil {
		return err
	}

	check := Status{SHA: job.HeadSHA, Name: "Verity", ExternalID: job.WorkflowID, State: "in_progress", Title: "Verifying pull request", Summary: "Verity is verifying this exact commit."}
	if err := w.GitHub.PublishCheck(ctx, parts[0], parts[1], check); err != nil {
		return err
	}

	root, cleanup, err := fetchPullCandidate(ctx, parts[0], parts[1], job.Number, job.HeadSHA, gitToken, w.GitBaseURL)
	if err != nil {
		return err
	}
	defer cleanup()
	verificationID := stableVerificationID(job.WorkflowID)
	verified, err := w.Store.Verification(ctx, verificationID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		manifest := w.Manifest
		verifier := w.verifier
		if verifier == nil {
			verifier = rootlessPullRequestVerifier{Snapshotter: w.Snapshotter, OutputLimit: w.OutputLimit}
		}
		verified, err = verifier.Verify(ctx, root, verificationID, job.WorkflowID, manifest, job.HeadSHA, pullRequestBaseRevision(job.Payload), "github:"+job.Repository)
		if err != nil {
			return err
		}
		verified, err = proof.AttachCheckEvidence(ctx, verified, w.Artifacts, w.Store, w.now())
		if err != nil {
			return err
		}
		if err = w.Store.SaveVerification(ctx, verified); err != nil {
			return err
		}
	}
	if verified.CandidateSHA != job.HeadSHA || verified.WorkflowID != job.WorkflowID || verified.CandidateKind != verification.CandidateGitCommit {
		return errors.New("persisted verification does not match pull request candidate")
	}
	if err := w.validateProof(ctx, verified); err != nil {
		return err
	}

	// Recheck both durable supersession and GitHub's live PR head before any
	// completed result is published.
	current, err = w.Store.PullJob(ctx, job.WorkflowID)
	if err != nil {
		return err
	}
	if current.State != "RUNNING" {
		return fmt.Errorf("pull request job was superseded (%s)", current.State)
	}
	head, err := w.GitHub.PullRequestHead(ctx, parts[0], parts[1], job.Number)
	if err != nil {
		return err
	}
	state, conclusion := mapConclusion(verified.State)
	if head != job.HeadSHA {
		state, conclusion = verification.StateStale, "cancelled"
	}
	if err := w.finishWorkflow(ctx, job.WorkflowID, state == verification.StatePass); err != nil {
		return err
	}
	completed := Status{SHA: job.HeadSHA, Name: "Verity", ExternalID: job.WorkflowID, State: "completed", Conclusion: conclusion, Title: "Verity: " + string(state), Summary: checkSummary(state, verificationID), StartedAt: verified.StartedAt, CompletedAt: w.now()}
	if err := w.GitHub.PublishCheck(ctx, parts[0], parts[1], completed); err != nil {
		return err
	}
	if head == job.HeadSHA {
		latest, err := w.GitHub.PullRequestHead(ctx, parts[0], parts[1], job.Number)
		if err != nil {
			return err
		}
		if latest == job.HeadSHA {
			return w.GitHub.PublishPRSummary(ctx, parts[0], parts[1], CommentSummary{SHA: job.HeadSHA, PullNumber: job.Number, State: string(state)})
		}
	}
	return nil
}

func checkSummary(state verification.State, verificationID string) string {
	return fmt.Sprintf("Verification result: %s. Proof ID: %s. Export with `verity proof export %s proof-%s.json`.", state, verificationID, verificationID, verificationID)
}

func pullRequestBaseRevision(payload string) string {
	var event struct {
		BaseSHA     string `json:"base_sha"`
		PullRequest struct {
			Base struct {
				SHA string `json:"sha"`
			} `json:"base"`
		} `json:"pull_request"`
	}
	if json.Unmarshal([]byte(payload), &event) != nil {
		return ""
	}
	if fullSHA(event.BaseSHA) {
		return event.BaseSHA
	}
	if !fullSHA(event.PullRequest.Base.SHA) {
		return ""
	}
	return event.PullRequest.Base.SHA
}

func (w PullWorker) finishWorkflow(ctx context.Context, id string, success bool) error {
	run, err := w.Store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	before := run.Transitions
	if run.State == control.Ready {
		if err = run.Start(); err != nil {
			return err
		}
		if _, err = run.ScheduleOne(); err != nil {
			return err
		}
	}
	if run.State == control.Succeeded && success || run.State == control.Failed && !success {
		return nil
	}
	if run.State != control.Running {
		return fmt.Errorf("workflow is %s", run.State)
	}
	if len(run.Tasks) != 1 {
		return errors.New("pull workflow must contain one verification task")
	}
	if run.Tasks[0].State == control.TaskReady {
		if err = run.BeginTask("verify"); err != nil {
			return err
		}
	}
	if run.Tasks[0].State == control.TaskRunning {
		if err = run.FinishTask("verify", success); err != nil {
			return err
		}
	}
	if run.State == control.Running && success {
		_, err = run.ScheduleOne()
	}
	if err != nil {
		return err
	}
	return w.Store.UpdateRun(ctx, run, before)
}

func (w PullWorker) validateProof(ctx context.Context, run verification.Run) error {
	items, err := w.Store.Evidence(ctx, run.WorkflowID, run.CandidateSHA)
	if err != nil {
		return err
	}
	refs := make([]proof.ArtifactReference, 0, len(items))
	for _, item := range items {
		refs = append(refs, proof.ArtifactReference{EvidenceID: item.ID, Digest: item.ContentSHA256, Size: item.Size})
	}
	audit, err := w.Store.ProofAudit(ctx, run.WorkflowID)
	if err != nil {
		return err
	}
	bundle := proof.Bundle{SchemaVersion: proof.CurrentBundleSchemaVersion, WorkflowID: run.WorkflowID, CandidateKind: run.CandidateKind, Candidate: run.Candidate, CandidateSHA: run.CandidateSHA, CandidateBase: run.CandidateBase, CandidateRepository: run.CandidateRepository, TrustLevel: run.TrustLevel, ManifestSHA256: run.ManifestSHA256, IsolationPolicy: run.IsolationPolicy, ContainerImage: run.ContainerImage, Verification: run, Evidence: items, Artifacts: refs, Audit: audit}
	if _, err = bundle.Marshal(); err != nil {
		return fmt.Errorf("validate proof bundle: %w", err)
	}
	return bundle.VerifyArtifacts(ctx, w.Artifacts)
}

func mapConclusion(state verification.State) (verification.State, string) {
	switch state {
	case verification.StatePass:
		return state, "success"
	case verification.StateFail:
		return state, "failure"
	case verification.StateStale, verification.StateCancelled:
		return state, "cancelled"
	default:
		return state, "action_required"
	}
}

func (w PullWorker) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func stableVerificationID(workflow string) string {
	digest := sha256.Sum256([]byte(workflow))
	return "github-verification-" + hex.EncodeToString(digest[:16])
}

func fetchPullCandidate(ctx context.Context, owner, repo string, number int, expected, token, gitBaseURL string) (string, func(), error) {
	remote, authScope, err := pullCandidateRemote(gitBaseURL, owner, repo)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "verity-github-candidate-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	runGit := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"}
		if token != "" {
			auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
			cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http."+authScope+"/.extraheader", "GIT_CONFIG_VALUE_0=AUTHORIZATION: basic "+auth)
		}
		out, e := cmd.CombinedOutput()
		if e != nil {
			return nil, fmt.Errorf("fetch pull request candidate: %w: %s", e, strings.TrimSpace(string(out)))
		}
		return out, nil
	}
	if _, err = runGit("init", "--bare"); err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err = runGit("fetch", "--no-tags", remote, "+refs/pull/"+fmt.Sprint(number)+"/head:refs/verity/candidate"); err != nil {
		cleanup()
		return "", nil, err
	}
	resolved, err := runGit("rev-parse", "refs/verity/candidate^{commit}")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if strings.TrimSpace(string(resolved)) != expected {
		cleanup()
		return "", nil, errors.New("fetched pull request ref does not match webhook head SHA")
	}
	return filepath.Clean(dir), cleanup, nil
}

func validRepositoryName(repository string) bool {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
				return false
			}
		}
	}
	return true
}

func pullCandidateRemote(gitBaseURL, owner, repo string) (remote, authScope string, err error) {
	if !validRepositoryName(owner + "/" + repo) {
		return "", "", errors.New("invalid GitHub owner or repository name")
	}
	if gitBaseURL == "" {
		gitBaseURL = "https://github.com"
	}
	base, err := url.Parse(gitBaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Opaque != "" {
		return "", "", errors.New("GitHub Git base URL must be HTTPS without credentials, query, or fragment")
	}
	for _, part := range strings.Split(base.EscapedPath(), "/") {
		if part == "." || part == ".." || strings.Contains(strings.ToLower(part), "%2e") {
			return "", "", errors.New("GitHub Git base URL cannot contain traversal path segments")
		}
	}
	basePath := strings.TrimRight(base.EscapedPath(), "/")
	authScope = base.Scheme + "://" + base.Host + basePath
	remote = authScope + "/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + ".git"
	return remote, authScope, nil
}
