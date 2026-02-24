package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/bootstrap"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/config"
	"github.com/samhotchkiss/otter-camp/internal/db"
	oclog "github.com/samhotchkiss/otter-camp/internal/log"
	"github.com/samhotchkiss/otter-camp/internal/migrate"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	secretsvc "github.com/samhotchkiss/otter-camp/internal/secret"
	"github.com/samhotchkiss/otter-camp/internal/server"
	skillsvc "github.com/samhotchkiss/otter-camp/internal/skill"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	"github.com/samhotchkiss/otter-camp/internal/worker"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return 1
	}

	switch args[0] {
	case "serve":
		return runServe()
	case "worker":
		return runWorker()
	case "migrate":
		return runMigrate()
	case "bootstrap":
		return runBootstrap()
	case "backup":
		return runBackup(args[1:])
	case "secret":
		return runSecret(args[1:])
	case "skill":
		return runSkill(args[1:])
	case "magic-link":
		return runMagicLink(args[1:])
	case "reset-password":
		return runResetPassword(args[1:])
	case "unlock-account":
		return runUnlockAccount(args[1:])
	case "version":
		fmt.Fprintln(os.Stdout, version)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printUsage(os.Stderr)
		return 1
	}
}

func runServe() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}

	logger, err := oclog.New(cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger error: %v\n", err)
		return 1
	}

	_ = clock.New(cfg.Mode)

	pool, err := db.NewFromEnv(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "database config error: %v\n", err)
		return 1
	}
	defer pool.Close()

	authService, err := authsvc.NewService(authsvc.Options{
		Users:    repo.NewHumanUserRepo(pool.Raw()),
		Sessions: repo.NewAuthSessionRepo(pool.Raw()),
		APIKeys:  repo.NewAPIKeyRepo(pool.Raw()),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth service setup error: %v\n", err)
		return 1
	}

	bootstrapper := bootstrap.NewFromEnv(pool.Raw(), logger, version)

	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Version:      version,
		Logger:       logger,
		AuthService:  authService,
		TestMode:     cfg.Mode == config.ModeTest,
		TestResetter: bootstrapper,
	})

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signalCh)

	err = server.Run(context.Background(), server.Options{
		Addr:            cfg.Addr,
		Logger:          logger,
		Version:         version,
		Handler:         handler,
		SignalCh:        signalCh,
		ShutdownTimeout: 30 * time.Second,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		return 1
	}

	return 0
}

func runWorker() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}

	logger, err := oclog.New(cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "logger error: %v\n", err)
		return 1
	}

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signalCh)

	if err := worker.Run(context.Background(), logger, signalCh); err != nil {
		fmt.Fprintf(os.Stderr, "worker error: %v\n", err)
		return 1
	}

	return 0
}

func runMigrate() int {
	pool, err := db.NewFromEnv(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "database config error: %v\n", err)
		return 1
	}
	defer pool.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	runner := migrate.NewRunner(pool.Raw(), logger)
	if err := runner.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "migration error: %v\n", err)
		return 1
	}

	return 0
}

func runBootstrap() int {
	pool, err := db.NewFromEnv(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "database config error: %v\n", err)
		return 1
	}
	defer pool.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	bootstrapper := bootstrap.New(bootstrap.Options{
		Pool:    pool.Raw(),
		Logger:  logger,
		Version: version,
		Config:  bootstrap.ConfigFromEnv(),
		Progress: func(event bootstrap.ProgressEvent) {
			if event.Message != "" {
				fmt.Fprintf(os.Stdout, "step %d (%s): %s (%s)\n", event.Number, event.Name, event.Status, event.Message)
				return
			}
			fmt.Fprintf(os.Stdout, "step %d (%s): %s\n", event.Number, event.Name, event.Status)
		},
	})

	if err := bootstrapper.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap error: %v\n", err)
		return 1
	}

	fmt.Fprintln(os.Stdout, "bootstrap complete")
	return 0
}

func runBackup(args []string) int {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	outputDir := flags.String("output-dir", "./backups", "output directory for backup files")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "backup argument error: %v\n", err)
		return 1
	}
	_ = outputDir

	if _, err := storage.New(storage.ConfigFromEnv(os.LookupEnv)); err != nil {
		fmt.Fprintf(os.Stderr, "storage config error: %v\n", err)
		return 1
	}

	fmt.Fprintln(os.Stdout, "backup not yet implemented")
	return 0
}

func runSecret(args []string) int {
	if len(args) == 0 {
		printSecretUsage(os.Stderr)
		return 1
	}

	switch args[0] {
	case "set":
		return runSecretSet(args[1:])
	case "list":
		return runSecretList(args[1:])
	case "delete":
		return runSecretDelete(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown secret command: %s\n", args[0])
		printSecretUsage(os.Stderr)
		return 1
	}
}

func runSecretSet(args []string) int {
	flags := flag.NewFlagSet("secret set", flag.ContinueOnError)
	orgIDRaw := flags.String("org-id", "", "organization id (or OTTERCAMP_ORG_ID)")
	slug := flags.String("slug", "", "secret slug")
	displayName := flags.String("display-name", "", "secret display name")
	description := flags.String("description", "", "secret description")
	valueFlag := flags.String("value", "", "secret value (prefer stdin)")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "secret set argument error: %v\n", err)
		return 1
	}

	orgID, err := parseOrgID(*orgIDRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "secret set org error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*slug) == "" {
		fmt.Fprintln(os.Stderr, "secret set requires --slug")
		return 1
	}
	if strings.TrimSpace(*displayName) == "" {
		fmt.Fprintln(os.Stderr, "secret set requires --display-name")
		return 1
	}

	value, err := readSecretValue(*valueFlag, os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "secret set value error: %v\n", err)
		return 1
	}

	service, cleanup, err := newSecretServiceFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "secret set setup error: %v\n", err)
		return 1
	}
	defer cleanup()

	if err := service.Set(context.Background(), orgID, strings.TrimSpace(*slug), *displayName, *description, value, secretsvc.Principal{
		Type: "human",
		ID:   uuid.Nil,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "secret set failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "saved secret %s\n", strings.TrimSpace(*slug))
	return 0
}

func runSecretList(args []string) int {
	flags := flag.NewFlagSet("secret list", flag.ContinueOnError)
	orgIDRaw := flags.String("org-id", "", "organization id (or OTTERCAMP_ORG_ID)")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "secret list argument error: %v\n", err)
		return 1
	}

	orgID, err := parseOrgID(*orgIDRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "secret list org error: %v\n", err)
		return 1
	}

	service, cleanup, err := newSecretServiceFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "secret list setup error: %v\n", err)
		return 1
	}
	defer cleanup()

	secrets, err := service.List(context.Background(), orgID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "secret list failed: %v\n", err)
		return 1
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "slug\tdisplay_name\tkey_version\tupdated_at")
	for _, secret := range secrets {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", secret.Slug, secret.DisplayName, secret.KeyVersion, secret.UpdatedAt.UTC().Format(time.RFC3339))
	}
	_ = tw.Flush()
	return 0
}

func runSecretDelete(args []string) int {
	flags := flag.NewFlagSet("secret delete", flag.ContinueOnError)
	orgIDRaw := flags.String("org-id", "", "organization id (or OTTERCAMP_ORG_ID)")
	slug := flags.String("slug", "", "secret slug")
	force := flags.Bool("force", false, "delete even when references are detected")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "secret delete argument error: %v\n", err)
		return 1
	}

	orgID, err := parseOrgID(*orgIDRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "secret delete org error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*slug) == "" {
		fmt.Fprintln(os.Stderr, "secret delete requires --slug")
		return 1
	}

	pool, err := db.NewFromEnv(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "secret delete setup error: %v\n", err)
		return 1
	}
	defer pool.Close()

	repository := repo.NewSecretRepo(pool.Raw())
	service := secretsvc.NewService(repository)

	blockers, err := service.CheckDeleteSafety(context.Background(), orgID, strings.TrimSpace(*slug))
	if err != nil {
		fmt.Fprintf(os.Stderr, "secret delete safety check failed: %v\n", err)
		return 1
	}

	if len(blockers) > 0 {
		fmt.Fprintln(os.Stdout, "blocking references:")
		for _, blocker := range blockers {
			fmt.Fprintf(os.Stdout, "  - %s\n", blocker)
		}
		if !*force {
			confirmed, err := promptDeleteConfirmation(os.Stdin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "secret delete confirmation error: %v\n", err)
				return 1
			}
			if !confirmed {
				fmt.Fprintln(os.Stdout, "secret delete cancelled")
				return 1
			}
		}
	}

	if *force && len(blockers) > 0 {
		if err := repository.Delete(context.Background(), orgID, strings.TrimSpace(*slug)); err != nil {
			fmt.Fprintf(os.Stderr, "secret delete failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "deleted secret %s\n", strings.TrimSpace(*slug))
		return 0
	}

	if err := service.Delete(context.Background(), orgID, strings.TrimSpace(*slug), secretsvc.Principal{
		Type: "human",
		ID:   uuid.Nil,
	}); err != nil {
		if errors.Is(err, secretsvc.ErrSecretInUse) {
			fmt.Fprintf(os.Stderr, "secret delete failed: %v (use --force to bypass)\n", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "secret delete failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "deleted secret %s\n", strings.TrimSpace(*slug))
	return 0
}

func readSecretValue(valueFlag string, stdin *os.File) (string, error) {
	if stdin != nil {
		info, err := stdin.Stat()
		if err != nil {
			return "", fmt.Errorf("stat stdin: %w", err)
		}
		if info.Mode()&os.ModeCharDevice == 0 {
			data, err := io.ReadAll(stdin)
			if err != nil {
				return "", fmt.Errorf("read stdin: %w", err)
			}
			if len(data) == 0 {
				return "", fmt.Errorf("stdin is empty")
			}
			return strings.TrimRight(string(data), "\r\n"), nil
		}
	}

	if strings.TrimSpace(valueFlag) == "" {
		return "", fmt.Errorf("provide secret value via stdin or --value")
	}
	return valueFlag, nil
}

func promptDeleteConfirmation(stdin *os.File) (bool, error) {
	fmt.Fprint(os.Stdout, "continue delete? [y/N]: ")
	reader := bufio.NewReader(stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func runSkill(args []string) int {
	if len(args) == 0 {
		printSkillUsage(os.Stderr)
		return 1
	}

	switch args[0] {
	case "create":
		return runSkillCreate(args[1:])
	case "update":
		return runSkillUpdate(args[1:])
	case "delete":
		return runSkillDelete(args[1:])
	case "list":
		return runSkillList(args[1:])
	case "check":
		return runSkillCheck(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown skill command: %s\n", args[0])
		printSkillUsage(os.Stderr)
		return 1
	}
}

func runSkillCreate(args []string) int {
	flags := flag.NewFlagSet("skill create", flag.ContinueOnError)
	orgIDRaw := flags.String("org-id", "", "organization id (or OTTERCAMP_ORG_ID)")
	slug := flags.String("slug", "", "skill slug")
	displayName := flags.String("display-name", "", "skill display name")
	description := flags.String("description", "", "skill description")
	filePath := flags.String("file-path", "", "skill file path")
	createdByType := flags.String("created-by-type", "system", "created by type (human|agent|system)")
	createdByIDRaw := flags.String("created-by-id", "", "created by id (uuid)")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "skill create argument error: %v\n", err)
		return 1
	}

	orgID, err := parseOrgID(*orgIDRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill create org error: %v\n", err)
		return 1
	}

	createdByID := uuid.Nil
	if strings.TrimSpace(*createdByIDRaw) != "" {
		createdByID, err = uuid.Parse(strings.TrimSpace(*createdByIDRaw))
		if err != nil {
			fmt.Fprintf(os.Stderr, "skill create created-by-id error: %v\n", err)
			return 1
		}
	}

	service, cleanup, err := newSkillServiceFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill create setup error: %v\n", err)
		return 1
	}
	defer cleanup()

	created, err := service.Create(context.Background(), orgID, skillsvc.CreateRequest{
		Slug:          *slug,
		DisplayName:   *displayName,
		Description:   *description,
		FilePath:      *filePath,
		CreatedByType: *createdByType,
		CreatedByID:   createdByID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill create failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "created skill %s (%s)\n", created.Slug, created.ID)
	return 0
}

func runSkillUpdate(args []string) int {
	flags := flag.NewFlagSet("skill update", flag.ContinueOnError)
	orgIDRaw := flags.String("org-id", "", "organization id (or OTTERCAMP_ORG_ID)")
	slug := flags.String("slug", "", "skill slug")
	displayName := flags.String("display-name", "", "skill display name")
	description := flags.String("description", "", "skill description")
	filePath := flags.String("file-path", "", "skill file path")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "skill update argument error: %v\n", err)
		return 1
	}

	orgID, err := parseOrgID(*orgIDRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill update org error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*slug) == "" {
		fmt.Fprintln(os.Stderr, "skill update requires --slug")
		return 1
	}

	var req skillsvc.UpdateRequest
	hasUpdate := false
	if strings.TrimSpace(*displayName) != "" {
		value := *displayName
		req.DisplayName = &value
		hasUpdate = true
	}
	if strings.TrimSpace(*description) != "" {
		value := *description
		req.Description = &value
		hasUpdate = true
	}
	if strings.TrimSpace(*filePath) != "" {
		value := *filePath
		req.FilePath = &value
		hasUpdate = true
	}
	if !hasUpdate {
		fmt.Fprintln(os.Stderr, "skill update requires at least one of --display-name, --description, or --file-path")
		return 1
	}

	service, cleanup, err := newSkillServiceFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill update setup error: %v\n", err)
		return 1
	}
	defer cleanup()

	found, err := service.List(context.Background(), orgID, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill update list failed: %v\n", err)
		return 1
	}

	var targetID uuid.UUID
	for _, skill := range found {
		if skill.Slug == strings.TrimSpace(*slug) {
			targetID = skill.ID
			break
		}
	}
	if targetID == uuid.Nil {
		fmt.Fprintf(os.Stderr, "skill update failed: slug %q not found\n", *slug)
		return 1
	}

	updated, err := service.Update(context.Background(), targetID, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill update failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "updated skill %s (version=%d)\n", updated.Slug, updated.Version)
	return 0
}

func runSkillDelete(args []string) int {
	flags := flag.NewFlagSet("skill delete", flag.ContinueOnError)
	orgIDRaw := flags.String("org-id", "", "organization id (or OTTERCAMP_ORG_ID)")
	slug := flags.String("slug", "", "skill slug")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "skill delete argument error: %v\n", err)
		return 1
	}

	orgID, err := parseOrgID(*orgIDRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill delete org error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*slug) == "" {
		fmt.Fprintln(os.Stderr, "skill delete requires --slug")
		return 1
	}

	service, cleanup, err := newSkillServiceFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill delete setup error: %v\n", err)
		return 1
	}
	defer cleanup()

	found, err := service.List(context.Background(), orgID, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill delete list failed: %v\n", err)
		return 1
	}

	var targetID uuid.UUID
	for _, skill := range found {
		if skill.Slug == strings.TrimSpace(*slug) {
			targetID = skill.ID
			break
		}
	}
	if targetID == uuid.Nil {
		fmt.Fprintf(os.Stderr, "skill delete failed: slug %q not found\n", *slug)
		return 1
	}

	if err := service.Delete(context.Background(), targetID); err != nil {
		fmt.Fprintf(os.Stderr, "skill delete failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "deleted skill %s\n", strings.TrimSpace(*slug))
	return 0
}

func runSkillList(args []string) int {
	flags := flag.NewFlagSet("skill list", flag.ContinueOnError)
	orgIDRaw := flags.String("org-id", "", "organization id (or OTTERCAMP_ORG_ID)")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "skill list argument error: %v\n", err)
		return 1
	}

	orgID, err := parseOrgID(*orgIDRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill list org error: %v\n", err)
		return 1
	}

	service, cleanup, err := newSkillServiceFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill list setup error: %v\n", err)
		return 1
	}
	defer cleanup()

	skills, err := service.List(context.Background(), orgID, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill list failed: %v\n", err)
		return 1
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "slug\tdisplay_name\tfile_path\tversion\tis_active")
	for _, skill := range skills {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%t\n", skill.Slug, skill.DisplayName, skill.FilePath, skill.Version, skill.IsActive)
	}
	_ = tw.Flush()
	return 0
}

func runSkillCheck(args []string) int {
	flags := flag.NewFlagSet("skill check", flag.ContinueOnError)
	orgIDRaw := flags.String("org-id", "", "organization id (or OTTERCAMP_ORG_ID)")
	skillsDir := flags.String("skills-dir", "./skills/", "skills directory")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "skill check argument error: %v\n", err)
		return 1
	}

	orgID, err := parseOrgID(*orgIDRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill check org error: %v\n", err)
		return 1
	}

	service, cleanup, err := newSkillServiceFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill check setup error: %v\n", err)
		return 1
	}
	defer cleanup()

	report, err := service.CheckConsistency(context.Background(), orgID, *skillsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill check failed: %v\n", err)
		return 1
	}

	printSkillCheckReport(report)
	if len(report.MissingFiles) > 0 || len(report.UnregisteredFiles) > 0 || len(report.Mismatches) > 0 {
		return 1
	}
	return 0
}

func printSkillCheckReport(report *skillsvc.ConsistencyReport) {
	printList := func(title string, values []string) {
		fmt.Fprintln(os.Stdout, title+":")
		if len(values) == 0 {
			fmt.Fprintln(os.Stdout, "  none")
			return
		}
		for _, value := range values {
			fmt.Fprintf(os.Stdout, "  - %s\n", value)
		}
	}

	printList("MissingFiles", report.MissingFiles)
	printList("UnregisteredFiles", report.UnregisteredFiles)
	printList("Mismatches", report.Mismatches)
}

func runMagicLink(args []string) int {
	flags := flag.NewFlagSet("magic-link", flag.ContinueOnError)
	email := flags.String("email", "", "email for magic link")
	orgIDRaw := flags.String("org-id", "", "organization id (optional)")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "magic-link argument error: %v\n", err)
		return 1
	}

	if strings.TrimSpace(*email) == "" {
		fmt.Fprintln(os.Stderr, "magic-link requires --email")
		return 1
	}

	service, users, cleanup, err := newAuthServiceFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "magic-link setup error: %v\n", err)
		return 1
	}
	defer cleanup()
	_ = users

	ctx := context.Background()
	if strings.TrimSpace(*orgIDRaw) != "" {
		orgID, err := uuid.Parse(strings.TrimSpace(*orgIDRaw))
		if err != nil {
			fmt.Fprintf(os.Stderr, "magic-link org-id error: %v\n", err)
			return 1
		}
		ctx = authsvc.WithOrganizationID(ctx, orgID)
	}

	result, err := service.MagicLink(ctx, strings.TrimSpace(*email))
	if err != nil {
		fmt.Fprintf(os.Stderr, "magic-link failed: %v\n", err)
		return 1
	}

	fmt.Fprintln(os.Stdout, result.Token)
	return 0
}

func runResetPassword(args []string) int {
	flags := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	userIDRaw := flags.String("user-id", "", "user id")
	newPassword := flags.String("new-password", "", "new password")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "reset-password argument error: %v\n", err)
		return 1
	}

	userID, err := parseUUIDFlag(*userIDRaw, "user-id")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reset-password user-id error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*newPassword) == "" {
		fmt.Fprintln(os.Stderr, "reset-password requires --new-password")
		return 1
	}

	service, users, cleanup, err := newAuthServiceFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reset-password setup error: %v\n", err)
		return 1
	}
	defer cleanup()

	user, err := users.GetByID(context.Background(), userID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reset-password user lookup failed: %v\n", err)
		return 1
	}

	ctx := authsvc.WithOrganizationID(context.Background(), user.OrganizationID)
	magic, err := service.MagicLink(ctx, user.Email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reset-password token generation failed: %v\n", err)
		return 1
	}
	if err := service.ResetPassword(ctx, magic.Token, *newPassword); err != nil {
		fmt.Fprintf(os.Stderr, "reset-password failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "reset password for user %s\n", userID)
	return 0
}

func runUnlockAccount(args []string) int {
	flags := flag.NewFlagSet("unlock-account", flag.ContinueOnError)
	userIDRaw := flags.String("user-id", "", "user id")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "unlock-account argument error: %v\n", err)
		return 1
	}

	userID, err := parseUUIDFlag(*userIDRaw, "user-id")
	if err != nil {
		fmt.Fprintf(os.Stderr, "unlock-account user-id error: %v\n", err)
		return 1
	}

	service, _, cleanup, err := newAuthServiceFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "unlock-account setup error: %v\n", err)
		return 1
	}
	defer cleanup()

	if err := service.UnlockAccount(context.Background(), userID); err != nil {
		fmt.Fprintf(os.Stderr, "unlock-account failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "unlocked user %s\n", userID)
	return 0
}

func newAuthServiceFromEnv() (authsvc.Service, *repo.HumanUserRepo, func(), error) {
	pool, err := db.NewFromEnv(context.Background())
	if err != nil {
		return nil, nil, nil, err
	}

	userRepo := repo.NewHumanUserRepo(pool.Raw())
	sessionRepo := repo.NewAuthSessionRepo(pool.Raw())
	apiKeyRepo := repo.NewAPIKeyRepo(pool.Raw())

	service, err := authsvc.NewService(authsvc.Options{
		Users:    userRepo,
		Sessions: sessionRepo,
		APIKeys:  apiKeyRepo,
		Clock:    clock.Real{},
	})
	if err != nil {
		pool.Close()
		return nil, nil, nil, err
	}

	cleanup := func() {
		pool.Close()
	}
	return service, userRepo, cleanup, nil
}

func newSkillServiceFromEnv() (skillsvc.Service, func(), error) {
	pool, err := db.NewFromEnv(context.Background())
	if err != nil {
		return nil, nil, err
	}

	repository := repo.NewSkillRepo(pool.Raw())
	service := skillsvc.NewService(repository)
	cleanup := func() {
		pool.Close()
	}
	return service, cleanup, nil
}

func newSecretServiceFromEnv() (secretsvc.Service, func(), error) {
	pool, err := db.NewFromEnv(context.Background())
	if err != nil {
		return nil, nil, err
	}

	repository := repo.NewSecretRepo(pool.Raw())
	service := secretsvc.NewService(repository)
	cleanup := func() {
		pool.Close()
	}
	return service, cleanup, nil
}

func parseOrgID(flagValue string) (uuid.UUID, error) {
	raw := strings.TrimSpace(flagValue)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("OTTERCAMP_ORG_ID"))
	}
	if raw == "" {
		return uuid.Nil, fmt.Errorf("organization id required via --org-id or OTTERCAMP_ORG_ID")
	}
	return uuid.Parse(raw)
}

func parseUUIDFlag(rawValue, name string) (uuid.UUID, error) {
	raw := strings.TrimSpace(rawValue)
	if raw == "" {
		return uuid.Nil, fmt.Errorf("%s is required", name)
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, err
	}
	return parsed, nil
}

func printSkillUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp skill <create|update|delete|list|check> [flags]")
}

func printSecretUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp secret <set|list|delete> [flags]")
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp <serve|worker|migrate|bootstrap|backup|secret|skill|magic-link|reset-password|unlock-account|version>")
}
