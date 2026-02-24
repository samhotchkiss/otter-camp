package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/config"
	"github.com/samhotchkiss/otter-camp/internal/db"
	oclog "github.com/samhotchkiss/otter-camp/internal/log"
	"github.com/samhotchkiss/otter-camp/internal/migrate"
	"github.com/samhotchkiss/otter-camp/internal/server"
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
	case "backup":
		return runBackup(args[1:])
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

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signalCh)

	err = server.Run(context.Background(), server.Options{
		Addr:            cfg.Addr,
		Logger:          logger,
		Version:         version,
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

func printUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp <serve|worker|migrate|backup|version>")
}
