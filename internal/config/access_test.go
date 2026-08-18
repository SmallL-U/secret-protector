package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigWriteAccess(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "config.yml")
	if err := SaveAtomic(filename, New()); err != nil {
		t.Fatal(err)
	}
	if !IsWritable(filename) {
		t.Fatal("writable configuration was reported read-only")
	}

	if err := os.Chmod(filename, 0o400); err != nil {
		t.Fatal(err)
	}
	if IsWritable(filename) {
		t.Fatal("read-only configuration was reported writable")
	}
	if err := RequireWritable(filename); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("RequireWritable() error = %v, want ErrReadOnly", err)
	}
}

func TestConfigWriteAccessChecksParentDirectory(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "nested", "config.yml")
	if !IsWritable(filename) {
		t.Fatal("configuration under a writable parent was reported read-only")
	}

	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(directory, 0o700)
	})
	if IsWritable(filename) {
		t.Fatal("configuration under a read-only parent was reported writable")
	}
}
