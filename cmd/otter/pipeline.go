package main

import (
	"encoding/json"
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
	ListPipelineSteps(projectID string) ([]ottercli.PipelineStep, error)
	CreatePipelineStep(projectID string, input ottercli.PipelineStepCreateInput) (ottercli.PipelineStep, error)
	DeletePipelineStep(projectID string, stepID string) error
	ReorderPipelineSteps(projectID string, stepIDs []string) ([]ottercli.PipelineStep, error)
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
	ProjectRef            string
	RequireHumanReview    bool
	HasRequireHumanReview bool
	StepSpecs             []pipelineSetStepSpec
	Org                   string
	JSON                  bool
}

type pipelineAddStepOptions struct {
	ProjectRef  string
	Name        string
	Description string
	StepType    string
	AgentRef    string
	Position    int
	AutoAdvance bool
	Org         string
	JSON        bool
}

type pipelineSetStepSpec struct {
	Name        string
	Description string
	StepType    string
	AgentRef    string
	AutoAdvance bool
}

func handlePipeline(args []string) {
	if err := runPipelineCommand(args, newPipelineCommandClient, os.Stdout); err != nil {
		die(err.Error())
	}
}

func runPipelineCommand(args []string, factory pipelineClientFactory, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: otter pipeline <show|set|add-step|roles|set-role> ...")
	}

	switch args[0] {
	case "show":
		opts, err := parsePipelineRolesOptions(args[1:])
		if err != nil {
			return err
		}
		client, err := factory(opts.Org)
		if err != nil {
			return err
		}
		return runPipelineShow(client, opts, out)
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
	case "add-step":
		opts, err := parsePipelineAddStepOptions(args[1:])
		if err != nil {
			return err
		}
		client, err := factory(opts.Org)
		if err != nil {
			return err
		}
		return runPipelineAddStep(client, opts, out)
	default:
		return errors.New("usage: otter pipeline <show|set|add-step|roles|set-role> ...")
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
	stepsJSON := flags.String("steps", "", "pipeline step chain JSON")
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
		return pipelineSetOptions{}, errors.New("usage: otter pipeline set --project <project-name-or-id> --steps '<json>'")
	}

	rawSteps := strings.TrimSpace(*stepsJSON)
	raw := strings.TrimSpace(*requireHumanReview)
	if rawSteps != "" && raw != "" {
		return pipelineSetOptions{}, errors.New("use only one of --steps or --require-human-review")
	}

	if rawSteps != "" {
		stepSpecs, err := parsePipelineSetStepsJSON(rawSteps)
		if err != nil {
			return pipelineSetOptions{}, err
		}
		return pipelineSetOptions{
			ProjectRef: strings.TrimSpace(*project),
			StepSpecs:  stepSpecs,
			Org:        strings.TrimSpace(*org),
			JSON:       *jsonOut,
		}, nil
	}

	if raw == "" {
		return pipelineSetOptions{}, errors.New("either --steps or --require-human-review is required")
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return pipelineSetOptions{}, errors.New("--require-human-review must be true or false")
	}
	return pipelineSetOptions{
		ProjectRef:            strings.TrimSpace(*project),
		RequireHumanReview:    value,
		HasRequireHumanReview: true,
		Org:                   strings.TrimSpace(*org),
		JSON:                  *jsonOut,
	}, nil
}

func parsePipelineAddStepOptions(args []string) (pipelineAddStepOptions, error) {
	flags := flag.NewFlagSet("pipeline add-step", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	project := flags.String("project", "", "project name or id (required)")
	name := flags.String("name", "", "step name (required)")
	description := flags.String("description", "", "step description")
	stepType := flags.String("type", "", "step type (agent_work|agent_review|human_review)")
	agent := flags.String("agent", "", "agent id/name/slug")
	position := flags.Int("position", 0, "1-based step position (required)")
	autoAdvance := flags.Bool("auto-advance", true, "auto-advance when step completes")
	org := flags.String("org", "", "org id override")
	jsonOut := flags.Bool("json", false, "JSON output")
	if err := flags.Parse(args); err != nil {
		return pipelineAddStepOptions{}, err
	}
	if strings.TrimSpace(*project) == "" {
		return pipelineAddStepOptions{}, errors.New("--project is required")
	}
	if strings.TrimSpace(*name) == "" {
		return pipelineAddStepOptions{}, errors.New("--name is required")
	}
	normalizedType, err := normalizePipelineStepType(*stepType)
	if err != nil {
		return pipelineAddStepOptions{}, err
	}
	if *position <= 0 {
		return pipelineAddStepOptions{}, errors.New("--position must be a positive integer")
	}
	if len(flags.Args()) != 0 {
		return pipelineAddStepOptions{}, errors.New("usage: otter pipeline add-step --project <project-name-or-id> --name <name> --type <agent_work|agent_review|human_review> --position <n> [--agent <agent>]")
	}

	return pipelineAddStepOptions{
		ProjectRef:  strings.TrimSpace(*project),
		Name:        strings.TrimSpace(*name),
		Description: strings.TrimSpace(*description),
		StepType:    normalizedType,
		AgentRef:    strings.TrimSpace(*agent),
		Position:    *position,
		AutoAdvance: *autoAdvance,
		Org:         strings.TrimSpace(*org),
		JSON:        *jsonOut,
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

func runPipelineShow(client pipelineCommandClient, opts pipelineRolesOptions, out io.Writer) error {
	project, err := client.FindProject(opts.ProjectRef)
	if err != nil {
		return err
	}
	steps, err := client.ListPipelineSteps(project.ID)
	if err != nil {
		return err
	}
	if opts.JSON {
		printJSONTo(out, steps)
		return nil
	}

	fmt.Fprintf(out, "Project: %s\n", project.Name)
	if len(steps) == 0 {
		fmt.Fprintln(out, "No pipeline steps configured.")
		return nil
	}
	for _, step := range steps {
		fmt.Fprintf(
			out,
			"%d. %s [%s] agent=%s auto_advance=%t\n",
			step.StepNumber,
			step.Name,
			step.StepType,
			formatRoleAgentID(step.AssignedAgentID),
			step.AutoAdvance,
		)
	}
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
	if len(opts.StepSpecs) > 0 {
		return runPipelineSetSteps(client, opts, out)
	}
	if !opts.HasRequireHumanReview {
		return errors.New("either --steps or --require-human-review is required")
	}
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

func runPipelineSetSteps(client pipelineCommandClient, opts pipelineSetOptions, out io.Writer) error {
	project, err := client.FindProject(opts.ProjectRef)
	if err != nil {
		return err
	}

	existing, err := client.ListPipelineSteps(project.ID)
	if err != nil {
		return err
	}
	for _, step := range existing {
		if err := client.DeletePipelineStep(project.ID, step.ID); err != nil {
			return err
		}
	}

	created := make([]ottercli.PipelineStep, 0, len(opts.StepSpecs))
	for idx, spec := range opts.StepSpecs {
		var assignedAgentID *string
		if spec.AgentRef != "" {
			agent, resolveErr := client.ResolveAgent(spec.AgentRef)
			if resolveErr != nil {
				return resolveErr
			}
			assignedAgentID = &agent.ID
		}
		step, createErr := client.CreatePipelineStep(project.ID, ottercli.PipelineStepCreateInput{
			StepNumber:      idx + 1,
			Name:            spec.Name,
			Description:     spec.Description,
			AssignedAgentID: assignedAgentID,
			StepType:        spec.StepType,
			AutoAdvance:     spec.AutoAdvance,
		})
		if createErr != nil {
			return createErr
		}
		created = append(created, step)
	}

	if opts.JSON {
		printJSONTo(out, created)
		return nil
	}
	fmt.Fprintf(out, "Set %d pipeline steps for %s\n", len(created), project.Name)
	for _, step := range created {
		fmt.Fprintf(out, "%d. %s [%s]\n", step.StepNumber, step.Name, step.StepType)
	}
	return nil
}

func runPipelineAddStep(client pipelineCommandClient, opts pipelineAddStepOptions, out io.Writer) error {
	project, err := client.FindProject(opts.ProjectRef)
	if err != nil {
		return err
	}
	steps, err := client.ListPipelineSteps(project.ID)
	if err != nil {
		return err
	}
	if opts.Position > len(steps)+1 {
		return fmt.Errorf("--position must be between 1 and %d", len(steps)+1)
	}

	var assignedAgentID *string
	if opts.AgentRef != "" {
		agent, resolveErr := client.ResolveAgent(opts.AgentRef)
		if resolveErr != nil {
			return resolveErr
		}
		assignedAgentID = &agent.ID
	}

	created, err := client.CreatePipelineStep(project.ID, ottercli.PipelineStepCreateInput{
		StepNumber:      len(steps) + 1,
		Name:            opts.Name,
		Description:     opts.Description,
		AssignedAgentID: assignedAgentID,
		StepType:        opts.StepType,
		AutoAdvance:     opts.AutoAdvance,
	})
	if err != nil {
		return err
	}

	orderedIDs := make([]string, 0, len(steps)+1)
	insertIndex := opts.Position - 1
	for idx, step := range steps {
		if idx == insertIndex {
			orderedIDs = append(orderedIDs, created.ID)
		}
		orderedIDs = append(orderedIDs, step.ID)
	}
	if insertIndex >= len(steps) {
		orderedIDs = append(orderedIDs, created.ID)
	}

	reordered, err := client.ReorderPipelineSteps(project.ID, orderedIDs)
	if err != nil {
		return err
	}

	if opts.JSON {
		printJSONTo(out, reordered)
		return nil
	}
	fmt.Fprintf(out, "Added pipeline step %q at position %d for %s\n", opts.Name, opts.Position, project.Name)
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

func normalizePipelineStepType(raw string) (string, error) {
	stepType := strings.ToLower(strings.TrimSpace(raw))
	switch stepType {
	case "agent_work", "agent_review", "human_review":
		return stepType, nil
	default:
		return "", errors.New("--type must be one of agent_work, agent_review, human_review")
	}
}

func parsePipelineSetStepsJSON(raw string) ([]pipelineSetStepSpec, error) {
	type inputStep struct {
		Name            string `json:"name"`
		Description     string `json:"description"`
		Type            string `json:"type"`
		StepType        string `json:"step_type"`
		Agent           string `json:"agent"`
		AgentID         string `json:"agent_id"`
		AssignedAgentID string `json:"assigned_agent_id"`
		AutoAdvance     *bool  `json:"auto_advance"`
	}

	var parsed []inputStep
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, errors.New("--steps must be valid JSON array")
	}
	if len(parsed) == 0 {
		return nil, errors.New("--steps must include at least one step")
	}

	out := make([]pipelineSetStepSpec, 0, len(parsed))
	for idx, step := range parsed {
		name := strings.TrimSpace(step.Name)
		if name == "" {
			return nil, fmt.Errorf("--steps[%d].name is required", idx)
		}
		stepTypeRaw := step.Type
		if strings.TrimSpace(stepTypeRaw) == "" {
			stepTypeRaw = step.StepType
		}
		stepType, err := normalizePipelineStepType(stepTypeRaw)
		if err != nil {
			return nil, fmt.Errorf("--steps[%d].type must be one of agent_work, agent_review, human_review", idx)
		}
		agentRef := strings.TrimSpace(step.Agent)
		if agentRef == "" {
			agentRef = strings.TrimSpace(step.AgentID)
		}
		if agentRef == "" {
			agentRef = strings.TrimSpace(step.AssignedAgentID)
		}

		autoAdvance := true
		if step.AutoAdvance != nil {
			autoAdvance = *step.AutoAdvance
		}
		out = append(out, pipelineSetStepSpec{
			Name:        name,
			Description: strings.TrimSpace(step.Description),
			StepType:    stepType,
			AgentRef:    agentRef,
			AutoAdvance: autoAdvance,
		})
	}

	return out, nil
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
