package verification

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCandidateArchiveEnforcesEntryQuota(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	archive, err := candidateArchiveWithLimits(root, 1<<20, 1)
	if archive != nil {
		_ = archive.Close()
		_ = os.Remove(archive.Name())
	}
	if !errors.Is(err, ErrSnapshotQuotaExceeded) {
		t.Fatalf("archive over entry quota: %v", err)
	}
}

func TestUnpackSnapshotEnforcesArchiveContentAndEntryQuotas(t *testing.T) {
	data := testTarArchive(t, map[string]string{"one.txt": "1234", "two.txt": "5678"})
	if _, _, err := unpackSnapshotWithLimits(data, int64(len(data)), 7, 10); !errors.Is(err, ErrSnapshotQuotaExceeded) {
		t.Fatalf("extracted content over quota: %v", err)
	}
	if _, _, err := unpackSnapshotWithLimits(data, int64(len(data)), 20, 1); !errors.Is(err, ErrSnapshotQuotaExceeded) {
		t.Fatalf("entry count over quota: %v", err)
	}
	if _, _, err := unpackSnapshotWithLimits(data, int64(len(data)-1), 20, 10); !errors.Is(err, ErrSnapshotQuotaExceeded) {
		t.Fatalf("archive over quota: %v", err)
	}
}

func testTarArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	w := tar.NewWriter(&data)
	for name, body := range files {
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
