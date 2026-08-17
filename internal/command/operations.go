package command

import (
	"fmt"
	"io"
	"text/tabwriter"

	"secret-protector/internal/config"
	secrettoken "secret-protector/internal/token"
)

func writeRouteList(output io.Writer, cfg *config.Config) error {
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "NAME\tUPSTREAM\tAUTH\tTOKENS"); err != nil {
		return err
	}
	for _, route := range cfg.Routes {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%d\n", route.Name, publicURL(route.Upstream.URL), route.Upstream.Auth.Mode, len(route.Downstream.Tokens)); err != nil {
			return err
		}
	}

	return writer.Flush()
}

func addRoute(filename string, options routeAddOptions) (string, error) {
	issuedToken, err := secrettoken.Generate()
	if err != nil {
		return "", err
	}
	route := options.route(issuedToken)
	err = config.UpdateFile(filename, func(next *config.Config) error {
		if routeIndex(next, route.Name) >= 0 {
			return fmt.Errorf("route already exists: %s", route.Name)
		}
		next.Routes = append(next.Routes, route)
		return nil
	})
	if err != nil {
		return "", err
	}

	return issuedToken, nil
}

func removeRoute(filename string, name string) error {
	return config.UpdateFile(filename, func(next *config.Config) error {
		index := routeIndex(next, name)
		if index < 0 {
			return fmt.Errorf("route not found: %s", name)
		}
		next.Routes = append(next.Routes[:index], next.Routes[index+1:]...)
		return nil
	})
}

func issueDownstreamToken(filename string, routeName string, tokenName string) (string, error) {
	value, err := secrettoken.Generate()
	if err != nil {
		return "", err
	}
	err = config.UpdateFile(filename, func(next *config.Config) error {
		index := routeIndex(next, routeName)
		if index < 0 {
			return fmt.Errorf("route not found: %s", routeName)
		}
		next.Routes[index].Downstream.Tokens = append(next.Routes[index].Downstream.Tokens, config.AccessToken{Name: tokenName, Value: value})
		return nil
	})
	if err != nil {
		return "", err
	}

	return value, nil
}

func writeTokenList(output io.Writer, cfg *config.Config, routeName string) error {
	index := routeIndex(cfg, routeName)
	if index < 0 {
		return fmt.Errorf("route not found: %s", routeName)
	}

	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "NAME\tFINGERPRINT"); err != nil {
		return err
	}
	for _, accessToken := range cfg.Routes[index].Downstream.Tokens {
		if _, err := fmt.Fprintf(writer, "%s\t%s\n", accessToken.Name, secrettoken.Fingerprint(accessToken.Value)); err != nil {
			return err
		}
	}

	return writer.Flush()
}

func revokeDownstreamToken(filename string, routeName string, tokenName string) error {
	return config.UpdateFile(filename, func(next *config.Config) error {
		routePosition := routeIndex(next, routeName)
		if routePosition < 0 {
			return fmt.Errorf("route not found: %s", routeName)
		}
		tokenPosition := accessTokenIndex(next.Routes[routePosition].Downstream.Tokens, tokenName)
		if tokenPosition < 0 {
			return fmt.Errorf("token not found: %s", tokenName)
		}
		tokens := next.Routes[routePosition].Downstream.Tokens
		next.Routes[routePosition].Downstream.Tokens = append(tokens[:tokenPosition], tokens[tokenPosition+1:]...)
		return nil
	})
}
