package command

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"secret-protector/internal/config"
)

func newTokenCommand(configPath *string) *cobra.Command {
	command := &cobra.Command{
		Use:   "token",
		Short: "Issue, list, and revoke downstream tokens",
	}
	command.AddCommand(
		newTokenIssueCommand(configPath),
		newTokenListCommand(configPath),
		newTokenRevokeCommand(configPath),
	)

	return command
}

func newTokenIssueCommand(configPath *string) *cobra.Command {
	var name string
	command := &cobra.Command{
		Use:   "issue ROUTE",
		Short: "Issue a downstream token",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if name == "" {
				return errors.New("--name is required")
			}
			value, err := issueDownstreamToken(*configPath, args[0], name)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(command.OutOrStdout(), "downstream token %s: %s\n", name, value)
			return err
		},
	}
	command.Flags().StringVar(&name, "name", "", "unique token name")

	return command
}

func newTokenListCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list ROUTE",
		Short: "List downstream token names and fingerprints",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			cfg, _, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			return writeTokenList(command.OutOrStdout(), cfg, args[0])
		},
	}
}

func newTokenRevokeCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke ROUTE TOKEN_NAME",
		Short: "Revoke a downstream token",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			if err := revokeDownstreamToken(*configPath, args[0], args[1]); err != nil {
				return err
			}

			_, err := fmt.Fprintf(command.OutOrStdout(), "downstream token %s revoked\n", args[1])
			return err
		},
	}
}

func accessTokenIndex(tokens []config.AccessToken, name string) int {
	for i := range tokens {
		if tokens[i].Name == name {
			return i
		}
	}

	return -1
}
