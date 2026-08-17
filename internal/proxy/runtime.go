package proxy

import (
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
	if logger == nil {
		logger = slog.Default()
	}

	router, err := NewRouter(prepared, logger)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		server: prepared.Server,
		logger: logger,
	}
	runtime.current.Store(router)

	return runtime, nil
}

func (runtime *Runtime) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	router := runtime.current.Load()
	if router == nil {
		writeError(writer, http.StatusServiceUnavailable, "not_ready", "the proxy is not ready")
		return
	}

	router.ServeHTTP(writer, request)
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
