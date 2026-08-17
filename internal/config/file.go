package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const generatedHeader = "# Managed by secret-protector. This file contains secrets; keep mode 0600.\n"

func SaveAtomic(filename string, cfg *Config) error {
	prepared, err := Prepare(cfg)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(prepared)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append([]byte(generatedHeader), data...)

	return writeAtomic(filename, data)
}

func UpdateFile(filename string, mutate func(*Config) error) error {
	current, _, err := Load(filename)
	if err != nil {
		return err
	}

	next := Clone(current)
	if err := mutate(next); err != nil {
		return err
	}

	return SaveAtomic(filename, next)
}

func writeAtomic(filename string, data []byte) error {
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	temporary, err := os.CreateTemp(directory, ".secret-protector-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary config permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(temporaryName, filename); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}

	dirHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open config directory: %w", err)
	}
	defer dirHandle.Close()
	if err := dirHandle.Sync(); err != nil {
		return fmt.Errorf("sync config directory: %w", err)
	}

	return nil
}
