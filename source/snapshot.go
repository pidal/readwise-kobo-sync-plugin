package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// Detect checkpoints, WAL resets, appends, and rollback-journal activity across
// extraction. This is a conservative guard, not a SQLite read transaction.
func databaseStamp(path string) (string, error) {
	stamp := ""
	for _, suffix := range []string{"", "-wal", "-journal"} {
		f, err := os.Open(path + suffix)
		if os.IsNotExist(err) && suffix != "" {
			stamp += suffix + ":missing;"
			continue
		}
		if err != nil {
			return "", err
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			return "", err
		}
		header := make([]byte, 100)
		n, err := f.ReadAt(header, 0)
		if err != nil && err != io.EOF {
			f.Close()
			return "", err
		}
		header = header[:n]
		f.Close()
		if suffix == "-journal" && len(header) >= 8 {
			nonzero := false
			for _, b := range header[:8] {
				if b != 0 {
					nonzero = true
				}
			}
			if nonzero {
				return "", fmt.Errorf("Kobo database has an active rollback journal; retry after reading settles")
			}
		}
		stamp += fmt.Sprintf("%s:%d:%d:%x;", suffix, info.Size(), info.ModTime().UnixNano(), sha256.Sum256(header))
	}
	return stamp, nil
}
