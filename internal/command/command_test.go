package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"secret-protector/internal/config"
)

func TestCobraManagementWorkflow(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.yml")
	output, err := executeCommand("--config", filename, "config", "init", "--listen", "127.0.0.1:18080")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "created") {
		t.Fatalf("init output = %q", output)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}

	output, err = executeCommand(
		"--config", filename,
		"route", "add",
		"--name", "example",
		"--upstream-url", "https://example.test/base?private=upstream-query-secret",
		"--auth-mode", "bearer",
		"--upstream-token", "upstream-secret",
	)
	if err != nil {
		t.Fatal(err)
	}
	firstToken := regexp.MustCompile(`sp_[A-Za-z0-9_-]{24}`).FindString(output)
	if firstToken == "" {
		t.Fatalf("route add did not print an issued token: %q", output)
	}

	if _, err := executeCommand("--config", filename, "config", "validate"); err != nil {
		t.Fatal(err)
	}
	output, err = executeCommand("--config", filename, "route", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "PREFIX") {
		t.Fatalf("route list still contains a path prefix column: %s", output)
	}
	for _, secret := range []string{"upstream-secret", "upstream-query-secret", firstToken} {
		if strings.Contains(output, secret) {
			t.Fatalf("route list exposed %q: %s", secret, output)
		}
	}

	output, err = executeCommand("--config", filename, "token", "issue", "example", "--name", "automation")
	if err != nil {
		t.Fatal(err)
	}
	secondToken := regexp.MustCompile(`sp_[A-Za-z0-9_-]{24}`).FindString(output)
	if secondToken == "" || secondToken == firstToken {
		t.Fatalf("token issue output = %q", output)
	}
	output, err = executeCommand("--config", filename, "token", "list", "example")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, firstToken) || strings.Contains(output, secondToken) {
		t.Fatalf("token list exposed a token: %s", output)
	}
	if !strings.Contains(output, "default") || !strings.Contains(output, "automation") {
		t.Fatalf("token list output = %q", output)
	}

	if _, err := executeCommand("--config", filename, "token", "revoke", "example", "automation"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 1 || len(cfg.Routes[0].Downstream.Tokens) != 1 {
		t.Fatalf("unexpected config after revoke: %#v", cfg.Routes)
	}

	if _, err := executeCommand("--config", filename, "route", "remove", "example"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = config.Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 0 {
		t.Fatalf("routes remain after remove: %#v", cfg.Routes)
	}
}

func TestReadOnlyConfigKeepsReadCommandsAndRejectsWriteCommands(t *testing.T) {
	writeCommands := []struct {
		name string
		args []string
	}{
		{name: "config init", args: []string{"config", "init", "--force"}},
		{name: "route add", args: []string{"route", "add", "--name", "other", "--upstream-url", "https://other.test", "--auth-mode", "bearer", "--upstream-token", "other-secret"}},
		{name: "route remove", args: []string{"route", "remove", "api"}},
		{name: "token issue", args: []string{"token", "issue", "api", "--name", "other"}},
		{name: "token revoke", args: []string{"token", "revoke", "api", "client"}},
	}

	for _, test := range writeCommands {
		t.Run(test.name, func(t *testing.T) {
			filename := writeReadOnlyTestConfig(t)
			before, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}

			args := append([]string{"--config", filename}, test.args...)
			_, err = executeCommand(args...)
			if !errors.Is(err, config.ErrReadOnly) {
				t.Fatalf("command error = %v, want ErrReadOnly", err)
			}

			after, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("read-only command changed the configuration")
			}
		})
	}

	filename := writeReadOnlyTestConfig(t)
	readCommands := [][]string{
		{"config", "validate"},
		{"route", "list"},
		{"token", "list", "api"},
	}
	for _, args := range readCommands {
		commandArgs := append([]string{"--config", filename}, args...)
		if _, err := executeCommand(commandArgs...); err != nil {
			t.Fatalf("read command %q failed: %v", strings.Join(args, " "), err)
		}
	}

	help, err := executeCommand("route", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help, "[write]") {
		t.Fatalf("route help does not mark write commands: %s", help)
	}
}

func TestInteractiveManagerMarksAndRejectsReadOnlyWrites(t *testing.T) {
	filename := writeReadOnlyTestConfig(t)
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}

	output, err := executeCommandWithInput("2\n0\n", "--config", filename, "manage")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "[read-only]") || !strings.Contains(output, "[write]") {
		t.Fatalf("interactive read-only markers are missing: %s", output)
	}
	if !strings.Contains(output, config.ErrReadOnly.Error()) {
		t.Fatalf("interactive write rejection is missing: %s", output)
	}

	after, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("interactive read-only command changed the configuration")
	}
}

func writeReadOnlyTestConfig(t *testing.T) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "config.yml")
	cfg := config.New()
	cfg.Routes = []config.Route{
		{
			Name: "api",
			Upstream: config.UpstreamConfig{
				URL: "https://example.test",
				Auth: config.UpstreamAuth{
					Mode:  "bearer",
					Token: "upstream-secret",
				},
			},
			Downstream: config.DownstreamConfig{
				QueryParams: []string{"token"},
				Tokens: []config.AccessToken{
					{Name: "client", Value: "downstream-secret"},
				},
			},
		},
	}
	if err := config.SaveAtomic(filename, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filename, 0o400); err != nil {
		t.Fatal(err)
	}

	return filename
}

func TestInteractiveManagementWorkflow(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.yml")
	input := strings.Join([]string{
		"",    // create missing config: yes
		"",    // default listen address
		"2",   // add route
		"api", // route name
		"https://example.test",
		"",                // default auto auth
		"upstream-secret", // upstream token
		"",                // follow downstream query parameter
		"",                // follow downstream credential header
		"service-user",    // upstream Basic username
		"",                // use token as Basic password
		"",                // default downstream query parameter
		"",                // no downstream credential header
		"",                // default initial token name
		"4",               // issue token
		"api",
		"automation",
		"5", // list tokens
		"api",
		"6", // revoke token
		"api",
		"automation",
		"y",
		"7", // validate
		"1", // list routes
		"0", // exit
	}, "\n") + "\n"

	output, err := executeCommandWithInput(input, "--config", filename, "manage")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "upstream-secret") {
		t.Fatal("interactive output exposed an upstream secret")
	}
	issued := regexp.MustCompile(`sp_[A-Za-z0-9_-]{24}`).FindAllString(output, -1)
	if len(issued) != 2 || issued[0] == issued[1] {
		t.Fatalf("issued tokens = %#v", issued)
	}
	if !strings.Contains(output, "config.yml is valid") || !strings.Contains(output, "automation") {
		t.Fatalf("interactive output is incomplete: %s", output)
	}

	cfg, _, err := config.Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 1 {
		t.Fatalf("route count = %d", len(cfg.Routes))
	}
	route := cfg.Routes[0]
	if route.Upstream.Auth.Mode != "auto" || route.Upstream.Auth.Token != "upstream-secret" {
		t.Fatalf("interactive auth config = %#v", route.Upstream.Auth)
	}
	if route.Upstream.Auth.Username != "service-user" || route.Upstream.Auth.Password != "" {
		t.Fatalf("interactive Basic fallback config = %#v", route.Upstream.Auth)
	}
	if len(route.Downstream.Tokens) != 1 || route.Downstream.Tokens[0].Name != "default" {
		t.Fatalf("interactive route config = %#v", route)
	}

	removeInput := "3\napi\ny\n0\n"
	output, err = executeCommandWithInput(removeInput, "--config", filename, "manage")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "route api removed") {
		t.Fatalf("remove output = %q", output)
	}
	cfg, _, err = config.Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 0 {
		t.Fatalf("routes remain after interactive remove: %#v", cfg.Routes)
	}
}

func TestInvalidInteractiveUpdateReturnsToMenuAndKeepsFile(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.yml")
	if err := config.SaveAtomic(filename, config.New()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Join([]string{
		"2",
		"broken",
		"ftp://example.test",
		"bearer",
		"must-not-be-printed",
		"", // default query parameter
		"", // no downstream credential header
		"", // default token name
		"0",
	}, "\n") + "\n"

	output, err := executeCommandWithInput(input, "--config", filename, "manage")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "error:") {
		t.Fatalf("interactive error was not shown: %s", output)
	}
	if strings.Contains(output, "must-not-be-printed") {
		t.Fatal("interactive error output exposed an upstream token")
	}
	after, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("invalid interactive update changed config")
	}
}

func TestInteractiveManagementCanDeclineInitialization(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.yml")
	if _, err := executeCommandWithInput("n\n", "--config", filename, "manage"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config was created after declining initialization: %v", err)
	}
}

func TestInvalidCLIUpdateLeavesConfigUnchanged(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.yml")
	if _, err := executeCommand("--config", filename, "config", "init"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}

	_, err = executeCommand(
		"--config", filename,
		"route", "add",
		"--name", "broken",
		"--upstream-url", "https://example.test",
	)
	if err == nil {
		t.Fatal("invalid route add succeeded")
	}
	after, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("invalid CLI update changed config")
	}
}

func TestServeStartsUnreadyAndRecoversAfterValidConfig(t *testing.T) {
	upstreamAuth := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamAuth <- request.Header.Get("Authorization")
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	filename := filepath.Join(t.TempDir(), "config.yml")
	invalidData := []byte("version: 1\n" +
		"server:\n" +
		"  listen: 127.0.0.1:0\n" +
		"  reload_interval: 5ms\n" +
		"  shutdown_timeout: 1s\n" +
		"routes:\n" +
		"  - name: api\n" +
		"    upstream:\n" +
		"      url: ftp://example.test\n" +
		"      auth:\n" +
		"        mode: bearer\n" +
		"        token: startup-secret-must-not-appear\n")
	if err := os.WriteFile(filename, invalidData, 0o600); err != nil {
		t.Fatal(err)
	}

	logs := &synchronizedBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serve(ctx, filename, logger)
	}()
	defer cancel()

	listenAddress := waitForListenAddress(t, logs)
	status, body, err := requestStatus(listenAddress, "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusServiceUnavailable || !strings.Contains(body, `"status":"unavailable"`) {
		t.Fatalf("unready health response = %d %q", status, body)
	}
	if !strings.Contains(logs.String(), `"level":"WARN"`) || !strings.Contains(logs.String(), `"ready":false`) {
		t.Fatalf("startup logs do not report unready state: %s", logs.String())
	}
	if strings.Contains(logs.String(), "startup-secret-must-not-appear") {
		t.Fatal("startup warning exposed invalid config content")
	}

	cfg := config.New()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Server.ReloadInterval = "5ms"
	cfg.Server.ShutdownTimeout = "1s"
	cfg.Routes = []config.Route{
		{
			Name: "api",
			Upstream: config.UpstreamConfig{
				URL: upstream.URL,
				Auth: config.UpstreamAuth{
					Mode:  "bearer",
					Token: "recovered-upstream-token",
				},
			},
			Downstream: config.DownstreamConfig{
				QueryParams: []string{"token"},
				Tokens: []config.AccessToken{
					{Name: "client", Value: "client-token"},
				},
			},
		},
	}
	if err := config.SaveAtomic(filename, cfg); err != nil {
		t.Fatal(err)
	}
	waitForCommand(t, time.Second, func() bool {
		actual, _, requestErr := requestStatus(listenAddress, "/healthz")
		return requestErr == nil && actual == http.StatusOK
	})
	requestProxy(t, listenAddress)
	if actual := <-upstreamAuth; actual != "Bearer recovered-upstream-token" {
		t.Fatalf("recovered upstream auth = %q", actual)
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not stop after context cancellation")
	}
}

func TestServeReloadsValidFileAndKeepsSnapshotAfterInvalidFile(t *testing.T) {
	upstreamAuth := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamAuth <- request.Header.Get("Authorization")
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	filename := filepath.Join(t.TempDir(), "config.yml")
	cfg := config.New()
	cfg.Server.Listen = "127.0.0.1:0"
	cfg.Server.ReloadInterval = "5ms"
	cfg.Server.ShutdownTimeout = "1s"
	cfg.Routes = []config.Route{
		{
			Name: "api",
			Upstream: config.UpstreamConfig{
				URL: upstream.URL,
				Auth: config.UpstreamAuth{
					Mode:  "bearer",
					Token: "old-upstream-token",
				},
			},
			Downstream: config.DownstreamConfig{
				QueryParams: []string{"token"},
				Tokens: []config.AccessToken{
					{Name: "client", Value: "client-token"},
				},
			},
		},
	}
	if err := config.SaveAtomic(filename, cfg); err != nil {
		t.Fatal(err)
	}

	logs := &synchronizedBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serve(ctx, filename, logger)
	}()
	defer cancel()

	listenAddress := waitForListenAddress(t, logs)
	status, _, err := requestStatus(listenAddress, "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("initial health status = %d", status)
	}

	requestProxy(t, listenAddress)
	if actual := <-upstreamAuth; actual != "Bearer old-upstream-token" {
		t.Fatalf("initial upstream auth = %q", actual)
	}

	next, _, err := config.Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	next.Routes[0].Upstream.Auth.Token = "new-upstream-token"
	if err := config.SaveAtomic(filename, next); err != nil {
		t.Fatal(err)
	}
	waitForCommand(t, time.Second, func() bool {
		return strings.Contains(logs.String(), `"msg":"config reloaded"`)
	})
	requestProxy(t, listenAddress)
	if actual := <-upstreamAuth; actual != "Bearer new-upstream-token" {
		t.Fatalf("reloaded upstream auth = %q", actual)
	}

	if err := os.WriteFile(filename, []byte("version: invalid\nsecret: must-not-appear-in-log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForCommand(t, time.Second, func() bool {
		return strings.Contains(logs.String(), `"level":"WARN"`)
	})
	if strings.Contains(logs.String(), "must-not-appear-in-log") {
		t.Fatal("reload warning exposed invalid config content")
	}
	status, _, err = requestStatus(listenAddress, "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("health after rejected reload = %d", status)
	}
	requestProxy(t, listenAddress)
	if actual := <-upstreamAuth; actual != "Bearer new-upstream-token" {
		t.Fatalf("upstream auth after invalid file = %q", actual)
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not stop after context cancellation")
	}
}

func TestServeReturnsErrorWhenListenAddressCannotBeBound(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	filename := filepath.Join(t.TempDir(), "config.yml")
	cfg := config.New()
	cfg.Server.Listen = occupied.Addr().String()
	if err := config.SaveAtomic(filename, cfg); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err = serve(context.Background(), filename, logger)
	if err == nil || !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("serve() error = %v", err)
	}
}

func executeCommand(args ...string) (string, error) {
	return executeCommandWithInput("", args...)
}

func executeCommandWithInput(input string, args ...string) (string, error) {
	command := NewRootCommand()
	output := &bytes.Buffer{}
	command.SetIn(strings.NewReader(input))
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs(args)
	err := command.Execute()

	return output.String(), err
}

func requestProxy(t *testing.T, listenAddress string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "http://"+listenAddress+"/client/selected/path", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer client-token")
	client := &http.Client{Timeout: time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("proxy status = %d", response.StatusCode)
	}
}

func requestStatus(listenAddress string, path string) (int, string, error) {
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + listenAddress + path)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return 0, "", err
	}

	return response.StatusCode, string(body), nil
}

func waitForListenAddress(t *testing.T, logs *synchronizedBuffer) string {
	t.Helper()
	listenPattern := regexp.MustCompile(`"listen":"([^"]+)"`)
	var listenAddress string
	waitForCommand(t, time.Second, func() bool {
		match := listenPattern.FindStringSubmatch(logs.String())
		if len(match) != 2 {
			return false
		}
		listenAddress = match[1]
		return true
	})

	return listenAddress
}

func waitForCommand(t *testing.T, timeout time.Duration, condition func() bool) {
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

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}
