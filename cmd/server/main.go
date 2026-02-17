package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
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
	"github.com/samhotchkiss/otter-camp/internal/scheduler"
	"github.com/samhotchkiss/otter-camp/internal/store"
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
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			run(workerCtx)
		}()
	}

	// Auto-migrate database on startup
	if migDB, err := store.DB(); err == nil {
		if err := automigrate.Run(migDB, "migrations"); err != nil {
			log.Printf("⚠️  Auto-migration failed: %v", err)
		}
	} else {
		log.Printf("⚠️  Auto-migration skipped; database unavailable: %v", err)
	}

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
			worker := memory.NewEllieIngestionWorker(
				store.NewEllieIngestionStore(db),
				memory.EllieIngestionWorkerConfig{
					Interval:   cfg.EllieIngestion.Interval,
					BatchSize:  cfg.EllieIngestion.BatchSize,
					MaxPerRoom: cfg.EllieIngestion.MaxPerRoom,
				},
			)
			startWorker(worker.Start)
			log.Printf(
				"✅ Ellie ingestion worker started (interval=%s batch=%d max_per_room=%d)",
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

	log.Printf("🦦 Otter Camp starting on port %s", cfg.Port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		cancelWorkers()
		workerWG.Wait()
		log.Fatalf("server failed: %v", err)
	}
	cancelWorkers()
	workerWG.Wait()
}

// Deploy trigger: 1770312576
