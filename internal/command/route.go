package command

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"secret-protector/internal/config"
)

type routeAddOptions struct {
	name                  string
	upstreamURL           string
	authMode              string
	upstreamToken         string
	upstreamUsername      string
	upstreamPassword      string
	queryParam            string
	downstreamQueryParams []string
	tokenName             string
}

func newRouteCommand(configPath *string) *cobra.Command {
	command := &cobra.Command{
		Use:   "route",
		Short: "Manage proxy routes",
	}
	command.AddCommand(
		newRouteListCommand(configPath),
		newRouteAddCommand(configPath),
		newRouteRemoveCommand(configPath),
	)

	return command
}

func newRouteListCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List routes without exposing credentials",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			cfg, _, err := config.Load(*configPath)
			if err != nil {
				return err
			}

			return writeRouteList(command.OutOrStdout(), cfg)
		},
	}
}

func newRouteAddCommand(configPath *string) *cobra.Command {
	options := routeAddOptions{}
	command := &cobra.Command{
		Use:   "add",
		Short: "Add a route and issue its first downstream token",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if options.name == "" {
				return errors.New("--name is required")
			}
			if options.upstreamURL == "" {
				return errors.New("--upstream-url is required")
			}
			if options.tokenName == "" {
				return errors.New("--token-name must not be empty")
			}

			issuedToken, err := addRoute(*configPath, options)
			if err != nil {
				return err
			}

			if _, err := fmt.Fprintf(command.OutOrStdout(), "route %s added\n", options.name); err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "downstream token %s: %s\n", options.tokenName, issuedToken)
			return err
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.name, "name", "", "unique route name")
	flags.StringVar(&options.upstreamURL, "upstream-url", "", "upstream base URL")
	flags.StringVar(&options.authMode, "auth-mode", "auto", "upstream auth mode: auto, bearer, query, or basic")
	flags.StringVar(&options.upstreamToken, "upstream-token", "", "upstream token used by auto, bearer, or query mode")
	flags.StringVar(&options.upstreamUsername, "upstream-username", "", "upstream Basic username")
	flags.StringVar(&options.upstreamPassword, "upstream-password", "", "upstream Basic password")
	flags.StringVar(&options.queryParam, "query-param", "token", "upstream query parameter name")
	flags.StringArrayVar(&options.downstreamQueryParams, "downstream-query-param", nil, "accepted downstream query parameter; repeatable")
	flags.StringVar(&options.tokenName, "token-name", "default", "name of the initially issued downstream token")

	return command
}

func (options routeAddOptions) route(issuedToken string) config.Route {
	return config.Route{
		Name: options.name,
		Upstream: config.UpstreamConfig{
			URL: options.upstreamURL,
			Auth: config.UpstreamAuth{
				Mode:       options.authMode,
				Token:      options.upstreamToken,
				Username:   options.upstreamUsername,
				Password:   options.upstreamPassword,
				QueryParam: options.queryParam,
			},
		},
		Downstream: config.DownstreamConfig{
			QueryParams: append([]string(nil), options.downstreamQueryParams...),
			Tokens: []config.AccessToken{
				{Name: options.tokenName, Value: issuedToken},
			},
		},
	}
}

func newRouteRemoveCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "remove NAME",
		Short: "Remove a route",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			name := args[0]
			if err := removeRoute(*configPath, name); err != nil {
				return err
			}

			_, err := fmt.Fprintf(command.OutOrStdout(), "route %s removed\n", name)
			return err
		},
	}
}

func routeIndex(cfg *config.Config, name string) int {
	for i := range cfg.Routes {
		if cfg.Routes[i].Name == name {
			return i
		}
	}

	return -1
}

func publicURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return "<invalid>"
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""

	return parsed.String()
}
