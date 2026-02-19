package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/automigrate"
	"github.com/samhotchkiss/otter-camp/internal/config"
	"github.com/samhotchkiss/otter-camp/internal/github"
	"github.com/samhotchkiss/otter-camp/internal/githubsync"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/migration"
	"github.com/samhotchkiss/otter-camp/internal/scheduler"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

var (
	openServerDB         = store.DB
	runServerAutoMigrate = automigrate.Run
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	workerCtx, cancelWorkers := context.WithCancel(signalCtx)
	defer cancelWorkers()
	var workerWG sync.WaitGroup
	startWorker := func(run func(context.Context)) {
		startWorkerWithRecovery(workerCtx, &workerWG, "worker", log.Printf, run)
	}

	startWorker(func(context.Context) {
		runServerAutoMigration(log.Printf)
	})

	router := api.NewRouter()

	if cfg.GitHub.Enabled {
		db, err := store.DB()
		if err != nil {
			log.Printf("⚠️  GitHub poller disabled; database unavailable: %v", err)
		} else {
			githubClient, err := github.NewClient(cfg.GitHub.APIBaseURL)
			if err != nil {
				log.Printf("⚠️  GitHub poller disabled; github client init failed: %v", err)
			} else {
				poller := githubsync.NewRepoDriftPoller(
					store.NewProjectRepoStore(db),
					store.NewGitHubSyncJobStore(db),
					&githubsync.GitHubBranchHeadClient{Client: githubClient},
					cfg.GitHub.PollInterval,
				)
				startWorker(poller.Start)
				log.Printf("✅ GitHub drift poller started (interval=%s)", cfg.GitHub.PollInterval)
			}
		}
	}

	if cfg.EllieIngestion.Enabled {
		db, err := store.DB()
		if err != nil {
			log.Printf("⚠️  Ellie ingestion worker disabled; database unavailable: %v", err)
		} else {
			var llmExtractor memory.EllieIngestionLLMExtractor
			openClawExtractor, extractorErr := memory.NewEllieIngestionOpenClawExtractorFromEnv()
			if extractorErr != nil {
				log.Printf("⚠️  Ellie ingestion OpenClaw extractor disabled; init failed: %v", extractorErr)
			} else {
				if openClawHandler := api.OpenClawHandlerForRuntime(); openClawHandler != nil {
					bridgeRunner, bridgeErr := memory.NewEllieIngestionOpenClawBridgeRunner(openClawHandler)
					if bridgeErr != nil {
						log.Printf("⚠️  Ellie ingestion OpenClaw bridge runner disabled; init failed: %v", bridgeErr)
					} else {
						openClawExtractor.SetBridgeRunner(bridgeRunner)
						log.Printf("✅ Ellie ingestion OpenClaw bridge runner enabled")
					}
				} else {
					// Without a runtime OpenClaw handler, the extractor will fall back to exec'ing
					// the OpenClaw binary directly. In hosted deployments this is often unavailable,
					// which would degrade ingestion to heuristic-only (very low yield).
					openClawBinary := strings.TrimSpace(os.Getenv("ELLIE_INGESTION_OPENCLAW_BINARY"))
					if openClawBinary == "" {
						openClawBinary = "openclaw"
					}
					if _, err := exec.LookPath(openClawBinary); err != nil {
						log.Printf("⚠️  Ellie ingestion OpenClaw extractor disabled; no OpenClaw bridge handler and %q not found in PATH", openClawBinary)
						openClawExtractor = nil
					}
				}
				if openClawExtractor != nil {
					llmExtractor = openClawExtractor
					log.Printf("✅ Ellie ingestion OpenClaw extractor enabled")
				}
			}

			// Note: Ellie ingestion should not call cloud LLM APIs directly. All LLM calls
			// must route through OpenClaw via the bridge, and should fail/retry gracefully
			// when OpenClaw is unavailable.

			worker := memory.NewEllieIngestionWorker(
				store.NewEllieIngestionStore(db),
				memory.EllieIngestionWorkerConfig{
					OrgID:                cfg.OrgID,
					Interval:             cfg.EllieIngestion.Interval,
					BatchSize:            cfg.EllieIngestion.BatchSize,
					MaxPerRoom:           cfg.EllieIngestion.MaxPerRoom,
					BackfillMaxPerRoom:   cfg.EllieIngestion.BackfillMaxPerRoom,
					BackfillWindowSize:   cfg.EllieIngestion.BackfillWindowSize,
					BackfillWindowStride: cfg.EllieIngestion.BackfillWindowStride,
					WindowGap:            cfg.EllieIngestion.WindowGap,
					Mode:                 memory.EllieIngestionMode(cfg.EllieIngestion.Mode),
					LLMExtractor:         llmExtractor,
				},
			)
			startWorker(worker.Start)
			log.Printf(
				"✅ Ellie ingestion worker started (mode=%s interval=%s batch=%d max_per_room=%d)",
				cfg.EllieIngestion.Mode,
				cfg.EllieIngestion.Interval,
				cfg.EllieIngestion.BatchSize,
				cfg.EllieIngestion.MaxPerRoom,
			)
		}
	}

	if cfg.ConversationTokenBackfill.Enabled {
		db, err := store.DB()
		if err != nil {
			log.Printf("⚠️  Conversation token backfill worker disabled; database unavailable: %v", err)
		} else {
			worker := memory.NewConversationTokenBackfillWorker(
				store.NewConversationTokenStore(db),
				memory.ConversationTokenBackfillWorkerConfig{
					BatchSize:    cfg.ConversationTokenBackfill.BatchSize,
					PollInterval: cfg.ConversationTokenBackfill.PollInterval,
				},
			)
			startWorker(worker.Start)
			log.Printf(
				"✅ Conversation token backfill worker started (interval=%s batch=%d)",
				cfg.ConversationTokenBackfill.PollInterval,
				cfg.ConversationTokenBackfill.BatchSize,
			)
		}
	}

	var (
		conversationEmbedder     memory.Embedder
		conversationEmbedderErr  error
		conversationEmbedderInit bool
	)
	getConversationEmbedder := func() (memory.Embedder, error) {
		if conversationEmbedderInit {
			return conversationEmbedder, conversationEmbedderErr
		}
		conversationEmbedderInit = true
		conversationEmbedder, conversationEmbedderErr = memory.NewEmbedder(memory.EmbedderConfig{
			Provider:      memory.Provider(strings.ToLower(cfg.ConversationEmbedding.Provider)),
			Model:         cfg.ConversationEmbedding.Model,
			Dimension:     cfg.ConversationEmbedding.Dimension,
			OllamaURL:     cfg.ConversationEmbedding.OllamaURL,
			OpenAIBaseURL: cfg.ConversationEmbedding.OpenAIBaseURL,
			OpenAIAPIKey:  cfg.ConversationEmbedding.OpenAIAPIKey,
		}, nil)
		return conversationEmbedder, conversationEmbedderErr
	}

	if cfg.EllieContextInjection.Enabled {
		db, err := store.DB()
		if err != nil {
			log.Printf("⚠️  Ellie context injection worker disabled; database unavailable: %v", err)
		} else {
			embedder, err := getConversationEmbedder()
			if err != nil {
				log.Printf("⚠️  Ellie context injection worker disabled; embedder init failed: %v", err)
			} else {
				service := memory.NewEllieProactiveInjectionService(memory.EllieProactiveInjectionConfig{
					Threshold: cfg.EllieContextInjection.Threshold,
					MaxItems:  cfg.EllieContextInjection.MaxItems,
				})
				worker := memory.NewEllieContextInjectionWorker(
					store.NewEllieContextInjectionStoreWithDimension(db, cfg.ConversationEmbedding.Dimension),
					embedder,
					service,
					memory.EllieContextInjectionWorkerConfig{
						BatchSize:         cfg.EllieContextInjection.BatchSize,
						PollInterval:      cfg.EllieContextInjection.PollInterval,
						Threshold:         cfg.EllieContextInjection.Threshold,
						MaxMemoriesPerMsg: cfg.EllieContextInjection.MaxItems,
						CooldownMessages:  cfg.EllieContextInjection.CooldownMessages,
					},
				)
				startWorker(worker.Start)
				log.Printf(
					"✅ Ellie context injection worker started (interval=%s batch=%d threshold=%.2f cooldown=%d max_items=%d)",
					cfg.EllieContextInjection.PollInterval,
					cfg.EllieContextInjection.BatchSize,
					cfg.EllieContextInjection.Threshold,
					cfg.EllieContextInjection.CooldownMessages,
					cfg.EllieContextInjection.MaxItems,
				)
			}
		}
	}

	if cfg.ConversationEmbedding.Enabled {
		db, err := store.DB()
		if err != nil {
			log.Printf("⚠️  Conversation embedding worker disabled; database unavailable: %v", err)
		} else {
			embedder, err := getConversationEmbedder()
			if err != nil {
				log.Printf("⚠️  Conversation embedding worker disabled; embedder init failed: %v", err)
			} else {
				worker := memory.NewConversationEmbeddingWorker(
					store.NewConversationEmbeddingStoreWithDimension(db, cfg.ConversationEmbedding.Dimension),
					embedder,
					memory.ConversationEmbeddingWorkerConfig{
						BatchSize:    cfg.ConversationEmbedding.BatchSize,
						PollInterval: cfg.ConversationEmbedding.PollInterval,
					},
				)
				startWorker(worker.Start)
				log.Printf(
					"✅ Conversation embedding worker started (provider=%s model=%s batch=%d interval=%s)",
					cfg.ConversationEmbedding.Provider,
					cfg.ConversationEmbedding.Model,
					cfg.ConversationEmbedding.BatchSize,
					cfg.ConversationEmbedding.PollInterval,
				)
			}
		}
	}

	// OpenClaw migration pipeline (hosted: driven via API progress rows; local: can be driven via CLI).
	// This worker only acts when migration_progress has phases in status=running.
	{
		db, err := store.DB()
		if err != nil {
			log.Printf("⚠️  OpenClaw migration pipeline disabled; database unavailable: %v", err)
		} else {
			embedder, embedErr := getConversationEmbedder()
			if embedErr != nil {
				log.Printf("⚠️  OpenClaw migration pipeline disabled; embedder init failed: %v", embedErr)
			} else {
				gatewayCaller := memory.NewOpenClawGatewayCallerFromEnv()
				if openClawHandler := api.OpenClawHandlerForRuntime(); openClawHandler != nil {
					bridgeRunner, runnerErr := memory.NewOpenClawGatewayCallBridgeRunner(openClawHandler)
					if runnerErr != nil {
						log.Printf("⚠️  OpenClaw migration pipeline bridge runner disabled; init failed: %v", runnerErr)
					} else {
						gatewayCaller.SetBridgeRunner(bridgeRunner)
						log.Printf("✅ OpenClaw migration pipeline bridge runner enabled")
					}
				}

				// Use a backfill-mode ingestion worker for the migration pipeline so we use
				// count-based windows rather than collapsing long rooms into a few 15m windows.
				var migrationIngestionExtractor memory.EllieIngestionLLMExtractor
				if extractor, err := memory.NewEllieIngestionOpenClawExtractorFromEnv(); err != nil {
					log.Printf("⚠️  OpenClaw migration ingestion extractor disabled; init failed: %v", err)
				} else {
					if openClawHandler := api.OpenClawHandlerForRuntime(); openClawHandler != nil {
						bridgeRunner, bridgeErr := memory.NewEllieIngestionOpenClawBridgeRunner(openClawHandler)
						if bridgeErr != nil {
							log.Printf("⚠️  OpenClaw migration ingestion bridge runner disabled; init failed: %v", bridgeErr)
						} else {
							extractor.SetBridgeRunner(bridgeRunner)
						}
					}
					migrationIngestionExtractor = extractor
				}
				migrationIngestionWorker := memory.NewEllieIngestionWorker(
					store.NewEllieIngestionStore(db),
					memory.EllieIngestionWorkerConfig{
						OrgID:                cfg.OrgID,
						Interval:             cfg.EllieIngestion.Interval,
						BatchSize:            cfg.EllieIngestion.BatchSize,
						MaxPerRoom:           cfg.EllieIngestion.MaxPerRoom,
						BackfillMaxPerRoom:   cfg.EllieIngestion.BackfillMaxPerRoom,
						BackfillWindowSize:   cfg.EllieIngestion.BackfillWindowSize,
						BackfillWindowStride: cfg.EllieIngestion.BackfillWindowStride,
						WindowGap:            cfg.EllieIngestion.WindowGap,
						Mode:                 memory.EllieIngestionModeBackfill,
						LLMExtractor:         migrationIngestionExtractor,
					},
				)

				entityWorker := memory.NewEllieEntitySynthesisWorker(
					store.NewEllieEntitySynthesisStore(db),
					embedder,
					store.NewConversationEmbeddingStoreWithDimension(db, cfg.ConversationEmbedding.Dimension),
					memory.EllieEntitySynthesisWorkerConfig{
						Synthesizer: &memory.EllieOpenClawEntitySynthesizer{Caller: gatewayCaller},
					},
				)
				taxonomyWorker := memory.NewEllieTaxonomyClassifierWorker(
					store.NewEllieTaxonomyStore(db),
					memory.EllieTaxonomyClassifierWorkerConfig{
						LLM: &memory.EllieOpenClawTaxonomyClassifier{Caller: gatewayCaller},
					},
				)
				dedupWorker := memory.NewEllieDedupWorker(
					&memory.EllieDedupStoreAdapter{Store: store.NewEllieDedupStore(db)},
					memory.EllieDedupWorkerConfig{
						Reviewer:          &memory.EllieOpenClawDedupReviewer{Caller: gatewayCaller},
						MaxClustersPerRun: 2,
					},
				)
				docsScanner := &memory.EllieProjectDocsScanner{
					Summarizer:      &memory.EllieOpenClawProjectDocSummarizer{Caller: gatewayCaller},
					EmbeddingClient: embedder,
				}

				pipeline := &migration.OpenClawPipelineWorker{
					DB:                 db,
					ProgressStore:      store.NewMigrationProgressStore(db),
					EmbeddingStore:     store.NewConversationEmbeddingStoreWithDimension(db, cfg.ConversationEmbedding.Dimension),
					IngestionStore:     store.NewEllieIngestionStore(db),
					IngestionWorker:    migrationIngestionWorker,
					EntityWorker:       entityWorker,
					DedupWorker:        dedupWorker,
					TaxonomyWorker:     taxonomyWorker,
					ProjectDocsScanner: docsScanner,
					ProjectDocsStore:   &memory.EllieProjectDocsStoreAdapter{Store: store.NewEllieProjectDocsStore(db)},
					PollInterval:       3 * time.Second,
					Logf:               log.Printf,
				}
				startWorker(pipeline.Start)
				log.Printf("✅ OpenClaw migration pipeline worker started")
			}
		}
	}

	if cfg.ConversationSegmentation.Enabled {
		db, err := store.DB()
		if err != nil {
			log.Printf("⚠️  Conversation segmentation worker disabled; database unavailable: %v", err)
		} else {
			worker := memory.NewConversationSegmentationWorker(
				store.NewConversationSegmentationStore(db),
				memory.ConversationSegmentationWorkerConfig{
					BatchSize:    cfg.ConversationSegmentation.BatchSize,
					PollInterval: cfg.ConversationSegmentation.PollInterval,
					GapThreshold: cfg.ConversationSegmentation.GapThreshold,
				},
			)
			startWorker(worker.Start)
			log.Printf(
				"✅ Conversation segmentation worker started (batch=%d interval=%s gap=%s)",
				cfg.ConversationSegmentation.BatchSize,
				cfg.ConversationSegmentation.PollInterval,
				cfg.ConversationSegmentation.GapThreshold,
			)
		}
	}

	if cfg.JobScheduler.Enabled {
		db, err := store.DB()
		if err != nil {
			log.Printf("⚠️  Agent job scheduler worker disabled; database unavailable: %v", err)
		} else if strings.TrimSpace(cfg.OrgID) == "" {
			log.Printf("⚠️  Agent job scheduler worker disabled; OTTER_ORG_ID is not configured")
		} else {
			worker := scheduler.NewAgentJobWorker(
				store.NewAgentJobStore(db),
				scheduler.AgentJobWorkerConfig{
					PollInterval:  cfg.JobScheduler.PollInterval,
					MaxPerPoll:    cfg.JobScheduler.MaxPerPoll,
					RunTimeout:    cfg.JobScheduler.RunTimeout,
					MaxRunHistory: cfg.JobScheduler.MaxRunHistory,
					WorkspaceID:   cfg.OrgID,
				},
			)
			worker.Logf = log.Printf
			startWorker(worker.Start)
			log.Printf(
				"✅ Agent job scheduler worker started (interval=%s max_per_poll=%d run_timeout=%s max_run_history=%d)",
				cfg.JobScheduler.PollInterval,
				cfg.JobScheduler.MaxPerPoll,
				cfg.JobScheduler.RunTimeout,
				cfg.JobScheduler.MaxRunHistory,
			)
		}
	}

	server := &http.Server{
		Addr:    "0.0.0.0:" + cfg.Port,
		Handler: router,
	}
	go func() {
		<-signalCtx.Done()
		cancelWorkers()
		workerWG.Wait()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("⚠️  HTTP server shutdown failed: %v", err)
		}
	}()

	log.Printf("🦦 Otter Camp starting: bind=%s health=/health", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		cancelWorkers()
		workerWG.Wait()
		log.Fatalf("server failed (addr=%s): %v", server.Addr, err)
	}
	cancelWorkers()
	workerWG.Wait()
}

func startWorkerWithRecovery(
	workerCtx context.Context,
	workerWG *sync.WaitGroup,
	name string,
	logf func(string, ...interface{}),
	run func(context.Context),
) {
	if workerWG == nil || run == nil {
		return
	}
	if logf == nil {
		logf = log.Printf
	}
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = "worker"
	}

	workerWG.Add(1)
	go func() {
		defer workerWG.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				logf("❌ worker panic: name=%s error=%v", trimmedName, recovered)
			}
		}()
		run(workerCtx)
	}()
}

func runServerAutoMigration(logf func(string, ...interface{})) {
	if logf == nil {
		logf = log.Printf
	}

	migDB, err := openServerDB()
	if err != nil {
		logf("⚠️  Auto-migration skipped; database unavailable: %v", err)
		return
	}
	if err := runServerAutoMigrate(migDB, "migrations"); err != nil {
		logf("⚠️  Auto-migration failed: %v", err)
		return
	}
	logf("✅ Auto-migration complete")
}

// Deploy trigger: 1770312576
