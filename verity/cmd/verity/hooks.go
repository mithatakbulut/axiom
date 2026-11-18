package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"verity/internal/domain"
	"verity/internal/hooks"
	"verity/internal/process/control"
	"verity/internal/proof"
	"verity/internal/store/sqlite"
	"verity/internal/verification"
)

func hooksCommand(args []string, opts options) error {
	configPath, err := filepath.Abs(opts.configPath())
	if err != nil {
		return err
	}
	repository := filepath.Dir(filepath.Dir(configPath))
	switch args[1] {
	case "install":
		binary, err := os.Executable()
		if err != nil {
			return err
		}
		ctx := context.Background()
		preExisting, inspectErr := hooks.Inspect(ctx, repository)
		if inspectErr != nil {
			return inspectErr
		}
		status, err := hooks.Install(ctx, repository, binary, len(args) == 3 && args[2] == "--chain")
		if err != nil {
			return err
		}
		post, err := hooks.InstallPostCommit(ctx, repository, binary, len(args) == 3 && args[2] == "--chain")
		if err != nil {
			if !preExisting.Installed {
				_, _ = hooks.Uninstall(ctx, repository)
			}
			return fmt.Errorf("pre-commit installed; post-commit install failed: %w", err)
		}
		fmt.Println(status.Message)
		fmt.Println(status.HookPath)
		fmt.Println(post.Message)
		fmt.Println(post.HookPath)
		return nil
	case "status":
		status, err := hooks.Inspect(context.Background(), repository)
		if err != nil {
			return err
		}
		post, postErr := hooks.InspectPostCommit(context.Background(), repository)
		if postErr != nil {
			return postErr
		}
		fmt.Println("pre-commit:", status.Message)
		if status.HookPath != "" {
			fmt.Println(status.HookPath)
		}
		fmt.Println("post-commit:", post.Message)
		if post.HookPath != "" {
			fmt.Println(post.HookPath)
		}
		if status.Installed && !status.Active {
			return errors.New("managed hook is not active")
		}
		if post.Installed && !post.Active {
			return errors.New("managed post-commit hook is not active")
		}
		return nil
	case "uninstall":
		status, err := hooks.Uninstall(context.Background(), repository)
		if err != nil {
			return err
		}
		post, postErr := hooks.UninstallPostCommit(context.Background(), repository)
		if postErr != nil {
			return postErr
		}
		fmt.Println(status.Message)
		fmt.Println(post.Message)
		return nil
	case "run":
		return runPreCommitVerification(context.Background(), configPath)
	case "post-commit":
		return bindPostCommit(context.Background(), configPath)
	default:
		return errors.New("usage: verity hooks install|status|uninstall|run")
	}
}

func bindPostCommit(ctx context.Context, configPath string) error {
	projectRoot := filepath.Dir(filepath.Dir(configPath))
	gitOutput := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", projectRoot}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	commit, err := gitOutput("rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve committed revision: %w", err)
	}
	tree, err := gitOutput("rev-parse", "HEAD^{tree}")
	if err != nil {
		return fmt.Errorf("resolve committed tree: %w", err)
	}
	commonDir, err := gitOutput("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("resolve Git common directory: %w", err)
	}
	commonDir, err = filepath.EvalSymlinks(commonDir)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	dbPath := cfg.Database
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(projectRoot, dbPath)
	}
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	verificationRun, found, err := db.LatestPassingIndexVerification(ctx, commonDir, tree)
	if err != nil {
		return err
	}
	if !found {
		fmt.Fprintf(os.Stderr, "Verity: commit %s tree %s has no matching passing pre-commit proof; no commit binding recorded.\n", commit, tree)
		return nil
	}
	audit, err := db.ProofAudit(ctx, verificationRun.WorkflowID)
	if err != nil {
		return err
	}
	eventID, err := domain.NewID("proof")
	if err != nil {
		return err
	}
	event := proof.AuditEvent{
		ID: string(eventID), WorkflowID: verificationRun.WorkflowID, Sequence: int64(len(audit) + 1),
		Type: "commit_tree_bound", CandidateSHA: tree, Actor: "git-post-commit-hook", At: time.Now().UTC(),
		Details: map[string]string{"commit_sha": commit, "verification_id": verificationRun.ID, "candidate_kind": string(verification.CandidateGitIndex)},
	}
	if err := db.AppendProofAudit(ctx, event); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Verity: commit %s bound to passing staged-tree proof %s.\n", commit, verificationRun.ID)
	return nil
}

func runPreCommitVerification(ctx context.Context, configPath string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	projectRoot := filepath.Dir(filepath.Dir(configPath))
	manifest, err := verification.LoadManifest(filepath.Join(projectRoot, verification.ManifestPath))
	if err != nil {
		return fmt.Errorf("load trusted verification manifest: %w", err)
	}
	checks, configured := manifest.Profiles[verification.PreCommit]
	if !configured || len(checks) == 0 {
		return errors.New("no checks are configured for the pre-commit profile in .verity/verification.yaml")
	}
	candidate, err := verification.CaptureIndex(ctx, projectRoot)
	if err != nil {
		return fmt.Errorf("capture staged candidate: %w", err)
	}
	if err := rejectCandidateControlFiles(ctx, projectRoot, candidate.Tree, configPath); err != nil {
		return err
	}
	configDigest, err := trustedFileDigest(configPath)
	if err != nil {
		return err
	}
	plan, err := manifest.Plan(candidate.Tree, verification.PreCommit)
	if err != nil {
		return err
	}
	workflow, err := domain.NewID("hook")
	if err != nil {
		return err
	}
	digest, err := indexPlanApprovalDigest(string(workflow), candidate, configDigest, plan, manifest)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Verity pre-commit plan (%s)\n", string(workflow))
	fmt.Fprintf(os.Stderr, "Candidate tree: %s\nBase HEAD: %s\nApproval digest: %s\n", candidate.Tree, candidate.Base, digest)
	fmt.Fprintf(os.Stderr, "Isolation policy: %s\n", plan.IsolationPolicy)
	for _, check := range plan.Checks {
		fmt.Fprintf(os.Stderr, "- %s required=%t argv=%q\n", check.ID, check.Required, check.Argv)
	}
	fmt.Fprintln(os.Stderr, "The git commit invocation approves this exact displayed plan.")

	dbPath := cfg.Database
	if !filepath.IsAbs(dbPath) {
		dbPath = filepath.Join(projectRoot, dbPath)
	}
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	definition := control.Definition{
		SchemaVersion: 1, ID: string(workflow), Name: "Pre-commit verification", Version: 1,
		Tasks:    []control.Task{{ID: "verify", Title: "Verify staged candidate", MaxAttempts: 1}},
		MaxTasks: 1, MaxAttempts: 1, MaxTransitions: 8,
	}
	if err := db.SaveDefinition(ctx, definition); err != nil {
		return err
	}
	run, err := control.NewRun(string(workflow), definition, time.Now())
	if err != nil {
		return err
	}
	if err := run.Start(); err != nil {
		return err
	}
	if _, err := run.Schedule(); err != nil {
		return err
	}
	if err := run.BeginTask("verify"); err != nil {
		return err
	}
	if err := db.CreateRun(ctx, run); err != nil {
		return err
	}
	before := run.Transitions
	engine := verification.Engine{
		Runner:              verification.CommandRunner{Snapshotter: verification.IndexSnapshotter{}},
		Candidate:           verification.IndexCurrent{Repository: candidate.RepositoryRoot, Candidate: candidate},
		CandidateKind:       verification.CandidateGitIndex,
		CandidateBase:       candidate.Base,
		CandidateRepository: candidate.GitCommonDir,
	}
	result, verifyErr := engine.Verify(ctx, candidate.RepositoryRoot, string(workflow), string(workflow), "verify", manifest, verification.PreCommit, candidate.Tree, true)
	if verifyErr == nil {
		artifactRoot := cfg.DataDir
		if !filepath.IsAbs(artifactRoot) {
			artifactRoot = filepath.Join(projectRoot, artifactRoot)
		}
		result, verifyErr = proof.AttachCheckEvidence(ctx, result, proof.LocalArtifactStore{Root: filepath.Join(artifactRoot, "artifacts")}, db, time.Now().UTC())
	}
	if verifyErr == nil {
		verifyErr = db.SaveVerification(ctx, result)
	}
	passed := verifyErr == nil && result.State == verification.StatePass
	if err := run.FinishTask("verify", passed); err != nil {
		return errors.Join(verifyErr, err)
	}
	if run.State == control.Running {
		if _, err := run.Schedule(); err != nil {
			return errors.Join(verifyErr, err)
		}
	}
	if err := db.UpdateRun(ctx, run, before); err != nil {
		return errors.Join(verifyErr, err)
	}
	if verifyErr != nil {
		return fmt.Errorf("pre-commit verification failed; workflow %s: %w", workflow, verifyErr)
	}
	fmt.Fprintf(os.Stderr, "Verification %s: %s\nInspect with: verity proof show %s\n", result.ID, result.State, result.ID)
	if !passed {
		return fmt.Errorf("pre-commit verification %s; inspect workflow %s and proof %s", result.State, workflow, result.ID)
	}
	return nil
}
