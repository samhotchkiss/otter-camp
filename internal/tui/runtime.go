package tui

import (
	"strconv"
	"strings"
	"time"
)

const (
	defaultColdOpenDuration         = 1100 * time.Millisecond
	defaultTourDuration             = 2 * time.Minute
	defaultMemorySteadyStateBoundMB = 128
)

type RuntimeHints struct {
	ModifierReliabilityUncertain bool
	FirstRun                     bool
	Clock                        func() time.Time
	ColdOpenDuration             time.Duration
	TourDuration                 time.Duration
	MemorySteadyStateBoundBytes  uint64
	DisableMemorySampler         bool
}

func (h RuntimeHints) now() time.Time {
	if h.Clock != nil {
		return h.Clock().UTC()
	}
	return time.Now().UTC()
}

func (h RuntimeHints) coldOpenDuration() time.Duration {
	value := h.ColdOpenDuration
	if value <= 0 {
		value = defaultColdOpenDuration
	}
	if value > 1200*time.Millisecond {
		return 1200 * time.Millisecond
	}
	return value
}

func (h RuntimeHints) tourDuration() time.Duration {
	if h.TourDuration <= 0 {
		return defaultTourDuration
	}
	return h.TourDuration
}

func (h RuntimeHints) memoryBoundBytes() uint64 {
	if h.MemorySteadyStateBoundBytes > 0 {
		return h.MemorySteadyStateBoundBytes
	}
	return uint64(defaultMemorySteadyStateBoundMB) * 1024 * 1024
}

func DetectRuntimeHints(getenv func(string) string) RuntimeHints {
	tmux := strings.TrimSpace(getenv("TMUX")) != "" || strings.TrimSpace(getenv("STY")) != ""
	term := strings.ToLower(strings.TrimSpace(getenv("TERM")))
	if strings.Contains(term, "screen") || strings.Contains(term, "tmux") {
		tmux = true
	}

	memoryBound := uint64(defaultMemorySteadyStateBoundMB) * 1024 * 1024
	rawMemoryBound := strings.TrimSpace(getenv("OTTERCAMP_TUI_MEMORY_BOUND_MB"))
	if rawMemoryBound != "" {
		if parsed, err := strconv.ParseUint(rawMemoryBound, 10, 64); err == nil && parsed > 0 {
			memoryBound = parsed * 1024 * 1024
		}
	}

	return RuntimeHints{
		ModifierReliabilityUncertain: tmux,
		ColdOpenDuration:             defaultColdOpenDuration,
		TourDuration:                 defaultTourDuration,
		MemorySteadyStateBoundBytes:  memoryBound,
	}
}
