package main

import (
	"os"
	"path/filepath"
)

func ensureDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}
