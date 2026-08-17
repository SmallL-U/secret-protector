package config

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"os"
	"time"
)

type ReloadFunc func(*Config) error

func Watch(ctx context.Context, filename string, interval time.Duration, initialData []byte, reload ReloadFunc, logger *slog.Logger) {
	lastAttempt := sha256.Sum256(initialData)
	lastReadError := ""
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			data, err := os.ReadFile(filename)
			readFailed := err != nil
			newReadFailure := readFailed && err.Error() != lastReadError
			if newReadFailure {
				logger.Warn("config reload skipped; keeping last valid snapshot", "error", "cannot read config file")
				lastReadError = err.Error()
			}
			if readFailed {
				continue
			}

			lastReadError = ""
			hash := sha256.Sum256(data)
			if hash == lastAttempt {
				continue
			}
			lastAttempt = hash

			next, err := Parse(data)
			if err != nil {
				logger.Warn("config reload rejected; keeping last valid snapshot", "error", "configuration is invalid")
				continue
			}
			if err := reload(next); err != nil {
				logger.Warn("config reload rejected; keeping last valid snapshot", "error", err)
				continue
			}

			logger.Info("config reloaded")
		}
	}
}
