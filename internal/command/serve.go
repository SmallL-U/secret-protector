package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/spf13/cobra"

	"secret-protector/internal/config"
	proxyserver "secret-protector/internal/proxy"
)

func newServeCommand(configPath *string, logger *slog.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the reverse proxy",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return serve(command.Context(), *configPath, logger)
		},
	}
}

func serve(ctx context.Context, filename string, logger *slog.Logger) error {
	cfg, initialData, startupErr := config.Load(filename)
	serverConfig := config.New().Server
	if startupErr == nil {
		serverConfig = cfg.Server
	} else if parsedServer, err := config.ParseServer(initialData); err == nil {
		serverConfig = parsedServer
	}

	runtime := proxyserver.NewUnreadyRuntime(serverConfig, logger)
	if startupErr == nil {
		startupErr = runtime.Reload(cfg)
	}
	listener, err := net.Listen("tcp", serverConfig.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", serverConfig.Listen, err)
	}

	server := &http.Server{
		Handler:           runtime,
		ReadHeaderTimeout: serverConfig.ReadHeaderDuration(),
		IdleTimeout:       serverConfig.IdleDuration(),
	}
	watchCtx, stopWatching := context.WithCancel(ctx)
	defer stopWatching()
	go config.Watch(watchCtx, filename, serverConfig.ReloadDuration(), initialData, runtime.Reload, logger)

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	if startupErr != nil {
		logger.Warn("startup config unavailable; proxy is not ready", "error", "configuration is invalid")
	}
	logger.Info("proxy started", "listen", listener.Addr().String(), "config", filename, "ready", runtime.Ready())

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve proxy: %w", err)
	case <-ctx.Done():
		stopWatching()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serverConfig.ShutdownDuration())
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down proxy: %w", err)
		}
		logger.Info("proxy stopped")
		return nil
	}
}
