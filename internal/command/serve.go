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
	cfg, initialData, err := config.Load(filename)
	if err != nil {
		return fmt.Errorf("startup refused: %w", err)
	}

	runtime, err := proxyserver.NewRuntime(cfg, logger)
	if err != nil {
		return fmt.Errorf("startup refused: build routes: %w", err)
	}
	listener, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Server.Listen, err)
	}

	server := &http.Server{
		Handler:           runtime,
		ReadHeaderTimeout: cfg.Server.ReadHeaderDuration(),
		IdleTimeout:       cfg.Server.IdleDuration(),
	}
	watchCtx, stopWatching := context.WithCancel(ctx)
	defer stopWatching()
	go config.Watch(watchCtx, filename, cfg.Server.ReloadDuration(), initialData, runtime.Reload, logger)

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	logger.Info("proxy started", "listen", listener.Addr().String(), "config", filename)

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve proxy: %w", err)
	case <-ctx.Done():
		stopWatching()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownDuration())
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down proxy: %w", err)
		}
		logger.Info("proxy stopped")
		return nil
	}
}
