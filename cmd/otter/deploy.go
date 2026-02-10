package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

type deployCommandClient interface {
	FindProject(query string) (ottercli.Project, error)
	GetDeployConfig(projectID string) (ottercli.DeployConfig, error)
	SetDeployConfig(projectID string, cfg ottercli.DeployConfig) (ottercli.DeployConfig, error)
}

type deployClientFactory func(orgOverride string) (deployCommandClient, error)

type deployConfigOptions struct {
	ProjectRef string
	Org        string
	JSON       bool
}

type deploySetOptions struct {
	ProjectRef string
	Method     string
	RepoURL    *string
	Branch     string
	Command    *string
	Org        string
	JSON       bool
}

func handleDeploy(args []string) {
	if err := runDeployCommand(args, newDeployCommandClient, os.Stdout); err != nil {
		die(err.Error())
	}
}

func runDeployCommand(args []string, factory deployClientFactory, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: otter deploy <config|set> ...")
	}

	switch args[0] {
	case "config":
		opts, err := parseDeployConfigOptions(args[1:])
		if err != nil {
			return err
		}
		client, err := factory(opts.Org)
		if err != nil {
			return err
		}
		return runDeployConfig(client, opts, out)
	case "set":
		opts, err := parseDeploySetOptions(args[1:])
		if err != nil {
			return err
		}
		client, err := factory(opts.Org)
		if err != nil {
			return err
		}
		return runDeploySet(client, opts, out)
	default:
		return errors.New("usage: otter deploy <config|set> ...")
	}
}

func parseDeployConfigOptions(args []string) (deployConfigOptions, error) {
	flags := flag.NewFlagSet("deploy config", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	project := flags.String("project", "", "project name or id (required)")
	org := flags.String("org", "", "org id override")
	jsonOut := flags.Bool("json", false, "JSON output")
	if err := flags.Parse(args); err != nil {
		return deployConfigOptions{}, err
	}
	if strings.TrimSpace(*project) == "" {
		return deployConfigOptions{}, errors.New("--project is required")
	}
	if len(flags.Args()) != 0 {
		return deployConfigOptions{}, errors.New("usage: otter deploy config --project <project-name-or-id>")
	}
	return deployConfigOptions{
		ProjectRef: strings.TrimSpace(*project),
		Org:        strings.TrimSpace(*org),
		JSON:       *jsonOut,
	}, nil
}

func parseDeploySetOptions(args []string) (deploySetOptions, error) {
	flags := flag.NewFlagSet("deploy set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	project := flags.String("project", "", "project name or id (required)")
	method := flags.String("method", "", "deploy method (none|github_push|cli_command)")
	repo := flags.String("repo", "", "GitHub repo URL override")
	branch := flags.String("branch", "", "GitHub branch")
	command := flags.String("command", "", "CLI command")
	org := flags.String("org", "", "org id override")
	jsonOut := flags.Bool("json", false, "JSON output")
	if err := flags.Parse(args); err != nil {
		return deploySetOptions{}, err
	}

	if strings.TrimSpace(*project) == "" {
		return deploySetOptions{}, errors.New("--project is required")
	}
	if len(flags.Args()) != 0 {
		return deploySetOptions{}, errors.New("usage: otter deploy set --project <project-name-or-id> --method <none|github_push|cli_command> [--repo <url>] [--branch <name>] [--command <cmd>]")
	}

	normalizedMethod := strings.ToLower(strings.TrimSpace(*method))
	switch normalizedMethod {
	case "none", "github_push", "cli_command":
	default:
		return deploySetOptions{}, errors.New("--method must be one of none, github_push, cli_command")
	}

	repoVal := strings.TrimSpace(*repo)
	branchVal := strings.TrimSpace(*branch)
	commandVal := strings.TrimSpace(*command)

	switch normalizedMethod {
	case "none":
		if repoVal != "" {
			return deploySetOptions{}, errors.New("--repo is not valid for --method none")
		}
		if branchVal != "" {
			return deploySetOptions{}, errors.New("--branch is not valid for --method none")
		}
		if commandVal != "" {
			return deploySetOptions{}, errors.New("--command is not valid for --method none")
		}
	case "github_push":
		if commandVal != "" {
			return deploySetOptions{}, errors.New("--command is not valid for --method github_push")
		}
		if branchVal == "" {
			branchVal = "main"
		}
	case "cli_command":
		if repoVal != "" {
			return deploySetOptions{}, errors.New("--repo is not valid for --method cli_command")
		}
		if branchVal != "" {
			return deploySetOptions{}, errors.New("--branch is not valid for --method cli_command")
		}
		if commandVal == "" {
			return deploySetOptions{}, errors.New("--command is required for --method cli_command")
		}
	}

	var repoPtr *string
	if repoVal != "" {
		repoCopy := repoVal
		repoPtr = &repoCopy
	}
	var commandPtr *string
	if commandVal != "" {
		commandCopy := commandVal
		commandPtr = &commandCopy
	}

	return deploySetOptions{
		ProjectRef: strings.TrimSpace(*project),
		Method:     normalizedMethod,
		RepoURL:    repoPtr,
		Branch:     branchVal,
		Command:    commandPtr,
		Org:        strings.TrimSpace(*org),
		JSON:       *jsonOut,
	}, nil
}

func runDeployConfig(client deployCommandClient, opts deployConfigOptions, out io.Writer) error {
	project, err := client.FindProject(opts.ProjectRef)
	if err != nil {
		return err
	}
	cfg, err := client.GetDeployConfig(project.ID)
	if err != nil {
		return err
	}
	if opts.JSON {
		printJSONTo(out, cfg)
		return nil
	}

	fmt.Fprintf(out, "Project: %s\n", project.Name)
	fmt.Fprintf(out, "Method: %s\n", cfg.DeployMethod)
	if cfg.GitHubRepoURL != nil && strings.TrimSpace(*cfg.GitHubRepoURL) != "" {
		fmt.Fprintf(out, "Repo: %s\n", strings.TrimSpace(*cfg.GitHubRepoURL))
	}
	if strings.TrimSpace(cfg.GitHubBranch) != "" {
		fmt.Fprintf(out, "Branch: %s\n", strings.TrimSpace(cfg.GitHubBranch))
	}
	if cfg.CLICommand != nil && strings.TrimSpace(*cfg.CLICommand) != "" {
		fmt.Fprintf(out, "Command: %s\n", strings.TrimSpace(*cfg.CLICommand))
	}
	return nil
}

func runDeploySet(client deployCommandClient, opts deploySetOptions, out io.Writer) error {
	project, err := client.FindProject(opts.ProjectRef)
	if err != nil {
		return err
	}
	updated, err := client.SetDeployConfig(project.ID, ottercli.DeployConfig{
		DeployMethod:  opts.Method,
		GitHubRepoURL: opts.RepoURL,
		GitHubBranch:  opts.Branch,
		CLICommand:    opts.Command,
	})
	if err != nil {
		return err
	}

	if opts.JSON {
		printJSONTo(out, updated)
		return nil
	}
	fmt.Fprintf(out, "Updated deployment for %s\n", project.Name)
	fmt.Fprintf(out, "Method: %s\n", updated.DeployMethod)
	if updated.GitHubRepoURL != nil && strings.TrimSpace(*updated.GitHubRepoURL) != "" {
		fmt.Fprintf(out, "Repo: %s\n", strings.TrimSpace(*updated.GitHubRepoURL))
	}
	if strings.TrimSpace(updated.GitHubBranch) != "" {
		fmt.Fprintf(out, "Branch: %s\n", strings.TrimSpace(updated.GitHubBranch))
	}
	if updated.CLICommand != nil && strings.TrimSpace(*updated.CLICommand) != "" {
		fmt.Fprintf(out, "Command: %s\n", strings.TrimSpace(*updated.CLICommand))
	}
	return nil
}

func newDeployCommandClient(orgOverride string) (deployCommandClient, error) {
	cfg, err := ottercli.LoadConfig()
	if err != nil {
		return nil, err
	}
	return ottercli.NewClient(cfg, orgOverride)
}
