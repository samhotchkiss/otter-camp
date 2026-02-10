package main

import (
	"context"
	"log"
	"net/http"

	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/automigrate"
	"github.com/samhotchkiss/otter-camp/internal/config"
	"github.com/samhotchkiss/otter-camp/internal/github"
	"github.com/samhotchkiss/otter-camp/internal/githubsync"
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

	log.Printf("🦦 Otter Camp starting on port %s", cfg.Port)
	if err := http.ListenAndServe("0.0.0.0:"+cfg.Port, router); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

// Deploy trigger: 1770312576
