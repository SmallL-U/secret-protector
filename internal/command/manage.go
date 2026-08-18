package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"secret-protector/internal/config"
)

var errInteractiveExit = errors.New("interactive session exited")

type interactiveManager struct {
	ctx        context.Context
	filename   string
	input      io.Reader
	output     io.Writer
	accessible bool
	tty        bool
	tracked    *singleByteReader
}

type formOption struct {
	label string
	value string
}

// singleByteReader keeps one accessible form field from buffering answers meant
// for later fields when input comes from a pipe or a test reader.
type singleByteReader struct {
	input io.Reader
	eof   bool
}

func newManageCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "manage",
		Short: "Manage configuration interactively",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			manager := newInteractiveManager(command.Context(), *configPath, command.InOrStdin(), command.OutOrStdout())
			return manager.run()
		},
	}
}

func newInteractiveManager(ctx context.Context, filename string, input io.Reader, output io.Writer) *interactiveManager {
	manager := &interactiveManager{
		ctx:      ctx,
		filename: filename,
		input:    input,
		output:   output,
	}
	file, isFile := input.(*os.File)
	manager.tty = isFile && term.IsTerminal(file.Fd())
	manager.accessible = !manager.tty || os.Getenv("SECRET_PROTECTOR_ACCESSIBLE") != ""
	if manager.tty {
		return manager
	}

	manager.tracked = &singleByteReader{input: input}
	manager.input = manager.tracked

	return manager
}

func (manager *interactiveManager) run() error {
	err := manager.ensureConfig()
	if isInteractiveExit(err) {
		return nil
	}
	if err != nil {
		return err
	}

	for {
		choice, err := manager.mainMenu()
		if isInteractiveExit(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if choice == "0" {
			return nil
		}

		err = manager.runAction(choice)
		if isInteractiveExit(err) {
			return nil
		}
		if err == nil {
			continue
		}
		if _, writeErr := fmt.Fprintf(manager.output, "error: %v\n", err); writeErr != nil {
			return writeErr
		}
	}
}

func (manager *interactiveManager) ensureConfig() error {
	_, err := os.Stat(manager.filename)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect config: %w", err)
	}
	if err := config.RequireWritable(manager.filename); err != nil {
		return err
	}

	create := true
	if err := manager.runForm(
		huh.NewConfirm().
			Title("Configuration does not exist. Create it?").
			Affirmative("Create").
			Negative("Exit").
			Value(&create),
	); err != nil {
		return err
	}
	if !create {
		return errInteractiveExit
	}

	listen := "127.0.0.1:8080"
	if err := manager.runForm(requiredInput("Listen address", &listen)); err != nil {
		return err
	}
	cfg := config.New()
	cfg.Server.Listen = listen
	if err := config.SaveAtomic(manager.filename, cfg); err != nil {
		return err
	}
	_, err = fmt.Fprintf(manager.output, "created %s\n", manager.filename)

	return err
}

func (manager *interactiveManager) mainMenu() (string, error) {
	choice := ""
	title := "Secret Protector configuration manager ([write] changes configuration)"
	if !config.IsWritable(manager.filename) {
		title = "Secret Protector configuration manager [read-only] ([write] unavailable)"
	}
	field := manager.choiceField(title, &choice,
		formOption{label: "1  List routes", value: "1"},
		formOption{label: "2  Add route [write]", value: "2"},
		formOption{label: "3  Remove route [write]", value: "3"},
		formOption{label: "4  Issue downstream token [write]", value: "4"},
		formOption{label: "5  List downstream tokens", value: "5"},
		formOption{label: "6  Revoke downstream token [write]", value: "6"},
		formOption{label: "7  Validate configuration", value: "7"},
		formOption{label: "0  Exit", value: "0"},
	)
	if err := manager.runForm(field); err != nil {
		return "", err
	}

	return choice, nil
}

func (manager *interactiveManager) runAction(choice string) error {
	if interactiveActionWrites(choice) {
		if err := config.RequireWritable(manager.filename); err != nil {
			return err
		}
	}

	switch choice {
	case "1":
		return manager.listRoutes()
	case "2":
		return manager.addRoute()
	case "3":
		return manager.removeRoute()
	case "4":
		return manager.issueToken()
	case "5":
		return manager.listTokens()
	case "6":
		return manager.revokeToken()
	case "7":
		return manager.validateConfig()
	default:
		return errors.New("unknown interactive action")
	}
}

func interactiveActionWrites(choice string) bool {
	switch choice {
	case "2", "3", "4", "6":
		return true
	default:
		return false
	}
}

func (manager *interactiveManager) listRoutes() error {
	cfg, _, err := config.Load(manager.filename)
	if err != nil {
		return err
	}

	return writeRouteList(manager.output, cfg)
}

func (manager *interactiveManager) addRoute() error {
	options := routeAddOptions{authMode: "auto"}
	if err := manager.runForm(
		requiredInput("Route name", &options.name),
		requiredInput("Upstream URL", &options.upstreamURL),
		manager.choiceField("Upstream auth mode", &options.authMode,
			formOption{label: "Follow downstream automatically", value: "auto"},
			formOption{label: "Bearer token", value: "bearer"},
			formOption{label: "Query parameter", value: "query"},
			formOption{label: "HTTP header", value: "header"},
			formOption{label: "Basic auth", value: "basic"},
		),
	); err != nil {
		return err
	}
	if err := manager.promptUpstreamAuth(&options); err != nil {
		return err
	}

	queryParams := "token"
	headers := ""
	options.tokenName = "default"
	if err := manager.runForm(
		requiredInput("Downstream query parameters (comma-separated)", &queryParams),
		huh.NewInput().Title("Downstream credential headers (comma-separated, blank for none)").Value(&headers),
		requiredInput("Initial downstream token name", &options.tokenName),
	); err != nil {
		return err
	}
	options.downstreamQueryParams = splitCommaSeparated(queryParams)
	options.downstreamHeaders = splitCommaSeparated(headers)

	issuedToken, err := addRoute(manager.filename, options)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(manager.output, "route %s added\n", options.name); err != nil {
		return err
	}
	_, err = fmt.Fprintf(manager.output, "downstream token %s: %s\n", options.tokenName, issuedToken)

	return err
}

func (manager *interactiveManager) promptUpstreamAuth(options *routeAddOptions) error {
	switch options.authMode {
	case "auto":
		return manager.promptAutoAuth(options)
	case "bearer":
		return manager.runForm(manager.requiredSecretInput("Upstream token", &options.upstreamToken))
	case "query":
		return manager.promptQueryAuth(options)
	case "header":
		return manager.promptHeaderAuth(options)
	case "basic":
		return manager.promptBasicAuth(options)
	default:
		return errors.New("unsupported upstream auth mode")
	}
}

func (manager *interactiveManager) promptAutoAuth(options *routeAddOptions) error {
	return manager.runForm(
		manager.requiredSecretInput("Upstream token", &options.upstreamToken),
		huh.NewInput().Title("Upstream query parameter (blank follows downstream)").Value(&options.queryParam),
		huh.NewInput().Title("Upstream credential header (blank follows downstream)").Value(&options.headerName),
		huh.NewInput().Title("Upstream Basic username (blank follows downstream)").Value(&options.upstreamUsername),
		manager.secretInput("Upstream Basic password (blank uses token)", &options.upstreamPassword),
	)
}

func (manager *interactiveManager) promptQueryAuth(options *routeAddOptions) error {
	options.queryParam = "token"
	return manager.runForm(
		manager.requiredSecretInput("Upstream token", &options.upstreamToken),
		requiredInput("Upstream query parameter", &options.queryParam),
	)
}

func (manager *interactiveManager) promptHeaderAuth(options *routeAddOptions) error {
	options.headerName = "X-API-Key"
	return manager.runForm(
		manager.requiredSecretInput("Upstream token", &options.upstreamToken),
		requiredInput("Upstream credential header", &options.headerName),
	)
}

func (manager *interactiveManager) promptBasicAuth(options *routeAddOptions) error {
	return manager.runForm(
		requiredInput("Upstream Basic username", &options.upstreamUsername),
		manager.requiredSecretInput("Upstream Basic password", &options.upstreamPassword),
	)
}

func (manager *interactiveManager) removeRoute() error {
	cfg, _, err := config.Load(manager.filename)
	if err != nil {
		return err
	}
	if len(cfg.Routes) == 0 {
		_, err := fmt.Fprintln(manager.output, "No routes configured.")
		return err
	}
	if err := writeRouteList(manager.output, cfg); err != nil {
		return err
	}

	name := ""
	confirmed := false
	if err := manager.runForm(
		requiredInput("Route name to remove", &name),
		huh.NewConfirm().Title("Remove this route?").Value(&confirmed),
	); err != nil {
		return err
	}
	if !confirmed {
		_, err := fmt.Fprintln(manager.output, "Canceled.")
		return err
	}
	if err := removeRoute(manager.filename, name); err != nil {
		return err
	}
	_, err = fmt.Fprintf(manager.output, "route %s removed\n", name)

	return err
}

func (manager *interactiveManager) issueToken() error {
	cfg, _, err := config.Load(manager.filename)
	if err != nil {
		return err
	}
	if len(cfg.Routes) == 0 {
		_, err := fmt.Fprintln(manager.output, "No routes configured.")
		return err
	}
	if err := writeRouteList(manager.output, cfg); err != nil {
		return err
	}

	routeName := ""
	tokenName := ""
	if err := manager.runForm(
		requiredInput("Route name", &routeName),
		requiredInput("New token name", &tokenName),
	); err != nil {
		return err
	}
	value, err := issueDownstreamToken(manager.filename, routeName, tokenName)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(manager.output, "downstream token %s: %s\n", tokenName, value)

	return err
}

func (manager *interactiveManager) listTokens() error {
	cfg, _, err := config.Load(manager.filename)
	if err != nil {
		return err
	}
	if len(cfg.Routes) == 0 {
		_, err := fmt.Fprintln(manager.output, "No routes configured.")
		return err
	}
	if err := writeRouteList(manager.output, cfg); err != nil {
		return err
	}

	routeName := ""
	if err := manager.runForm(requiredInput("Route name", &routeName)); err != nil {
		return err
	}

	return writeTokenList(manager.output, cfg, routeName)
}

func (manager *interactiveManager) revokeToken() error {
	cfg, _, err := config.Load(manager.filename)
	if err != nil {
		return err
	}
	if len(cfg.Routes) == 0 {
		_, err := fmt.Fprintln(manager.output, "No routes configured.")
		return err
	}
	if err := writeRouteList(manager.output, cfg); err != nil {
		return err
	}

	routeName := ""
	if err := manager.runForm(requiredInput("Route name", &routeName)); err != nil {
		return err
	}
	if err := writeTokenList(manager.output, cfg, routeName); err != nil {
		return err
	}

	tokenName := ""
	confirmed := false
	if err := manager.runForm(
		requiredInput("Token name to revoke", &tokenName),
		huh.NewConfirm().Title("Revoke this token?").Value(&confirmed),
	); err != nil {
		return err
	}
	if !confirmed {
		_, err := fmt.Fprintln(manager.output, "Canceled.")
		return err
	}
	if err := revokeDownstreamToken(manager.filename, routeName, tokenName); err != nil {
		return err
	}
	_, err = fmt.Fprintf(manager.output, "downstream token %s revoked\n", tokenName)

	return err
}

func (manager *interactiveManager) validateConfig() error {
	if _, _, err := config.Load(manager.filename); err != nil {
		return err
	}
	_, err := fmt.Fprintf(manager.output, "%s is valid\n", manager.filename)

	return err
}

func (manager *interactiveManager) runForm(fields ...huh.Field) error {
	form := huh.NewForm(huh.NewGroup(fields...)).
		WithInput(manager.input).
		WithOutput(manager.output).
		WithAccessible(manager.accessible)
	err := form.RunWithContext(manager.ctx)
	if errors.Is(err, huh.ErrUserAborted) {
		return errInteractiveExit
	}
	if err != nil {
		return err
	}
	if manager.tracked != nil && manager.tracked.eof {
		return io.EOF
	}

	return nil
}

func (manager *interactiveManager) choiceField(title string, value *string, options ...formOption) huh.Field {
	if !manager.accessible {
		items := make([]huh.Option[string], 0, len(options))
		for _, option := range options {
			items = append(items, huh.NewOption(option.label, option.value))
		}
		return huh.NewSelect[string]().Title(title).Options(items...).Value(value)
	}

	allowed := make([]string, 0, len(options))
	labels := make([]string, 0, len(options))
	valid := make(map[string]struct{}, len(options))
	for _, option := range options {
		allowed = append(allowed, option.value)
		labels = append(labels, option.label)
		valid[option.value] = struct{}{}
	}
	defaultValue := *value
	return huh.NewInput().
		Title(title).
		Description("Choices: " + strings.Join(labels, ", ")).
		Value(value).
		Validate(func(input string) error {
			candidate := defaultIfEmpty(strings.ToLower(strings.TrimSpace(input)), defaultValue)
			if _, ok := valid[candidate]; ok {
				return nil
			}
			return fmt.Errorf("choose one of: %s", strings.Join(allowed, ", "))
		})
}

func (manager *interactiveManager) secretInput(title string, value *string) *huh.Input {
	field := huh.NewInput().Title(title).Value(value)
	if manager.tty {
		field.EchoMode(huh.EchoModePassword)
	}

	return field
}

func (manager *interactiveManager) requiredSecretInput(title string, value *string) *huh.Input {
	field := manager.secretInput(title, value)
	defaultValue := *value
	return field.Validate(requiredValidator(defaultValue))
}

func requiredInput(title string, value *string) *huh.Input {
	defaultValue := *value
	return huh.NewInput().Title(title).Value(value).Validate(requiredValidator(defaultValue))
}

func requiredValidator(defaultValue string) func(string) error {
	return func(input string) error {
		if strings.TrimSpace(input) != "" || defaultValue != "" {
			return nil
		}

		return errors.New("value is required")
	}
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}

	return result
}

func defaultIfEmpty(value string, defaultValue string) string {
	if value != "" {
		return value
	}

	return defaultValue
}

func isInteractiveExit(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, errInteractiveExit)
}

func (reader *singleByteReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}

	read, err := reader.input.Read(buffer)
	if errors.Is(err, io.EOF) && read == 0 {
		reader.eof = true
	}

	return read, err
}
