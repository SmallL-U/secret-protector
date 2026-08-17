package command

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	var configPath string
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	root := &cobra.Command{
		Use:           "secret-protector",
		Short:         "Protect upstream credentials behind a local reverse proxy",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().StringVar(&configPath, "config", "config.yml", "YAML configuration file")
	root.AddCommand(
		newServeCommand(&configPath, logger),
		newManageCommand(&configPath),
		newConfigCommand(&configPath),
		newRouteCommand(&configPath),
		newTokenCommand(&configPath),
	)

	return root
}
