package service

import (
	"os"
	"path/filepath"
)

// executablePath returns the absolute path to the running binary.
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}
