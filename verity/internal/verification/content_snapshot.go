package verification

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ContentSnapshotter verifies an immutable, locally stored source archive by
// digest before unpacking it into a disposable directory.
type ContentSnapshotter struct{ Root string }

func (s ContentSnapshotter) Snapshot(ctx context.Context, _ string, digest string) (string, func(), error) {
	if len(digest) != 64 {
		return "", nil, ErrInvalidCandidate
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", nil, ErrInvalidCandidate
	}
	path := filepath.Join(s.Root, digest+".tar")
	info, err := os.Lstat(path)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, errors.New("source snapshot is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, defaultArchiveLimit+1))
	if err != nil {
		return "", nil, err
	}
	if len(data) > defaultArchiveLimit {
		return "", nil, ErrArchiveTooLarge
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != digest {
		return "", nil, errors.New("source snapshot digest mismatch")
	}
	return unpackSnapshot(data)
}

// CreateContentSnapshot stores only explicitly selected project paths. It
// never follows symlinks and refuses metadata/private state directories.
func CreateContentSnapshot(ctx context.Context, root, destination string, selected []string) (string, error) {
	if len(selected) == 0 {
		return "", errors.New("select at least one source path")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	paths := map[string]bool{}
	for _, name := range selected {
		if filepath.IsAbs(name) {
			return "", errors.New("snapshot paths must be relative")
		}
		clean := filepath.Clean(name)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", errors.New("select explicit paths within the project")
		}
		current := root
		for _, component := range strings.Split(clean, string(filepath.Separator)) {
			current = filepath.Join(current, component)
			info, err := os.Lstat(current)
			if err != nil {
				return "", err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("snapshot path is a symlink: %s", name)
			}
		}
		err = filepath.WalkDir(filepath.Join(root, clean), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			for _, component := range strings.Split(rel, string(filepath.Separator)) {
				if component == ".git" || component == ".verity" || component == "node_modules" || component == "dist" {
					if entry.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("snapshot path is a symlink: %s", rel)
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("snapshot path is not a regular file: %s", rel)
			}
			paths[rel] = true
			if len(paths) > 100000 {
				return errors.New("too many snapshot files")
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	if len(paths) == 0 {
		return "", errors.New("snapshot contains no files")
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	var data limitedBuffer
	data.limit = defaultArchiveLimit
	tw := tar.NewWriter(&data)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		f, err := os.Open(filepath.Join(root, name))
		if err != nil {
			return "", err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return "", err
		}
		if !info.Mode().IsRegular() || info.Size() > defaultArchiveLimit {
			f.Close()
			return "", ErrArchiveTooLarge
		}
		mode := int64(0600)
		if info.Mode()&0111 != 0 {
			mode = 0700
		}
		err = tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(name), Mode: mode, Size: info.Size(), Typeflag: tar.TypeReg})
		if err == nil {
			_, err = io.CopyN(tw, f, info.Size())
		}
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		if data.truncated {
			return "", ErrArchiveTooLarge
		}
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if data.truncated {
		return "", ErrArchiveTooLarge
	}
	sum := sha256.Sum256(data.Bytes())
	digest := hex.EncodeToString(sum[:])
	if err := os.MkdirAll(destination, 0700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(destination, ".snapshot-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = io.Copy(file, bytes.NewReader(data.Bytes()))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	target := filepath.Join(destination, digest+".tar")
	if err := os.Link(file.Name(), target); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		path, cleanup, checkErr := (ContentSnapshotter{Root: destination}).Snapshot(ctx, root, digest)
		_ = path
		if checkErr != nil {
			return "", checkErr
		}
		cleanup()
	}
	return digest, nil
}
