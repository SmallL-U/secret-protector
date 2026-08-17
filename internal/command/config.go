package command

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"secret-protector/internal/config"
)

func newConfigCommand(configPath *string) *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Initialize and validate configuration",
	}
	command.AddCommand(
		newConfigInitCommand(configPath),
		newConfigValidateCommand(configPath),
	)

	return command
}

func newConfigInitCommand(configPath *string) *cobra.Command {
	var listen string
	var force bool

	command := &cobra.Command{
		Use:   "init",
		Short: "Create an empty configuration",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := ensureConfigCanBeCreated(*configPath, force); err != nil {
				return err
			}

			cfg := config.New()
			cfg.Server.Listen = listen
			if err := config.SaveAtomic(*configPath, cfg); err != nil {
				return err
			}
			_, err := fmt.Fprintf(command.OutOrStdout(), "created %s\n", *configPath)
			return err
		},
	}
	command.Flags().StringVar(&listen, "listen", "127.0.0.1:8080", "TCP address to listen on")
	command.Flags().BoolVar(&force, "force", false, "replace an existing configuration")

	return command
}

func ensureConfigCanBeCreated(filename string, force bool) error {
	if force {
		return nil
	}

	_, err := os.Stat(filename)
	if err == nil {
		return fmt.Errorf("config already exists: %s (use --force to replace it)", filename)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return fmt.Errorf("inspect config: %w", err)
}

func newConfigValidateCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Strictly parse and validate configuration",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, _, err := config.Load(*configPath); err != nil {
				return err
			}
			_, err := fmt.Fprintf(command.OutOrStdout(), "%s is valid\n", *configPath)
			return err
		},
	}
}
