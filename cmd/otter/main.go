package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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

const (
	authSetupCommand  = "otter auth login --token <your-token> --org <org-id>"
	authTokenHelpURL  = "https://otter.camp/settings"
	defaultLabelColor = "#6b7280"
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
	case "project":
		handleProject(os.Args[2:])
	case "clone":
		handleClone(os.Args[2:])
	case "remote":
		handleRemote(os.Args[2:])
	case "repo":
		handleRepo(os.Args[2:])
	case "issue":
		handleIssue(os.Args[2:])
	case "label":
		handleLabel(os.Args[2:])
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
  project          Manage projects
  clone            Clone a project repo
  remote add       Add origin remote for project
  repo info        Show repo URL for project
  issue            Manage project issues
  label            Manage labels
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

func handleProject(args []string) {
	const projectUsage = "usage: otter project <list|create|view|archive|delete|label> ..."
	if len(args) == 0 {
		fmt.Println(projectUsage)
		os.Exit(1)
	}

	switch args[0] {
	case "label":
		handleProjectLabel(args[1:])
		return
	case "list":
		flags := flag.NewFlagSet("project list", flag.ExitOnError)
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		var labels stringSliceFlag
		flags.Var(&labels, "label", "label name or id filter (repeatable)")
		_ = flags.Parse(args[1:])

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		labelIDs, err := resolveLabelFilterIDs(client, labels)
		dieIf(err)
		projects, err := client.ListProjects(labelIDs...)
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
		fmt.Println("usage: otter issue <create|list|view|comment|assign|close|reopen|label> ...")
		os.Exit(1)
	}

	switch args[0] {
	case "label":
		handleIssueLabel(args[1:])
		return
	case "create":
		flags := flag.NewFlagSet("issue create", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required)")
		body := flags.String("body", "", "issue body")
		parent := flags.String("parent", "", "parent issue id or number")
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
		parentIssueID, err := resolveOptionalParentIssueID(
			client,
			strings.TrimSpace(*projectRef),
			strings.TrimSpace(*parent),
		)
		dieIf(err)
		if parentIssueID != nil {
			payload["parent_issue_id"] = *parentIssueID
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
		if issue.ParentIssueID != nil {
			fmt.Printf("Parent: %s\n", *issue.ParentIssueID)
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
		var labels stringSliceFlag
		flags.Var(&labels, "label", "label name or id filter (repeatable)")
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

		labelIDs, err := resolveLabelFilterIDs(client, labels)
		dieIf(err)
		issues, err := client.ListIssues(project.ID, filters, labelIDs...)
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

		fmt.Print(issueViewOutput(client, issue, *jsonOut))

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
		agentID, err := resolveIssueCommentAuthorRef(client, authorRef, os.Getenv("OTTER_AGENT_ID"))
		dieIf(err)

		dieIf(client.CommentIssue(issueID, agentID, body))
		fmt.Println("Comment added.")

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
		fmt.Println("usage: otter issue <create|list|view|comment|assign|close|reopen|label> ...")
		os.Exit(1)
	}
}

func handleLabel(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: otter label <list|create|delete> ...")
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("label list", flag.ExitOnError)
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		labels, err := client.ListLabels()
		dieIf(err)

		if *jsonOut {
			printJSON(labels)
			return
		}
		if len(labels) == 0 {
			fmt.Println("No labels found.")
			return
		}
		for _, label := range labels {
			fmt.Printf("%s\t%s\t%s\n", label.ID, label.Name, label.Color)
		}
	case "create":
		flags := flag.NewFlagSet("label create", flag.ExitOnError)
		color := flags.String("color", "", "label color (hex)")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("usage: otter label create <name> [--color <hex>]")
		}
		name := strings.TrimSpace(strings.Join(flags.Args(), " "))
		if name == "" {
			die("label name is required")
		}

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		label, err := client.CreateLabel(name, strings.TrimSpace(*color))
		dieIf(err)

		if *jsonOut {
			printJSON(label)
			return
		}
		fmt.Printf("Created label: %s (%s)\n", label.Name, label.ID)
	case "delete":
		flags := flag.NewFlagSet("label delete", flag.ExitOnError)
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) == 0 {
			die("usage: otter label delete <label-id-or-name>")
		}
		labelRef := strings.TrimSpace(strings.Join(flags.Args(), " "))

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		label, err := client.ResolveLabel(labelRef)
		dieIf(err)
		dieIf(client.DeleteLabel(label.ID))

		if *jsonOut {
			printJSON(map[string]any{"deleted": true, "label": label})
			return
		}
		fmt.Printf("Deleted label: %s (%s)\n", label.Name, label.ID)
	default:
		fmt.Println("usage: otter label <list|create|delete> ...")
		os.Exit(1)
	}
}

func handleProjectLabel(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: otter project label <add|remove> ...")
		os.Exit(1)
	}
	switch args[0] {
	case "add":
		flags := flag.NewFlagSet("project label add", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required)")
		color := flags.String("color", defaultLabelColor, "default color when auto-creating labels")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if strings.TrimSpace(*projectRef) == "" {
			die("--project is required")
		}
		if len(flags.Args()) == 0 {
			die("usage: otter project label add --project <project> <label-id-or-name>")
		}
		labelRef := strings.TrimSpace(strings.Join(flags.Args(), " "))

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		project, err := client.FindProject(*projectRef)
		dieIf(err)
		label, err := resolveLabelForAdd(client, labelRef, strings.TrimSpace(*color))
		dieIf(err)
		labels, err := client.AddProjectLabels(project.ID, []string{label.ID})
		dieIf(err)

		if *jsonOut {
			printJSON(labels)
			return
		}
		fmt.Printf("Added label %s to project %s\n", label.Name, project.Name)
	case "remove":
		flags := flag.NewFlagSet("project label remove", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required)")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if strings.TrimSpace(*projectRef) == "" {
			die("--project is required")
		}
		if len(flags.Args()) == 0 {
			die("usage: otter project label remove --project <project> <label-id-or-name>")
		}
		labelRef := strings.TrimSpace(strings.Join(flags.Args(), " "))

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		project, err := client.FindProject(*projectRef)
		dieIf(err)
		label, err := client.ResolveLabel(labelRef)
		dieIf(err)
		dieIf(client.RemoveProjectLabel(project.ID, label.ID))

		if *jsonOut {
			printJSON(map[string]any{"removed": true, "label": label, "project_id": project.ID})
			return
		}
		fmt.Printf("Removed label %s from project %s\n", label.Name, project.Name)
	default:
		fmt.Println("usage: otter project label <add|remove> ...")
		os.Exit(1)
	}
}

func handleIssueLabel(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: otter issue label <add|remove> ...")
		os.Exit(1)
	}
	switch args[0] {
	case "add":
		flags := flag.NewFlagSet("issue label add", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required for issue number)")
		color := flags.String("color", defaultLabelColor, "default color when auto-creating labels")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) < 2 {
			die("usage: otter issue label add [--project <project>] <issue-id-or-number> <label-id-or-name>")
		}
		issueRef := strings.TrimSpace(flags.Args()[0])
		labelRef := strings.TrimSpace(strings.Join(flags.Args()[1:], " "))

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		issueID, err := resolveIssueID(client, strings.TrimSpace(*projectRef), issueRef)
		dieIf(err)
		issue, err := client.GetIssue(issueID)
		dieIf(err)
		label, err := resolveLabelForAdd(client, labelRef, strings.TrimSpace(*color))
		dieIf(err)
		labels, err := client.AddIssueLabels(issue.ProjectID, issue.ID, []string{label.ID})
		dieIf(err)

		if *jsonOut {
			printJSON(labels)
			return
		}
		fmt.Printf("Added label %s to issue #%d\n", label.Name, issue.IssueNumber)
	case "remove":
		flags := flag.NewFlagSet("issue label remove", flag.ExitOnError)
		projectRef := flags.String("project", "", "project name or id (required for issue number)")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])
		if len(flags.Args()) < 2 {
			die("usage: otter issue label remove [--project <project>] <issue-id-or-number> <label-id-or-name>")
		}
		issueRef := strings.TrimSpace(flags.Args()[0])
		labelRef := strings.TrimSpace(strings.Join(flags.Args()[1:], " "))

		cfg, err := ottercli.LoadConfig()
		dieIf(err)
		client, _ := ottercli.NewClient(cfg, *org)
		issueID, err := resolveIssueID(client, strings.TrimSpace(*projectRef), issueRef)
		dieIf(err)
		issue, err := client.GetIssue(issueID)
		dieIf(err)
		label, err := client.ResolveLabel(labelRef)
		dieIf(err)
		dieIf(client.RemoveIssueLabel(issue.ProjectID, issue.ID, label.ID))

		if *jsonOut {
			printJSON(map[string]any{"removed": true, "label": label, "issue_id": issue.ID})
			return
		}
		fmt.Printf("Removed label %s from issue #%d\n", label.Name, issue.IssueNumber)
	default:
		fmt.Println("usage: otter issue label <add|remove> ...")
		os.Exit(1)
	}
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	*s = append(*s, trimmed)
	return nil
}

func resolveLabelForAdd(client *ottercli.Client, labelRef, color string) (ottercli.Label, error) {
	trimmed := strings.TrimSpace(labelRef)
	if trimmed == "" {
		return ottercli.Label{}, errors.New("label is required")
	}
	if issueUUIDPattern.MatchString(trimmed) {
		return client.ResolveLabel(trimmed)
	}
	color = strings.TrimSpace(color)
	if color == "" {
		color = defaultLabelColor
	}
	return client.EnsureLabel(trimmed, color)
}

func resolveLabelFilterIDs(client *ottercli.Client, raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(raw))
	labelIDs := make([]string, 0, len(raw))
	for _, entry := range raw {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		label, err := client.ResolveLabel(trimmed)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[label.ID]; exists {
			continue
		}
		seen[label.ID] = struct{}{}
		labelIDs = append(labelIDs, label.ID)
	}
	return labelIDs, nil
}

func resolveAgentName(client *ottercli.Client, agentID string) string {
	if agent, err := client.ResolveAgent(agentID); err == nil && agent.Name != "" {
		return agent.Name
	}
	return agentID
}

func issueViewOutput(client *ottercli.Client, issue ottercli.Issue, jsonOut bool) string {
	if jsonOut {
		payload, _ := json.MarshalIndent(issue, "", "  ")
		return string(payload) + "\n"
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "Issue #%d (%s)\n", issue.IssueNumber, issue.ID)
	fmt.Fprintf(&builder, "Title: %s\n", issue.Title)
	fmt.Fprintf(&builder, "State: %s / %s\n", issue.State, issue.WorkStatus)
	fmt.Fprintf(&builder, "Priority: %s\n", issue.Priority)
	if issue.OwnerAgentID != nil {
		fmt.Fprintf(&builder, "Owner: %s\n", resolveAgentName(client, *issue.OwnerAgentID))
	}
	if issue.Body != nil {
		fmt.Fprintf(&builder, "\n%s\n", strings.TrimSpace(*issue.Body))
	}
	return builder.String()
}

func resolveIssueCommentAuthorRef(client *ottercli.Client, explicitAuthorRef, envAuthorRef string) (string, error) {
	authorRef := strings.TrimSpace(explicitAuthorRef)
	if authorRef == "" {
		authorRef = strings.TrimSpace(envAuthorRef)
	}
	if authorRef == "" {
		resp, err := client.WhoAmI()
		if err == nil {
			authorRef = strings.TrimSpace(resp.User.Name)
		}
	}
	if authorRef == "" {
		return "", errors.New("comment requires --author or OTTER_AGENT_ID")
	}
	agent, err := client.ResolveAgent(authorRef)
	if err != nil {
		return "", err
	}
	return agent.ID, nil
}

func resolveOptionalParentIssueID(client *ottercli.Client, projectRef, parentRef string) (*string, error) {
	parentRef = strings.TrimSpace(parentRef)
	if parentRef == "" {
		return nil, nil
	}
	if client == nil {
		return nil, errors.New("client is required when --parent is set")
	}
	parentIssueID, err := resolveIssueID(client, projectRef, parentRef)
	if err != nil {
		return nil, fmt.Errorf("invalid --parent reference: %w", err)
	}
	return &parentIssueID, nil
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
	value := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, errors.New("not numeric")
		}
		value = value*10 + int(ch-'0')
	}
	if value <= 0 {
		return 0, errors.New("must be > 0")
	}
	return value, nil
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
