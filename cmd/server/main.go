package main

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/automigrate"
	"github.com/samhotchkiss/otter-camp/internal/config"
	"github.com/samhotchkiss/otter-camp/internal/github"
	"github.com/samhotchkiss/otter-camp/internal/githubsync"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
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
				go poller.Start(context.Background())
				log.Printf("✅ GitHub drift poller started (interval=%s)", cfg.GitHub.PollInterval)
			}
		}
	}

	if cfg.ConversationEmbedding.Enabled {
		db, err := store.DB()
		if err != nil {
			log.Printf("⚠️  Conversation embedding worker disabled; database unavailable: %v", err)
		} else {
			embedder, err := memory.NewEmbedder(memory.EmbedderConfig{
				Provider:      memory.Provider(strings.ToLower(cfg.ConversationEmbedding.Provider)),
				Model:         cfg.ConversationEmbedding.Model,
				Dimension:     cfg.ConversationEmbedding.Dimension,
				OllamaURL:     cfg.ConversationEmbedding.OllamaURL,
				OpenAIBaseURL: cfg.ConversationEmbedding.OpenAIBaseURL,
				OpenAIAPIKey:  cfg.ConversationEmbedding.OpenAIAPIKey,
			}, nil)
			if err != nil {
				log.Printf("⚠️  Conversation embedding worker disabled; embedder init failed: %v", err)
			} else {
				worker := memory.NewConversationEmbeddingWorker(
					store.NewConversationEmbeddingStore(db),
					embedder,
					memory.ConversationEmbeddingWorkerConfig{
						BatchSize:    cfg.ConversationEmbedding.BatchSize,
						PollInterval: cfg.ConversationEmbedding.PollInterval,
					},
				)
				go worker.Start(context.Background())
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
			go worker.Start(context.Background())
			log.Printf(
				"✅ Conversation segmentation worker started (batch=%d interval=%s gap=%s)",
				cfg.ConversationSegmentation.BatchSize,
				cfg.ConversationSegmentation.PollInterval,
				cfg.ConversationSegmentation.GapThreshold,
			)
		}
	}

	log.Printf("🦦 Otter Camp starting on port %s", cfg.Port)
	if err := http.ListenAndServe("0.0.0.0:"+cfg.Port, router); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

// Deploy trigger: 1770312576
