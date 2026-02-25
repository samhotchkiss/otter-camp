package oclog

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/samhotchkiss/otter-camp/internal/config"
	"github.com/samhotchkiss/otter-camp/internal/security"
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
	scrubber := security.NewSecretScrubber()
	if cfg.Mode == config.ModeProduction {
		base := slog.NewJSONHandler(out, opts)
		return slog.New(security.NewScrubbingHandler(base, scrubber)), nil
	}

	base := slog.NewTextHandler(out, opts)
	return slog.New(security.NewScrubbingHandler(base, scrubber)), nil
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
