package verification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// IndexCandidate identifies the full staged tree and its separate HEAD provenance.
type IndexCandidate struct {
	Tree           string `json:"tree"`
	Base           string `json:"base"`
	RepositoryRoot string `json:"repository_root"`
	GitCommonDir   string `json:"git_common_dir"`
}

// Current returns the staged tree only while both the index tree and its HEAD
// provenance still match the captured candidate.
type IndexCurrent struct {
	Repository string
	Candidate  IndexCandidate
}

func (c IndexCurrent) Current(ctx context.Context) (string, error) {
	if err := CheckIndexCurrent(ctx, c.Repository, c.Candidate); err != nil {
		return "", err
	}
	return c.Candidate.Tree, nil
}

func resolveGitRepository(ctx context.Context, repository string) (string, string, error) {
	command := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repository, "rev-parse"}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	root, err := command("--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("resolve Git repository root: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}
	commonDir, err := command("--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", fmt.Errorf("resolve Git common directory: %w", err)
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(root, commonDir)
	}
	commonDir, err = filepath.Abs(commonDir)
	if err != nil {
		return "", "", err
	}
	commonDir, err = filepath.EvalSymlinks(commonDir)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(commonDir)
	if err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("invalid Git common directory %q", commonDir)
	}
	return root, commonDir, nil
}

// CaptureIndex resolves a complete Git index to a tree. It fails for unmerged
// entries and does not change the index, working tree, branch, or refs.
func CaptureIndex(ctx context.Context, repository string) (IndexCandidate, error) {
	repository, commonDir, err := resolveGitRepository(ctx, repository)
	if err != nil {
		return IndexCandidate{}, err
	}
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repository}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w", args[0], err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	indexPath, err := run("rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return IndexCandidate{}, err
	}
	source, err := os.Open(indexPath)
	if err != nil {
		return IndexCandidate{}, err
	}
	defer source.Close()
	tempDir, err := os.MkdirTemp("", "verity-index-copy-*")
	if err != nil {
		return IndexCandidate{}, err
	}
	defer os.RemoveAll(tempDir)
	sharedPath, err := run("rev-parse", "--path-format=absolute", "--shared-index-path")
	if err != nil {
		return IndexCandidate{}, err
	}
	if sharedPath != "" {
		shared, openErr := os.Open(sharedPath)
		if openErr != nil {
			return IndexCandidate{}, openErr
		}
		copyFile, createErr := os.OpenFile(filepath.Join(tempDir, filepath.Base(sharedPath)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if createErr != nil {
			shared.Close()
			return IndexCandidate{}, createErr
		}
		_, copyErr := io.Copy(copyFile, shared)
		shared.Close()
		closeErr := copyFile.Close()
		if copyErr != nil {
			return IndexCandidate{}, copyErr
		}
		if closeErr != nil {
			return IndexCandidate{}, closeErr
		}
	}
	indexCopy, err := os.CreateTemp(tempDir, "index-*")
	if err != nil {
		return IndexCandidate{}, err
	}
	defer os.Remove(indexCopy.Name())
	_, err = io.Copy(indexCopy, source)
	closeErr := indexCopy.Close()
	if err != nil {
		return IndexCandidate{}, err
	}
	if closeErr != nil {
		return IndexCandidate{}, closeErr
	}
	indexEnv := append(os.Environ(), "GIT_INDEX_FILE="+indexCopy.Name())
	unmerged := exec.CommandContext(ctx, "git", "-C", repository, "ls-files", "-u", "-z")
	unmerged.Env = indexEnv
	if out, err := unmerged.Output(); err != nil {
		return IndexCandidate{}, fmt.Errorf("inspect staged Git index: %w", err)
	} else if len(out) > 0 {
		return IndexCandidate{}, ErrUnmergedIndex
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repository, "write-tree")
	cmd.Env = indexEnv
	out, err := cmd.Output()
	tree := strings.TrimSpace(string(out))
	if err != nil {
		return IndexCandidate{}, err
	}
	base, err := run("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return IndexCandidate{}, fmt.Errorf("resolve base HEAD: %w", err)
	}
	return IndexCandidate{Tree: tree, Base: base, RepositoryRoot: repository, GitCommonDir: commonDir}, nil
}

// IndexSnapshotter materializes blobs directly from the captured Git tree;
// neither checkout filters nor working-tree files participate in capture.
type IndexSnapshotter struct{}

func (IndexSnapshotter) Snapshot(ctx context.Context, repository, tree string) (string, func(), error) {
	if !validRevision(tree) {
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
	cmd := exec.CommandContext(ctx, "git", "-C", repository, "ls-tree", "-r", "-z", "--full-tree", tree)
	var listing limitedBuffer
	listing.limit = defaultArchiveLimit
	cmd.Stdout = &listing
	if err := cmd.Run(); err != nil {
		return "", nil, fmt.Errorf("list index tree: %w", err)
	}
	if listing.truncated {
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
	fail := func(err error) (string, func(), error) { cleanup(); return "", nil, err }
	var total int64
	entryCount := 0
	for _, entry := range bytes.Split(listing.Bytes(), []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		entryCount++
		if entryCount > defaultSnapshotEntryLimit {
			return fail(ErrSnapshotQuotaExceeded)
		}
		fields := bytes.SplitN(entry, []byte{'\t'}, 2)
		if len(fields) != 2 {
			return fail(errors.New("invalid Git tree entry"))
		}
		meta := strings.Fields(string(fields[0]))
		if len(meta) != 3 || meta[1] != "blob" {
			return fail(errors.New("unsupported Git tree entry (including submodules)"))
		}
		name := string(fields[1])
		clean := filepath.Clean(filepath.FromSlash(name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fail(ErrUnsafeArchive)
		}
		if meta[0] != "100644" && meta[0] != "100755" && meta[0] != "120000" {
			return fail(fmt.Errorf("unsupported Git mode %s", meta[0]))
		}
		blob := exec.CommandContext(ctx, "git", "-C", repository, "cat-file", "blob", meta[2])
		var data limitedBuffer
		data.limit = defaultArchiveLimit
		blob.Stdout = &data
		if err := blob.Run(); err != nil {
			return fail(fmt.Errorf("read index blob: %w", err))
		}
		total += int64(data.Len())
		if data.truncated || total > defaultArchiveLimit {
			return fail(ErrArchiveTooLarge)
		}
		if bytes.HasPrefix(data.Bytes(), []byte("version https://git-lfs.github.com/spec/v1\n")) {
			return fail(fmt.Errorf("Git LFS pointer unsupported: %s", name))
		}
		target := filepath.Join(dir, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return fail(err)
		}
		if meta[0] == "120000" {
			if err := os.Symlink(data.String(), target); err != nil {
				return fail(err)
			}
			continue
		}
		mode := os.FileMode(0600)
		if meta[0] == "100755" {
			mode = 0700
		}
		if err := os.WriteFile(target, data.Bytes(), mode); err != nil {
			return fail(err)
		}
	}
	releaseOnReturn = false
	return dir, func() {
		cleanup()
		release()
	}, nil
}

// CheckIndexCurrent fails closed if the staged tree or its base changed.
func CheckIndexCurrent(ctx context.Context, repository string, candidate IndexCandidate) error {
	current, err := CaptureIndex(ctx, repository)
	if err != nil {
		return err
	}
	if current != candidate {
		return ErrCandidateChanged
	}
	return nil
}

// RejectCandidatePolicyPath prevents a candidate from supplying the policy
// used to verify itself. Policy is loaded from the operator's local config.
func RejectCandidatePolicyPath(ctx context.Context, repository, candidate, policyPath string) error {
	if !validRevision(candidate) {
		return ErrInvalidCandidate
	}
	clean := path.Clean(policyPath)
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return ErrUnsafeArchive
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repository, "ls-tree", "-r", "-z", "--full-tree", candidate, "--", ":(literal)"+clean)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("inspect candidate verification policy path: %w", err)
	}
	if len(out) > 0 {
		return ErrCandidatePolicy
	}
	return nil
}
