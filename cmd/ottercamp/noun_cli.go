package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	clitools "github.com/samhotchkiss/otter-camp/internal/cli"
)

type cliAgent struct {
	ID              uuid.UUID  `json:"id"`
	OrganizationID  uuid.UUID  `json:"organization_id"`
	DisplayName     string     `json:"display_name"`
	AgentClass      string     `json:"agent_class"`
	LifecycleStatus string     `json:"lifecycle_status"`
	AgentType       string     `json:"agent_type"`
	CreatedAt       *time.Time `json:"created_at,omitempty"`
}

type cliProject struct {
	ID           uuid.UUID `json:"id"`
	Slug         string    `json:"slug"`
	DisplayName  string    `json:"display_name"`
	Description  string    `json:"description"`
	DeliveryMode string    `json:"delivery_mode"`
	Status       string    `json:"status"`
}

type cliTask struct {
	ID              uuid.UUID  `json:"id"`
	ProjectID       uuid.UUID  `json:"project_id"`
	TaskNumber      int        `json:"task_number"`
	Title           string     `json:"title"`
	Description     *string    `json:"description"`
	WorkStatus      string     `json:"work_status"`
	AssignedAgentID *uuid.UUID `json:"assigned_agent_id"`
}

type cliOrganization struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	Slug       string    `json:"slug"`
	AdminEmail string    `json:"admin_email"`
}

type cliAgentListFilter struct {
	AgentClass      string
	AgentType       string
	LifecycleStatus string
	Limit           int
}

type cliProjectListFilter struct {
	SlugPrefix string
	Limit      int
}

type cliTaskListFilter struct {
	Status string
	Limit  int
}

func runAgentCommand(args []string) int {
	if len(args) == 0 {
		printAgentUsage(os.Stderr)
		return 1
	}

	switch args[0] {
	case "list":
		return runAgentList(args[1:])
	case "create":
		return runAgentCreate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown agent command: %s\n", args[0])
		printAgentUsage(os.Stderr)
		return 1
	}
}

func runProjectCommand(args []string) int {
	if len(args) == 0 {
		printProjectUsage(os.Stderr)
		return 1
	}

	switch args[0] {
	case "list":
		return runProjectList(args[1:])
	case "create":
		return runProjectCreate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown project command: %s\n", args[0])
		printProjectUsage(os.Stderr)
		return 1
	}
}

func runTaskCommand(args []string) int {
	if len(args) == 0 {
		printTaskUsage(os.Stderr)
		return 1
	}

	switch args[0] {
	case "list":
		return runTaskList(args[1:])
	case "create":
		return runTaskCreate(args[1:])
	case "queue":
		return runTaskQueue(args[1:])
	case "cancel":
		return runTaskCancel(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown task command: %s\n", args[0])
		printTaskUsage(os.Stderr)
		return 1
	}
}

func runOrgCommand(args []string) int {
	if len(args) == 0 {
		printOrgUsage(os.Stderr)
		return 1
	}

	switch args[0] {
	case "create":
		return runOrgCreate(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown org command: %s\n", args[0])
		printOrgUsage(os.Stderr)
		return 1
	}
}

func runAgentList(args []string) int {
	flags := flag.NewFlagSet("agent list", flag.ContinueOnError)
	outputModeFlag := flags.String("output", defaultOutputMode, "output mode: table|json|quiet")
	serverURL := flags.String("server-url", "", "server URL override")
	apiKey := flags.String("api-key", "", "API key override")
	agentClass := flags.String("class", "", "agent class filter: staff|temp")
	agentType := flags.String("type", "", "agent type filter: pm|worker|reviewer|general")
	lifecycle := flags.String("lifecycle-status", "", "lifecycle filter")
	limit := flags.Int("limit", 100, "max rows")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "agent list argument error: %v\n", err)
		return 1
	}

	outputMode, err := normalizeOutputMode(*outputModeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent list output error: %v\n", err)
		return 1
	}

	client, err := newCLIAPIClient(*serverURL, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent list setup error: %v\n", err)
		return 1
	}

	items, err := client.listAgents(context.Background(), cliAgentListFilter{
		AgentClass:      strings.ToLower(strings.TrimSpace(*agentClass)),
		AgentType:       strings.ToLower(strings.TrimSpace(*agentType)),
		LifecycleStatus: strings.ToLower(strings.TrimSpace(*lifecycle)),
		Limit:           *limit,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent list failed: %v\n", err)
		return 1
	}

	switch outputMode {
	case clitools.OutputModeJSON:
		return writeJSONEnvelope(map[string]any{"data": items}, "agent list")
	case clitools.OutputModeQuiet:
		for _, item := range items {
			fmt.Fprintln(os.Stdout, item.ID.String())
		}
		return 0
	default:
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tCLASS\tTYPE\tLIFECYCLE")
		for _, item := range items {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", item.ID, item.DisplayName, item.AgentClass, item.AgentType, item.LifecycleStatus)
		}
		_ = tw.Flush()
		return 0
	}
}

func runAgentCreate(args []string) int {
	flags := flag.NewFlagSet("agent create", flag.ContinueOnError)
	name := flags.String("name", "", "agent display name")
	agentClass := flags.String("class", "staff", "agent class: staff|temp")
	agentType := flags.String("type", "general", "agent type: pm|worker|reviewer|general")
	systemPrompt := flags.String("system-prompt", "", "agent system prompt")
	outputModeFlag := flags.String("output", defaultOutputMode, "output mode: table|json|quiet")
	serverURL := flags.String("server-url", "", "server URL override")
	apiKey := flags.String("api-key", "", "API key override")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "agent create argument error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*name) == "" {
		fmt.Fprintln(os.Stderr, "agent create requires --name")
		return 1
	}

	outputMode, err := normalizeOutputMode(*outputModeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent create output error: %v\n", err)
		return 1
	}

	client, err := newCLIAPIClient(*serverURL, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent create setup error: %v\n", err)
		return 1
	}

	created, err := client.createAgent(context.Background(), map[string]any{
		"display_name":  strings.TrimSpace(*name),
		"agent_class":   strings.ToLower(strings.TrimSpace(*agentClass)),
		"agent_type":    strings.ToLower(strings.TrimSpace(*agentType)),
		"system_prompt": strings.TrimSpace(*systemPrompt),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent create failed: %v\n", err)
		return 1
	}

	switch outputMode {
	case clitools.OutputModeJSON:
		return writeJSONEnvelope(map[string]any{"data": created}, "agent create")
	case clitools.OutputModeQuiet:
		fmt.Fprintln(os.Stdout, created.ID.String())
		return 0
	default:
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tCLASS\tTYPE\tLIFECYCLE")
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", created.ID, created.DisplayName, created.AgentClass, created.AgentType, created.LifecycleStatus)
		_ = tw.Flush()
		return 0
	}
}

func runProjectList(args []string) int {
	flags := flag.NewFlagSet("project list", flag.ContinueOnError)
	outputModeFlag := flags.String("output", defaultOutputMode, "output mode: table|json|quiet")
	serverURL := flags.String("server-url", "", "server URL override")
	apiKey := flags.String("api-key", "", "API key override")
	slugPrefix := flags.String("slug-prefix", "", "slug prefix filter")
	limit := flags.Int("limit", 100, "max rows")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "project list argument error: %v\n", err)
		return 1
	}

	outputMode, err := normalizeOutputMode(*outputModeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "project list output error: %v\n", err)
		return 1
	}

	client, err := newCLIAPIClient(*serverURL, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "project list setup error: %v\n", err)
		return 1
	}

	items, err := client.listProjects(context.Background(), cliProjectListFilter{
		SlugPrefix: strings.TrimSpace(*slugPrefix),
		Limit:      *limit,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "project list failed: %v\n", err)
		return 1
	}

	switch outputMode {
	case clitools.OutputModeJSON:
		return writeJSONEnvelope(map[string]any{"data": items}, "project list")
	case clitools.OutputModeQuiet:
		for _, item := range items {
			fmt.Fprintln(os.Stdout, item.ID.String())
		}
		return 0
	default:
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSLUG\tNAME\tMODE\tSTATUS")
		for _, item := range items {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", item.ID, item.Slug, item.DisplayName, item.DeliveryMode, item.Status)
		}
		_ = tw.Flush()
		return 0
	}
}

func runProjectCreate(args []string) int {
	flags := flag.NewFlagSet("project create", flag.ContinueOnError)
	name := flags.String("name", "", "project display name")
	slug := flags.String("slug", "", "project slug")
	description := flags.String("description", "", "project description")
	deliveryMode := flags.String("delivery-mode", "gated", "delivery mode: gated|continuous|scheduled")
	outputModeFlag := flags.String("output", defaultOutputMode, "output mode: table|json|quiet")
	serverURL := flags.String("server-url", "", "server URL override")
	apiKey := flags.String("api-key", "", "API key override")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "project create argument error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*name) == "" {
		fmt.Fprintln(os.Stderr, "project create requires --name")
		return 1
	}

	projectSlug := strings.TrimSpace(*slug)
	if projectSlug == "" {
		projectSlug = slugifyCLIName(*name)
	}

	outputMode, err := normalizeOutputMode(*outputModeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "project create output error: %v\n", err)
		return 1
	}

	client, err := newCLIAPIClient(*serverURL, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "project create setup error: %v\n", err)
		return 1
	}

	created, err := client.createProject(context.Background(), map[string]any{
		"slug":          projectSlug,
		"display_name":  strings.TrimSpace(*name),
		"description":   strings.TrimSpace(*description),
		"delivery_mode": strings.ToLower(strings.TrimSpace(*deliveryMode)),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "project create failed: %v\n", err)
		return 1
	}

	switch outputMode {
	case clitools.OutputModeJSON:
		return writeJSONEnvelope(map[string]any{"data": created}, "project create")
	case clitools.OutputModeQuiet:
		fmt.Fprintln(os.Stdout, created.ID.String())
		return 0
	default:
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSLUG\tNAME\tMODE\tSTATUS")
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", created.ID, created.Slug, created.DisplayName, created.DeliveryMode, created.Status)
		_ = tw.Flush()
		return 0
	}
}

func runTaskList(args []string) int {
	flags := flag.NewFlagSet("task list", flag.ContinueOnError)
	projectIDFlag := flags.String("project-id", "", "project id")
	projectSlug := flags.String("project", "", "project slug")
	status := flags.String("status", "", "status filter")
	limit := flags.Int("limit", 100, "max rows")
	outputModeFlag := flags.String("output", defaultOutputMode, "output mode: table|json|quiet")
	serverURL := flags.String("server-url", "", "server URL override")
	apiKey := flags.String("api-key", "", "API key override")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "task list argument error: %v\n", err)
		return 1
	}

	projectIDRaw := strings.TrimSpace(*projectIDFlag)
	if projectIDRaw == "" && strings.TrimSpace(*projectSlug) == "" {
		fmt.Fprintln(os.Stderr, "task list requires --project-id or --project")
		return 1
	}

	outputMode, err := normalizeOutputMode(*outputModeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "task list output error: %v\n", err)
		return 1
	}

	client, err := newCLIAPIClient(*serverURL, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "task list setup error: %v\n", err)
		return 1
	}

	projectID, err := resolveProjectIDForTaskCommands(context.Background(), client, projectIDRaw, strings.TrimSpace(*projectSlug))
	if err != nil {
		fmt.Fprintf(os.Stderr, "task list project error: %v\n", err)
		return 1
	}

	items, err := client.listTasks(context.Background(), projectID, cliTaskListFilter{
		Status: strings.TrimSpace(*status),
		Limit:  *limit,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "task list failed: %v\n", err)
		return 1
	}

	switch outputMode {
	case clitools.OutputModeJSON:
		return writeJSONEnvelope(map[string]any{"data": items}, "task list")
	case clitools.OutputModeQuiet:
		for _, item := range items {
			fmt.Fprintln(os.Stdout, item.ID.String())
		}
		return 0
	default:
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNUMBER\tTITLE\tSTATUS\tASSIGNEE")
		for _, item := range items {
			assignee := ""
			if item.AssignedAgentID != nil {
				assignee = item.AssignedAgentID.String()
			}
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", item.ID, item.TaskNumber, item.Title, item.WorkStatus, assignee)
		}
		_ = tw.Flush()
		return 0
	}
}

func runTaskCreate(args []string) int {
	flags := flag.NewFlagSet("task create", flag.ContinueOnError)
	projectIDFlag := flags.String("project-id", "", "project id")
	projectSlug := flags.String("project", "", "project slug")
	title := flags.String("title", "", "task title")
	description := flags.String("description", "", "task description")
	queue := flags.Bool("queue", false, "queue task immediately after create")
	outputModeFlag := flags.String("output", defaultOutputMode, "output mode: table|json|quiet")
	serverURL := flags.String("server-url", "", "server URL override")
	apiKey := flags.String("api-key", "", "API key override")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "task create argument error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*title) == "" {
		fmt.Fprintln(os.Stderr, "task create requires --title")
		return 1
	}

	projectIDRaw := strings.TrimSpace(*projectIDFlag)
	if projectIDRaw == "" && strings.TrimSpace(*projectSlug) == "" {
		fmt.Fprintln(os.Stderr, "task create requires --project-id or --project")
		return 1
	}

	outputMode, err := normalizeOutputMode(*outputModeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "task create output error: %v\n", err)
		return 1
	}

	client, err := newCLIAPIClient(*serverURL, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "task create setup error: %v\n", err)
		return 1
	}

	projectID, err := resolveProjectIDForTaskCommands(context.Background(), client, projectIDRaw, strings.TrimSpace(*projectSlug))
	if err != nil {
		fmt.Fprintf(os.Stderr, "task create project error: %v\n", err)
		return 1
	}

	payload := map[string]any{"title": strings.TrimSpace(*title)}
	if strings.TrimSpace(*description) != "" {
		payload["description"] = strings.TrimSpace(*description)
	}

	created, err := client.createTask(context.Background(), projectID, payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "task create failed: %v\n", err)
		return 1
	}
	if *queue {
		queued, queueErr := client.transitionTask(context.Background(), created.ID.String(), "queue")
		if queueErr != nil {
			fmt.Fprintf(os.Stderr, "task queue failed after create: %v\n", queueErr)
			return 1
		}
		created = queued
	}

	switch outputMode {
	case clitools.OutputModeJSON:
		return writeJSONEnvelope(map[string]any{"data": created}, "task create")
	case clitools.OutputModeQuiet:
		fmt.Fprintln(os.Stdout, created.ID.String())
		return 0
	default:
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNUMBER\tTITLE\tSTATUS")
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", created.ID, created.TaskNumber, created.Title, created.WorkStatus)
		_ = tw.Flush()
		return 0
	}
}

func runTaskQueue(args []string) int {
	return runTaskTransition(args, "queue")
}

func runTaskCancel(args []string) int {
	return runTaskTransition(args, "cancel")
}

func runTaskTransition(args []string, action string) int {
	commandName := "task " + action
	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	outputModeFlag := flags.String("output", defaultOutputMode, "output mode: table|json|quiet")
	serverURL := flags.String("server-url", "", "server URL override")
	apiKey := flags.String("api-key", "", "API key override")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "%s argument error: %v\n", commandName, err)
		return 1
	}
	if flags.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "%s requires <task-id>\n", commandName)
		return 1
	}
	if _, err := uuid.Parse(strings.TrimSpace(flags.Arg(0))); err != nil {
		fmt.Fprintf(os.Stderr, "%s requires a valid task id\n", commandName)
		return 1
	}

	outputMode, err := normalizeOutputMode(*outputModeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s output error: %v\n", commandName, err)
		return 1
	}

	client, err := newCLIAPIClient(*serverURL, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s setup error: %v\n", commandName, err)
		return 1
	}

	updated, err := client.transitionTask(context.Background(), strings.TrimSpace(flags.Arg(0)), action)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", commandName, err)
		return 1
	}

	switch outputMode {
	case clitools.OutputModeJSON:
		return writeJSONEnvelope(map[string]any{"data": updated}, commandName)
	case clitools.OutputModeQuiet:
		fmt.Fprintln(os.Stdout, updated.ID.String())
		return 0
	default:
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNUMBER\tTITLE\tSTATUS")
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", updated.ID, updated.TaskNumber, updated.Title, updated.WorkStatus)
		_ = tw.Flush()
		return 0
	}
}

func runOrgCreate(args []string) int {
	flags := flag.NewFlagSet("org create", flag.ContinueOnError)
	name := flags.String("name", "", "organization name")
	slug := flags.String("slug", "", "organization slug")
	adminEmail := flags.String("admin-email", "", "admin email")
	adminPassword := flags.String("admin-password", "", "admin password")
	adminName := flags.String("admin-name", "", "admin display name")
	outputModeFlag := flags.String("output", defaultOutputMode, "output mode: table|json|quiet")
	serverURL := flags.String("server-url", "", "server URL override")
	apiKey := flags.String("api-key", "", "API key override")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "org create argument error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*name) == "" {
		fmt.Fprintln(os.Stderr, "org create requires --name")
		return 1
	}

	orgSlug := strings.TrimSpace(*slug)
	if orgSlug == "" {
		orgSlug = slugifyCLIName(*name)
	}

	outputMode, err := normalizeOutputMode(*outputModeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "org create output error: %v\n", err)
		return 1
	}

	client, err := newCLIAPIClient(*serverURL, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "org create setup error: %v\n", err)
		return 1
	}

	payload := map[string]any{
		"name": strings.TrimSpace(*name),
		"slug": orgSlug,
	}
	if strings.TrimSpace(*adminEmail) != "" {
		payload["admin_email"] = strings.TrimSpace(*adminEmail)
	}
	if strings.TrimSpace(*adminPassword) != "" {
		payload["admin_password"] = strings.TrimSpace(*adminPassword)
	}
	if strings.TrimSpace(*adminName) != "" {
		payload["admin_name"] = strings.TrimSpace(*adminName)
	}

	created, err := client.createOrganization(context.Background(), payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "org create failed: %v\n", err)
		return 1
	}

	switch outputMode {
	case clitools.OutputModeJSON:
		return writeJSONEnvelope(map[string]any{"data": created}, "org create")
	case clitools.OutputModeQuiet:
		fmt.Fprintln(os.Stdout, created.ID.String())
		return 0
	default:
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSLUG\tNAME\tADMIN_EMAIL")
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", created.ID, created.Slug, created.Name, created.AdminEmail)
		_ = tw.Flush()
		return 0
	}
}

func resolveProjectIDForTaskCommands(ctx context.Context, client *cliAPIClient, projectIDRaw, projectSlug string) (string, error) {
	if strings.TrimSpace(projectIDRaw) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(projectIDRaw))
		if err != nil {
			return "", fmt.Errorf("invalid project id")
		}
		return parsed.String(), nil
	}
	return client.lookupProjectID(ctx, strings.TrimSpace(projectSlug))
}

func (c *cliAPIClient) listAgents(ctx context.Context, filter cliAgentListFilter) ([]cliAgent, error) {
	query := url.Values{}
	if strings.TrimSpace(filter.AgentClass) != "" {
		query.Set("agent_class", strings.TrimSpace(filter.AgentClass))
	}
	if strings.TrimSpace(filter.AgentType) != "" {
		query.Set("agent_type", strings.TrimSpace(filter.AgentType))
	}
	if strings.TrimSpace(filter.LifecycleStatus) != "" {
		query.Set("lifecycle_status", strings.TrimSpace(filter.LifecycleStatus))
	}
	if filter.Limit > 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}

	path := "/v1/agents"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var resp struct {
		Data []cliAgent `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *cliAPIClient) createAgent(ctx context.Context, payload map[string]any) (*cliAgent, error) {
	var resp struct {
		Data cliAgent `json:"data"`
	}
	if err := c.request(ctx, http.MethodPost, "/v1/agents", payload, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (c *cliAPIClient) listProjects(ctx context.Context, filter cliProjectListFilter) ([]cliProject, error) {
	query := url.Values{}
	if strings.TrimSpace(filter.SlugPrefix) != "" {
		query.Set("slug_prefix", strings.TrimSpace(filter.SlugPrefix))
	}
	if filter.Limit > 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}

	path := "/v1/projects"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var resp struct {
		Data []cliProject `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *cliAPIClient) createProject(ctx context.Context, payload map[string]any) (*cliProject, error) {
	var resp struct {
		Data cliProject `json:"data"`
	}
	if err := c.request(ctx, http.MethodPost, "/v1/projects", payload, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (c *cliAPIClient) listTasks(ctx context.Context, projectID string, filter cliTaskListFilter) ([]cliTask, error) {
	query := url.Values{}
	if strings.TrimSpace(filter.Status) != "" {
		query.Set("status", strings.TrimSpace(filter.Status))
	}
	if filter.Limit > 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := fmt.Sprintf("/v1/projects/%s/tasks", url.PathEscape(strings.TrimSpace(projectID)))
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var resp struct {
		Data []cliTask `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *cliAPIClient) createTask(ctx context.Context, projectID string, payload map[string]any) (*cliTask, error) {
	path := fmt.Sprintf("/v1/projects/%s/tasks", url.PathEscape(strings.TrimSpace(projectID)))
	var resp struct {
		Data cliTask `json:"data"`
	}
	if err := c.request(ctx, http.MethodPost, path, payload, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (c *cliAPIClient) transitionTask(ctx context.Context, taskID, action string) (*cliTask, error) {
	path := fmt.Sprintf("/v1/tasks/%s/%s", url.PathEscape(strings.TrimSpace(taskID)), url.PathEscape(strings.TrimSpace(action)))
	var resp struct {
		Data cliTask `json:"data"`
	}
	if err := c.request(ctx, http.MethodPost, path, map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (c *cliAPIClient) createOrganization(ctx context.Context, payload map[string]any) (*cliOrganization, error) {
	var resp struct {
		Data cliOrganization `json:"data"`
	}
	if err := c.request(ctx, http.MethodPost, "/v1/orgs", payload, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func printAgentUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp agent <list|create> [flags]")
}

func printProjectUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp project <list|create> [flags]")
}

func printTaskUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp task <list|create|queue|cancel> [flags]")
}

func printOrgUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp org create --name <name> [flags]")
}

func normalizeOutputMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		mode = defaultOutputMode
	}
	switch mode {
	case clitools.OutputModeTable, clitools.OutputModeJSON, clitools.OutputModeQuiet:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported output mode %q", raw)
	}
}

func writeJSONEnvelope(payload any, commandName string) int {
	formatter, err := clitools.NewOutputFormatter(clitools.OutputModeJSON, os.Stdout, defaultNoColor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s output error: %v\n", commandName, err)
		return 1
	}
	if err := formatter.WriteJSON(payload); err != nil {
		fmt.Fprintf(os.Stderr, "%s output error: %v\n", commandName, err)
		return 1
	}
	return 0
}

var nonSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func slugifyCLIName(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = nonSlugPattern.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "untitled"
	}
	return slug
}
