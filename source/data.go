package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// data/ is never shipped in an update. Move legacy files without discarding
// either copy when both old and new names exist.
func prepareData(dir string) (string, error) {
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0700); err != nil {
		return "", err
	}
	for _, pair := range [][2]string{{"token.txt", "token.conf"}, {"state.json", "sync-state.json"}} {
		old := filepath.Join(dir, pair[0])
		dst := filepath.Join(data, pair[1])
		b, err := os.ReadFile(old)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		current, err := os.ReadFile(dst)
		if err == nil {
			if !bytes.Equal(b, current) {
				return "", fmt.Errorf("both %s and %s exist and differ; back up and resolve before syncing", old, dst)
			}
			if err := os.Remove(old); err != nil {
				return "", err
			}
		} else if os.IsNotExist(err) {
			if err := os.Rename(old, dst); err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	oldStatus := filepath.Join(dir, "last-result.txt")
	if _, err := os.Stat(oldStatus); err == nil {
		dst := filepath.Join(data, "legacy-last-result.log")
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			if err := os.Rename(oldStatus, dst); err != nil {
				return "", err
			}
		}
	}
	return data, nil
}

func atomicWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".sync-state-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	// Flush directory metadata where supported (FAT may not implement it).
	if d, e := os.Open(filepath.Dir(path)); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
