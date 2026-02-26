package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const stateFileName = "tui-state.json"

type UIState struct {
	LastActiveView        string     `json:"last_active_view"`
	LastActiveChatSession string     `json:"last_active_chat_session"`
	PanelProportions      [3]float64 `json:"panel_proportions"`
	SidebarVisible        bool       `json:"sidebar_visible"`
}

func DefaultState() UIState {
	return UIState{
		LastActiveView:        "main",
		LastActiveChatSession: "",
		PanelProportions:      [3]float64{0.22, 0.46, 0.32},
		SidebarVisible:        false,
	}
}

func ResolveStatePath(getenv func(string) string, homeDir func() (string, error)) (string, error) {
	xdgConfigHome := strings.TrimSpace(getenv("XDG_CONFIG_HOME"))
	if xdgConfigHome != "" {
		return filepath.Join(xdgConfigHome, "ottercamp", stateFileName), nil
	}

	home, err := homeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	home = strings.TrimSpace(home)
	if home == "" {
		return "", fmt.Errorf("resolve home directory: empty path")
	}
	return filepath.Join(home, ".config", "ottercamp", stateFileName), nil
}

func LoadState(path string) (UIState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultState(), nil
		}
		return UIState{}, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return DefaultState(), nil
	}

	var state UIState
	if err := json.Unmarshal(raw, &state); err != nil {
		return UIState{}, err
	}
	return normalizeState(state), nil
}

func StateFileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func SaveState(path string, state UIState) error {
	normalized := normalizeState(state)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	encoded, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, encoded, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func normalizeState(state UIState) UIState {
	defaults := DefaultState()
	normalized := state
	if _, ok := panelFromView(state.LastActiveView); !ok {
		normalized.LastActiveView = defaults.LastActiveView
	}
	normalized.PanelProportions = normalizeProportions(state.PanelProportions, defaults.PanelProportions)
	return normalized
}

func normalizeProportions(value [3]float64, fallback [3]float64) [3]float64 {
	valid := true
	sum := 0.0
	for _, proportion := range value {
		if proportion <= 0 {
			valid = false
			break
		}
		sum += proportion
	}
	if !valid || sum <= 0 {
		return fallback
	}

	return [3]float64{
		value[0] / sum,
		value[1] / sum,
		value[2] / sum,
	}
}
