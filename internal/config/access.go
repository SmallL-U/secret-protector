package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrReadOnly = errors.New("configuration is read-only")

// IsWritable reports whether filename can be replaced with the atomic write
// strategy used by SaveAtomic.
func IsWritable(filename string) bool {
	if !fileAllowsWrite(filename) {
		return false
	}

	directory, ok := nearestExistingDirectory(filepath.Dir(filename))
	if !ok {
		return false
	}

	return directoryAllowsWrite(directory)
}

// RequireWritable returns ErrReadOnly when filename cannot be atomically
// replaced.
func RequireWritable(filename string) error {
	if IsWritable(filename) {
		return nil
	}

	return fmt.Errorf("%w: %s", ErrReadOnly, filename)
}

func fileAllowsWrite(filename string) bool {
	info, err := os.Stat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil || info.IsDir() || info.Mode().Perm()&0o222 == 0 {
		return false
	}

	file, err := os.OpenFile(filename, os.O_WRONLY, 0)
	if err != nil {
		return false
	}

	return file.Close() == nil
}

func nearestExistingDirectory(directory string) (string, bool) {
	for {
		info, err := os.Stat(directory)
		if err == nil {
			return directory, info.IsDir()
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", false
		}

		parent := filepath.Dir(directory)
		if parent == directory {
			return "", false
		}
		directory = parent
	}
}

func directoryAllowsWrite(directory string) bool {
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o222 == 0 {
		return false
	}

	temporary, err := os.CreateTemp(directory, ".secret-protector-write-check-*.tmp")
	if err != nil {
		return false
	}
	temporaryName := temporary.Name()
	closeErr := temporary.Close()
	removeErr := os.Remove(temporaryName)

	return closeErr == nil && removeErr == nil
}
