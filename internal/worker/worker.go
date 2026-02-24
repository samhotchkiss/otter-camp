package worker

import (
	"context"
	"log/slog"
	"os"
)

func Run(ctx context.Context, logger *slog.Logger, signalCh <-chan os.Signal) error {
	if logger == nil {
		logger = slog.Default()
	}

	logger.Info("worker started")
	defer logger.Info("worker stopped")

	select {
	case <-ctx.Done():
		return nil
	case <-signalCh:
		return nil
	}
}
