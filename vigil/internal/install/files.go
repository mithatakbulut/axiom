package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxConfigSize = 4 << 20

type file struct {
	Data   []byte      `json:"data"`
	Mode   os.FileMode `json:"mode"`
	Exists bool        `json:"exists"`
}

func readFile(path string, limit int64) (file, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return file{}, nil
	}
	if err != nil {
		return file{}, err
	}
	if !info.Mode().IsRegular() {
		return file{}, fmt.Errorf("%s must be a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return file{}, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return file{}, err
	}
	if !os.SameFile(info, opened) {
		return file{}, fmt.Errorf("%s changed while opening it", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return file{}, err
	}
	if int64(len(data)) > limit {
		return file{}, fmt.Errorf("%s exceeds the size limit", path)
	}
	return file{Data: data, Mode: opened.Mode().Perm(), Exists: true}, nil
}

func (f file) equal(other file) bool {
	return f.Exists == other.Exists && f.Mode == other.Mode && bytes.Equal(f.Data, other.Data)
}

func (f file) withData(data []byte) file {
	if !f.Exists {
		f.Mode = 0600
	}
	f.Exists = true
	f.Data = data
	return f
}

func replace(path string, before, after file, limit int64) error {
	if before.equal(after) {
		return nil
	}
	var tmp string
	if after.Exists {
		f, err := os.CreateTemp(filepath.Dir(path), ".vigil-*")
		if err != nil {
			return err
		}
		tmp = f.Name()
		defer func() { _ = os.Remove(tmp) }()
		err = f.Chmod(after.Mode)
		if err == nil {
			_, err = f.Write(after.Data)
		}
		if err == nil {
			err = f.Sync()
		}
		if err = errors.Join(err, f.Close()); err != nil {
			return err
		}
	}
	current, err := readFile(path, limit)
	if err != nil {
		return err
	}
	if !current.equal(before) {
		return fmt.Errorf("%s changed during the operation", path)
	}
	if !after.Exists {
		return os.Remove(path)
	}
	return os.Rename(tmp, path)
}
