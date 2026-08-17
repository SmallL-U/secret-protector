package proxy

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"secret-protector/internal/config"
)

type Runtime struct {
	current atomic.Pointer[Router]
	server  config.ServerConfig
	logger  *slog.Logger
	reload  sync.Mutex
}

func NewRuntime(cfg *config.Config, logger *slog.Logger) (*Runtime, error) {
	prepared, err := config.Prepare(cfg)
	if err != nil {
		return nil, err
	}

	router, err := NewRouter(prepared, logger)
	if err != nil {
		return nil, err
	}
	runtime := NewUnreadyRuntime(prepared.Server, logger)
	runtime.current.Store(router)

	return runtime, nil
}

func NewUnreadyRuntime(server config.ServerConfig, logger *slog.Logger) *Runtime {
	if logger == nil {
		logger = slog.Default()
	}

	return &Runtime{
		server: server,
		logger: logger,
	}
}

func (runtime *Runtime) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/healthz" {
		runtime.serveHealth(writer, request)
		return
	}

	router := runtime.current.Load()
	if router == nil {
		writeError(writer, http.StatusServiceUnavailable, "not_ready", "the proxy is not ready")
		return
	}

	router.ServeHTTP(writer, request)
}

func (runtime *Runtime) Ready() bool {
	return runtime.current.Load() != nil
}

func (runtime *Runtime) serveHealth(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "health checks require GET or HEAD")
		return
	}

	status := http.StatusOK
	state := "ok"
	if !runtime.Ready() {
		status = http.StatusServiceUnavailable
		state = "unavailable"
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if request.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(writer).Encode(map[string]string{"status": state})
}

func (runtime *Runtime) Reload(next *config.Config) error {
	runtime.reload.Lock()
	defer runtime.reload.Unlock()

	prepared, err := config.Prepare(next)
	if err != nil {
		return err
	}
	if prepared.Server != runtime.server {
		return errors.New("server settings changed; restart is required")
	}

	router, err := NewRouter(prepared, runtime.logger)
	if err != nil {
		return err
	}
	runtime.current.Store(router)

	return nil
}
