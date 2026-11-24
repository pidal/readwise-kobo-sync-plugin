package main

import (
	"os"
	"path/filepath"
)

// Personal files live separately from the installed program files.
func prepareData(dir string) (string, error) {
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0700); err != nil {
		return "", err
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
