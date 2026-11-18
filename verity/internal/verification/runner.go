package verification

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultOutputLimit = 1 << 20
const defaultArchiveLimit = 512 << 20
const defaultSnapshotEntryLimit = 50_000
const snapshotOwnerMarker = ".verity-snapshot-owner"
const snapshotOwnerMarkerValue = "verity-snapshot-owner-v1\n"

// Snapshotter produces an isolated tree for the requested immutable revision.
type Snapshotter interface {
	Snapshot(context.Context, string, string) (path string, cleanup func(), err error)
}

type CheckRunner interface {
	Execute(context.Context, string, Plan, bool) ([]CheckResult, error)
}

// GitSnapshotter materializes a commit with git archive. Checks can alter the
// disposable tree, but never the candidate worktree or Git metadata.
type GitSnapshotter struct{ GitBinary string }

func (s GitSnapshotter) Snapshot(ctx context.Context, repository, revision string) (string, func(), error) {
	if !validRevision(revision) {
		return "", nil, ErrInvalidCandidate
	}
	release, err := acquireSnapshotSlot(ctx)
	if err != nil {
		return "", nil, err
	}
	releaseOnReturn := true
	defer func() {
		if releaseOnReturn {
			release()
		}
	}()
	root, err := filepath.Abs(repository)
	if err != nil {
		return "", nil, err
	}
	git := s.GitBinary
	if git == "" {
		git = "git"
	}
	verify := exec.CommandContext(ctx, git, "-C", root, "rev-parse", "--verify", revision+"^{commit}")
	sha, err := verify.Output()
	if err != nil {
		return "", nil, fmt.Errorf("resolve candidate: %w", err)
	}
	if strings.TrimSpace(string(sha)) != revision {
		return "", nil, ErrCandidateChanged
	}
	archive := exec.CommandContext(ctx, git, "-C", root, "archive", "--format=tar", revision)
	var archiveData limitedBuffer
	archiveData.limit = defaultArchiveLimit
	archive.Stdout = &archiveData
	var archiveErr bytes.Buffer
	archive.Stderr = &archiveErr
	err = archive.Run()
	if err != nil {
		return "", nil, fmt.Errorf("archive candidate: %w: %s", err, strings.TrimSpace(archiveErr.String()))
	}
	if archiveData.truncated {
		return "", nil, ErrArchiveTooLarge
	}
	dir, cleanup, err := unpackSnapshotLocked(archiveData.Bytes(), defaultArchiveLimit, defaultArchiveLimit, defaultSnapshotEntryLimit)
	if err != nil {
		return "", nil, err
	}
	releaseOnReturn = false
	return dir, func() {
		cleanup()
		release()
	}, nil
}

func unpackSnapshot(data []byte) (string, func(), error) {
	return unpackSnapshotWithLimitsContext(context.Background(), data, defaultArchiveLimit, defaultArchiveLimit, defaultSnapshotEntryLimit)
}

func unpackSnapshotWithLimits(data []byte, archiveLimit, contentLimit int64, entryLimit int) (string, func(), error) {
	return unpackSnapshotWithLimitsContext(context.Background(), data, archiveLimit, contentLimit, entryLimit)
}

func unpackSnapshotWithLimitsContext(ctx context.Context, data []byte, archiveLimit, contentLimit int64, entryLimit int) (string, func(), error) {
	release, err := acquireSnapshotSlot(ctx)
	if err != nil {
		return "", nil, err
	}
	dir, cleanup, err := unpackSnapshotLocked(data, archiveLimit, contentLimit, entryLimit)
	if err != nil {
		release()
		return "", nil, err
	}
	return dir, func() {
		cleanup()
		release()
	}, nil
}

func unpackSnapshotLocked(data []byte, archiveLimit, contentLimit int64, entryLimit int) (string, func(), error) {
	if archiveLimit <= 0 || contentLimit <= 0 || entryLimit <= 0 || int64(len(data)) > archiveLimit {
		return "", nil, ErrSnapshotQuotaExceeded
	}
	leaseDir, err := os.MkdirTemp("", "verity-candidate-snapshot-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(leaseDir) }
	if err := os.WriteFile(filepath.Join(leaseDir, snapshotOwnerMarker), []byte(snapshotOwnerMarkerValue), 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	dir := filepath.Join(leaseDir, "source")
	if err := os.Mkdir(dir, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	tr := tar.NewReader(bytes.NewReader(data))
	var extracted int64
	entries := 0
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			cleanup()
			return "", nil, e
		}
		entries++
		if entries > entryLimit {
			cleanup()
			return "", nil, ErrSnapshotQuotaExceeded
		}
		name := filepath.Clean(filepath.FromSlash(h.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			cleanup()
			return "", nil, ErrUnsafeArchive
		}
		target := filepath.Join(dir, name)
		if !strings.HasPrefix(target, dir+string(filepath.Separator)) {
			cleanup()
			return "", nil, ErrUnsafeArchive
		}
		switch h.Typeflag {
		case tar.TypeXHeader, tar.TypeXGlobalHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		case tar.TypeDir:
			e = os.MkdirAll(target, 0700)
		case tar.TypeReg, tar.TypeRegA:
			if h.Size < 0 || h.Size > contentLimit-extracted {
				e = ErrSnapshotQuotaExceeded
				break
			}
			if e = os.MkdirAll(filepath.Dir(target), 0700); e == nil {
				var f *os.File
				mode := os.FileMode(0600)
				if h.Mode&0111 != 0 {
					mode = 0700
				}
				f, e = os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
				if e == nil {
					copied, copyErr := io.CopyN(f, tr, h.Size)
					e = copyErr
					if e == nil && copied != h.Size {
						e = io.ErrUnexpectedEOF
					}
					if closeErr := f.Close(); e == nil {
						e = closeErr
					}
					if e == nil {
						extracted += copied
					}
				}
			}
		default:
			e = ErrUnsafeArchive
		}
		if e != nil {
			cleanup()
			return "", nil, e
		}
	}
	return dir, cleanup, nil
}

type CommandRunner struct {
	Snapshotter Snapshotter
	OutputLimit int
	BaseEnv     []string
}

// Execute requires explicit approval of the displayed plan. Commands are run
// directly (without a shell) in a throwaway source snapshot with bounded time
// and output. The default environment does not inherit credentials.
func (r CommandRunner) Execute(ctx context.Context, repository string, plan Plan, approved bool) ([]CheckResult, error) {
	if !approved {
		return nil, ErrNotApproved
	}
	if plan.TrustLevel == TrustUntrusted && plan.IsolationPolicy != IsolationRootlessContainer {
		return blockedChecks(plan, "untrusted checks require rootless container isolation"), nil
	}
	if plan.IsolationPolicy == IsolationRootlessContainer {
		return (ContainerRunner{Snapshotter: r.Snapshotter, OutputLimit: r.OutputLimit}).Execute(ctx, repository, plan, true)
	}
	if !validRevision(plan.CandidateSHA) {
		return nil, ErrInvalidCandidate
	}
	if plan.IsolationPolicy != IsolationLocalProcess {
		return nil, ErrUnsupportedIsolation
	}
	s := r.Snapshotter
	if s == nil {
		s = GitSnapshotter{}
	}
	limit := r.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	results := make([]CheckResult, 0, len(plan.Checks))
	for _, check := range plan.Checks {
		path, cleanup, err := s.Snapshot(ctx, repository, plan.CandidateSHA)
		if err != nil {
			return nil, err
		}
		result := CheckResult{ID: check.ID, Name: check.Name, Required: check.Required, CriteriaIDs: append([]string(nil), check.CriteriaIDs...), StartedAt: time.Now().UTC()}
		if len(check.Argv) == 0 || check.Timeout <= 0 {
			result.Outcome = Blocked
			result.Diagnostic = "invalid planned command or timeout"
			result.FinishedAt = time.Now().UTC()
			results = append(results, result)
			cleanup()
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, check.Timeout)
		argv := check.Argv
		if check.Adapter == "playwright" {
			argv = []string{"test", "--reporter=line", "--", check.Argv[0]}
		}
		binary := argv[0]
		if check.Adapter == "playwright" {
			binary = "playwright"
		}
		cmd := exec.CommandContext(checkCtx, binary, argv[1:]...)
		cmd.Dir = path
		cmd.Env = r.environment(path, check.BaseURL)
		var out limitedBuffer
		out.limit = limit
		cmd.Stdout = &out
		cmd.Stderr = &out
		err = cmd.Run()
		cancel()
		result.Output = redactKnownSecrets(out.String())
		result.FinishedAt = time.Now().UTC()
		if err == nil {
			result.Outcome = Passed
			z := 0
			result.ExitCode = &z
		} else if errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
			result.Outcome = TimedOut
			result.Diagnostic = "check timed out"
		} else if errors.Is(ctx.Err(), context.Canceled) {
			result.Outcome = Blocked
			result.Diagnostic = "verification cancelled"
		} else {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code := ee.ExitCode()
				result.ExitCode = &code
				result.Outcome = Failed
				result.Diagnostic = err.Error()
			} else {
				result.Outcome = Blocked
				result.Diagnostic = err.Error()
			}
		}
		results = append(results, result)
		cleanup()
		if ctx.Err() != nil {
			for _, pending := range plan.Checks[len(results):] {
				results = append(results, CheckResult{ID: pending.ID, Name: pending.Name, Required: pending.Required, Outcome: Blocked, Diagnostic: "verification cancelled", CriteriaIDs: pending.CriteriaIDs})
			}
			break
		}
	}
	return results, nil
}
func (r CommandRunner) environment(tmp, baseURL string) []string {
	var env []string
	for _, item := range r.BaseEnv {
		if strings.HasPrefix(item, "PATH=") || strings.HasPrefix(item, "LANG=") || strings.HasPrefix(item, "LC_ALL=") {
			env = append(env, item)
		}
	}
	if len(env) == 0 {
		env = append(env, "PATH="+os.Getenv("PATH"))
	}
	env = append(env, "HOME="+tmp, "TMPDIR="+tmp, "CI=true")
	if baseURL != "" {
		env = append(env, "PLAYWRIGHT_TEST_BASE_URL="+baseURL)
	}
	return env
}

type limitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	room := b.limit - b.Len()
	if room > 0 {
		if room > n {
			room = n
		}
		_, _ = b.Buffer.Write(p[:room])
	}
	if room < n {
		b.truncated = true
	}
	return n, nil
}
func (b *limitedBuffer) String() string {
	v := b.Buffer.String()
	if b.truncated {
		v += "\n[output truncated]"
	}
	return v
}
