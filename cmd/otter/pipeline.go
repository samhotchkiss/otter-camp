package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

type pipelineCommandClient interface {
	FindProject(query string) (ottercli.Project, error)
	ResolveAgent(query string) (ottercli.Agent, error)
	GetPipelineRoles(projectID string) (ottercli.PipelineRoles, error)
	SetPipelineRoles(projectID string, roles ottercli.PipelineRoles) (ottercli.PipelineRoles, error)
	SetProjectRequireHumanReview(projectID string, requireHumanReview bool) (ottercli.Project, error)
}

type pipelineClientFactory func(orgOverride string) (pipelineCommandClient, error)

type pipelineRolesOptions struct {
	ProjectRef string
	Org        string
	JSON       bool
}

type pipelineSetRoleOptions struct {
	ProjectRef string
	Role       string
	AgentRef   string
	Clear      bool
	Org        string
	JSON       bool
}

type pipelineSetOptions struct {
	ProjectRef         string
	RequireHumanReview bool
	Org                string
	JSON               bool
}

func handlePipeline(args []string) {
	if err := runPipelineCommand(args, newPipelineCommandClient, os.Stdout); err != nil {
		die(err.Error())
	}
}

func runPipelineCommand(args []string, factory pipelineClientFactory, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: otter pipeline <roles|set-role|set> ...")
	}

	switch args[0] {
	case "roles":
		opts, err := parsePipelineRolesOptions(args[1:])
		if err != nil {
			return err
		}
		client, err := factory(opts.Org)
		if err != nil {
			return err
		}
		return runPipelineRoles(client, opts, out)
	case "set-role":
		opts, err := parsePipelineSetRoleOptions(args[1:])
		if err != nil {
			return err
		}
		client, err := factory(opts.Org)
		if err != nil {
			return err
		}
		return runPipelineSetRole(client, opts, out)
	case "set":
		opts, err := parsePipelineSetOptions(args[1:])
		if err != nil {
			return err
		}
		client, err := factory(opts.Org)
		if err != nil {
			return err
		}
		return runPipelineSet(client, opts, out)
	default:
		return errors.New("usage: otter pipeline <roles|set-role|set> ...")
	}
}

func parsePipelineRolesOptions(args []string) (pipelineRolesOptions, error) {
	flags := flag.NewFlagSet("pipeline roles", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	project := flags.String("project", "", "project name or id (required)")
	org := flags.String("org", "", "org id override")
	jsonOut := flags.Bool("json", false, "JSON output")
	if err := flags.Parse(args); err != nil {
		return pipelineRolesOptions{}, err
	}
	if strings.TrimSpace(*project) == "" {
		return pipelineRolesOptions{}, errors.New("--project is required")
	}
	if len(flags.Args()) != 0 {
		return pipelineRolesOptions{}, errors.New("usage: otter pipeline roles --project <project-name-or-id>")
	}
	return pipelineRolesOptions{
		ProjectRef: strings.TrimSpace(*project),
		Org:        strings.TrimSpace(*org),
		JSON:       *jsonOut,
	}, nil
}

func parsePipelineSetRoleOptions(args []string) (pipelineSetRoleOptions, error) {
	flags := flag.NewFlagSet("pipeline set-role", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	project := flags.String("project", "", "project name or id (required)")
	role := flags.String("role", "", "role (planner|worker|reviewer)")
	agent := flags.String("agent", "", "agent id/name/slug")
	clear := flags.Bool("none", false, "clear role assignment")
	org := flags.String("org", "", "org id override")
	jsonOut := flags.Bool("json", false, "JSON output")
	if err := flags.Parse(args); err != nil {
		return pipelineSetRoleOptions{}, err
	}
	if strings.TrimSpace(*project) == "" {
		return pipelineSetRoleOptions{}, errors.New("--project is required")
	}
	if len(flags.Args()) != 0 {
		return pipelineSetRoleOptions{}, errors.New("usage: otter pipeline set-role --project <project-name-or-id> --role <planner|worker|reviewer> [--agent <agent>|--none]")
	}

	normalizedRole, err := normalizePipelineRole(*role)
	if err != nil {
		return pipelineSetRoleOptions{}, err
	}

	agentRef := strings.TrimSpace(*agent)
	if agentRef != "" && *clear {
		return pipelineSetRoleOptions{}, errors.New("use only one of --agent or --none")
	}
	if agentRef == "" && !*clear {
		return pipelineSetRoleOptions{}, errors.New("either --agent or --none is required")
	}

	return pipelineSetRoleOptions{
		ProjectRef: strings.TrimSpace(*project),
		Role:       normalizedRole,
		AgentRef:   agentRef,
		Clear:      *clear,
		Org:        strings.TrimSpace(*org),
		JSON:       *jsonOut,
	}, nil
}

func parsePipelineSetOptions(args []string) (pipelineSetOptions, error) {
	flags := flag.NewFlagSet("pipeline set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	project := flags.String("project", "", "project name or id (required)")
	requireHumanReview := flags.String("require-human-review", "", "true or false")
	org := flags.String("org", "", "org id override")
	jsonOut := flags.Bool("json", false, "JSON output")
	if err := flags.Parse(args); err != nil {
		return pipelineSetOptions{}, err
	}
	if strings.TrimSpace(*project) == "" {
		return pipelineSetOptions{}, errors.New("--project is required")
	}
	if len(flags.Args()) != 0 {
		return pipelineSetOptions{}, errors.New("usage: otter pipeline set --project <project-name-or-id> --require-human-review <true|false>")
	}

	raw := strings.TrimSpace(*requireHumanReview)
	if raw == "" {
		return pipelineSetOptions{}, errors.New("--require-human-review is required")
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return pipelineSetOptions{}, errors.New("--require-human-review must be true or false")
	}

	return pipelineSetOptions{
		ProjectRef:         strings.TrimSpace(*project),
		RequireHumanReview: value,
		Org:                strings.TrimSpace(*org),
		JSON:               *jsonOut,
	}, nil
}

func runPipelineRoles(client pipelineCommandClient, opts pipelineRolesOptions, out io.Writer) error {
	project, err := client.FindProject(opts.ProjectRef)
	if err != nil {
		return err
	}
	roles, err := client.GetPipelineRoles(project.ID)
	if err != nil {
		return err
	}
	if opts.JSON {
		printJSONTo(out, roles)
		return nil
	}

	fmt.Fprintf(out, "Project: %s\n", project.Name)
	fmt.Fprintf(out, "Planner:  %s\n", formatRoleAgentID(roles.Planner.AgentID))
	fmt.Fprintf(out, "Worker:   %s\n", formatRoleAgentID(roles.Worker.AgentID))
	fmt.Fprintf(out, "Reviewer: %s\n", formatRoleAgentID(roles.Reviewer.AgentID))
	return nil
}

func runPipelineSetRole(client pipelineCommandClient, opts pipelineSetRoleOptions, out io.Writer) error {
	project, err := client.FindProject(opts.ProjectRef)
	if err != nil {
		return err
	}

	current, err := client.GetPipelineRoles(project.ID)
	if err != nil {
		return err
	}

	var roleAgentID *string
	if !opts.Clear {
		agent, resolveErr := client.ResolveAgent(opts.AgentRef)
		if resolveErr != nil {
			return resolveErr
		}
		roleAgentID = &agent.ID
	}

	if err := setPipelineRoleAssignment(&current, opts.Role, roleAgentID); err != nil {
		return err
	}
	updated, err := client.SetPipelineRoles(project.ID, current)
	if err != nil {
		return err
	}

	if opts.JSON {
		printJSONTo(out, updated)
		return nil
	}
	fmt.Fprintf(out, "Updated %s role for %s to %s\n", opts.Role, project.Name, formatRoleAgentID(roleAgentID))
	return nil
}

func runPipelineSet(client pipelineCommandClient, opts pipelineSetOptions, out io.Writer) error {
	project, err := client.FindProject(opts.ProjectRef)
	if err != nil {
		return err
	}
	updated, err := client.SetProjectRequireHumanReview(project.ID, opts.RequireHumanReview)
	if err != nil {
		return err
	}
	if opts.JSON {
		printJSONTo(out, updated)
		return nil
	}
	fmt.Fprintf(out, "Require human review for %s: %t\n", updated.Name, updated.RequireHumanReview)
	return nil
}

func normalizePipelineRole(raw string) (string, error) {
	role := strings.ToLower(strings.TrimSpace(raw))
	switch role {
	case "planner", "worker", "reviewer":
		return role, nil
	default:
		return "", errors.New("--role must be one of planner, worker, reviewer")
	}
}

func setPipelineRoleAssignment(roles *ottercli.PipelineRoles, role string, agentID *string) error {
	switch role {
	case "planner":
		roles.Planner.AgentID = agentID
	case "worker":
		roles.Worker.AgentID = agentID
	case "reviewer":
		roles.Reviewer.AgentID = agentID
	default:
		return errors.New("invalid pipeline role")
	}
	return nil
}

func formatRoleAgentID(agentID *string) string {
	if agentID == nil || strings.TrimSpace(*agentID) == "" {
		return "unassigned"
	}
	return strings.TrimSpace(*agentID)
}

func newPipelineCommandClient(orgOverride string) (pipelineCommandClient, error) {
	cfg, err := ottercli.LoadConfig()
	if err != nil {
		return nil, err
	}
	return ottercli.NewClient(cfg, orgOverride)
}
