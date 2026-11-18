package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"verity/internal/candidate"
	"verity/internal/proof"
	"verity/internal/store/sqlite"
	"verity/internal/verification"
)

func TestPullRequestBaseRevisionIsExtractedOnlyWhenFullSHA(t *testing.T) {
	sha := strings.Repeat("a", 40)
	payload := `{"pull_request":{"base":{"sha":"` + sha + `"}}}`
	if got := pullRequestBaseRevision(payload); got != sha {
		t.Fatalf("base revision=%q want %q", got, sha)
	}
	if got := pullRequestBaseRevision(`{"base_sha":"` + sha + `"}`); got != sha {
		t.Fatalf("normalized base revision=%q want %q", got, sha)
	}
	for _, invalid := range []string{`{}`, `{"pull_request":{"base":{"sha":"main"}}}`, `{invalid`} {
		if got := pullRequestBaseRevision(invalid); got != "" {
			t.Fatalf("invalid base revision accepted: %q", got)
		}
	}
}

func TestCheckSummaryProvidesCandidateProofExportCommand(t *testing.T) {
	proofID := "github-verification-0123456789abcdef"
	summary := checkSummary(verification.StatePass, proofID)
	if !strings.Contains(summary, "Proof ID: "+proofID) || !strings.Contains(summary, "verity proof export "+proofID+" proof-"+proofID+".json") {
		t.Fatalf("check summary does not identify a downloadable local proof: %q", summary)
	}
}

func TestPullCandidateRemoteValidatesPublicAndEnterpriseURLs(t *testing.T) {
	for _, tc := range []struct {
		name, base, remote, scope string
		wantErr                   bool
	}{
		{"public default", "", "https://github.com/acme/app.git", "https://github.com", false},
		{"enterprise prefix", "https://git.example.test/enterprise/", "https://git.example.test/enterprise/acme/app.git", "https://git.example.test/enterprise", false},
		{"plain HTTP", "http://git.example.test", "", "", true},
		{"userinfo", "https://user:secret@git.example.test", "", "", true},
		{"query", "https://git.example.test?token=secret", "", "", true},
		{"fragment", "https://git.example.test/#fragment", "", "", true},
		{"traversal", "https://git.example.test/../outside", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote, scope, err := pullCandidateRemote(tc.base, "acme", "app")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("accepted unsafe base URL %q", tc.base)
				}
				return
			}
			if err != nil || remote != tc.remote || scope != tc.scope {
				t.Fatalf("remote=(%q,%q,%v), want (%q,%q)", remote, scope, err, tc.remote, tc.scope)
			}
		})
	}
	if validRepositoryName("acme/../app") || validRepositoryName("acme/app/extra") {
		t.Fatal("unsafe repository path was accepted")
	}
}

func TestFetchPullCandidateUsesEnterpriseRemoteAndScopesCredentials(t *testing.T) {
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "git.log")
	sha := strings.Repeat("a", 40)
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" \"$GIT_CONFIG_KEY_0\" \"$GIT_CONFIG_VALUE_0\" >> %q\ncase \"$*\" in *rev-parse*) echo %s;; esac\n", logPath, sha)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	root, cleanup, err := fetchPullCandidate(context.Background(), "acme", "app", 12, sha, "fixture-token", "https://git.example.test/enterprise")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err = os.Stat(root); err != nil {
		t.Fatalf("candidate directory: %v", err)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(log)
	for _, want := range []string{
		"https://git.example.test/enterprise/acme/app.git",
		"http.https://git.example.test/enterprise/.extraheader",
		"AUTHORIZATION: basic eC1hY2Nlc3MtdG9rZW46Zml4dHVyZS10b2tlbg==",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("git invocation log missing %q: %s", want, text)
		}
	}
}

type fixedSnapshotter struct{ root string }

func (s fixedSnapshotter) Snapshot(context.Context, string, string) (string, func(), error) {
	return s.root, func() {}, nil
}

type passingPullVerifier struct{}

func (passingPullVerifier) Verify(_ context.Context, _ string, verificationID, workflowID string, manifest verification.Manifest, candidateSHA, baseRevision, repository string) (verification.Run, error) {
	plan, err := manifest.Plan(candidateSHA, verification.Standard)
	if err != nil {
		return verification.Run{}, err
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return verification.Run{}, err
	}
	manifestHash := sha256.Sum256(manifestBytes)
	now := time.Now().UTC()
	zero := 0
	run := verification.Run{
		ID: verificationID, SchemaVersion: verification.CurrentRunSchemaVersion, WorkflowID: workflowID, TaskID: "verify",
		Candidate:     candidate.Identity{SchemaVersion: 1, Kind: candidate.GitCommit, ID: candidateSHA, Repository: repository, BaseRevision: baseRevision, CapturedAt: now, Provenance: "github-worker-test"},
		CandidateKind: verification.CandidateGitCommit, CandidateSHA: candidateSHA, CandidateBase: baseRevision,
		CandidateRepository: repository, TrustLevel: verification.TrustUntrusted, ManifestSHA256: hex.EncodeToString(manifestHash[:]),
		IsolationPolicy: verification.IsolationRootlessContainer, ContainerImage: manifest.ContainerImage,
		Profile: verification.Standard, StartedAt: now, Criteria: append([]verification.Criterion(nil), manifest.Criteria...),
	}
	for _, check := range plan.Checks {
		run.Checks = append(run.Checks, verification.CheckResult{ID: check.ID, Name: check.Name, Required: check.Required,
			Outcome: verification.Passed, StartedAt: now, FinishedAt: now, ExitCode: &zero, Output: "fixture verification passed",
			CriteriaIDs: append([]string(nil), check.CriteriaIDs...)})
	}
	run.State = verification.Evaluate(run, manifest.Review)
	run.FinishedAt = now
	return run, run.Validate()
}

func TestPullWorkerPublishesExactCandidateAndKeepsSupersededHeadRed(t *testing.T) {
	sha, newer := strings.Repeat("a", 40), strings.Repeat("b", 40)
	for _, tc := range []struct {
		name           string
		liveHead       string
		wantConclusion string
		wantSummary    bool
	}{{"current head passes", sha, "success", true}, {"head changes while verifying", newer, "cancelled", false}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("candidate\n"), 0600); err != nil {
				t.Fatal(err)
			}
			db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "worker.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			payload := []byte(`{"action":"opened","number":12,"repository":{"full_name":"acme/app"},"pull_request":{"head":{"sha":"` + sha + `"},"base":{"sha":"` + strings.Repeat("e", 40) + `"}}}`)
			mac := hmac.New(sha256.New, []byte("fixture-webhook-secret"))
			_, _ = mac.Write(payload)
			request := httptest.NewRequest(http.MethodPost, "/github/webhook", strings.NewReader(string(payload)))
			request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
			request.Header.Set("X-GitHub-Delivery", "delivery-1")
			request.Header.Set("X-GitHub-Event", "pull_request")
			responseRecorder := httptest.NewRecorder()
			Webhook{Secret: []byte("fixture-webhook-secret"), Handler: PullWorkflowHandler{Repository: "acme/app", Store: db}}.ServeHTTP(responseRecorder, request)
			if responseRecorder.Code != http.StatusAccepted {
				t.Fatalf("signed delivery status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
			}
			links, err := db.PullWorkflows(ctx, "acme/app")
			if err != nil || len(links) != 1 {
				t.Fatalf("workflow association: %#v %v", links, err)
			}
			manifest, err := verification.ParseManifest([]byte("version: 1\ntrust_level: untrusted\nisolation: rootless_container_no_network\ncontainer_image: checks@sha256:" + strings.Repeat("c", 64) + "\ncommands:\n  check:\n    command: ['true']\n    required: true\n    timeout_seconds: 10\n    criteria: [works]\ncriteria:\n  - id: works\n    description: Works\n"))
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			gitScript := "#!/bin/sh\nfor arg do last=\"$arg\"; done\nif [ \"$last\" = 'refs/verity/candidate^{commit}' ]; then echo " + sha + "; fi\n"
			if err = os.WriteFile(filepath.Join(bin, "git"), []byte(gitScript), 0700); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			checkGets, headGets := 0, 0
			var completed map[string]any
			summaryPublished := false
			client := Client{BaseURL: "https://api.example.test", Token: "fixture-token", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				mu.Lock()
				defer mu.Unlock()
				path := r.URL.Path
				switch {
				case r.Method == http.MethodGet && strings.Contains(path, "/check-runs"):
					checkGets++
					if checkGets == 1 {
						return response(r, http.StatusOK, `{"check_runs":[]}`), nil
					}
					return response(r, http.StatusOK, `{"check_runs":[{"id":91,"name":"Verity","external_id":"`+links[0].WorkflowID+`"}]}`), nil
				case r.Method == http.MethodPost && strings.HasSuffix(path, "/check-runs"):
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["head_sha"] != sha {
						t.Errorf("created check for wrong SHA: %v", body["head_sha"])
					}
					return response(r, http.StatusOK, `{}`), nil
				case r.Method == http.MethodPatch && strings.HasSuffix(path, "/check-runs/91"):
					_ = json.NewDecoder(r.Body).Decode(&completed)
					if completed["head_sha"] != sha {
						t.Errorf("completed check for wrong SHA: %v", completed["head_sha"])
					}
					return response(r, http.StatusOK, `{}`), nil
				case r.Method == http.MethodGet && strings.HasSuffix(path, "/pulls/12"):
					headGets++
					return response(r, http.StatusOK, `{"head":{"sha":"`+tc.liveHead+`"}}`), nil
				case r.Method == http.MethodGet && strings.Contains(path, "/comments"):
					return response(r, http.StatusOK, `[]`), nil
				case r.Method == http.MethodPost && strings.Contains(path, "/comments"):
					summaryPublished = true
					return response(r, http.StatusOK, `{}`), nil
				default:
					t.Errorf("unexpected GitHub request %s %s", r.Method, path)
					return response(r, http.StatusNotFound, `{}`), nil
				}
			})}}
			worker := PullWorker{Store: db, Repository: "acme/app", GitHub: client, Manifest: manifest, Artifacts: proof.LocalArtifactStore{Root: filepath.Join(t.TempDir(), "artifacts")}, Snapshotter: fixedSnapshotter{root: root}, OutputLimit: 1024, Now: func() time.Time { return time.Now().UTC() }, verifier: passingPullVerifier{}}
			oldPath := os.Getenv("PATH")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+oldPath)
			// The candidate fetch uses the fake git executable. The injected runner
			// exercises worker orchestration without claiming container isolation.
			worker.Snapshotter = fixedSnapshotter{root: root}
			if err = worker.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			job, err := db.PullJob(ctx, links[0].WorkflowID)
			if err != nil || job.State != "COMPLETED" {
				t.Fatalf("worker job=%+v err=%v", job, err)
			}
			verified, err := db.Verification(ctx, stableVerificationID(links[0].WorkflowID))
			if err != nil || verified.CandidateSHA != sha || verified.State != verification.StatePass {
				t.Fatalf("verification=%+v err=%v", verified, err)
			}
			want := tc.wantConclusion
			if completed["conclusion"] != want {
				t.Fatalf("Check Run conclusion=%v want %s", completed["conclusion"], want)
			}
			wantHeadGets := 2
			if tc.wantConclusion == "cancelled" {
				wantHeadGets = 1 // Verification was stale, so publication stops before a final head read.
			}
			if completed["status"] != "completed" || checkGets != 2 || headGets != wantHeadGets {
				t.Fatalf("publication flow: completed=%v checkGets=%d headGets=%d wantHeadGets=%d", completed, checkGets, headGets, wantHeadGets)
			}
			output, _ := completed["output"].(map[string]any)
			summary, _ := output["summary"].(string)
			proofID := stableVerificationID(links[0].WorkflowID)
			if !strings.Contains(summary, "verity proof export "+proofID) {
				t.Fatalf("Check Run proof export reference missing: %q", summary)
			}
			if summaryPublished != tc.wantSummary {
				t.Fatalf("PR summary published=%t want %t", summaryPublished, tc.wantSummary)
			}
		})
	}
}

func TestPullWorkerRequeuesRateLimitedPublicationWithoutFalseSuccess(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "worker.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sha := strings.Repeat("d", 40)
	event := Event{DeliveryID: "rate-limited-delivery", Type: "pull_request", Action: "opened", Repository: "acme/app", Number: 3, HeadSHA: sha}
	if err = (PullWorkflowHandler{Repository: "acme/app", Store: db}).HandleGitHubEventAtomic(ctx, event); err != nil {
		t.Fatal(err)
	}
	links, err := db.PullWorkflows(ctx, "acme/app")
	if err != nil || len(links) != 1 {
		t.Fatalf("workflow association: %#v %v", links, err)
	}
	manifest, err := verification.ParseManifest([]byte("version: 1\ntrust_level: untrusted\nisolation: rootless_container_no_network\ncontainer_image: checks@sha256:" + strings.Repeat("c", 64) + "\ncommands:\n  check:\n    command: [echo, ok]\n    required: true\n    timeout_seconds: 10\n    criteria: [works]\ncriteria:\n  - id: works\n    description: Works\n"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := Client{BaseURL: "https://api.example.test", Token: "fixture-token", MaxRetries: 0, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, http.StatusTooManyRequests, `{"message":"rate limited"}`), nil
	})}}
	now := time.Now().UTC()
	worker := PullWorker{Store: db, Repository: "acme/app", GitHub: client, Manifest: manifest, Artifacts: proof.LocalArtifactStore{Root: filepath.Join(t.TempDir(), "artifacts")}, Now: func() time.Time { return now }}
	if err = worker.RunOnce(ctx); !errors.Is(err, ErrRateLimit) {
		t.Fatalf("worker error=%v, want ErrRateLimit", err)
	}
	job, err := db.PullJob(ctx, links[0].WorkflowID)
	if err != nil || job.State != "PENDING" || job.Attempts != 1 || !strings.Contains(job.LastError, ErrRateLimit.Error()) || !job.NextAttempt.After(now) {
		t.Fatalf("rate-limited job was not durably requeued: %+v err=%v", job, err)
	}
	run, err := db.GetRun(ctx, links[0].WorkflowID)
	if err != nil || run.State != "READY" {
		t.Fatalf("rate limit falsely advanced workflow: state=%s err=%v", run.State, err)
	}
	if calls != 1 {
		t.Fatalf("rate-limit handling made %d API calls, want one with retries disabled", calls)
	}
}
