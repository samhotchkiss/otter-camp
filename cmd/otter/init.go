package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

type initOptions struct {
	Mode    string
	Name    string
	Email   string
	OrgName string
	APIBase string
}

type initBootstrapClient interface {
	OnboardingBootstrap(input ottercli.OnboardingBootstrapRequest) (ottercli.OnboardingBootstrapResponse, error)
}

var (
	loadInitConfig = ottercli.LoadConfig
	saveInitConfig = ottercli.SaveConfig
	newInitClient  = func(apiBase string) (initBootstrapClient, error) {
		client, err := ottercli.NewClient(ottercli.Config{APIBaseURL: strings.TrimSpace(apiBase)}, "")
		if err != nil {
			return nil, err
		}
		return client, nil
	}
)

const initLocalDefaultAPIBaseURL = "http://localhost:4200"

func handleInit(args []string) {
	if err := runInitCommand(args, os.Stdin, os.Stdout); err != nil {
		die(err.Error())
	}
}

func runInitCommand(args []string, in io.Reader, out io.Writer) error {
	opts, err := parseInitOptions(args)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(in)
	mode := opts.Mode
	if mode == "" {
		mode, err = promptInitMode(reader, out)
		if err != nil {
			return err
		}
	}

	switch mode {
	case "local":
		return runLocalInit(opts, reader, out)
	case "hosted":
		fmt.Fprintln(out, "Visit otter.camp/setup to get started.")
		return nil
	default:
		return errors.New("--mode must be local or hosted")
	}
}

func parseInitOptions(args []string) (initOptions, error) {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	mode := flags.String("mode", "", "onboarding mode (local|hosted)")
	name := flags.String("name", "", "display name for local bootstrap")
	email := flags.String("email", "", "email for local bootstrap")
	orgName := flags.String("org-name", "", "organization name for local bootstrap")
	apiBase := flags.String("api", "", "API base URL override")
	if err := flags.Parse(args); err != nil {
		return initOptions{}, err
	}
	if len(flags.Args()) != 0 {
		return initOptions{}, errors.New("usage: otter init [--mode <local|hosted>]")
	}

	normalizedMode := strings.ToLower(strings.TrimSpace(*mode))
	if normalizedMode != "" && normalizedMode != "local" && normalizedMode != "hosted" {
		return initOptions{}, errors.New("--mode must be local or hosted")
	}

	return initOptions{
		Mode:    normalizedMode,
		Name:    strings.TrimSpace(*name),
		Email:   strings.TrimSpace(*email),
		OrgName: strings.TrimSpace(*orgName),
		APIBase: strings.TrimSpace(*apiBase),
	}, nil
}

func promptInitMode(reader *bufio.Reader, out io.Writer) (string, error) {
	if reader == nil {
		return "", errors.New("input reader is required")
	}

	for {
		fmt.Fprintln(out, "Welcome to Otter Camp!")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "How are you setting up?")
		fmt.Fprintln(out, "  [1] Local install (run everything on this machine)")
		fmt.Fprintln(out, "  [2] Hosted (connect to otter.camp)")
		fmt.Fprint(out, "> ")

		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}

		choice := strings.ToLower(strings.TrimSpace(line))
		switch choice {
		case "", "1", "local":
			return "local", nil
		case "2", "hosted":
			return "hosted", nil
		}

		if errors.Is(err, io.EOF) {
			return "", errors.New("init mode selection required")
		}
		fmt.Fprintln(out, "Please enter 1 for local or 2 for hosted.")
	}
}

func runLocalInit(opts initOptions, reader *bufio.Reader, out io.Writer) error {
	if reader == nil {
		return errors.New("input reader is required")
	}

	cfg, err := loadInitConfig()
	if err != nil {
		return err
	}

	req, err := collectLocalInitBootstrapInput(opts, reader, out)
	if err != nil {
		return err
	}

	apiBase := resolveInitAPIBaseURL(cfg.APIBaseURL, opts.APIBase)
	client, err := newInitClient(apiBase)
	if err != nil {
		return err
	}

	resp, err := client.OnboardingBootstrap(req)
	if err != nil {
		return err
	}

	cfg.APIBaseURL = apiBase
	cfg.Token = strings.TrimSpace(resp.Token)
	cfg.DefaultOrg = strings.TrimSpace(resp.OrgID)
	if err := saveInitConfig(cfg); err != nil {
		return err
	}

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Account setup complete.")
	fmt.Fprintf(out, "Your auth token (saved to CLI config): %s\n", cfg.Token)
	fmt.Fprintf(out, "Organization: %s\n", initFirstNonEmpty(req.OrganizationName, resp.OrgSlug, resp.OrgID))
	fmt.Fprintf(out, "Dashboard: %s\n", initLocalDefaultAPIBaseURL)
	fmt.Fprintln(out, "Next step: otter whoami")
	return nil
}

func collectLocalInitBootstrapInput(opts initOptions, reader *bufio.Reader, out io.Writer) (ottercli.OnboardingBootstrapRequest, error) {
	name := strings.TrimSpace(opts.Name)
	email := strings.TrimSpace(opts.Email)
	orgName := strings.TrimSpace(opts.OrgName)

	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Let's set up your account.")
	fmt.Fprintln(out, "")

	if name == "" {
		name = promptRequiredField(reader, out, "Your name: ")
	}
	if email == "" {
		email = promptRequiredField(reader, out, "Email: ")
	}
	if orgName == "" {
		orgName = promptRequiredField(reader, out, "Organization name: ")
	}

	if name == "" {
		return ottercli.OnboardingBootstrapRequest{}, errors.New("name is required")
	}
	if email == "" {
		return ottercli.OnboardingBootstrapRequest{}, errors.New("email is required")
	}
	if !strings.Contains(email, "@") {
		return ottercli.OnboardingBootstrapRequest{}, errors.New("invalid email")
	}
	if orgName == "" {
		return ottercli.OnboardingBootstrapRequest{}, errors.New("organization name is required")
	}

	return ottercli.OnboardingBootstrapRequest{
		Name:             name,
		Email:            email,
		OrganizationName: orgName,
	}, nil
}

func promptRequiredField(reader *bufio.Reader, out io.Writer, label string) string {
	for {
		fmt.Fprint(out, label)
		line, err := reader.ReadString('\n')
		value := strings.TrimSpace(line)
		if value != "" {
			return value
		}
		if errors.Is(err, io.EOF) {
			return ""
		}
		fmt.Fprintln(out, "This field is required.")
	}
}

func resolveInitAPIBaseURL(existing, override string) string {
	override = strings.TrimSpace(override)
	if override != "" {
		return strings.TrimRight(override, "/")
	}

	existing = strings.TrimSpace(existing)
	if existing == "" || strings.EqualFold(existing, "https://api.otter.camp") {
		return initLocalDefaultAPIBaseURL
	}
	return strings.TrimRight(existing, "/")
}

func initFirstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
