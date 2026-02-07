package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
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
  project create   Create a project
  clone            Clone a project repo
  remote add       Add origin remote for project
  repo info        Show repo URL for project
  version          Show CLI version
`)
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
	if len(args) == 0 {
		fmt.Println("usage: otter project create <name>")
		os.Exit(1)
	}

	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("project create", flag.ExitOnError)
		slug := flags.String("slug", "", "custom project slug")
		description := flags.String("description", "", "project description")
		status := flags.String("status", "", "status (active|archived|completed)")
		repoURL := flags.String("repo-url", "", "repo URL")
		org := flags.String("org", "", "org id override")
		jsonOut := flags.Bool("json", false, "JSON output")
		_ = flags.Parse(args[1:])

		nameArgs := flags.Args()
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
		if project.Slug != "" {
			fmt.Printf("Slug: %s\n", project.Slug)
		}
		if project.RepoURL != "" {
			fmt.Printf("Repo: %s\n", project.RepoURL)
		}
	default:
		fmt.Println("usage: otter project create <name>")
		os.Exit(1)
	}
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

	if strings.TrimSpace(project.RepoURL) == "" {
		die("project has no repo_url; set one first")
	}

	target := *pathFlag
	if target == "" {
		root := filepath.Join(userHomeDir(), "Documents", "OtterCamp")
		target = filepath.Join(root, project.Slug())
	}

	cmd := exec.Command("git", "clone", project.RepoURL, target)
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
				"repo_url": project.RepoURL,
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

		if origin, _ := gitRemoteURL(repoRoot, "origin"); origin != "" && !*force {
			die("origin already set; re-run with --force to overwrite")
		}

		if origin, _ := gitRemoteURL(repoRoot, "origin"); origin != "" && *force {
			dieIf(exec.Command("git", "-C", repoRoot, "remote", "set-url", "origin", project.RepoURL).Run())
			fmt.Println("Updated origin to", project.RepoURL)
			return
		}

		dieIf(exec.Command("git", "-C", repoRoot, "remote", "add", "origin", project.RepoURL).Run())
		fmt.Println("Added origin", project.RepoURL)
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
	die(err.Error())
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
