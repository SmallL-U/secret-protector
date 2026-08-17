package config

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseAppliesDefaultsAndNormalizesFollow(t *testing.T) {
	data := []byte(`
version: 1
routes:
  - name: example
    path_prefix: /api
    upstream:
      url: https://example.test
      auth:
        mode: follow
        token: upstream-secret
    downstream:
      tokens:
        - name: client
          value: downstream-secret
`)

	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "127.0.0.1:8080" {
		t.Fatalf("Listen = %q", cfg.Server.Listen)
	}
	if cfg.Routes[0].Upstream.Auth.Mode != "auto" {
		t.Fatalf("Mode = %q", cfg.Routes[0].Upstream.Auth.Mode)
	}
	if len(cfg.Routes[0].Downstream.QueryParams) != 1 || cfg.Routes[0].Downstream.QueryParams[0] != "token" {
		t.Fatalf("QueryParams = %#v", cfg.Routes[0].Downstream.QueryParams)
	}
}

func TestParseRejectsUnknownFieldsAndMultipleDocuments(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "unknown field",
			data: "version: 1\nunknown: true\n",
		},
		{
			name: "multiple documents",
			data: "version: 1\n---\nversion: 1\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(test.data)); err == nil {
				t.Fatal("Parse() succeeded, want error")
			}
		})
	}
}

func TestPrepareRejectsInvalidRoutes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "unnormalized prefix",
			mutate: func(cfg *Config) {
				cfg.Routes[0].PathPrefix = "/api/"
			},
		},
		{
			name: "unsupported upstream scheme",
			mutate: func(cfg *Config) {
				cfg.Routes[0].Upstream.URL = "ftp://example.test"
			},
		},
		{
			name: "auto without upstream token",
			mutate: func(cfg *Config) {
				cfg.Routes[0].Upstream.Auth.Token = ""
			},
		},
		{
			name: "duplicate downstream token",
			mutate: func(cfg *Config) {
				cfg.Routes[0].Downstream.Tokens = append(cfg.Routes[0].Downstream.Tokens, cfg.Routes[0].Downstream.Tokens[0])
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig()
			test.mutate(cfg)
			if _, err := Prepare(cfg); err == nil {
				t.Fatal("Prepare() succeeded, want error")
			}
		})
	}
}

func TestSaveAtomicUsesPrivatePermissionsAndPreservesValidFileOnFailure(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "nested", "config.yml")
	if err := SaveAtomic(filename, validConfig()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if actual := info.Mode().Perm(); actual != 0o600 {
		t.Fatalf("permissions = %o, want 600", actual)
	}

	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	err = UpdateFile(filename, func(next *Config) error {
		next.Routes[0].Upstream.Auth.Token = ""
		return nil
	})
	if err == nil {
		t.Fatal("UpdateFile() succeeded, want validation error")
	}
	after, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("invalid update changed the config file")
	}
}

func TestWatchRejectsInvalidContentAndReloadsNextValidSnapshot(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.yml")
	initial := validConfig()
	if err := SaveAtomic(filename, initial); err != nil {
		t.Fatal(err)
	}
	initialData, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reloaded := make(chan *Config, 1)
	logs := &lockedBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	go Watch(ctx, filename, 5*time.Millisecond, initialData, func(next *Config) error {
		reloaded <- next
		return nil
	}, logger)

	if err := os.WriteFile(filename, []byte("version: invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		return strings.Contains(logs.String(), "WARN")
	})
	if count := strings.Count(logs.String(), "WARN"); count != 1 {
		t.Fatalf("warning count = %d, want 1", count)
	}
	select {
	case <-reloaded:
		t.Fatal("invalid content was reloaded")
	default:
	}

	next := Clone(initial)
	next.Routes[0].Upstream.Auth.Token = "new-upstream-secret"
	if err := SaveAtomic(filename, next); err != nil {
		t.Fatal(err)
	}
	select {
	case actual := <-reloaded:
		if actual.Routes[0].Upstream.Auth.Token != "new-upstream-secret" {
			t.Fatal("watcher published the wrong snapshot")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for valid reload")
	}
}

func validConfig() *Config {
	cfg := New()
	cfg.Routes = []Route{
		{
			Name:       "example",
			PathPrefix: "/api",
			Upstream: UpstreamConfig{
				URL: "https://example.test",
				Auth: UpstreamAuth{
					Mode:  "auto",
					Token: "upstream-secret",
				},
			},
			Downstream: DownstreamConfig{
				QueryParams: []string{"token"},
				Tokens: []AccessToken{
					{Name: "client", Value: "downstream-secret"},
				},
			},
		},
	}

	return cfg
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

var _ io.Writer = (*lockedBuffer)(nil)
