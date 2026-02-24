package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/bootstrap"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/config"
	"github.com/samhotchkiss/otter-camp/internal/db"
	deliverysvc "github.com/samhotchkiss/otter-camp/internal/delivery"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	oclog "github.com/samhotchkiss/otter-camp/internal/log"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	memoryimporter "github.com/samhotchkiss/otter-camp/internal/memory/importer"
	"github.com/samhotchkiss/otter-camp/internal/migrate"
	"github.com/samhotchkiss/otter-camp/internal/policy"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	secretsvc "github.com/samhotchkiss/otter-camp/internal/secret"
	"github.com/samhotchkiss/otter-camp/internal/server"
	skillsvc "github.com/samhotchkiss/otter-camp/internal/skill"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/worker"
)

var (
	version = "dev"
	commit  = "unknown"
	builtAt = "unknown"

	newMemoryImportStore = func() (storage.Store, error) {
		return storage.New(storage.ConfigFromEnv(os.LookupEnv))
	}
	startMemoryImport           = startMemoryImportViaImporter
	newMemoryImportStatusClient = func() (memoryImportStatusClient, error) {
		return newCLIAPIClient("", "")
	}
	memoryImportPollInterval = 5 * time.Second
	memoryImportSleep        = time.Sleep
)

type memoryImportStartFunc func(ctx context.Context, orgID uuid.UUID, fileKey string, store storage.Store) (uuid.UUID, error)

type memoryImportStatusClient interface {
	GetMemoryImport(ctx context.Context, importID string) (cliMemoryImportStatus, error)
}

type deterministicMemoryQueryEmbedder struct{}

func (deterministicMemoryQueryEmbedder) Embed(_ context.Context, _ uuid.UUID, _ string, inputs []string) ([][]float32, error) {
	out := make([][]float32, 0, len(inputs))
	for _, input := range inputs {
		hasher := fnv.New64a()
		_, _ = hasher.Write([]byte(strings.ToLower(strings.TrimSpace(input))))
		seed := hasher.Sum64()

		vector := make([]float32, 1536)
		vector[0] = float32(seed%1000)/1000 + 0.001
		vector[1] = 1
		out = append(out, vector)
	}
	return out, nil
}

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
	case "bootstrap":
		return runBootstrap()
	case "migrate":
		return runMigrate()
	case "backup":
		return runBackup(args[1:])
	case "secret":
		return runSecret(args[1:])
	case "skill":
		return runSkill(args[1:])
	case "schedule":
		return runSchedule(args[1:])
	case "memory":
		return runMemory(args[1:])
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

	bus := eventbus.New(pool.Raw(), logger, eventbus.Config{})
	secretService := secretsvc.NewService(repo.NewSecretRepo(pool.Raw()))

	mcpService, err := mcp.NewService(mcp.ServiceOptions{
		Connections:      repo.NewMCPConnectionRepo(pool.Raw()),
		Catalog:          repo.NewMCPToolCatalogRepo(pool.Raw()),
		Bindings:         repo.NewMCPSecretBindingRepo(pool.Raw()),
		Resolver:         secretService,
		EventBus:         bus,
		TransportFactory: mcp.NewDefaultTransportFactory(nil),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp service setup error: %v\n", err)
		return 1
	}

	if err := mcp.RegisterNativeToolDefinitions(context.Background(), repo.NewToolDefinitionRepo(pool.Raw())); err != nil {
		fmt.Fprintf(os.Stderr, "mcp tool registration error: %v\n", err)
		return 1
	}

	auditRecorder := audit.NewService(repo.NewAuditEventRepo(pool.Raw()), logger)
	policyRepo := repo.NewCapabilityPolicyRepo(pool.Raw())
	policyEvaluator, err := policy.NewPolicyEvaluator(policy.EvaluatorOptions{
		Policies: policyRepo,
		Clock:    clock.New(cfg.Mode),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "policy evaluator setup error: %v\n", err)
		return 1
	}
	if err := policyEvaluator.LoadInstancePolicies(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "policy evaluator load instance policies error: %v\n", err)
		return 1
	}

	agentService, err := agentsvc.NewService(agentsvc.Options{
		Pool:   pool.Raw(),
		Agents: repo.NewAgentRepo(pool.Raw()),
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent service setup error: %v\n", err)
		return 1
	}
	assignmentService, err := agentsvc.NewAssignmentService(agentsvc.AssignmentServiceOptions{
		Pool:   pool.Raw(),
		Events: bus,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent assignment service setup error: %v\n", err)
		return 1
	}
	projectService, err := projectsvc.NewService(projectsvc.Options{
		Pool:   pool.Raw(),
		Events: bus,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "project service setup error: %v\n", err)
		return 1
	}
	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     pool.Raw(),
		EventBus: bus,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "task service setup error: %v\n", err)
		return 1
	}
	flowService, err := flowsvc.NewService(flowsvc.Options{
		Pool:         pool.Raw(),
		TasksService: taskService,
		Events:       bus,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "flow service setup error: %v\n", err)
		return 1
	}
	deliveryService, err := deliverysvc.NewService(deliverysvc.Options{
		Pool: pool.Raw(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "delivery service setup error: %v\n", err)
		return 1
	}
	chatService, err := chat.NewService(chat.Options{
		Pool:   pool.Raw(),
		Events: bus,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat service setup error: %v\n", err)
		return 1
	}

	memoryRetriever, err := memory.NewRetriever(memory.RetrieverOptions{
		Pool:     pool.Raw(),
		Embedder: deterministicMemoryQueryEmbedder{},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory retriever setup error: %v\n", err)
		return 1
	}

	store, err := storage.New(storage.ConfigFromEnv(os.LookupEnv))
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage config error: %v\n", err)
		return 1
	}
	memoryImporterService, err := memoryimporter.NewImporter(memoryimporter.ImporterOptions{
		Pool:  pool.Raw(),
		Store: store,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory importer setup error: %v\n", err)
		return 1
	}

	bootstrapper := bootstrap.NewBootstrapper(bootstrap.Options{
		Pool:    pool.Raw(),
		Logger:  logger,
		Store:   store,
		Version: version,
	})
	resetter := bootstrap.NewResetter(pool.Raw(), bootstrapper)

	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Version:     version,
		Commit:      commit,
		BuiltAt:     builtAt,
		Logger:      logger,
		AuthService: authService,
		Pool:        pool.Raw(),
		RouteRegistrars: []server.RouteRegistrar{
			server.NewRealtimeRouteRegistrar(server.RealtimeRouteOptions{
				Pool:   pool.Raw(),
				Events: bus,
				Logger: logger,
			}),
			server.NewAgentRouteRegistrar(
				agentService,
				repo.NewAgentProfileTemplateRepo(pool.Raw()),
				assignmentService,
				repo.NewAgentRepo(pool.Raw()),
				repo.NewProjectRepo(pool.Raw()),
				repo.NewSkillRepo(pool.Raw()),
				repo.NewAgentProjectAssignmentRepo(pool.Raw()),
				repo.NewAgentSkillAttachmentRepo(pool.Raw()),
			),
			server.NewMCPRouteRegistrar(mcpService, repo.NewMCPToolCatalogRepo(pool.Raw())),
			server.NewModelRouteRegistrar(pool.Raw()),
			server.NewProjectRouteRegistrar(projectService),
			server.NewTaskRouteRegistrar(taskService, flowService, deliveryService, pool.Raw()),
			server.NewChatRouteRegistrar(chatService, pool.Raw()),
			server.NewMemoryRouteRegistrar(server.MemoryRouteOptions{
				Pool:      pool.Raw(),
				Retriever: memoryRetriever,
				Importer:  memoryImporterService,
				Store:     store,
			}),
			server.NewCapabilityPolicyRouteRegistrar(server.CapabilityPolicyRouteOptions{
				Policies:      policyRepo,
				Projects:      repo.NewProjectRepo(pool.Raw()),
				Agents:        repo.NewAgentRepo(pool.Raw()),
				Evaluator:     policyEvaluator,
				AuditRecorder: auditRecorder,
				BootstrapMode: strings.EqualFold(strings.TrimSpace(os.Getenv("OTTERCAMP_BOOTSTRAP_MODE")), "true"),
			}),
		},
		TestMode:     cfg.Mode == config.ModeTest,
		TestResetter: resetter,
	})

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signalCh)

	serveCtx, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	mcpService.StartHealthScheduler(serveCtx)

	err = server.Run(serveCtx, server.Options{
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

	store, err := storage.New(storage.ConfigFromEnv(os.LookupEnv))
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage config error: %v\n", err)
		return 1
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bootstrapper := bootstrap.NewBootstrapper(bootstrap.Options{
		Pool:    pool.Raw(),
		Logger:  logger,
		Store:   store,
		Version: version,
		ProgressFn: func(progress bootstrap.Progress) {
			details := strings.TrimSpace(progress.Details)
			if details == "" {
				fmt.Fprintf(os.Stdout, "step %d (%s): %s\n", progress.Step, progress.Name, progress.Status)
				return
			}
			fmt.Fprintf(os.Stdout, "step %d (%s): %s - %s\n", progress.Step, progress.Name, progress.Status, details)
		},
	})

	if err := bootstrapper.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap error: %v\n", err)
		return 1
	}
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

func runSchedule(args []string) int {
	if len(args) == 0 {
		printScheduleUsage(os.Stderr)
		return 1
	}

	switch args[0] {
	case "list":
		return runScheduleList(args[1:])
	case "enable":
		return runScheduleToggle(args[1:], true)
	case "disable":
		return runScheduleToggle(args[1:], false)
	default:
		fmt.Fprintf(os.Stderr, "unknown schedule command: %s\n", args[0])
		printScheduleUsage(os.Stderr)
		return 1
	}
}

func runMemory(args []string) int {
	if len(args) == 0 {
		printMemoryUsage(os.Stderr)
		return 1
	}

	switch args[0] {
	case "import":
		return runMemoryImport(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown memory command: %s\n", args[0])
		printMemoryUsage(os.Stderr)
		return 1
	}
}

func runMemoryImport(args []string) int {
	flags := flag.NewFlagSet("memory import", flag.ContinueOnError)
	filePath := flags.String("file", "", "path to JSONL zip archive")
	orgIDRaw := flags.String("org-id", "", "organization id (or OTTERCAMP_ORG_ID)")
	wait := flags.Bool("wait", false, "wait for import completion")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "memory import argument error: %v\n", err)
		return 1
	}

	if strings.TrimSpace(*filePath) == "" {
		fmt.Fprintln(os.Stderr, "memory import requires --file")
		return 1
	}
	orgID, err := parseOrgID(*orgIDRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory import org error: %v\n", err)
		return 1
	}

	file, err := os.Open(strings.TrimSpace(*filePath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory import open file error: %v\n", err)
		return 1
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory import stat file error: %v\n", err)
		return 1
	}

	store, err := newMemoryImportStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory import storage error: %v\n", err)
		return 1
	}

	key := fmt.Sprintf("imports/%s/%s/%s", orgID, uuid.New(), filepath.Base(strings.TrimSpace(*filePath)))
	if err := store.Put(context.Background(), key, file, storage.PutOptions{ContentType: "application/zip", ContentLength: info.Size()}); err != nil {
		fmt.Fprintf(os.Stderr, "memory import upload error: %v\n", err)
		return 1
	}

	importID, err := startMemoryImport(context.Background(), orgID, key, store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory import start failed: %v\n", err)
		return 1
	}

	if !*wait {
		fmt.Fprintln(os.Stdout, importID.String())
		return 0
	}

	client, err := newMemoryImportStatusClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory import wait setup error: %v\n", err)
		return 1
	}

	final, err := waitForMemoryImportCompletion(context.Background(), client, importID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory import status check failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(
		os.Stdout,
		"import %s: status=%s total=%d processed=%d imported=%d rejected=%d\n",
		final.ID,
		final.Status,
		valueOrZero(final.TotalRecords),
		final.ProcessedRecords,
		final.ImportedRecords,
		final.RejectedRecords,
	)
	if final.Status == "failed" {
		if final.ErrorMessage != nil {
			fmt.Fprintln(os.Stderr, strings.TrimSpace(*final.ErrorMessage))
		}
		return 1
	}
	return 0
}

func startMemoryImportViaImporter(ctx context.Context, orgID uuid.UUID, fileKey string, store storage.Store) (uuid.UUID, error) {
	pool, err := db.NewFromEnv(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer pool.Close()

	imp, err := memoryimporter.NewImporter(memoryimporter.ImporterOptions{
		Pool:  pool.Raw(),
		Store: store,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return imp.StartImport(ctx, orgID, uuid.Nil, fileKey)
}

func waitForMemoryImportCompletion(ctx context.Context, client memoryImportStatusClient, importID uuid.UUID) (cliMemoryImportStatus, error) {
	if client == nil {
		return cliMemoryImportStatus{}, fmt.Errorf("memory import status client is required")
	}

	for {
		item, err := client.GetMemoryImport(ctx, importID.String())
		if err != nil {
			return cliMemoryImportStatus{}, err
		}
		if strings.TrimSpace(item.ID) == "" {
			item.ID = importID.String()
		}
		if item.Status == "completed" || item.Status == "failed" {
			return item, nil
		}
		memoryImportSleep(memoryImportPollInterval)
	}
}

func runScheduleList(args []string) int {
	flags := flag.NewFlagSet("schedule list", flag.ContinueOnError)
	projectSlug := flags.String("project", "", "project slug")
	jsonOutput := flags.Bool("json", false, "emit JSON output")
	apiURLFlag := flags.String("api-url", "", "ottercamp API base URL (or OTTERCAMP_API_URL)")
	apiKeyFlag := flags.String("api-key", "", "ottercamp API key (or OTTERCAMP_API_KEY)")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "schedule list argument error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*projectSlug) == "" {
		fmt.Fprintln(os.Stderr, "schedule list requires --project")
		return 1
	}

	client, err := newCLIAPIClient(*apiURLFlag, *apiKeyFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "schedule list setup error: %v\n", err)
		return 1
	}

	ctx := context.Background()
	projectID, err := client.lookupProjectID(ctx, strings.TrimSpace(*projectSlug))
	if err != nil {
		fmt.Fprintf(os.Stderr, "schedule list project lookup failed: %v\n", err)
		return 1
	}

	schedules, err := client.listSchedules(ctx, projectID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "schedule list failed: %v\n", err)
		return 1
	}

	if *jsonOutput {
		encoded, err := json.MarshalIndent(schedules, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "schedule list json encode failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, string(encoded))
		return 0
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCRON\tENABLED\tLAST_FIRED\tNEXT_RUN")
	for _, item := range schedules {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%t\t%s\t%s\n",
			item.DisplayName,
			item.CronExpression,
			item.IsEnabled,
			formatOptionalTime(item.LastFiredAt),
			formatOptionalTime(item.NextFireAt),
		)
	}
	_ = tw.Flush()
	return 0
}

func runScheduleToggle(args []string, enable bool) int {
	commandName := "schedule disable"
	if enable {
		commandName = "schedule enable"
	}

	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	projectSlug := flags.String("project", "", "project slug")
	jsonOutput := flags.Bool("json", false, "emit JSON output")
	apiURLFlag := flags.String("api-url", "", "ottercamp API base URL (or OTTERCAMP_API_URL)")
	apiKeyFlag := flags.String("api-key", "", "ottercamp API key (or OTTERCAMP_API_KEY)")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "%s argument error: %v\n", commandName, err)
		return 1
	}
	if strings.TrimSpace(*projectSlug) == "" {
		fmt.Fprintf(os.Stderr, "%s requires --project\n", commandName)
		return 1
	}
	if flags.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "%s requires schedule name argument\n", commandName)
		return 1
	}
	scheduleName := strings.TrimSpace(flags.Arg(0))
	if scheduleName == "" {
		fmt.Fprintf(os.Stderr, "%s requires non-empty schedule name\n", commandName)
		return 1
	}

	client, err := newCLIAPIClient(*apiURLFlag, *apiKeyFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s setup error: %v\n", commandName, err)
		return 1
	}

	ctx := context.Background()
	projectID, err := client.lookupProjectID(ctx, strings.TrimSpace(*projectSlug))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s project lookup failed: %v\n", commandName, err)
		return 1
	}

	schedules, err := client.listSchedules(ctx, projectID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s list failed: %v\n", commandName, err)
		return 1
	}

	selected, found := pickScheduleByName(schedules, scheduleName)
	if !found {
		fmt.Fprintf(os.Stderr, "%s failed: schedule %q not found\n", commandName, scheduleName)
		return 1
	}

	result, err := client.toggleSchedule(ctx, projectID, selected.ID, enable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", commandName, err)
		return 1
	}

	if *jsonOutput {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s json encode failed: %v\n", commandName, err)
			return 1
		}
		fmt.Fprintln(os.Stdout, string(encoded))
		return 0
	}

	if enable {
		fmt.Fprintf(os.Stdout, "enabled schedule %s (next run: %s)\n", selected.DisplayName, result.NextRunAt)
		return 0
	}
	fmt.Fprintf(os.Stdout, "disabled schedule %s\n", selected.DisplayName)
	return 0
}

type cliAPIClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func newCLIAPIClient(apiURL, apiKey string) (*cliAPIClient, error) {
	creds, err := loadCLIStoredCredentials()
	if err != nil {
		return nil, err
	}

	baseURL := strings.TrimSpace(apiURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("OTTERCAMP_API_URL"))
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(creds.APIURL)
	}
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}

	key := strings.TrimSpace(apiKey)
	if key == "" {
		key = strings.TrimSpace(os.Getenv("OTTERCAMP_API_KEY"))
	}
	if key == "" {
		key = strings.TrimSpace(creds.APIKey)
	}
	if key == "" {
		return nil, fmt.Errorf("api key required via --api-key, OTTERCAMP_API_KEY, or ~/.ottercamp/credentials")
	}

	return &cliAPIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  key,
		client:  &http.Client{Timeout: 15 * time.Second},
	}, nil
}

type cliStoredCredentials struct {
	APIURL string
	APIKey string
}

func loadCLIStoredCredentials() (cliStoredCredentials, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return cliStoredCredentials{}, nil
	}

	path := filepath.Join(home, ".ottercamp", "credentials")
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cliStoredCredentials{}, nil
	}
	if err != nil {
		return cliStoredCredentials{}, err
	}

	return parseCLIStoredCredentials(content), nil
}

func parseCLIStoredCredentials(raw []byte) cliStoredCredentials {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return cliStoredCredentials{}
	}

	var jsonObject map[string]any
	if err := json.Unmarshal(raw, &jsonObject); err == nil {
		return cliStoredCredentials{
			APIURL: pickString(jsonObject, "api_url", "apiURL", "url"),
			APIKey: pickString(jsonObject, "api_key", "apiKey", "key"),
		}
	}

	creds := cliStoredCredentials{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		switch key {
		case "api_url":
			if creds.APIURL == "" {
				creds.APIURL = value
			}
		case "api_key":
			if creds.APIKey == "" {
				creds.APIKey = value
			}
		}
	}
	if creds.APIKey == "" && !strings.ContainsAny(text, "\n\r=") {
		creds.APIKey = text
	}
	return creds
}

func pickString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		if s, ok := value.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func (c *cliAPIClient) lookupProjectID(ctx context.Context, slug string) (string, error) {
	query := url.Values{}
	query.Set("slug_prefix", slug)
	query.Set("limit", "200")

	var resp struct {
		Data []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, "/v1/projects?"+query.Encode(), nil, &resp); err != nil {
		return "", err
	}

	for _, item := range resp.Data {
		if item.Slug == slug {
			return item.ID, nil
		}
	}
	return "", fmt.Errorf("project %q not found", slug)
}

type cliSchedule struct {
	ID             string     `json:"id"`
	DisplayName    string     `json:"display_name"`
	CronExpression string     `json:"cron_expression"`
	IsEnabled      bool       `json:"is_enabled"`
	LastFiredAt    *time.Time `json:"last_fired_at"`
	NextFireAt     *time.Time `json:"next_fire_at"`
}

type cliMemoryImportStatus struct {
	ID               string  `json:"id"`
	Status           string  `json:"status"`
	TotalRecords     *int    `json:"total_records"`
	ProcessedRecords int     `json:"processed_records"`
	ImportedRecords  int     `json:"imported_records"`
	RejectedRecords  int     `json:"rejected_records"`
	ErrorMessage     *string `json:"error_message,omitempty"`
}

func (c *cliAPIClient) listSchedules(ctx context.Context, projectID string) ([]cliSchedule, error) {
	var resp struct {
		Data []cliSchedule `json:"data"`
	}
	path := fmt.Sprintf("/v1/projects/%s/schedules", url.PathEscape(projectID))
	if err := c.request(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func (c *cliAPIClient) GetMemoryImport(ctx context.Context, importID string) (cliMemoryImportStatus, error) {
	path := fmt.Sprintf("/v1/memory/imports/%s", url.PathEscape(strings.TrimSpace(importID)))
	var resp struct {
		Data cliMemoryImportStatus `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return cliMemoryImportStatus{}, err
	}
	return resp.Data, nil
}

type cliScheduleToggleResult struct {
	ScheduleID string `json:"schedule_id"`
	Enabled    *bool  `json:"enabled,omitempty"`
	NextRunAt  string `json:"next_run_at,omitempty"`
}

func (c *cliAPIClient) toggleSchedule(ctx context.Context, projectID, scheduleID string, enable bool) (*cliScheduleToggleResult, error) {
	action := "disable"
	if enable {
		action = "enable"
	}
	path := fmt.Sprintf("/v1/projects/%s/schedules/%s/%s", url.PathEscape(projectID), url.PathEscape(scheduleID), action)
	var resp struct {
		Data cliScheduleToggleResult `json:"data"`
	}
	if err := c.request(ctx, http.MethodPost, path, map[string]any{}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (c *cliAPIClient) request(ctx context.Context, method, path string, payload any, out any) error {
	fullURL := c.baseURL + path
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(encoded))
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("api %s %s returned %d: %s", method, path, res.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func pickScheduleByName(schedules []cliSchedule, name string) (cliSchedule, bool) {
	for _, item := range schedules {
		if item.DisplayName == name {
			return item, true
		}
	}
	for _, item := range schedules {
		if strings.EqualFold(item.DisplayName, name) {
			return item, true
		}
	}
	return cliSchedule{}, false
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
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

func printScheduleUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp schedule <list|enable|disable> [flags]")
}

func printMemoryUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp memory import --file <path> [--org-id <uuid>] [--wait]")
}

func printSecretUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp secret <set|list|delete> [flags]")
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp <serve|worker|bootstrap|migrate|backup|secret|skill|schedule|memory|magic-link|reset-password|unlock-account|version>")
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}
