package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	CurrentVersion = 1

	defaultListen            = "127.0.0.1:8080"
	defaultReloadInterval    = "2s"
	defaultReadHeaderTimeout = "10s"
	defaultIdleTimeout       = "60s"
	defaultShutdownTimeout   = "10s"
)

type Config struct {
	Version int          `yaml:"version"`
	Server  ServerConfig `yaml:"server,omitempty"`
	Routes  []Route      `yaml:"routes,omitempty"`
}

type ServerConfig struct {
	Listen            string `yaml:"listen,omitempty"`
	ReloadInterval    string `yaml:"reload_interval,omitempty"`
	ReadHeaderTimeout string `yaml:"read_header_timeout,omitempty"`
	IdleTimeout       string `yaml:"idle_timeout,omitempty"`
	ShutdownTimeout   string `yaml:"shutdown_timeout,omitempty"`
}

type Route struct {
	Name       string           `yaml:"name"`
	PathPrefix string           `yaml:"path_prefix"`
	Upstream   UpstreamConfig   `yaml:"upstream"`
	Downstream DownstreamConfig `yaml:"downstream"`
}

type UpstreamConfig struct {
	URL         string       `yaml:"url"`
	StripPrefix bool         `yaml:"strip_prefix,omitempty"`
	Auth        UpstreamAuth `yaml:"auth"`
}

type UpstreamAuth struct {
	Mode       string `yaml:"mode,omitempty"`
	Token      string `yaml:"token,omitempty"`
	Username   string `yaml:"username,omitempty"`
	Password   string `yaml:"password,omitempty"`
	QueryParam string `yaml:"query_param,omitempty"`
}

type DownstreamConfig struct {
	QueryParams []string      `yaml:"query_params,omitempty"`
	Tokens      []AccessToken `yaml:"tokens,omitempty"`
}

type AccessToken struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

func New() *Config {
	return &Config{
		Version: CurrentVersion,
		Server: ServerConfig{
			Listen:            defaultListen,
			ReloadInterval:    defaultReloadInterval,
			ReadHeaderTimeout: defaultReadHeaderTimeout,
			IdleTimeout:       defaultIdleTimeout,
			ShutdownTimeout:   defaultShutdownTimeout,
		},
	}
}

func Load(filename string) (*Config, []byte, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, nil, fmt.Errorf("read config: %w", err)
	}

	cfg, err := Parse(data)
	if err != nil {
		return nil, nil, err
	}

	return cfg, data, nil
}

func Parse(data []byte) (*Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var raw Config
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	var trailing any
	err := decoder.Decode(&trailing)
	if err == nil {
		return nil, errors.New("parse config: multiple YAML documents are not allowed")
	}
	if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return Prepare(&raw)
}

func Prepare(input *Config) (*Config, error) {
	if input == nil {
		return nil, errors.New("config is nil")
	}

	cfg := Clone(input)
	applyDefaults(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func Clone(input *Config) *Config {
	if input == nil {
		return nil
	}

	clone := *input
	clone.Routes = make([]Route, len(input.Routes))
	for i := range input.Routes {
		clone.Routes[i] = input.Routes[i]
		clone.Routes[i].Downstream.QueryParams = append([]string(nil), input.Routes[i].Downstream.QueryParams...)
		clone.Routes[i].Downstream.Tokens = append([]AccessToken(nil), input.Routes[i].Downstream.Tokens...)
	}

	return &clone
}

func (s ServerConfig) ReloadDuration() time.Duration {
	duration, _ := time.ParseDuration(s.ReloadInterval)
	return duration
}

func (s ServerConfig) ReadHeaderDuration() time.Duration {
	duration, _ := time.ParseDuration(s.ReadHeaderTimeout)
	return duration
}

func (s ServerConfig) IdleDuration() time.Duration {
	duration, _ := time.ParseDuration(s.IdleTimeout)
	return duration
}

func (s ServerConfig) ShutdownDuration() time.Duration {
	duration, _ := time.ParseDuration(s.ShutdownTimeout)
	return duration
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = defaultListen
	}
	if cfg.Server.ReloadInterval == "" {
		cfg.Server.ReloadInterval = defaultReloadInterval
	}
	if cfg.Server.ReadHeaderTimeout == "" {
		cfg.Server.ReadHeaderTimeout = defaultReadHeaderTimeout
	}
	if cfg.Server.IdleTimeout == "" {
		cfg.Server.IdleTimeout = defaultIdleTimeout
	}
	if cfg.Server.ShutdownTimeout == "" {
		cfg.Server.ShutdownTimeout = defaultShutdownTimeout
	}

	for i := range cfg.Routes {
		if cfg.Routes[i].Upstream.Auth.Mode == "" || cfg.Routes[i].Upstream.Auth.Mode == "follow" {
			cfg.Routes[i].Upstream.Auth.Mode = "auto"
		}
		if len(cfg.Routes[i].Downstream.QueryParams) == 0 {
			cfg.Routes[i].Downstream.QueryParams = []string{"token"}
		}
		if cfg.Routes[i].Upstream.Auth.Mode == "query" && cfg.Routes[i].Upstream.Auth.QueryParam == "" {
			cfg.Routes[i].Upstream.Auth.QueryParam = "token"
		}
	}
}

func validate(cfg *Config) error {
	if cfg.Version != CurrentVersion {
		return fmt.Errorf("version must be %d", CurrentVersion)
	}
	if err := validateServer(cfg.Server); err != nil {
		return err
	}

	names := make(map[string]struct{}, len(cfg.Routes))
	prefixes := make(map[string]struct{}, len(cfg.Routes))
	for i := range cfg.Routes {
		route := &cfg.Routes[i]
		if err := validateRoute(i, route); err != nil {
			return err
		}
		if _, exists := names[route.Name]; exists {
			return fmt.Errorf("routes[%d].name is duplicated", i)
		}
		if _, exists := prefixes[route.PathPrefix]; exists {
			return fmt.Errorf("routes[%d].path_prefix is duplicated", i)
		}
		names[route.Name] = struct{}{}
		prefixes[route.PathPrefix] = struct{}{}
	}

	return nil
}

func validateServer(server ServerConfig) error {
	_, portText, err := net.SplitHostPort(server.Listen)
	if err != nil {
		return errors.New("server.listen must be a valid host:port address")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return errors.New("server.listen port must be between 0 and 65535")
	}

	durations := []struct {
		name  string
		value string
	}{
		{name: "reload_interval", value: server.ReloadInterval},
		{name: "read_header_timeout", value: server.ReadHeaderTimeout},
		{name: "idle_timeout", value: server.IdleTimeout},
		{name: "shutdown_timeout", value: server.ShutdownTimeout},
	}
	for _, item := range durations {
		duration, parseErr := time.ParseDuration(item.value)
		if parseErr != nil || duration <= 0 {
			return fmt.Errorf("server.%s must be a positive Go duration", item.name)
		}
	}

	return nil
}

func validateRoute(index int, route *Route) error {
	field := fmt.Sprintf("routes[%d]", index)
	if strings.TrimSpace(route.Name) == "" {
		return fmt.Errorf("%s.name must not be empty", field)
	}
	if err := validatePathPrefix(route.PathPrefix); err != nil {
		return fmt.Errorf("%s.path_prefix %w", field, err)
	}
	if err := validateUpstream(route.Upstream); err != nil {
		return fmt.Errorf("%s.upstream %w", field, err)
	}
	if err := validateDownstream(route.Downstream); err != nil {
		return fmt.Errorf("%s.downstream %w", field, err)
	}

	return nil
}

func validatePathPrefix(prefix string) error {
	if prefix == "" || !strings.HasPrefix(prefix, "/") {
		return errors.New("must be an absolute path")
	}
	if path.Clean(prefix) != prefix {
		return errors.New("must be a normalized path without a trailing slash")
	}
	if strings.ContainsAny(prefix, "?#") {
		return errors.New("must not contain a query or fragment")
	}

	return nil
}

func validateUpstream(upstream UpstreamConfig) error {
	target, err := url.Parse(upstream.URL)
	if err != nil {
		return errors.New("url is invalid")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return errors.New("url scheme must be http or https")
	}
	if target.Host == "" {
		return errors.New("url must include a host")
	}
	if target.User != nil {
		return errors.New("url must not include userinfo")
	}
	if target.Fragment != "" {
		return errors.New("url must not include a fragment")
	}

	return validateUpstreamAuth(upstream.Auth)
}

func validateUpstreamAuth(auth UpstreamAuth) error {
	switch auth.Mode {
	case "auto":
		if auth.Token == "" {
			return errors.New("auth.token is required for auto mode")
		}
	case "bearer":
		if auth.Token == "" {
			return errors.New("auth.token is required for bearer mode")
		}
	case "query":
		if auth.Token == "" {
			return errors.New("auth.token is required for query mode")
		}
	case "basic":
		if auth.Username == "" {
			return errors.New("auth.username is required for basic mode")
		}
		if auth.Password == "" {
			return errors.New("auth.password is required for basic mode")
		}
	default:
		return errors.New("auth.mode must be auto, bearer, query, or basic")
	}

	if auth.QueryParam == "" {
		return nil
	}
	if err := validateQueryParam(auth.QueryParam); err != nil {
		return fmt.Errorf("auth.query_param %w", err)
	}

	return nil
}

func validateDownstream(downstream DownstreamConfig) error {
	params := make(map[string]struct{}, len(downstream.QueryParams))
	for i, param := range downstream.QueryParams {
		if err := validateQueryParam(param); err != nil {
			return fmt.Errorf("query_params[%d] %w", i, err)
		}
		if _, exists := params[param]; exists {
			return fmt.Errorf("query_params[%d] is duplicated", i)
		}
		params[param] = struct{}{}
	}

	names := make(map[string]struct{}, len(downstream.Tokens))
	values := make(map[string]struct{}, len(downstream.Tokens))
	for i, token := range downstream.Tokens {
		if strings.TrimSpace(token.Name) == "" {
			return fmt.Errorf("tokens[%d].name must not be empty", i)
		}
		if token.Value == "" {
			return fmt.Errorf("tokens[%d].value must not be empty", i)
		}
		if _, exists := names[token.Name]; exists {
			return fmt.Errorf("tokens[%d].name is duplicated", i)
		}
		if _, exists := values[token.Value]; exists {
			return fmt.Errorf("tokens[%d].value is duplicated", i)
		}
		names[token.Name] = struct{}{}
		values[token.Value] = struct{}{}
	}

	return nil
}

func validateQueryParam(param string) error {
	if strings.TrimSpace(param) == "" {
		return errors.New("must not be empty")
	}
	if strings.ContainsAny(param, "&=#") {
		return errors.New("contains an invalid character")
	}

	return nil
}
