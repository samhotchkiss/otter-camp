package config

import (
	"fmt"
	"os"
	"strings"
)

type Mode string

const (
	ModeProduction  Mode = "production"
	ModeDevelopment Mode = "development"
	ModeTest        Mode = "test"
)

type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

type Config struct {
	Mode     Mode
	LogLevel LogLevel
	Addr     string
}

func Load() (Config, error) {
	return LoadFromLookup(os.LookupEnv)
}

func LoadFromLookup(lookup func(string) (string, bool)) (Config, error) {
	modeValue, ok := lookup("OTTERCAMP_MODE")
	if !ok || strings.TrimSpace(modeValue) == "" {
		return Config{}, fmt.Errorf("OTTERCAMP_MODE is required")
	}

	mode, err := parseMode(modeValue)
	if err != nil {
		return Config{}, err
	}

	logLevel := LogLevelInfo
	if raw, ok := lookup("OTTERCAMP_LOG_LEVEL"); ok && strings.TrimSpace(raw) != "" {
		parsed, err := parseLogLevel(raw)
		if err != nil {
			return Config{}, err
		}
		logLevel = parsed
	}

	addr := ":4110"
	if raw, ok := lookup("OTTERCAMP_ADDR"); ok && strings.TrimSpace(raw) != "" {
		addr = strings.TrimSpace(raw)
	}

	return Config{
		Mode:     mode,
		LogLevel: logLevel,
		Addr:     addr,
	}, nil
}

func parseMode(raw string) (Mode, error) {
	mode := Mode(strings.ToLower(strings.TrimSpace(raw)))
	switch mode {
	case ModeProduction, ModeDevelopment, ModeTest:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid OTTERCAMP_MODE %q (expected production|development|test)", raw)
	}
}

func parseLogLevel(raw string) (LogLevel, error) {
	level := LogLevel(strings.ToLower(strings.TrimSpace(raw)))
	switch level {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
		return level, nil
	default:
		return "", fmt.Errorf("invalid OTTERCAMP_LOG_LEVEL %q (expected debug|info|warn|error)", raw)
	}
}
