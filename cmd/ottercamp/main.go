package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	"github.com/samhotchkiss/otter-camp/internal/audit"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/bootstrap"
	browsersvc "github.com/samhotchkiss/otter-camp/internal/browser"
	"github.com/samhotchkiss/otter-camp/internal/budget"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	clitools "github.com/samhotchkiss/otter-camp/internal/cli"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/config"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
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
	"github.com/samhotchkiss/otter-camp/internal/push"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	secretsvc "github.com/samhotchkiss/otter-camp/internal/secret"
	"github.com/samhotchkiss/otter-camp/internal/server"
	skillsvc "github.com/samhotchkiss/otter-camp/internal/skill"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	versionpkg "github.com/samhotchkiss/otter-camp/internal/version"
	"github.com/samhotchkiss/otter-camp/internal/worker"
)

var (
	version = versionpkg.Version
	commit  = versionpkg.Commit
	builtAt = versionpkg.BuiltAt

	defaultOutputMode  = clitools.OutputModeTable
	defaultNoColor     bool
	globalServerURL    string
	globalAPIKey       string
	credentialStore    = clitools.NewCredentialStore()
	ottercampDirectory = func() string {
		home, _ := os.UserHomeDir()
		if strings.TrimSpace(home) == "" {
			return ".ottercamp"
		}
		return filepath.Join(home, ".ottercamp")
	}

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
	options, remaining, err := parseGlobalCLIOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "global flags error: %v\n", err)
		return 1
	}
	applyGlobalCLIOptions(options)

	if len(remaining) == 0 {
		printUsage(os.Stderr)
		return 1
	}

	switch remaining[0] {
	case "server":
		return runServerCommand(remaining[1:])
	case "db":
		return runDBCommand(remaining[1:])
	case "auth":
		return runAuthCommand(remaining[1:])
	case "health":
		return runHealthCommand(remaining[1:])
	case "backup":
		return runBackupCommand(remaining[1:])
	case "serve":
		return runServerCommand(append([]string{"start"}, remaining[1:]...))
	case "stop":
		return runServerCommand([]string{"stop"})
	case "worker":
		return runWorker()
	case "bootstrap":
		return runBootstrap()
	case "migrate":
		return runDBCommand(append([]string{"migrate"}, remaining[1:]...))
	case "restore":
		return runBackupCommand(append([]string{"restore"}, remaining[1:]...))
	case "secret":
		return runSecret(remaining[1:])
	case "skill":
		return runSkill(remaining[1:])
	case "schedule":
		return runSchedule(remaining[1:])
	case "chat":
		return runChatCommand(remaining[1:])
	case "memory":
		return runMemory(remaining[1:])
	case "magic-link":
		return runAuthCommand(append([]string{"magic-link"}, remaining[1:]...))
	case "reset-password":
		return runAuthCommand(append([]string{"reset-password"}, remaining[1:]...))
	case "unlock-account":
		return runAuthCommand(append([]string{"unlock-account"}, remaining[1:]...))
	case "version":
		return runVersionCommand(remaining[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", remaining[0])
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
	flowSessionBridge, err := projectsvc.NewFlowSessionBridge(projectsvc.FlowSessionBridgeOptions{
		Pool:  pool.Raw(),
		Chats: chatService,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "flow session bridge setup error: %v\n", err)
		return 1
	}

	budgetService, err := budget.NewService(budget.Options{
		Pool:   pool.Raw(),
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "budget service setup error: %v\n", err)
		return 1
	}
	runService, err := controlplane.NewRunService(controlplane.RunServiceOptions{
		Pool:          pool.Raw(),
		EventBus:      bus,
		Budget:        budgetService,
		SessionBridge: flowSessionBridge,
		Logger:        logger,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "run service setup error: %v\n", err)
		return 1
	}

	store, err := storage.New(storage.ConfigFromEnv(os.LookupEnv))
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage config error: %v\n", err)
		return 1
	}
	browserExecutor, err := browsersvc.NewExecutor(browsersvc.ExecutorOptions{
		Pool:      pool.Raw(),
		Runs:      runService,
		Artifacts: controlplane.NewRunArtifactRepository(pool.Raw()),
		Store:     store,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "browser executor setup error: %v\n", err)
		return 1
	}

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:            pool.Raw(),
		EventBus:        bus,
		BrowserHandoffs: browserExecutor,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "task service setup error: %v\n", err)
		return 1
	}
	flowService, err := flowsvc.NewService(flowsvc.Options{
		Pool:          pool.Raw(),
		TasksService:  taskService,
		Events:        bus,
		SessionBridge: flowSessionBridge,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "flow service setup error: %v\n", err)
		return 1
	}

	pushPreferenceRepo := push.NewPreferenceRepository(pool.Raw())
	pushPreferenceService, err := push.NewPreferenceService(push.PreferenceServiceOptions{
		Repository: pushPreferenceRepo,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "push preference service setup error: %v\n", err)
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
	bootstrap.RegisterStarterTrioStep(bootstrapper, repo.NewAgentRepo(pool.Raw()))
	bootstrap.RegisterCapabilityPolicyStep(bootstrapper, repo.NewCapabilityPolicyRepo(pool.Raw()))
	resetter := bootstrap.NewResetter(pool.Raw(), bootstrapper)

	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Version:     version,
		Commit:      commit,
		BuiltAt:     builtAt,
		Logger:      logger,
		AuthService: authService,
		Pool:        pool.Raw(),
		Store:       store,
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
			server.NewOrgAuditRouteRegistrar(pool.Raw()),
			server.NewProjectRouteRegistrar(projectService),
			server.NewTaskRouteRegistrar(taskService, flowService, deliveryService, pool.Raw()),
			server.NewChatRouteRegistrar(chatService, pool.Raw()),
			server.NewMemoryRouteRegistrar(server.MemoryRouteOptions{
				Pool:      pool.Raw(),
				Retriever: memoryRetriever,
				Importer:  memoryImporterService,
				Store:     store,
				TestMode:  cfg.Mode == config.ModeTest,
			}),
			server.NewPushRouteRegistrar(pushPreferenceService, pushPreferenceRepo),
			server.NewCapabilityPolicyRouteRegistrar(server.CapabilityPolicyRouteOptions{
				Policies:      policyRepo,
				Projects:      repo.NewProjectRepo(pool.Raw()),
				Agents:        repo.NewAgentRepo(pool.Raw()),
				Evaluator:     policyEvaluator,
				AuditRecorder: auditRecorder,
				BootstrapMode: strings.EqualFold(strings.TrimSpace(os.Getenv("OTTERCAMP_BOOTSTRAP_MODE")), "true"),
			}),
			server.NewControlPlaneRouteRegistrar(server.ControlPlaneRouteOptions{
				Pool:       pool.Raw(),
				RunService: runService,
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
		TLSMode:         strings.TrimSpace(os.Getenv("OTTERCAMP_TLS_MODE")),
		TLSCertFile:     strings.TrimSpace(os.Getenv("OTTERCAMP_TLS_CERT")),
		TLSKeyFile:      strings.TrimSpace(os.Getenv("OTTERCAMP_TLS_KEY")),
		ACMEDomain:      strings.TrimSpace(os.Getenv("OTTERCAMP_TLS_ACME_DOMAIN")),
		ACMEEmail:       strings.TrimSpace(os.Getenv("OTTERCAMP_TLS_ACME_EMAIL")),
		ACMECacheDir:    filepath.Join(ottercampDirectory(), "acme"),
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
	runner, err := migrate.NewRunnerFromEnv(pool.Raw(), logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migration setup error: %v\n", err)
		return 1
	}
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
	bootstrap.RegisterStarterTrioStep(bootstrapper, repo.NewAgentRepo(pool.Raw()))
	bootstrap.RegisterCapabilityPolicyStep(bootstrapper, repo.NewCapabilityPolicyRepo(pool.Raw()))

	if err := bootstrapper.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap error: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, "Bootstrap complete")
	return 0
}

func runBackup(args []string) int {
	return runBackupCreate(args)
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
	slugFlag := flags.String("slug", "", "secret slug (legacy alias; prefer positional <slug>)")
	displayName := flags.String("display-name", "", "secret display name")
	description := flags.String("description", "", "secret description")
	valueFlag := flags.String("value", "", "secret value (prefer stdin)")
	fromFile := flags.String("from-file", "", "read secret value from file path")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "secret set argument error: %v\n", err)
		return 1
	}

	orgID, err := parseOrgID(*orgIDRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "secret set org error: %v\n", err)
		return 1
	}
	slug := resolveSecretSlug(flags.Args(), *slugFlag)
	if strings.TrimSpace(slug) == "" {
		fmt.Fprintln(os.Stderr, "secret set requires <slug> (or --slug)")
		return 1
	}
	if strings.TrimSpace(*displayName) == "" {
		fmt.Fprintln(os.Stderr, "secret set requires --display-name")
		return 1
	}

	value, err := readSecretValue(*valueFlag, *fromFile, os.Stdin, os.Stderr)
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

	if err := service.Set(context.Background(), orgID, strings.TrimSpace(slug), *displayName, *description, value, secretsvc.Principal{
		Type: "human",
		ID:   uuid.Nil,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "secret set failed: %v\n", err)
		return 1
	}

	fmt.Fprintf(os.Stdout, "saved secret %s\n", strings.TrimSpace(slug))
	return 0
}

func runSecretList(args []string) int {
	flags := flag.NewFlagSet("secret list", flag.ContinueOnError)
	orgIDRaw := flags.String("org-id", "", "organization id (or OTTERCAMP_ORG_ID)")
	outputMode := flags.String("output", defaultOutputMode, "output mode: table|json|quiet")
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

	formatter, err := clitools.NewOutputFormatter(*outputMode, os.Stdout, defaultNoColor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "secret list output error: %v\n", err)
		return 1
	}
	switch formatter.Mode() {
	case clitools.OutputModeJSON:
		if err := formatter.WriteJSON(secrets); err != nil {
			fmt.Fprintf(os.Stderr, "secret list output error: %v\n", err)
			return 1
		}
		return 0
	case clitools.OutputModeQuiet:
		for _, secret := range secrets {
			if err := formatter.WriteQuiet(secret.Slug); err != nil {
				fmt.Fprintf(os.Stderr, "secret list output error: %v\n", err)
				return 1
			}
		}
		return 0
	default:
		rows := make([][]string, 0, len(secrets))
		for _, secret := range secrets {
			rows = append(rows, []string{secret.Slug, secret.DisplayName, fmt.Sprintf("%d", secret.KeyVersion), secret.UpdatedAt.UTC().Format(time.RFC3339)})
		}
		if err := formatter.WriteTable([]string{"slug", "display_name", "key_version", "updated_at"}, rows); err != nil {
			fmt.Fprintf(os.Stderr, "secret list output error: %v\n", err)
			return 1
		}
		return 0
	}
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

func resolveSecretSlug(positional []string, slugFlag string) string {
	if len(positional) > 0 && strings.TrimSpace(positional[0]) != "" {
		return strings.TrimSpace(positional[0])
	}
	return strings.TrimSpace(slugFlag)
}

func readSecretValue(valueFlag, fromFile string, stdin *os.File, stderr io.Writer) (string, error) {
	return readSecretValueWithTerminal(valueFlag, fromFile, stdin, stderr, isTerminalFile)
}

func readSecretValueWithTerminal(valueFlag, fromFile string, stdin *os.File, stderr io.Writer, terminalFn func(*os.File) (bool, error)) (string, error) {
	fromFile = strings.TrimSpace(fromFile)
	if fromFile != "" {
		data, err := os.ReadFile(fromFile)
		if err != nil {
			return "", fmt.Errorf("read --from-file: %w", err)
		}
		value := strings.TrimRight(string(data), "\r\n")
		if strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("--from-file is empty")
		}
		return value, nil
	}

	valueFlag = strings.TrimSpace(valueFlag)
	if valueFlag != "" {
		return valueFlag, nil
	}

	if stdin != nil {
		isTTY, err := terminalFn(stdin)
		if err != nil {
			return "", err
		}

		if !isTTY {
			data, err := io.ReadAll(stdin)
			if err != nil {
				return "", fmt.Errorf("read stdin: %w", err)
			}
			if len(data) == 0 {
				return "", fmt.Errorf("stdin is empty")
			}
			return strings.TrimRight(string(data), "\r\n"), nil
		}

		if stderr != nil {
			fmt.Fprint(stderr, "Secret value: ")
		}
		reader := bufio.NewReader(stdin)
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		value := strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("secret value is empty")
		}
		return value, nil
	}

	return "", fmt.Errorf("provide secret value via --from-file, --value, or stdin")
}

func isTerminalFile(stdin *os.File) (bool, error) {
	if stdin == nil {
		return false, nil
	}
	info, err := stdin.Stat()
	if err != nil {
		return false, fmt.Errorf("stat stdin: %w", err)
	}
	return info.Mode()&os.ModeCharDevice != 0, nil
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
	creds, err := credentialStore.Load()
	if err != nil {
		return nil, err
	}

	baseURL := strings.TrimSpace(apiURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(globalServerURL)
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(creds.ServerURL)
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("OTTERCAMP_API_URL"))
	}
	if baseURL == "" {
		baseURL = clitools.ResolveServerURL("", creds)
	}

	key := strings.TrimSpace(apiKey)
	if key == "" {
		key = strings.TrimSpace(globalAPIKey)
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

type cliAdminUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
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

func (c *cliAPIClient) lookupUserIDByEmail(ctx context.Context, email string) (string, error) {
	query := url.Values{}
	query.Set("email", strings.TrimSpace(email))

	var resp struct {
		Data []cliAdminUser `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, "/v1/admin/users?"+query.Encode(), nil, &resp); err != nil {
		return "", err
	}
	for _, item := range resp.Data {
		if strings.EqualFold(strings.TrimSpace(item.Email), strings.TrimSpace(email)) {
			return item.ID, nil
		}
	}
	return "", fmt.Errorf("user %q not found", email)
}

func (c *cliAPIClient) adminResetPassword(ctx context.Context, userID, newPassword string) error {
	path := fmt.Sprintf("/v1/admin/users/%s/reset-password", url.PathEscape(strings.TrimSpace(userID)))
	return c.request(ctx, http.MethodPost, path, map[string]any{"new_password": newPassword}, nil)
}

func (c *cliAPIClient) adminMagicLink(ctx context.Context, userID string) (string, error) {
	path := fmt.Sprintf("/v1/admin/users/%s/magic-link", url.PathEscape(strings.TrimSpace(userID)))
	var resp struct {
		Data struct {
			MagicLinkURL string `json:"magic_link_url"`
		} `json:"data"`
	}
	if err := c.request(ctx, http.MethodPost, path, map[string]any{}, &resp); err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Data.MagicLinkURL), nil
}

func (c *cliAPIClient) adminUnlockAccount(ctx context.Context, userID string) error {
	path := fmt.Sprintf("/v1/admin/users/%s/unlock", url.PathEscape(strings.TrimSpace(userID)))
	return c.request(ctx, http.MethodPost, path, map[string]any{}, nil)
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
		return parseCLIAPIError(method, path, res.StatusCode, data)
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
	fmt.Fprintln(w, "usage: ottercamp [--server-url URL] [--api-key KEY] [--output table|json|quiet] [--no-color] <server|db|auth|secret|backup|health|version|schedule|chat>")
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

type globalCLIOptions struct {
	ServerURL string
	APIKey    string
	Output    string
	NoColor   bool
}

func parseGlobalCLIOptions(args []string) (globalCLIOptions, []string, error) {
	flags := flag.NewFlagSet("ottercamp", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	serverURL := flags.String("server-url", "", "server URL (or OTTERCAMP_SERVER_URL)")
	apiKey := flags.String("api-key", "", "API key (or OTTERCAMP_API_KEY)")
	output := flags.String("output", defaultOutputMode, "output mode: table|json|quiet")
	noColor := flags.Bool("no-color", false, "disable ANSI color in table output")
	if err := flags.Parse(args); err != nil {
		return globalCLIOptions{}, nil, err
	}
	return globalCLIOptions{
		ServerURL: strings.TrimSpace(*serverURL),
		APIKey:    strings.TrimSpace(*apiKey),
		Output:    strings.ToLower(strings.TrimSpace(*output)),
		NoColor:   *noColor,
	}, flags.Args(), nil
}

func applyGlobalCLIOptions(options globalCLIOptions) {
	if strings.TrimSpace(options.ServerURL) != "" {
		globalServerURL = strings.TrimSpace(options.ServerURL)
	}
	if strings.TrimSpace(options.APIKey) != "" {
		globalAPIKey = strings.TrimSpace(options.APIKey)
	}
	if strings.TrimSpace(options.Output) != "" {
		defaultOutputMode = strings.TrimSpace(options.Output)
	}
	defaultNoColor = options.NoColor
}

func runVersionCommand(args []string) int {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	jsonOutput := flags.Bool("json", false, "emit JSON output")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "version argument error: %v\n", err)
		return 1
	}

	payload := map[string]string{
		"version":    versionpkg.Version,
		"commit":     versionpkg.Commit,
		"built_at":   versionpkg.BuiltAt,
		"go_version": versionpkg.GoVersion,
	}
	if *jsonOutput {
		formatter, _ := clitools.NewOutputFormatter(clitools.OutputModeJSON, os.Stdout, defaultNoColor)
		if err := formatter.WriteJSON(payload); err != nil {
			fmt.Fprintf(os.Stderr, "version output error: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Fprintf(os.Stdout, "version=%s commit=%s built_at=%s\n", versionpkg.Version, versionpkg.Commit, versionpkg.BuiltAt)
	return 0
}

func runChatCommand(args []string) int {
	return runChatCommandImpl(args)
}

func runServerCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ottercamp server <start|stop> [flags]")
		return 1
	}
	switch args[0] {
	case "start":
		return runServerStart(args[1:])
	case "stop":
		return runServerStop()
	default:
		fmt.Fprintf(os.Stderr, "unknown server command: %s\n", args[0])
		return 1
	}
}

func runServerStart(args []string) int {
	flags := flag.NewFlagSet("server start", flag.ContinueOnError)
	port := flags.Int("port", 4110, "server port")
	_ = flags.Int("worker-concurrency", 4, "worker concurrency")
	tlsMode := flags.String("tls-mode", "none", "tls mode: none|manual|acme")
	tlsCert := flags.String("tls-cert", "", "manual tls certificate path")
	tlsKey := flags.String("tls-key", "", "manual tls key path")
	acmeDomain := flags.String("acme-domain", "", "acme domain")
	acmeEmail := flags.String("acme-email", "", "acme contact email")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "server start argument error: %v\n", err)
		return 1
	}

	mode := strings.ToLower(strings.TrimSpace(*tlsMode))
	switch mode {
	case "none":
	case "manual":
		if strings.TrimSpace(*tlsCert) == "" || strings.TrimSpace(*tlsKey) == "" {
			fmt.Fprintln(os.Stderr, "server start requires --tls-cert and --tls-key when --tls-mode=manual")
			return 1
		}
	case "acme":
		if strings.TrimSpace(*acmeDomain) == "" {
			fmt.Fprintln(os.Stderr, "server start requires --acme-domain when --tls-mode=acme")
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "invalid --tls-mode %q\n", *tlsMode)
		return 1
	}

	if err := os.MkdirAll(ottercampDirectory(), 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "server start pid path error: %v\n", err)
		return 1
	}
	pidPath := filepath.Join(ottercampDirectory(), "server.pid")
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "server start pid write error: %v\n", err)
		return 1
	}
	defer func() {
		_ = os.Remove(pidPath)
	}()

	_ = os.Setenv("OTTERCAMP_ADDR", fmt.Sprintf(":%d", *port))
	_ = os.Setenv("OTTERCAMP_TLS_MODE", mode)
	_ = os.Setenv("OTTERCAMP_TLS_CERT", strings.TrimSpace(*tlsCert))
	_ = os.Setenv("OTTERCAMP_TLS_KEY", strings.TrimSpace(*tlsKey))
	_ = os.Setenv("OTTERCAMP_TLS_ACME_DOMAIN", strings.TrimSpace(*acmeDomain))
	_ = os.Setenv("OTTERCAMP_TLS_ACME_EMAIL", strings.TrimSpace(*acmeEmail))

	return runServe()
}

func runServerStop() int {
	pidPath := filepath.Join(ottercampDirectory(), "server.pid")
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(os.Stderr, "Server is not running")
			return 1
		}
		fmt.Fprintf(os.Stderr, "server stop pid read error: %v\n", err)
		return 1
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		fmt.Fprintln(os.Stderr, "server stop pid file is invalid")
		return 1
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Server is not running")
		return 1
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		fmt.Fprintln(os.Stderr, "Server is not running")
		return 1
	}
	_ = os.Remove(pidPath)
	fmt.Fprintln(os.Stdout, "server stop signal sent")
	return 0
}

func runDBCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ottercamp db <migrate|status|reset> [flags]")
		return 1
	}
	switch args[0] {
	case "migrate":
		return runDBMigrate(args[1:])
	case "status":
		return runDBStatus(args[1:])
	case "reset":
		return runDBReset(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown db command: %s\n", args[0])
		return 1
	}
}

func runDBMigrate(args []string) int {
	flags := flag.NewFlagSet("db migrate", flag.ContinueOnError)
	dryRun := flags.Bool("dry-run", false, "print pending migrations without applying")
	target := flags.Int("target", 0, "apply up to this migration version")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "db migrate argument error: %v\n", err)
		return 1
	}

	pool, err := db.NewFromEnv(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "db migrate setup error: %v\n", err)
		return 1
	}
	defer pool.Close()

	all, err := migrate.LoadMigrationsFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "db migrate setup error: %v\n", err)
		return 1
	}
	if *target > 0 {
		all = filterMigrationsToTarget(all, *target)
	}

	applied, _, err := loadAppliedMigrationState(context.Background(), pool.Raw())
	if err != nil {
		fmt.Fprintf(os.Stderr, "db migrate state error: %v\n", err)
		return 1
	}
	pending := migrate.PendingMigrations(all, applied)
	if *dryRun {
		if len(pending) == 0 {
			fmt.Fprintln(os.Stdout, "no pending migrations")
			return 0
		}
		for _, item := range pending {
			fmt.Fprintf(os.Stdout, "pending %04d_%s\n", item.Version, item.Name)
		}
		return 0
	}

	for _, item := range pending {
		startedAt := time.Now()
		runner := migrate.NewRunnerWithFS(
			pool.Raw(),
			slog.New(slog.NewTextHandler(io.Discard, nil)),
			migrationMapFS([]migrate.Migration{item}),
		)
		if err := runner.Run(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "db migrate failed: %v\n", err)
			return 1
		}
		fmt.Fprint(os.Stdout, migrationAppliedLine(item, time.Since(startedAt)))
	}
	return 0
}

func runDBStatus(args []string) int {
	flags := flag.NewFlagSet("db status", flag.ContinueOnError)
	outputMode := flags.String("output", defaultOutputMode, "output mode: table|json|quiet")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "db status argument error: %v\n", err)
		return 1
	}

	pool, err := db.NewFromEnv(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "db status setup error: %v\n", err)
		return 1
	}
	defer pool.Close()

	all, err := migrate.LoadMigrationsFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "db status setup error: %v\n", err)
		return 1
	}
	applied, appliedAt, err := loadAppliedMigrationState(context.Background(), pool.Raw())
	if err != nil {
		fmt.Fprintf(os.Stderr, "db status setup error: %v\n", err)
		return 1
	}

	type row struct {
		Version   int        `json:"version"`
		File      string     `json:"file"`
		Applied   bool       `json:"applied"`
		AppliedAt *time.Time `json:"applied_at,omitempty"`
	}
	rows := make([]row, 0, len(all))
	for _, migration := range all {
		_, ok := applied[migration.Version]
		record := row{
			Version: migration.Version,
			File:    migration.File,
			Applied: ok,
		}
		if ok {
			value := appliedAt[migration.Version]
			record.AppliedAt = &value
		}
		rows = append(rows, record)
	}

	formatter, err := clitools.NewOutputFormatter(*outputMode, os.Stdout, defaultNoColor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db status output error: %v\n", err)
		return 1
	}
	switch formatter.Mode() {
	case clitools.OutputModeJSON:
		if err := formatter.WriteJSON(rows); err != nil {
			fmt.Fprintf(os.Stderr, "db status output error: %v\n", err)
			return 1
		}
	case clitools.OutputModeQuiet:
		pending := 0
		for _, item := range rows {
			if !item.Applied {
				pending++
			}
		}
		if err := formatter.WriteQuiet(fmt.Sprintf("%d", pending)); err != nil {
			fmt.Fprintf(os.Stderr, "db status output error: %v\n", err)
			return 1
		}
	default:
		tableRows := make([][]string, 0, len(rows))
		for _, item := range rows {
			appliedAt := ""
			if item.AppliedAt != nil {
				appliedAt = item.AppliedAt.UTC().Format(time.RFC3339)
			}
			tableRows = append(tableRows, []string{
				fmt.Sprintf("%04d", item.Version),
				item.File,
				fmt.Sprintf("%t", item.Applied),
				appliedAt,
			})
		}
		if err := formatter.WriteTable([]string{"#", "File", "Applied", "Applied At"}, tableRows); err != nil {
			fmt.Fprintf(os.Stderr, "db status output error: %v\n", err)
			return 1
		}
	}
	return 0
}

func runDBReset(args []string) int {
	flags := flag.NewFlagSet("db reset", flag.ContinueOnError)
	force := flags.Bool("force", false, "skip confirmation prompt")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "db reset argument error: %v\n", err)
		return 1
	}

	mode := strings.ToLower(strings.TrimSpace(os.Getenv("OTTERCAMP_MODE")))
	if mode != "test" && mode != "dev" && mode != "development" {
		fmt.Fprintln(os.Stderr, "db reset is only allowed in OTTERCAMP_MODE=test or OTTERCAMP_MODE=dev")
		return 1
	}
	if !*force {
		ok, err := promptDeleteConfirmation(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "db reset confirmation error: %v\n", err)
			return 1
		}
		if !ok {
			fmt.Fprintln(os.Stdout, "db reset cancelled")
			return 1
		}
	}

	pool, err := db.NewFromEnv(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "db reset setup error: %v\n", err)
		return 1
	}
	defer pool.Close()

	if _, err := pool.Raw().Exec(context.Background(), `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public; CREATE EXTENSION IF NOT EXISTS vector;`); err != nil {
		fmt.Fprintf(os.Stderr, "db reset schema reset error: %v\n", err)
		return 1
	}

	runner, err := migrate.NewRunnerFromEnv(pool.Raw(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "db reset migration setup error: %v\n", err)
		return 1
	}
	if err := runner.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "db reset migration error: %v\n", err)
		return 1
	}

	store, err := storage.New(storage.ConfigFromEnv(os.LookupEnv))
	if err != nil {
		fmt.Fprintf(os.Stderr, "db reset storage setup error: %v\n", err)
		return 1
	}
	bootstrapper := bootstrap.NewBootstrapper(bootstrap.Options{
		Pool:    pool.Raw(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:   store,
		Version: versionpkg.Version,
	})
	bootstrap.RegisterStarterTrioStep(bootstrapper, repo.NewAgentRepo(pool.Raw()))
	bootstrap.RegisterCapabilityPolicyStep(bootstrapper, repo.NewCapabilityPolicyRepo(pool.Raw()))
	if err := bootstrapper.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "db reset bootstrap error: %v\n", err)
		return 1
	}

	fmt.Fprintln(os.Stdout, "database reset complete")
	return 0
}

func runAuthCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ottercamp auth <reset-password|magic-link|unlock-account> [flags]")
		return 1
	}
	switch args[0] {
	case "reset-password":
		return runAuthResetPassword(args[1:])
	case "magic-link":
		return runAuthMagicLink(args[1:])
	case "unlock-account":
		return runAuthUnlockAccount(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown auth command: %s\n", args[0])
		return 1
	}
}

func runAuthResetPassword(args []string) int {
	flags := flag.NewFlagSet("auth reset-password", flag.ContinueOnError)
	userEmail := flags.String("user", "", "user email")
	newPassword := flags.String("new-password", "", "new password")
	serverURL := flags.String("server-url", "", "server URL override")
	apiKey := flags.String("api-key", "", "api key override")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "auth reset-password argument error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*userEmail) == "" || strings.TrimSpace(*newPassword) == "" {
		fmt.Fprintln(os.Stderr, "auth reset-password requires --user and --new-password")
		return 1
	}

	client, err := newCLIAPIClient(*serverURL, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth reset-password setup error: %v\n", err)
		return 1
	}
	userID, err := client.lookupUserIDByEmail(context.Background(), strings.TrimSpace(*userEmail))
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth reset-password user lookup failed: %v\n", err)
		return 1
	}
	if err := client.adminResetPassword(context.Background(), userID, strings.TrimSpace(*newPassword)); err != nil {
		fmt.Fprintf(os.Stderr, "auth reset-password failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "reset password for %s\n", strings.TrimSpace(*userEmail))
	return 0
}

func runAuthMagicLink(args []string) int {
	flags := flag.NewFlagSet("auth magic-link", flag.ContinueOnError)
	userEmail := flags.String("user", "", "user email")
	serverURL := flags.String("server-url", "", "server URL override")
	apiKey := flags.String("api-key", "", "api key override")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "auth magic-link argument error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*userEmail) == "" {
		fmt.Fprintln(os.Stderr, "auth magic-link requires --user")
		return 1
	}

	client, err := newCLIAPIClient(*serverURL, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth magic-link setup error: %v\n", err)
		return 1
	}
	userID, err := client.lookupUserIDByEmail(context.Background(), strings.TrimSpace(*userEmail))
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth magic-link user lookup failed: %v\n", err)
		return 1
	}
	linkURL, err := client.adminMagicLink(context.Background(), userID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth magic-link failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, linkURL)
	return 0
}

func runAuthUnlockAccount(args []string) int {
	flags := flag.NewFlagSet("auth unlock-account", flag.ContinueOnError)
	userEmail := flags.String("user", "", "user email")
	serverURL := flags.String("server-url", "", "server URL override")
	apiKey := flags.String("api-key", "", "api key override")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "auth unlock-account argument error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*userEmail) == "" {
		fmt.Fprintln(os.Stderr, "auth unlock-account requires --user")
		return 1
	}

	client, err := newCLIAPIClient(*serverURL, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth unlock-account setup error: %v\n", err)
		return 1
	}
	userID, err := client.lookupUserIDByEmail(context.Background(), strings.TrimSpace(*userEmail))
	if err != nil {
		fmt.Fprintf(os.Stderr, "auth unlock-account user lookup failed: %v\n", err)
		return 1
	}
	if err := client.adminUnlockAccount(context.Background(), userID); err != nil {
		fmt.Fprintf(os.Stderr, "auth unlock-account failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "unlocked %s\n", strings.TrimSpace(*userEmail))
	return 0
}

func runHealthCommand(args []string) int {
	flags := flag.NewFlagSet("health", flag.ContinueOnError)
	jsonOutput := flags.Bool("json", false, "emit JSON output")
	outputMode := flags.String("output", defaultOutputMode, "output mode: table|json|quiet")
	serverURL := flags.String("server-url", "", "server URL override")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "health argument error: %v\n", err)
		return 1
	}
	if *jsonOutput {
		*outputMode = clitools.OutputModeJSON
	}

	creds, err := credentialStore.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "health setup error: %v\n", err)
		return 1
	}
	baseURL := clitools.ResolveServerURL(strings.TrimSpace(*serverURL), creds)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, strings.TrimRight(baseURL, "/")+"/health/ready", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "health setup error: %v\n", err)
		return 1
	}
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "health request failed: %v\n", err)
		return 1
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "health read failed: %v\n", err)
		return 1
	}

	formatter, err := clitools.NewOutputFormatter(*outputMode, os.Stdout, defaultNoColor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "health output error: %v\n", err)
		return 1
	}
	if formatter.Mode() == clitools.OutputModeJSON {
		fmt.Fprintln(os.Stdout, strings.TrimSpace(string(body)))
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			return 0
		}
		return 1
	}

	var payload struct {
		Data struct {
			Status string          `json:"status"`
			Checks map[string]bool `json:"checks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		fmt.Fprintln(os.Stdout, strings.TrimSpace(string(body)))
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			return 0
		}
		return 1
	}

	rows := make([][]string, 0, len(payload.Data.Checks))
	allPass := true
	for name, pass := range payload.Data.Checks {
		if !pass {
			allPass = false
		}
		rows = append(rows, []string{name, fmt.Sprintf("%t", pass)})
	}
	if err := formatter.WriteTable([]string{"Check", "Pass"}, rows); err != nil {
		fmt.Fprintf(os.Stderr, "health output error: %v\n", err)
		return 1
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || !allPass {
		return 1
	}
	return 0
}

func runBackupCommand(args []string) int {
	if len(args) == 0 {
		return runBackupCreate(nil)
	}
	if strings.HasPrefix(args[0], "-") {
		return runBackupCreate(args)
	}
	switch args[0] {
	case "create":
		return runBackupCreate(args[1:])
	case "restore":
		return runBackupRestore(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown backup command: %s\n", args[0])
		return 1
	}
}

func runBackupCreate(args []string) int {
	flags := flag.NewFlagSet("backup create", flag.ContinueOnError)
	outputPath := flags.String("output", "", "output .tar.gz path")
	includeObjects := flags.Bool("include-objects", false, "include local object store files")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "backup argument error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*outputPath) == "" {
		fmt.Fprintln(os.Stderr, "backup requires --output")
		return 1
	}

	databaseURL := strings.TrimSpace(os.Getenv("OTTERCAMP_DATABASE_URL"))
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "backup requires OTTERCAMP_DATABASE_URL")
		return 1
	}

	tmpDir, err := os.MkdirTemp("", "ottercamp-backup-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup temp dir error: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmpDir)

	sqlPath := filepath.Join(tmpDir, "ottercamp_backup.sql")
	if err := runExternalCommand("pg_dump", "--file", sqlPath, databaseURL); err != nil {
		fmt.Fprintf(os.Stderr, "backup pg_dump error: %v\n", err)
		return 1
	}

	var objectsRoot string
	if *includeObjects {
		storageConfig := storage.ConfigFromEnv(os.LookupEnv)
		if strings.ToLower(strings.TrimSpace(storageConfig.Backend)) == storage.BackendS3 {
			fmt.Fprintln(os.Stderr, "backup --include-objects supports only fs storage backend")
			return 1
		}
		objectsRoot = storageConfig.FSRoot
		if err := copyTree(objectsRoot, filepath.Join(tmpDir, "objects")); err != nil {
			fmt.Fprintf(os.Stderr, "backup object copy error: %v\n", err)
			return 1
		}
	}

	if err := writeBackupArchive(*outputPath, sqlPath, filepath.Join(tmpDir, "objects"), *includeObjects && strings.TrimSpace(objectsRoot) != ""); err != nil {
		fmt.Fprintf(os.Stderr, "backup archive error: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, strings.TrimSpace(*outputPath))
	return 0
}

func runBackupRestore(args []string) int {
	flags := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	inputPath := flags.String("input", "", "input .tar.gz path")
	force := flags.Bool("force", false, "skip confirmation")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "restore argument error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*inputPath) == "" {
		fmt.Fprintln(os.Stderr, "restore requires --input")
		return 1
	}
	if !*force {
		ok, err := promptDeleteConfirmation(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "restore confirmation error: %v\n", err)
			return 1
		}
		if !ok {
			fmt.Fprintln(os.Stdout, "restore cancelled")
			return 1
		}
	}

	databaseURL := strings.TrimSpace(os.Getenv("OTTERCAMP_DATABASE_URL"))
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "restore requires OTTERCAMP_DATABASE_URL")
		return 1
	}

	tmpDir, err := os.MkdirTemp("", "ottercamp-restore-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "restore temp dir error: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmpDir)

	if err := extractBackupArchive(strings.TrimSpace(*inputPath), tmpDir); err != nil {
		fmt.Fprintf(os.Stderr, "restore extract error: %v\n", err)
		return 1
	}

	sqlPath := filepath.Join(tmpDir, "ottercamp_backup.sql")
	if err := runExternalCommand("psql", databaseURL, "-f", sqlPath); err != nil {
		fmt.Fprintf(os.Stderr, "restore psql error: %v\n", err)
		return 1
	}

	objectsDir := filepath.Join(tmpDir, "objects")
	if stat, err := os.Stat(objectsDir); err == nil && stat.IsDir() {
		storageConfig := storage.ConfigFromEnv(os.LookupEnv)
		if strings.ToLower(strings.TrimSpace(storageConfig.Backend)) != storage.BackendS3 {
			if err := copyTree(objectsDir, storageConfig.FSRoot); err != nil {
				fmt.Fprintf(os.Stderr, "restore object copy error: %v\n", err)
				return 1
			}
		}
	}
	fmt.Fprintln(os.Stdout, "restore completed")
	return 0
}

func loadAppliedMigrationState(ctx context.Context, pool *pgxpool.Pool) (map[int]struct{}, map[int]time.Time, error) {
	applied := make(map[int]struct{})
	appliedAt := make(map[int]time.Time)
	if pool == nil {
		return applied, appliedAt, fmt.Errorf("db pool is required")
	}

	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return nil, nil, err
	}
	if !exists {
		return applied, appliedAt, nil
	}

	rows, err := pool.Query(ctx, `SELECT version, applied_at FROM schema_migrations`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var version int
		var at time.Time
		if err := rows.Scan(&version, &at); err != nil {
			return nil, nil, err
		}
		applied[version] = struct{}{}
		appliedAt[version] = at
	}
	if rows.Err() != nil {
		return nil, nil, rows.Err()
	}
	return applied, appliedAt, nil
}

func filterMigrationsToTarget(all []migrate.Migration, target int) []migrate.Migration {
	if target <= 0 {
		return all
	}
	out := make([]migrate.Migration, 0, len(all))
	for _, item := range all {
		if item.Version <= target {
			out = append(out, item)
		}
	}
	return out
}

func migrationAppliedLine(item migrate.Migration, elapsed time.Duration) string {
	return fmt.Sprintf("Applying %04d_%s... done (%dms)\n", item.Version, item.Name, elapsed.Milliseconds())
}

func migrationMapFS(items []migrate.Migration) mapFS {
	files := make(mapFS, len(items))
	for _, item := range items {
		files[item.File] = []byte(item.SQL)
	}
	return files
}

type mapFS map[string][]byte

func (m mapFS) Open(name string) (fs.File, error) {
	clean := strings.TrimSpace(filepath.Clean(name))
	if clean == "." {
		return nil, fs.ErrNotExist
	}
	data, ok := m[clean]
	if !ok {
		return nil, fs.ErrNotExist
	}
	reader := bytes.NewReader(data)
	return &mapFile{
		name:   filepath.Base(clean),
		reader: reader,
		size:   int64(len(data)),
	}, nil
}

func (m mapFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if strings.TrimSpace(name) != "." {
		return nil, fs.ErrNotExist
	}
	names := make([]string, 0, len(m))
	for file := range m {
		names = append(names, file)
	}
	sort.Strings(names)
	entries := make([]fs.DirEntry, 0, len(names))
	for _, file := range names {
		entries = append(entries, mapDirEntry{
			name: filepath.Base(file),
			size: int64(len(m[file])),
		})
	}
	return entries, nil
}

type mapFile struct {
	name   string
	reader *bytes.Reader
	size   int64
}

func (f *mapFile) Stat() (fs.FileInfo, error) {
	return mapFileInfo{name: f.name, size: f.size}, nil
}

func (f *mapFile) Read(p []byte) (int, error) {
	return f.reader.Read(p)
}

func (f *mapFile) Close() error {
	return nil
}

type mapDirEntry struct {
	name string
	size int64
}

func (e mapDirEntry) Name() string {
	return e.name
}

func (e mapDirEntry) IsDir() bool {
	return false
}

func (e mapDirEntry) Type() fs.FileMode {
	return 0
}

func (e mapDirEntry) Info() (fs.FileInfo, error) {
	return mapFileInfo{name: e.name, size: e.size}, nil
}

type mapFileInfo struct {
	name string
	size int64
}

func (i mapFileInfo) Name() string {
	return i.name
}

func (i mapFileInfo) Size() int64 {
	return i.size
}

func (i mapFileInfo) Mode() fs.FileMode {
	return 0o444
}

func (i mapFileInfo) ModTime() time.Time {
	return time.Time{}
}

func (i mapFileInfo) IsDir() bool {
	return false
}

func (i mapFileInfo) Sys() any {
	return nil
}

func runExternalCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func writeBackupArchive(outputPath, sqlPath, objectsPath string, includeObjects bool) error {
	file, err := os.Create(strings.TrimSpace(outputPath))
	if err != nil {
		return err
	}
	defer file.Close()

	gz := gzip.NewWriter(file)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	if err := addFileToTar(tw, sqlPath, "ottercamp_backup.sql"); err != nil {
		return err
	}
	if includeObjects {
		if err := addDirectoryToTar(tw, objectsPath, "objects"); err != nil {
			return err
		}
	}
	return nil
}

func extractBackupArchive(inputPath, destination string) error {
	file, err := os.Open(strings.TrimSpace(inputPath))
	if err != nil {
		return err
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.Clean(header.Name))
		if header.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
}

func addFileToTar(tw *tar.Writer, sourcePath, targetPath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(targetPath)
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	file, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(tw, file)
	return err
}

func addDirectoryToTar(tw *tar.Writer, sourceDir, targetDir string) error {
	return filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		targetPath := filepath.ToSlash(filepath.Join(targetDir, rel))
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			header := &tar.Header{
				Name:     targetPath + "/",
				Typeflag: tar.TypeDir,
				Mode:     0o755,
			}
			return tw.WriteHeader(header)
		}
		return addFileToTar(tw, path, targetPath)
	})
}

func copyTree(sourceDir, destinationDir string) error {
	sourceDir = strings.TrimSpace(sourceDir)
	if sourceDir == "" {
		return nil
	}
	return filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destinationDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}
