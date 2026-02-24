package oclog

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/samhotchkiss/otter-camp/internal/config"
)

func New(cfg config.Config, out io.Writer) (*slog.Logger, error) {
	if out == nil {
		out = os.Stdout
	}

	level, err := toSlogLevel(cfg.LogLevel)
	if err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{Level: level}
	if cfg.Mode == config.ModeProduction {
		return slog.New(slog.NewJSONHandler(out, opts)), nil
	}

	return slog.New(slog.NewTextHandler(out, opts)), nil
}

func toSlogLevel(level config.LogLevel) (slog.Level, error) {
	switch level {
	case config.LogLevelDebug:
		return slog.LevelDebug, nil
	case config.LogLevelInfo:
		return slog.LevelInfo, nil
	case config.LogLevelWarn:
		return slog.LevelWarn, nil
	case config.LogLevelError:
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level: %q", level)
	}
}
