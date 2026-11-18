package lifecycle

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"verity/internal/proof"
	"verity/internal/verification"
)

type memoryEvidence struct{ records []proof.Evidence }

func (m *memoryEvidence) SaveEvidence(_ context.Context, e proof.Evidence) error {
	m.records = append(m.records, e)
	return nil
}
func (m *memoryEvidence) Evidence(_ context.Context, wf, sha string) ([]proof.Evidence, error) {
	var out []proof.Evidence
	for _, e := range m.records {
		if e.WorkflowID == wf && e.CandidateSHA == sha {
			out = append(out, e)
		}
	}
	return out, nil
}

type implement func(context.Context, string, int) (Candidate, error)

func (f implement) Implement(c context.Context, r string, n int) (Candidate, error) {
	return f(c, r, n)
}

type reviewer struct{}

func (reviewer) Review(context.Context, Candidate) (bool, string, error) {
	return true, "reviewed", nil
}

type humanReview struct {
	allow bool
	actor string
}

func (h humanReview) Decide(context.Context, Candidate, string) (bool, string, error) {
	return h.allow, h.actor, nil
}

type deployer struct{ sha string }

func (d *deployer) Deploy(_ context.Context, c Candidate) (Preview, error) {
	d.sha = c.SHA
	return Preview{ID: "preview-1", URL: "http://localhost", SourceSHA: c.SHA}, nil
}
func (*deployer) Remove(context.Context, Preview) error { return nil }

type smoke struct{}

func (smoke) Verify(context.Context, Preview) ([]byte, error) { return []byte("smoke passed"), nil }

type approval struct{ allow bool }

func (a approval) Approve(context.Context, string, string) (bool, error) { return a.allow, nil }

func TestLifecycleBuildsCandidateBoundProofAndBlocksProduction(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	run := func(name string, args ...string) {
		t.Helper()
		c := exec.Command(name, args...)
		c.Dir = repo
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%s: %v: %s", name, e, b)
		}
	}
	run("git", "init", "-q")
	run("git", "config", "user.email", "verity@example.test")
	run("git", "config", "user.name", "Verity Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "README.md")
	run("git", "commit", "-qm", "fixture")
	shaBytes, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := string(shaBytes[:len(shaBytes)-1])
	manifest := verification.Manifest{Version: 1, Commands: map[string]verification.CommandCheck{"ok": {Command: []string{"true"}, Required: true, TimeoutSeconds: 5, Criteria: []string{"works"}}}, Criteria: []verification.Criterion{{ID: "works", Description: "works"}}}
	artifacts := proof.LocalArtifactStore{Root: filepath.Join(t.TempDir(), "artifacts")}
	records := &memoryEvidence{}
	deploy := &deployer{}
	s := Service{Implementer: implement(func(context.Context, string, int) (Candidate, error) {
		return Candidate{SHA: sha, Kind: verification.CandidateGitCommit}, nil
	}), Verification: verification.Engine{Runner: verification.CommandRunner{}}, Reviewer: reviewer{}, Preview: deploy, Smoke: smoke{}, Approval: approval{allow: true}, Artifacts: artifacts, Evidence: records, Now: func() time.Time { return time.Unix(1, 0) }}
	out, err := s.Run(ctx, Config{WorkflowID: "wf_demo", RunID: "verify_demo", Repository: repo, Profile: verification.Standard}, "add feature", manifest, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Bundle.CandidateSHA != sha || deploy.sha != sha || out.ProductionDeployed {
		t.Fatalf("unexpected release result: %+v", out)
	}
	if err = out.Bundle.VerifyArtifacts(ctx, artifacts); err != nil {
		t.Fatal(err)
	}
	manifest.Review = true
	config := Config{WorkflowID: "wf_demo", RunID: "human_review", Repository: repo, Profile: verification.Standard}
	if _, err = s.Run(ctx, config, "add feature", manifest, true, true); err == nil {
		t.Fatal("reviewer alone must not satisfy human review")
	}
	s.HumanReview = humanReview{allow: true}
	if _, err = s.Run(ctx, config, "add feature", manifest, true, true); err == nil {
		t.Fatal("unattributed human review accepted")
	}
	s.HumanReview = humanReview{allow: false, actor: "reviewer"}
	if _, err = s.Run(ctx, config, "add feature", manifest, true, true); err != ErrGate {
		t.Fatalf("rejected human review: %v", err)
	}
	s.HumanReview = humanReview{allow: true, actor: "reviewer"}
	reviewed, err := s.Run(ctx, config, "add feature", manifest, true, true)
	if err != nil || reviewed.Verification.Review.HumanActor != "reviewer" {
		t.Fatalf("human review not recorded: %v", err)
	}
	if _, err = s.Run(ctx, Config{WorkflowID: "wf_demo", RunID: "verify_demo2", Repository: repo, Profile: verification.Standard, ProductionEnabled: true}, "add feature", manifest, true, true); err == nil {
		t.Fatal("production deployment must remain disabled")
	}
}
