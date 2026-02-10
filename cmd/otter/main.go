package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

var issueUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)
var chameleonSessionKeyPattern = regexp.MustCompile(
	`^agent:chameleon:oc:([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`,
)

const (
	authSetupCommand = "otter auth login --token <your-token> --org <org-id>"
	authTokenHelpURL = "https://otter.camp/settings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "auth":
		handleAuth(os.Args[2:])
	case "whoami":
		handleWhoami(os.Args[2:])
	case "release-gate":
		handleReleaseGate(os.Args[2:])
	case "project":
		handleProject(os.Args[2:])
	case "agent":
		handleAgent(os.Args[2:])
	case "memory":
		handleMemory(os.Args[2:])
	case "clone":
		handleClone(os.Args[2:])
	case "remote":
		handleRemote(os.Args[2:])
	case "repo":
		handleRepo(os.Args[2:])
	case "issue":
		handleIssue(os.Args[2:])
	case "version":
		fmt.Println("otter dev")
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`otter <command> [args]

Commands:
  auth login       Store API token + default org
  whoami           Validate token and show user
  release-gate     Run Spec 110 release gate checks
  project          Manage projects
  agent            Manage agents
  memory           Manage agent memory
  clone            Clone a project repo
  remote add       Add origin remote for project
  repo info        Show repo URL for project
  issue            Manage project issues
  version          Show CLI version`)
}

func handleAuth(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: otter auth login [--token <token>] [--org <org-id>] [--api <url>]")
		os.Exit(1)
	}

	switch args[0] {
	case "login":
		flags := flag.NewFlagSet("auth login", flag.ExitOnError)
		token := flags.String("token", "", "session token (oc_sess_*)")
		org := flags.String("org", "", "default org id")
		api := flags.String("api", "", "API base URL")
		_ = flags.Parse(args[1:])

		cfg, err := ottercli.LoadConfig()
		dieIf(err)

		if *api != "" {
			cfg.APIBaseURL = *api
		}
		if *token == "" {
			*token = prompt("Token: ")
		}
		if *org == "" {
			*org = prompt("Default org id (uuid): ")
		}
		if strings.TrimSpace(*token) == "" {
			die("token is required")
		}
		cfg.Token = strings.TrimSpace(*token)
		if strings.TrimSpace(*org) != "" {
			cfg.DefaultOrg = strings.TrimSpace(*org)
		}
		if err := ottercli.SaveConfig(cfg); err != nil {
			dieIf(err)
		}
		fmt.Println("Saved config to", mustConfigPath())
	default:
		fmt.Println("usage: otter auth login [--token <token>] [--org <org-id>] [--api <url>]")
		os.Exit(1)
	}
}

func handleWhoami(args []string) {
	flags := flag.NewFlagSet("whoami", flag.ExitOnError)
	jsonOut := flags.Bool("json", false, "JSON output")
	_ = flags.Parse(args)

	cfg, err := ottercli.LoadConfig()
	dieIf(err)
	client, _ := ottercli.NewClient(cfg, "")
	resp, err := client.WhoAmI()
	dieIf(err)

	if *jsonOut {
		payload, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(payload))
		return
	}

	fmt.Printf("User: %s (%s)\n", resp.User.Name, resp.User.Email)
	if cfg.DefaultOrg != "" {
		fmt.Printf("Default org: %s\n", cfg.DefaultOrg)
	}
}

func handleReleaseGate(args []string) {
	flags := flag.NewFlagSet("release-gate", flag.ExitOnError)
	org := flags.String("org", "", "org id override")
	jsonOut := flags.Bool("json", false, "JSON output")
	_ = flags.Parse(args)

	cfg, err := ottercli.LoadConfig()
	dieIf(err)
	client, _ := ottercli.NewClient(cfg, *org)
	payload, statusCode, requestErr := client.RunReleaseGate()
	if payload == nil {
		dieIf(requestErr)
		return
	}

	if *jsonOut {
		printJSON(payload)
		if requestErr != nil || !releaseGatePayloadOK(payload) || statusCode >= http.StatusBadRequest {
			os.Exit(1)
		}
		return
	}

	fmt.Println("Spec 110 Release Gate")
	checkList, _ := payload["checks"].([]interface{})
	for _, item := range checkList {
		check, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		category := strings.TrimSpace(toString(check["category"]))
		status := strings.ToUpper(strings.TrimSpace(toString(check["status"])))
		message := strings.TrimSpace(toString(check["message"]))
		if category == "" {
			category = "unknown"
		}
		if status == "" {
			status = "UNKNOWN"
		}
		if message == "" {
			fmt.Printf("[%s] %s\n", status, category)
			continue
		}
		fmt.Printf("[%s] %s - %s\n", status, category, message)
	}

	passed := releaseGatePayloadOK(payload)
	if requestErr == nil && statusCode < http.StatusBadRequest && passed {
		fmt.Println("Gate result: PASS")
		return
	}
	fmt.Println("Gate result: FAIL")
	if requestErr != nil {
		fmt.Fprintln(os.Stderr, formatCLIError(requestErr))
	}
	os.Exit(1)
}

func handleAgent(args []string) {
	const usageText = "usage: otter agent <whoami|list|create|edit|archive> ..."
	if len(args) == 0 {
		fmt.Println(usageText)
		os.Exit(1)
	}

	switch args[0] {
	case "whoami":
		flags := flag.NewFlagSet("agent whoami", flag.ExitOnError)
		sessionKey := flags.String("session", "", "canonical session key (agent:chameleon:oc:{uuid})")
		profile := flags.String("profile", "compact", "profile (compact|full)")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])

		agentID, err := parseChameleonSessionAgentID(*sessionKey)
		if err != nil {
			die(err.Error())
		}
		normalizedProfile := strings.ToLower(strings.TrimSpace(*profile))
		if normalizedProfile != "compact" && normalizedProfile != "full" {
			die("--profile must be compact or full")
		}

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		payload, err := client.AgentWhoAmI(agentID, strings.TrimSpace(*sessionKey), normalizedProfile)
		dieIf(err)

		if *jsonOut {
			printJSON(payload)
			return
		}
		profileValue, _ := payload["profile"].(string)
		fmt.Printf("Agent ID: %s\n", agentID)
		if profileValue != "" {
			fmt.Printf("Profile: %s\n", profileValue)
		}
		agent, _ := payload["agent"].(map[string]interface{})
		if len(agent) > 0 {
			if name := strings.TrimSpace(toString(agent["name"])); name != "" {
				fmt.Printf("Name: %s\n", name)
			}
			if role := strings.TrimSpace(toString(agent["role"])); role != "" {
				fmt.Printf("Role: %s\n", role)
			}
		}
	case "list":
		flags := flag.NewFlagSet("agent list", flag.ExitOnError)
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		agents, err := client.ListAgents()
		dieIf(err)

		if *jsonOut {
			printJSON(map[string]interface{}{
				"agents": agents,
				"total":  len(agents),
			})
			return
		}
		if len(agents) == 0 {
			fmt.Println("No agents found.")
			return
		}
		for _, agent := range agents {
			label := strings.TrimSpace(agent.Name)
			if label == "" {
				label = agent.ID
			}
			if strings.TrimSpace(agent.Slug) != "" {
				fmt.Printf("%s (%s)\n", label, agent.Slug)
			} else {
				fmt.Println(label)
			}
		}
	case "create":
		flags := flag.NewFlagSet("agent create", flag.ExitOnError)
		role := flags.String("role", "", "agent role label")
		slot := flags.String("slot", "", "agent slot override")
		model := flags.String("model", "gpt-5.2-codex", "OpenClaw model")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("usage: otter agent create \"Name\" [--role <role>] [--slot <slot>] [--model <model>]")
		}
		displayName := strings.TrimSpace(strings.Join(flags.Args(), " "))
		if displayName == "" {
			die("agent name is required")
		}
		normalizedSlot := strings.TrimSpace(*slot)
		if normalizedSlot == "" {
			normalizedSlot = slugifyAgentName(displayName)
		}

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		payload := buildAgentCreatePayload(displayName, normalizedSlot, strings.TrimSpace(*model), strings.TrimSpace(*role))
		response, err := client.CreateAgent(payload)
		dieIf(err)

		if *jsonOut {
			printJSON(response)
			return
		}
		fmt.Printf("Created agent slot: %s\n", normalizedSlot)
		fmt.Printf("Display name: %s\n", displayName)
		if roleValue := strings.TrimSpace(*role); roleValue != "" {
			fmt.Printf("Role requested: %s\n", roleValue)
		}
	case "edit":
		flags := flag.NewFlagSet("agent edit", flag.ExitOnError)
		name := flags.String("name", "", "new display name")
		role := flags.String("role", "", "new role")
		emoji := flags.String("emoji", "", "new emoji or icon token")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("usage: otter agent edit <agent-id> [--name <name>] [--role <role>] [--emoji <emoji>]")
		}
		agentID := strings.TrimSpace(flags.Args()[0])
		if agentID == "" {
			die("agent id is required")
		}

		patch := map[string]interface{}{}
		if trimmed := strings.TrimSpace(*name); trimmed != "" {
			patch["display_name"] = trimmed
		}
		if trimmed := strings.TrimSpace(*role); trimmed != "" {
			patch["role"] = trimmed
		}
		if trimmed := strings.TrimSpace(*emoji); trimmed != "" {
			patch["emoji"] = trimmed
		}
		if len(patch) == 0 {
			die("no changes provided; pass at least one of --name, --role, --emoji")
		}

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		response, err := client.UpdateAgent(agentID, patch)
		dieIf(err)

		if *jsonOut {
			printJSON(response)
			return
		}
		fmt.Printf("Updated agent: %s\n", agentID)
	case "archive":
		flags := flag.NewFlagSet("agent archive", flag.ExitOnError)
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("usage: otter agent archive <agent-id>")
		}
		agentID := strings.TrimSpace(flags.Args()[0])
		if agentID == "" {
			die("agent id is required")
		}

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		dieIf(client.ArchiveAgent(agentID))

		if *jsonOut {
			printJSON(map[string]interface{}{
				"ok":       true,
				"agent_id": agentID,
			})
			return
		}
		fmt.Printf("Archived agent: %s\n", agentID)
	default:
		fmt.Println(usageText)
		os.Exit(1)
	}
}

func handleMemory(args []string) {
	const usageText = "usage: otter memory <write|read|search> ..."
	if len(args) == 0 {
		fmt.Println(usageText)
		os.Exit(1)
	}

	switch args[0] {
	case "write":
		flags := flag.NewFlagSet("memory write", flag.ExitOnError)
		agentID := flags.String("agent", "", "agent UUID")
		daily := flags.Bool("daily", false, "write daily memory entry")
		kind := flags.String("kind", "", "memory kind override (daily|long_term|note)")
		date := flags.String("date", "", "entry date in YYYY-MM-DD (optional)")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])

		if len(flags.Args()) == 0 {
			die("usage: otter memory write --agent <uuid> [--daily] [--kind <kind>] [--date YYYY-MM-DD] \"content\"")
		}
		normalizedAgentID := strings.TrimSpace(*agentID)
		if err := validateAgentUUID(normalizedAgentID); err != nil {
			die(err.Error())
		}
		selectedKind, err := resolveMemoryWriteKind(*daily, *kind)
		if err != nil {
			die(err.Error())
		}
		content := strings.TrimSpace(strings.Join(flags.Args(), " "))
		if content == "" {
			die("memory content is required")
		}

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		payload := map[string]any{
			"kind":    selectedKind,
			"content": content,
		}
		if trimmedDate := strings.TrimSpace(*date); trimmedDate != "" {
			payload["date"] = trimmedDate
		}
		response, err := client.WriteAgentMemory(normalizedAgentID, payload)
		dieIf(err)

		if *jsonOut {
			printJSON(response)
			return
		}
		fmt.Printf("Wrote %s memory for agent %s\n", selectedKind, normalizedAgentID)
	case "read":
		flags := flag.NewFlagSet("memory read", flag.ExitOnError)
		agentID := flags.String("agent", "", "agent UUID")
		days := flags.Int("days", 2, "number of days of daily memory")
		includeLongTerm := flags.Bool("include-long-term", true, "include long-term memory entries")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])

		normalizedAgentID := strings.TrimSpace(*agentID)
		if err := validateAgentUUID(normalizedAgentID); err != nil {
			die(err.Error())
		}
		if *days <= 0 {
			die("--days must be positive")
		}

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		response, err := client.ReadAgentMemory(normalizedAgentID, *days, *includeLongTerm)
		dieIf(err)

		if *jsonOut {
			printJSON(response)
			return
		}
		fmt.Printf("Loaded memory for agent %s\n", normalizedAgentID)
		printJSON(response)
	case "search":
		flags := flag.NewFlagSet("memory search", flag.ExitOnError)
		agentID := flags.String("agent", "", "agent UUID")
		limit := flags.Int("limit", 20, "max results")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])

		if len(flags.Args()) == 0 {
			die("usage: otter memory search --agent <uuid> [--limit N] \"query\"")
		}
		normalizedAgentID := strings.TrimSpace(*agentID)
		if err := validateAgentUUID(normalizedAgentID); err != nil {
			die(err.Error())
		}
		query := strings.TrimSpace(strings.Join(flags.Args(), " "))
		if query == "" {
			die("query is required")
		}
		if *limit <= 0 {
			die("--limit must be positive")
		}

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		response, err := client.SearchAgentMemory(normalizedAgentID, query, *limit)
		dieIf(err)

		if *jsonOut {
			printJSON(response)
			return
		}
		fmt.Printf("Memory search results for %s\n", normalizedAgentID)
		printJSON(response)
	default:
		fmt.Println(usageText)
		os.Exit(1)
	}
}

func handleProject(args []string) {
	const projectUsage = "usage: otter project <list|create|view|archive|delete> ..."
	if len(args) == 0 {
		fmt.Println(projectUsage)
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("project list", flag.ExitOnError)
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)

		projects, err := client.ListProjects()
		dieIf(err)

		if *jsonOut {
			out, _ := json.MarshalIndent(projects, "", "  ")
			fmt.Println(string(out))
			return
		}

		if len(projects) == 0 {
			fmt.Println("No projects found.")
			return
		}
		for _, p := range projects {
			slug := p.Slug()
			if slug != "" {
				fmt.Printf("%-30s  %s\n", p.Name, slug)
			} else {
				fmt.Println(p.Name)
			}
		}
	case "create":
		flags := flag.NewFlagSet("project create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		slug := flags.String("slug", "", "custom project slug")
		description := flags.String("description", "", "project description")
		status := flags.String("status", "", "status (active|archived|completed)")
		repoURL := flags.String("repo-url", "", "repo URL")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		flagArgs, nameArgs, err := splitProjectCreateArgs(args[1:])
		if err != nil {
			die(err.Error())
		}
		if err := flags.Parse(flagArgs); err != nil {
			die(err.Error())
		}

		if len(nameArgs) == 0 {
			die("project name is required")
		}
		name := strings.Join(nameArgs, " ")

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)

		payload := map[string]interface{}{"name": name}
		if strings.TrimSpace(*slug) != "" {
			payload["slug"] = *slug
		}
		if strings.TrimSpace(*description) != "" {
			payload["description"] = *description
		}
		if strings.TrimSpace(*status) != "" {
			payload["status"] = *status
		}
		if strings.TrimSpace(*repoURL) != "" {
			payload["repo_url"] = *repoURL
		}

		project, err := client.CreateProject(payload)
		dieIf(err)

		if *jsonOut {
			out, _ := json.MarshalIndent(project, "", "  ")
			fmt.Println(string(out))
			return
		}

		fmt.Printf("Created project: %s\n", project.Name)
		if project.Slug() != "" {
			fmt.Printf("Slug: %s\n", project.Slug())
		}
		if project.RepoURL != "" {
			fmt.Printf("Repo: %s\n", project.RepoURL)
		}
	case "view":
		flags := flag.NewFlagSet("project view", flag.ExitOnError)
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("usage: otter project view <project-name-or-id>")
		}
		query := strings.Join(flags.Args(), " ")

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)

		project, err := client.FindProject(query)
		dieIf(err)

		if *jsonOut {
			printJSON(project)
			return
		}
		fmt.Printf("Project: %s\n", project.Name)
		fmt.Printf("ID: %s\n", project.ID)
		fmt.Printf("Status: %s\n", project.Status)
		if strings.TrimSpace(project.Description) != "" {
			fmt.Printf("Description: %s\n", strings.TrimSpace(project.Description))
		}
		if strings.TrimSpace(project.RepoURL) != "" {
			fmt.Printf("Repo: %s\n", project.RepoURL)
		}
	case "archive":
		flags := flag.NewFlagSet("project archive", flag.ExitOnError)
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("usage: otter project archive <project-name-or-id>")
		}
		query := strings.Join(flags.Args(), " ")

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)

		project, err := client.FindProject(query)
		dieIf(err)
		updated, err := client.PatchProject(project.ID, map[string]interface{}{"status": "archived"})
		dieIf(err)

		if *jsonOut {
			printJSON(updated)
			return
		}
		fmt.Printf("Archived project: %s\n", updated.Name)
	case "delete":
		flags := flag.NewFlagSet("project delete", flag.ExitOnError)
		yes := flags.Bool("yes", false, "confirm deletion")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("usage: otter project delete <project-name-or-id> [--yes]")
		}
		if !*yes {
			die("project delete requires --yes")
		}
		query := strings.Join(flags.Args(), " ")

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)

		project, err := client.FindProject(query)
		dieIf(err)
		dieIf(client.DeleteProject(project.ID))

		if *jsonOut {
			printJSON(map[string]interface{}{
				"ok": true,
				"data": map[string]string{
					"id":   project.ID,
					"name": project.Name,
				},
				"errors": []interface{}{},
			})
			return
		}
		fmt.Printf("Deleted project: %s\n", project.Name)
	default:
		fmt.Println(projectUsage)
		os.Exit(1)
	}
}

func splitProjectCreateArgs(args []string) ([]string, []string, error) {
	flagsWithValue := map[string]struct{}{
		"--slug":        {},
		"--description": {},
		"--status":      {},
		"--repo-url":    {},
		"--org":         {},
	}
	flagArgs := make([]string, 0, len(args))
	nameArgs := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "--" {
			nameArgs = append(nameArgs, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(token, "--") {
			nameArgs = append(nameArgs, token)
			continue
		}
		if strings.Contains(token, "=") {
			flagArgs = append(flagArgs, token)
			continue
		}
		if token == "--json" {
			flagArgs = append(flagArgs, token)
			continue
		}
		if _, ok := flagsWithValue[token]; ok {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("flag needs value: %s", token)
			}
			flagArgs = append(flagArgs, token, args[i+1])
			i++
			continue
		}
		flagArgs = append(flagArgs, token)
	}

	return flagArgs, nameArgs, nil
}

func handleClone(args []string) {
	flags := flag.NewFlagSet("clone", flag.ExitOnError)
	pathFlag := flags.String("path", "", "target path")
	org := flags.String("org", "", "org id override")
	jsonOut := flags.Bool("json", false, "JSON output")
	_ = flags.Parse(args)

	if len(flags.Args()) == 0 {
		die("project name or id required")
	}
	query := strings.Join(flags.Args(), " ")

	cfg, err := ottercli.LoadConfig()
	dieIf(err)
	client, _ := ottercli.NewClient(cfg, *org)
	project, err := client.FindProject(query)
	dieIf(err)

	repoURL := strings.TrimSpace(project.RepoURL)
	if repoURL == "" {
		repoURL = deriveManagedRepoURL(client.BaseURL, project.OrgID, project.ID)
	}
	if repoURL == "" {
		die("project has no repo_url; set one first")
	}

	target := *pathFlag
	if target == "" {
		root := filepath.Join(userHomeDir(), "Documents", "OtterCamp")
		target = filepath.Join(root, project.Slug())
	}

	cmd := exec.Command("git", "clone", repoURL, target)
	if !*jsonOut {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		if *jsonOut {
			printJSON(map[string]interface{}{
				"ok":     false,
				"data":   nil,
				"errors": []map[string]string{{"code": "CLONE_FAILED", "message": err.Error()}},
			})
		}
		die(err.Error())
	}

	if *jsonOut {
		printJSON(map[string]interface{}{
			"ok": true,
			"data": map[string]string{
				"project":  project.Name,
				"slug":     project.Slug(),
				"repo_url": repoURL,
				"path":     target,
			},
			"errors": []interface{}{},
		})
	} else {
		fmt.Printf("Cloned %s into %s\n", project.Name, target)
	}
}

func handleRemote(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: otter remote add <project> [--force]")
		os.Exit(1)
	}

	switch args[0] {
	case "add":
		flags := flag.NewFlagSet("remote add", flag.ExitOnError)
		force := flags.Bool("force", false, "overwrite origin if exists")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("project name or id required")
		}
		query := strings.Join(flags.Args(), " ")

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		project, err := client.FindProject(query)
		dieIf(err)

		if strings.TrimSpace(project.RepoURL) == "" {
			die("project has no repo_url; set one first")
		}

		repoRoot, err := gitRepoRoot()
		dieIf(err)

		existing, _ := gitRemoteURL(repoRoot, "origin")
		if existing != "" && !*force {
			die("origin already set; re-run with --force to overwrite")
		}

		action := "added"
		if existing != "" {
			dieIf(exec.Command("git", "-C", repoRoot, "remote", "set-url", "origin", project.RepoURL).Run())
			action = "updated"
		} else {
			dieIf(exec.Command("git", "-C", repoRoot, "remote", "add", "origin", project.RepoURL).Run())
		}

		if *jsonOut {
			payload, _ := json.MarshalIndent(map[string]string{
				"action":   action,
				"remote":   "origin",
				"repo_url": project.RepoURL,
				"project":  project.Name,
			}, "", "  ")
			fmt.Println(string(payload))
			return
		}
		if action == "updated" {
			fmt.Println("Updated origin to", project.RepoURL)
		} else {
			fmt.Println("Added origin", project.RepoURL)
		}
	default:
		fmt.Println("usage: otter remote add <project> [--force]")
		os.Exit(1)
	}
}

func handleRepo(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: otter repo info <project>")
		os.Exit(1)
	}

	switch args[0] {
	case "info":
		flags := flag.NewFlagSet("repo info", flag.ExitOnError)
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("project name or id required")
		}
		query := strings.Join(flags.Args(), " ")

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		project, err := client.FindProject(query)
		dieIf(err)

		if *jsonOut {
			payload, _ := json.MarshalIndent(project, "", "  ")
			fmt.Println(string(payload))
			return
		}
		fmt.Printf("Project: %s\n", project.Name)
		fmt.Printf("ID: %s\n", project.ID)
		if project.RepoURL != "" {
			fmt.Printf("Repo: %s\n", project.RepoURL)
		}
	default:
		fmt.Println("usage: otter repo info <project>")
		os.Exit(1)
	}
}

func handleIssue(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: otter issue <create|list|view|comment|ask|respond|assign|close|reopen> ...")
		os.Exit(1)
	}

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("issue create", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required)")
		body := flags.String("body", "", "issue body")
		assign := flags.String("assign", "", "owner agent id/name/slug")
		priority := flags.String("priority", "", "priority (P0-P3)")
		workStatus := flags.String("work-status", "", "work status")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])

		titleArgs := flags.Args()
		if len(titleArgs) == 0 {
			die("issue title is required")
		}
		title := strings.Join(titleArgs, " ")
		if strings.TrimSpace(*projectRef) == "" {
			die("--project is required")
		}

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		project, err := client.FindProject(*projectRef)
		dieIf(err)

		payload := map[string]interface{}{"title": title}
		if strings.TrimSpace(*body) != "" {
			payload["body"] = strings.TrimSpace(*body)
		}
		if strings.TrimSpace(*priority) != "" {
			payload["priority"] = strings.ToUpper(strings.TrimSpace(*priority))
		}
		if strings.TrimSpace(*workStatus) != "" {
			payload["work_status"] = strings.TrimSpace(*workStatus)
		}
		if strings.TrimSpace(*assign) != "" {
			agent, err := client.ResolveAgent(*assign)
			dieIf(err)
			payload["owner_agent_id"] = agent.ID
		}

		issue, err := client.CreateIssue(project.ID, payload)
		dieIf(err)
		if *jsonOut {
			printJSON(issue)
			return
		}
		fmt.Printf("Created issue #%d (%s)\n", issue.IssueNumber, issue.ID)
		fmt.Printf("Title: %s\n", issue.Title)
		fmt.Printf("Project: %s\n", project.Name)
		if issue.OwnerAgentID != nil {
			fmt.Printf("Owner: %s\n", resolveAgentName(client, *issue.OwnerAgentID))
		}
		fmt.Printf("Status: %s / %s\n", issue.State, issue.WorkStatus)
		fmt.Printf("Priority: %s\n", issue.Priority)

	case "list":
		flags := flag.NewFlagSet("issue list", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required)")
		state := flags.String("state", "", "state filter (open|closed)")
		origin := flags.String("origin", "", "origin filter (local|github)")
		workStatus := flags.String("work-status", "", "work status filter")
		priority := flags.String("priority", "", "priority filter (P0-P3)")
		owner := flags.String("owner", "", "owner agent id/name/slug")
		mine := flags.Bool("mine", false, "filter to current agent id (OTTER_AGENT_ID)")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])

		if strings.TrimSpace(*projectRef) == "" {
			die("--project is required")
		}

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		project, err := client.FindProject(*projectRef)
		dieIf(err)

		filters := map[string]string{}
		if strings.TrimSpace(*state) != "" {
			filters["state"] = strings.TrimSpace(*state)
		}
		if strings.TrimSpace(*origin) != "" {
			filters["origin"] = strings.TrimSpace(*origin)
		}
		if strings.TrimSpace(*workStatus) != "" {
			filters["work_status"] = strings.TrimSpace(*workStatus)
		}
		if strings.TrimSpace(*priority) != "" {
			filters["priority"] = strings.ToUpper(strings.TrimSpace(*priority))
		}

		ownerFilter := strings.TrimSpace(*owner)
		if *mine {
			if ownerFilter != "" {
				die("use only one of --owner or --mine")
			}
			ownerFilter = strings.TrimSpace(os.Getenv("OTTER_AGENT_ID"))
			if ownerFilter == "" {
				die("--mine requires OTTER_AGENT_ID to be set")
			}
		}
		if ownerFilter != "" {
			agent, err := client.ResolveAgent(ownerFilter)
			dieIf(err)
			filters["owner_agent_id"] = agent.ID
		}

		issues, err := client.ListIssues(project.ID, filters)
		dieIf(err)

		if *jsonOut {
			printJSON(map[string]interface{}{
				"project_id": project.ID,
				"items":      issues,
				"total":      len(issues),
			})
			return
		}

		if len(issues) == 0 {
			fmt.Printf("No issues found for %s\n", project.Name)
			return
		}

		for _, issue := range issues {
			ownerText := ""
			if issue.OwnerAgentID != nil {
				ownerText = " owner=" + resolveAgentName(client, *issue.OwnerAgentID)
			}
			fmt.Printf("#%d [%s/%s] %s (priority=%s%s)\n",
				issue.IssueNumber,
				issue.State,
				issue.WorkStatus,
				issue.Title,
				issue.Priority,
				ownerText,
			)
		}

	case "view":
		flags := flag.NewFlagSet("issue view", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required for issue number)")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("issue id or number is required")
		}
		issueRef := strings.TrimSpace(flags.Args()[0])

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)

		issueID, err := resolveIssueID(client, strings.TrimSpace(*projectRef), issueRef)
		dieIf(err)
		issue, err := client.GetIssue(issueID)
		dieIf(err)

		if *jsonOut {
			printJSON(issue)
			return
		}
		fmt.Printf("Issue #%d (%s)\n", issue.IssueNumber, issue.ID)
		fmt.Printf("Title: %s\n", issue.Title)
		fmt.Printf("State: %s / %s\n", issue.State, issue.WorkStatus)
		fmt.Printf("Priority: %s\n", issue.Priority)
		if issue.OwnerAgentID != nil {
			fmt.Printf("Owner: %s\n", resolveAgentName(client, *issue.OwnerAgentID))
		}
		if issue.Body != nil {
			fmt.Printf("\n%s\n", strings.TrimSpace(*issue.Body))
		}

	case "comment":
		flags := flag.NewFlagSet("issue comment", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required for issue number)")
		author := flags.String("author", "", "author agent id/name/slug")
		org := flags.String("org", "", "org id override")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) < 2 {
			die("usage: otter issue comment <issue-id-or-number> <body>")
		}
		issueRef := strings.TrimSpace(flags.Args()[0])
		body := strings.TrimSpace(strings.Join(flags.Args()[1:], " "))
		if body == "" {
			die("comment body is required")
		}

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)

		issueID, err := resolveIssueID(client, strings.TrimSpace(*projectRef), issueRef)
		dieIf(err)

		authorRef := strings.TrimSpace(*author)
		if authorRef == "" {
			authorRef = strings.TrimSpace(os.Getenv("OTTER_AGENT_ID"))
		}
		if authorRef == "" {
			// Fall back to authenticated user's name
			resp, err := client.WhoAmI()
			if err == nil && resp.User.Name != "" {
				authorRef = resp.User.Name
			}
		}
		if authorRef == "" {
			die("comment requires --author or OTTER_AGENT_ID")
		}
		agent, err := client.ResolveAgent(authorRef)
		dieIf(err)

		dieIf(client.CommentIssue(issueID, agent.ID, body))
		fmt.Println("Comment added.")

	case "ask":
		flags := flag.NewFlagSet("issue ask", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required for issue number)")
		author := flags.String("author", "", "questionnaire author name (defaults to whoami name)")
		title := flags.String("title", "", "questionnaire title")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		var questionSpecs cliRepeatedFlag
		flags.Var(&questionSpecs, "question", "question JSON (repeatable)")
		_ = flags.Parse(args[1:])

		if len(flags.Args()) == 0 {
			die("usage: otter issue ask <issue-id-or-number> --question '{\"id\":\"q1\",\"text\":\"...\",\"type\":\"text\"}'")
		}
		issueRef := strings.TrimSpace(flags.Args()[0])
		questions, err := parseIssueAskQuestions(questionSpecs)
		dieIf(err)

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)

		issueID, err := resolveIssueID(client, strings.TrimSpace(*projectRef), issueRef)
		dieIf(err)

		authorName := strings.TrimSpace(*author)
		if authorName == "" {
			if whoami, whoErr := client.WhoAmI(); whoErr == nil {
				authorName = strings.TrimSpace(whoami.User.Name)
			}
		}
		if authorName == "" {
			die("--author is required when user identity cannot be inferred")
		}

		var titlePtr *string
		if trimmedTitle := strings.TrimSpace(*title); trimmedTitle != "" {
			titlePtr = &trimmedTitle
		}

		created, err := client.AskIssueQuestionnaire(issueID, ottercli.CreateIssueQuestionnaireInput{
			Author:    authorName,
			Title:     titlePtr,
			Questions: questions,
		})
		dieIf(err)
		if *jsonOut {
			printJSON(created)
			return
		}
		fmt.Printf("Created questionnaire %s on issue %s\n", created.ID, issueID)
		fmt.Printf("Questions: %d\n", len(created.Questions))

	case "respond":
		flags := flag.NewFlagSet("issue respond", flag.ExitOnError)
		respondedBy := flags.String("responded-by", "", "respondent name (defaults to whoami name)")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		var responseSpecs cliRepeatedFlag
		flags.Var(&responseSpecs, "response", "response entry as question_id=value (repeatable)")
		_ = flags.Parse(args[1:])

		if len(flags.Args()) == 0 {
			die("usage: otter issue respond <questionnaire-id> --response q1=true --response q2='\"text\"'")
		}
		questionnaireID := strings.TrimSpace(flags.Args()[0])
		responses, err := parseIssueRespondEntries(responseSpecs)
		dieIf(err)

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)

		responder := strings.TrimSpace(*respondedBy)
		if responder == "" {
			if whoami, whoErr := client.WhoAmI(); whoErr == nil {
				responder = strings.TrimSpace(whoami.User.Name)
			}
		}
		if responder == "" {
			die("--responded-by is required when user identity cannot be inferred")
		}

		updated, err := client.RespondIssueQuestionnaire(questionnaireID, ottercli.RespondIssueQuestionnaireInput{
			RespondedBy: responder,
			Responses:   responses,
		})
		dieIf(err)
		if *jsonOut {
			printJSON(updated)
			return
		}
		fmt.Printf("Submitted questionnaire response for %s\n", updated.ID)

	case "assign":
		flags := flag.NewFlagSet("issue assign", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required for issue number)")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) < 2 {
			die("usage: otter issue assign <issue-id-or-number> <agent>")
		}
		issueRef := strings.TrimSpace(flags.Args()[0])
		agentRef := strings.TrimSpace(flags.Args()[1])

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)

		issueID, err := resolveIssueID(client, strings.TrimSpace(*projectRef), issueRef)
		dieIf(err)
		agent, err := client.ResolveAgent(agentRef)
		dieIf(err)

		updated, err := client.PatchIssue(issueID, map[string]interface{}{"owner_agent_id": agent.ID})
		dieIf(err)
		if *jsonOut {
			printJSON(updated)
			return
		}
		fmt.Printf("Assigned issue #%d to %s (%s)\n", updated.IssueNumber, agent.Name, agent.ID)

	case "close":
		flags := flag.NewFlagSet("issue close", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required for issue number)")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("issue id or number is required")
		}
		issueRef := strings.TrimSpace(flags.Args()[0])

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		issueID, err := resolveIssueID(client, strings.TrimSpace(*projectRef), issueRef)
		dieIf(err)

		updated, err := client.PatchIssue(issueID, map[string]interface{}{
			"state":       "closed",
			"work_status": "done",
		})
		dieIf(err)
		if *jsonOut {
			printJSON(updated)
			return
		}
		fmt.Printf("Closed issue #%d\n", updated.IssueNumber)

	case "reopen":
		flags := flag.NewFlagSet("issue reopen", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required for issue number)")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("issue id or number is required")
		}
		issueRef := strings.TrimSpace(flags.Args()[0])

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		issueID, err := resolveIssueID(client, strings.TrimSpace(*projectRef), issueRef)
		dieIf(err)

		updated, err := client.PatchIssue(issueID, map[string]interface{}{
			"state":       "open",
			"work_status": "queued",
		})
		dieIf(err)
		if *jsonOut {
			printJSON(updated)
			return
		}
		fmt.Printf("Reopened issue #%d\n", updated.IssueNumber)

	default:
		fmt.Println("usage: otter issue <create|list|view|comment|ask|respond|assign|close|reopen> ...")
		os.Exit(1)
	}
}

func resolveAgentName(client *ottercli.Client, agentID string) string {
	if agent, err := client.ResolveAgent(agentID); err == nil && agent.Name != "" {
		return agent.Name
	}
	return agentID
}

func resolveIssueID(client *ottercli.Client, projectRef, issueRef string) (string, error) {
	issueRef = strings.TrimSpace(issueRef)
	if issueRef == "" {
		return "", errors.New("issue reference is required")
	}
	if issueUUIDPattern.MatchString(issueRef) {
		return issueRef, nil
	}

	numberText := strings.TrimPrefix(issueRef, "#")
	issueNumber, err := parsePositiveInt(numberText)
	if err != nil {
		return "", fmt.Errorf("issue reference must be UUID or issue number")
	}
	if strings.TrimSpace(projectRef) == "" {
		return "", errors.New("--project is required when issue reference is a number")
	}

	project, err := client.FindProject(projectRef)
	if err != nil {
		return "", err
	}
	issues, err := client.ListIssues(project.ID, map[string]string{
		"issue_number": strconv.Itoa(issueNumber),
		"limit":        "1",
	})
	if err != nil {
		return "", err
	}
	for _, issue := range issues {
		if int(issue.IssueNumber) == issueNumber {
			return issue.ID, nil
		}
	}
	return "", fmt.Errorf("issue #%d not found in project %s", issueNumber, project.Name)
}

func parsePositiveInt(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errors.New("empty value")
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("not numeric")
	}
	if value <= 0 {
		return 0, errors.New("must be > 0")
	}
	return value, nil
}

type cliRepeatedFlag []string

func (f *cliRepeatedFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, ",")
}

func (f *cliRepeatedFlag) Set(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return errors.New("value is required")
	}
	*f = append(*f, trimmed)
	return nil
}

func parseIssueAskQuestions(specs []string) ([]ottercli.QuestionnaireQuestion, error) {
	if len(specs) == 0 {
		return nil, errors.New("at least one --question is required")
	}

	questions := make([]ottercli.QuestionnaireQuestion, 0, len(specs))
	seen := make(map[string]struct{}, len(specs))
	for _, raw := range specs {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()

		var question ottercli.QuestionnaireQuestion
		if err := decoder.Decode(&question); err != nil {
			return nil, fmt.Errorf("invalid --question value %q: %w", raw, err)
		}

		id := strings.TrimSpace(question.ID)
		if id == "" {
			return nil, errors.New("question id is required")
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate question id: %s", id)
		}
		seen[id] = struct{}{}

		questionText := strings.TrimSpace(question.Text)
		if questionText == "" {
			return nil, fmt.Errorf("question %s text is required", id)
		}

		questionType := strings.TrimSpace(strings.ToLower(question.Type))
		if questionType == "" {
			return nil, fmt.Errorf("question %s type is required", id)
		}
		if !isSupportedQuestionType(questionType) {
			return nil, fmt.Errorf("question %s has unsupported type %q", id, questionType)
		}

		options := make([]string, 0, len(question.Options))
		optionSeen := make(map[string]struct{}, len(question.Options))
		for _, option := range question.Options {
			trimmed := strings.TrimSpace(option)
			if trimmed == "" {
				continue
			}
			if _, exists := optionSeen[trimmed]; exists {
				continue
			}
			optionSeen[trimmed] = struct{}{}
			options = append(options, trimmed)
		}

		if questionType == "select" || questionType == "multiselect" {
			if len(options) == 0 {
				return nil, fmt.Errorf("question %s requires options", id)
			}
		} else {
			options = nil
		}

		placeholder := ""
		if questionType == "text" || questionType == "textarea" {
			placeholder = strings.TrimSpace(question.Placeholder)
		}

		questions = append(questions, ottercli.QuestionnaireQuestion{
			ID:          id,
			Text:        questionText,
			Type:        questionType,
			Required:    question.Required,
			Options:     options,
			Placeholder: placeholder,
			Default:     question.Default,
		})
	}

	return questions, nil
}

func parseIssueRespondEntries(entries []string) (map[string]any, error) {
	if len(entries) == 0 {
		return nil, errors.New("at least one --response is required")
	}

	responses := make(map[string]any, len(entries))
	for _, entry := range entries {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --response value %q; expected question_id=value", entry)
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("invalid --response value %q: question id is required", entry)
		}
		if _, exists := responses[key]; exists {
			return nil, fmt.Errorf("duplicate response key: %s", key)
		}

		rawValue := strings.TrimSpace(parts[1])
		if rawValue == "" {
			return nil, fmt.Errorf("invalid --response value %q: response value is required", entry)
		}

		var value any
		if err := json.Unmarshal([]byte(rawValue), &value); err != nil {
			value = rawValue
		}
		responses[key] = value
	}

	return responses, nil
}

func isSupportedQuestionType(questionType string) bool {
	switch questionType {
	case "text", "textarea", "boolean", "select", "multiselect", "number", "date":
		return true
	default:
		return false
	}
}

func parseChameleonSessionAgentID(sessionKey string) (string, error) {
	trimmed := strings.TrimSpace(sessionKey)
	if trimmed == "" {
		return "", errors.New("--session is required")
	}
	matches := chameleonSessionKeyPattern.FindStringSubmatch(trimmed)
	if len(matches) != 2 {
		return "", errors.New("--session must match agent:chameleon:oc:{uuid}")
	}
	return strings.ToLower(matches[1]), nil
}

func validateAgentUUID(agentID string) error {
	trimmed := strings.TrimSpace(agentID)
	if trimmed == "" {
		return errors.New("--agent is required")
	}
	if !issueUUIDPattern.MatchString(trimmed) {
		return errors.New("--agent must be a UUID")
	}
	return nil
}

func resolveMemoryWriteKind(daily bool, explicitKind string) (string, error) {
	if daily {
		return "daily", nil
	}
	kind := strings.TrimSpace(strings.ToLower(explicitKind))
	if kind == "" {
		return "note", nil
	}
	switch kind {
	case "daily", "long_term", "note":
		return kind, nil
	default:
		return "", errors.New("--kind must be one of: daily, long_term, note")
	}
}

func slugifyAgentName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = strings.ReplaceAll(normalized, " ", "-")
	normalized = regexp.MustCompile(`[^a-z0-9\-]+`).ReplaceAllString(normalized, "-")
	normalized = strings.Trim(normalized, "-")
	if normalized == "" {
		return "agent"
	}
	return normalized
}

func buildAgentCreatePayload(displayName, slot, model, role string) map[string]interface{} {
	payload := map[string]interface{}{
		"slot":         slot,
		"display_name": displayName,
		"model":        model,
	}
	if trimmedRole := strings.TrimSpace(role); trimmedRole != "" {
		payload["role"] = trimmedRole
	}
	return payload
}

func toString(value interface{}) string {
	if value == nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return str
	}
	return fmt.Sprintf("%v", value)
}

func releaseGatePayloadOK(payload map[string]interface{}) bool {
	if payload == nil {
		return false
	}
	parsed, ok := payload["ok"].(bool)
	return ok && parsed
}

func prompt(label string) string {
	fmt.Print(label)
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func dieIf(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	die(formatCLIError(err))
}

func formatCLIError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	lower := strings.ToLower(message)
	if strings.Contains(lower, "missing auth token") || strings.Contains(lower, "missing org id") {
		return fmt.Sprintf("No auth config found. Run:\n\n  %s\n\nGet your token at: %s -> API Tokens", authSetupCommand, authTokenHelpURL)
	}
	return message
}

func mustConfigPath() string {
	path, err := ottercli.ConfigPath()
	if err != nil {
		return "config"
	}
	return path
}

func gitRepoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("not a git repository")
	}
	return strings.TrimSpace(string(out)), nil
}

func gitRemoteURL(repoRoot, name string) (string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "remote", "get-url", name)
	out, err := cmd.Output()
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

func printJSON(v interface{}) {
	payload, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(payload))
}

func userHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "."
}

func deriveManagedRepoURL(apiBaseURL, orgID, projectID string) string {
	base := strings.TrimSpace(apiBaseURL)
	orgID = strings.TrimSpace(orgID)
	projectID = strings.TrimSpace(projectID)
	if base == "" || orgID == "" || projectID == "" {
		return ""
	}

	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""

	root := strings.TrimRight(parsed.String(), "/")
	return fmt.Sprintf("%s/git/%s/%s.git", root, url.PathEscape(orgID), url.PathEscape(projectID))
}
