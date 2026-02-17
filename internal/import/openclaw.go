package importer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var ErrOpenClawNotFound = errors.New("openclaw installation not found")

var identityFileNames = []string{"SOUL.md", "IDENTITY.md", "MEMORY.md", "TOOLS.md"}

type DetectOpenClawOptions struct {
	HomeDir string
}

type OpenClawInstallation struct {
	RootDir       string
	ConfigPath    string
	SessionsDir   string
	WorkspacesDir string
	Gateway       OpenClawGatewayConfig
	Agents        []OpenClawAgentWorkspace
}

type OpenClawGatewayConfig struct {
	Host  string
	Port  int
	Token string
}

type OpenClawAgentWorkspace struct {
	ID           string
	Name         string
	WorkspaceDir string
}

type ImportedAgentIdentity struct {
	ID           string
	Name         string
	WorkspaceDir string
	Soul         string
	Identity     string
	Memory       string
	Tools        string
	SourceFiles  map[string]string
}

type EnsureOpenClawRequiredAgentsOptions struct {
	IncludeChameleon bool
}

type EnsureOpenClawRequiredAgentsResult struct {
	Updated            bool
	AddedElephant      bool
	AddedChameleon     bool
	MemoryWorkspaceDir string
}

type openClawConfigFile struct {
	Gateway        map[string]any  `json:"gateway"`
	SessionsDir    string          `json:"sessions_dir"`
	SessionsDir2   string          `json:"sessionsDir"`
	WorkspacesDir  string          `json:"workspaces_dir"`
	WorkspacesDir2 string          `json:"workspacesDir"`
	Host           string          `json:"host"`
	Port           int             `json:"port"`
	Token          string          `json:"token"`
	APIKey         string          `json:"api_key"`
	Agents         json.RawMessage `json:"agents"`
	Slots          json.RawMessage `json:"slots"`
}

type openClawAgentCandidate struct {
	ID        string
	Name      string
	Workspace string
}

var elephantSOULTemplate = strings.TrimSpace(`# Ellie (Elephant)

You are Ellie, Otter Camp's memory and retrieval specialist. Your job is to read agent
session logs, extract what is worth remembering, and distribute that context through memory
and knowledge commands.

Ask Ellie First:
- Before finalizing decisions, check memory retrieval context first.
- If relevant context is missing, say so explicitly and request clarification.

Zero Hallucination:
- Never invent missing context.
- If retrieval has no supporting evidence, return "no relevant memory found" and stop.

You run quietly and prioritize signal over noise.
`)

var elephantStateTemplate = map[string]any{
	"file_offsets": map[string]any{},
	"last_run":     nil,
	"extraction_stats": map[string]any{
		"total_runs":             0,
		"total_memories_written": 0,
		"total_knowledge_shared": 0,
		"last_run_duration_ms":   0,
	},
}

func EnsureOpenClawRequiredAgents(
	install *OpenClawInstallation,
	opts EnsureOpenClawRequiredAgentsOptions,
) (EnsureOpenClawRequiredAgentsResult, error) {
	if install == nil {
		return EnsureOpenClawRequiredAgentsResult{}, errors.New("installation is required")
	}
	configPath := strings.TrimSpace(install.ConfigPath)
	if configPath == "" || !isFile(configPath) {
		return EnsureOpenClawRequiredAgentsResult{}, nil
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		return EnsureOpenClawRequiredAgentsResult{}, err
	}
	root := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &root); err != nil {
			return EnsureOpenClawRequiredAgentsResult{}, err
		}
	}

	result := EnsureOpenClawRequiredAgentsResult{}
	switch agents := root["agents"].(type) {
	case map[string]any:
		if list, ok := agents["list"].([]any); ok {
			updatedList, addedMemory, addedChameleon := ensureListAgentSlots(list, opts)
			agents["list"] = updatedList
			result.AddedElephant = addedMemory
			result.AddedChameleon = addedChameleon
			result.Updated = addedMemory || addedChameleon
		} else {
			addedMemory := false
			if !mapHasAnyAgentID(agents, "elephant", "ellie") {
				addedMemory = ensureMapAgentSlot(agents, "elephant", buildElephantSlot())
			}
			addedChameleon := false
			if opts.IncludeChameleon {
				addedChameleon = ensureMapAgentSlot(agents, "chameleon", buildChameleonSlot())
			}
			result.AddedElephant = addedMemory
			result.AddedChameleon = addedChameleon
			result.Updated = addedMemory || addedChameleon
		}
		root["agents"] = agents
	case []any:
		updated, addedMemory, addedChameleon := ensureListAgentSlots(agents, opts)
		result.Updated = addedMemory || addedChameleon
		result.AddedElephant = addedMemory
		result.AddedChameleon = addedChameleon
		root["agents"] = updated
	default:
		agentsObj := map[string]any{
			"list": []any{},
		}
		updated, addedMemory, addedChameleon := ensureListAgentSlots(agentsObj["list"].([]any), opts)
		agentsObj["list"] = updated
		root["agents"] = agentsObj
		result.Updated = addedMemory || addedChameleon
		result.AddedElephant = addedMemory
		result.AddedChameleon = addedChameleon
	}

	if result.AddedElephant {
		workspaceDir, err := ensureElephantWorkspace(install.RootDir)
		if err != nil {
			return EnsureOpenClawRequiredAgentsResult{}, err
		}
		result.MemoryWorkspaceDir = workspaceDir
	}

	if !result.Updated {
		return result, nil
	}

	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return EnsureOpenClawRequiredAgentsResult{}, err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(configPath, encoded, 0o644); err != nil {
		return EnsureOpenClawRequiredAgentsResult{}, err
	}
	return result, nil
}

func ensureListAgentSlots(
	agents []any,
	opts EnsureOpenClawRequiredAgentsOptions,
) (updated []any, addedMemory bool, addedChameleon bool) {
	updated = append([]any{}, agents...)
	if !listHasAnyAgentID(updated, "elephant", "ellie") {
		updated = append(updated, buildElephantSlot())
		addedMemory = true
	}
	if opts.IncludeChameleon && !listHasAgentID(updated, "chameleon") {
		updated = append(updated, buildChameleonSlot())
		addedChameleon = true
	}
	return updated, addedMemory, addedChameleon
}

func listHasAgentID(agents []any, id string) bool {
	target := strings.TrimSpace(strings.ToLower(id))
	if target == "" {
		return false
	}
	for _, item := range agents {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		candidate := strings.ToLower(strings.TrimSpace(lookupString(record, "id", "slug", "agent_id", "agent")))
		if candidate == target {
			return true
		}
	}
	return false
}

func listHasAnyAgentID(agents []any, ids ...string) bool {
	for _, id := range ids {
		if listHasAgentID(agents, id) {
			return true
		}
	}
	return false
}

func ensureMapAgentSlot(agents map[string]any, id string, value map[string]any) bool {
	target := strings.TrimSpace(strings.ToLower(id))
	if target == "" {
		return false
	}
	for key, existing := range agents {
		if strings.EqualFold(strings.TrimSpace(key), target) {
			return false
		}
		record, ok := existing.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(lookupString(record, "id", "slug", "agent_id", "agent")), target) {
			return false
		}
	}
	agents[id] = value
	return true
}

func mapHasAnyAgentID(agents map[string]any, ids ...string) bool {
	for _, id := range ids {
		target := strings.TrimSpace(strings.ToLower(id))
		if target == "" {
			continue
		}
		for key, existing := range agents {
			if strings.EqualFold(strings.TrimSpace(key), target) {
				return true
			}
			record, ok := existing.(map[string]any)
			if !ok {
				continue
			}
			candidate := strings.ToLower(strings.TrimSpace(lookupString(record, "id", "slug", "agent_id", "agent")))
			if candidate == target {
				return true
			}
		}
	}
	return false
}

func buildElephantSlot() map[string]any {
	return map[string]any{
		"id":        "elephant",
		"name":      "Ellie",
		"model":     "anthropic/claude-sonnet-4-20250514",
		"workspace": "~/.openclaw/workspace-elephant",
		"thinking":  "low",
		"channels":  []any{},
	}
}

func buildChameleonSlot() map[string]any {
	return map[string]any{
		"id":        "chameleon",
		"name":      "Chameleon",
		"workspace": "~/.openclaw/workspace-chameleon",
	}
}

func ensureElephantWorkspace(rootDir string) (string, error) {
	base := strings.TrimSpace(rootDir)
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, ".openclaw")
		}
	}
	if base == "" {
		return "", errors.New("openclaw root dir is required for memory workspace setup")
	}
	workspaceDir := filepath.Join(base, "workspace-elephant")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return "", err
	}
	info, err := os.Lstat(workspaceDir)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("workspace path is not a real directory: %s", workspaceDir)
	}

	if err := writeFileIfMissing(filepath.Join(workspaceDir, "SOUL.md"), []byte(elephantSOULTemplate+"\n"), 0o644); err != nil {
		return "", err
	}

	stateRaw, err := json.MarshalIndent(elephantStateTemplate, "", "  ")
	if err != nil {
		return "", err
	}
	stateRaw = append(stateRaw, '\n')
	if err := writeFileIfMissing(filepath.Join(workspaceDir, "elephant-state.json"), stateRaw, 0o644); err != nil {
		return "", err
	}

	return workspaceDir, nil
}

func writeFileIfMissing(path string, content []byte, mode fs.FileMode) error {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink: %s", path)
		}
		if info.Mode().IsRegular() {
			return nil
		}
		return fmt.Errorf("path already exists and is not a regular file: %s", path)
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		existing, statErr := os.Lstat(path)
		if statErr != nil {
			return err
		}
		if existing.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink: %s", path)
		}
		if existing.Mode().IsRegular() {
			return nil
		}
		return fmt.Errorf("path already exists and is not a regular file: %s", path)
	}
	defer file.Close()

	if _, err := file.Write(content); err != nil {
		return err
	}
	return nil
}

func DetectOpenClawInstallation(opts DetectOpenClawOptions) (*OpenClawInstallation, error) {
	rootDir, err := detectOpenClawRoot(opts)
	if err != nil {
		return nil, err
	}

	configPath := detectOpenClawConfigPath(rootDir)
	config, err := loadOpenClawConfig(configPath)
	if err != nil {
		return nil, err
	}

	workspacesDir := resolveConfiguredPath(rootDir, firstNonEmpty(config.WorkspacesDir, config.WorkspacesDir2))
	if workspacesDir == "" {
		workspacesDir = detectWorkspaceRoot(rootDir)
	}

	sessionsDir := resolveConfiguredPath(rootDir, firstNonEmpty(config.SessionsDir, config.SessionsDir2))
	if sessionsDir == "" {
		sessionsDir = detectFirstExistingDir(filepath.Join(rootDir, "sessions"))
	}

	agents, err := collectOpenClawAgents(rootDir, workspacesDir, config)
	if err != nil {
		return nil, err
	}

	return &OpenClawInstallation{
		RootDir:       rootDir,
		ConfigPath:    configPath,
		SessionsDir:   sessionsDir,
		WorkspacesDir: workspacesDir,
		Gateway:       parseGatewayConfig(config),
		Agents:        agents,
	}, nil
}

func ImportOpenClawAgentIdentities(install *OpenClawInstallation) ([]ImportedAgentIdentity, error) {
	if install == nil {
		return nil, errors.New("installation is required")
	}
	guard, err := NewOpenClawSourceGuard(install.RootDir)
	if err != nil {
		return nil, err
	}

	identities := make([]ImportedAgentIdentity, 0, len(install.Agents))
	for _, agent := range install.Agents {
		payload := ImportedAgentIdentity{
			ID:           agent.ID,
			Name:         agent.Name,
			WorkspaceDir: agent.WorkspaceDir,
			SourceFiles:  make(map[string]string, len(identityFileNames)),
		}

		for _, fileName := range identityFileNames {
			content, sourcePath, found, err := readIdentityFileGuarded(agent.WorkspaceDir, fileName, guard)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			payload.SourceFiles[fileName] = sourcePath

			switch fileName {
			case "SOUL.md":
				payload.Soul = content
			case "IDENTITY.md":
				payload.Identity = content
			case "MEMORY.md":
				payload.Memory = content
			case "TOOLS.md":
				payload.Tools = content
			}
		}

		identities = append(identities, payload)
	}

	sort.Slice(identities, func(i, j int) bool {
		return identities[i].ID < identities[j].ID
	})
	return identities, nil
}

func detectOpenClawRoot(opts DetectOpenClawOptions) (string, error) {
	candidates := make([]string, 0, 3)
	if strings.TrimSpace(opts.HomeDir) != "" {
		candidates = append(candidates, opts.HomeDir)
	}
	if env := strings.TrimSpace(os.Getenv("OPENCLAW_HOME")); env != "" {
		candidates = append(candidates, env)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		candidates = append(candidates, filepath.Join(home, ".openclaw"))
	}

	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		expanded := expandHomeDir(candidate)
		cleaned := filepath.Clean(expanded)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		if isDirectory(cleaned) {
			return cleaned, nil
		}
	}

	return "", ErrOpenClawNotFound
}

func detectOpenClawConfigPath(rootDir string) string {
	return detectFirstExistingFile(
		filepath.Join(rootDir, "openclaw.json"),
		filepath.Join(rootDir, "config", "openclaw.json"),
	)
}

func loadOpenClawConfig(configPath string) (openClawConfigFile, error) {
	if strings.TrimSpace(configPath) == "" {
		return openClawConfigFile{}, nil
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		return openClawConfigFile{}, err
	}

	var cfg openClawConfigFile
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return openClawConfigFile{}, err
	}
	return cfg, nil
}

func parseGatewayConfig(config openClawConfigFile) OpenClawGatewayConfig {
	raw := config.Gateway
	host := lookupString(raw, "host", "hostname")
	port := lookupInt(raw, "port")
	token := lookupString(raw, "token", "api_key", "apiKey")

	urlHost, urlPort := parseGatewayURL(lookupString(raw, "url", "ws_url", "wsUrl", "target"))
	if host == "" {
		host = urlHost
	}
	if port <= 0 {
		port = urlPort
	}
	if host == "" {
		host = strings.TrimSpace(config.Host)
	}
	if port <= 0 {
		port = config.Port
	}
	if token == "" {
		token = firstNonEmpty(strings.TrimSpace(config.Token), strings.TrimSpace(config.APIKey))
	}

	return OpenClawGatewayConfig{
		Host:  host,
		Port:  port,
		Token: token,
	}
}

func parseGatewayURL(raw string) (string, int) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", 0
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", 0
	}

	port := 0
	if parsed.Port() != "" {
		port = lookupInt(map[string]any{"port": parsed.Port()}, "port")
	}
	if port <= 0 {
		switch strings.ToLower(parsed.Scheme) {
		case "ws", "http":
			port = 80
		case "wss", "https":
			port = 443
		}
	}
	return parsed.Hostname(), port
}

func collectOpenClawAgents(rootDir, workspaceRoot string, config openClawConfigFile) ([]OpenClawAgentWorkspace, error) {
	candidates := make([]openClawAgentCandidate, 0)
	candidates = append(candidates, extractAgentCandidates(config.Agents)...)
	candidates = append(candidates, extractAgentCandidates(config.Slots)...)

	agents := make([]OpenClawAgentWorkspace, 0)
	byWorkspace := map[string]struct{}{}

	for _, candidate := range candidates {
		workspace := resolveWorkspacePath(rootDir, workspaceRoot, candidate)
		if workspace == "" {
			continue
		}
		if _, ok := byWorkspace[workspace]; ok {
			continue
		}
		byWorkspace[workspace] = struct{}{}

		id := strings.TrimSpace(candidate.ID)
		if id == "" {
			id = filepath.Base(workspace)
		}
		name := strings.TrimSpace(candidate.Name)
		if name == "" {
			name = id
		}
		agents = append(agents, OpenClawAgentWorkspace{
			ID:           id,
			Name:         name,
			WorkspaceDir: workspace,
		})
	}

	if workspaceRoot != "" {
		entries, err := os.ReadDir(workspaceRoot)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			// Skip directories that don't look like agent workspaces.
			// A valid workspace has at least one identity file (SOUL.md or IDENTITY.md).
			name := entry.Name()
			if isSkippableWorkspaceDir(name) {
				continue
			}
			workspace := filepath.Join(workspaceRoot, name)
			clean := filepath.Clean(workspace)
			if _, ok := byWorkspace[clean]; ok {
				continue
			}
			if !hasAnyIdentityFile(clean) {
				continue
			}
			byWorkspace[clean] = struct{}{}
			agents = append(agents, OpenClawAgentWorkspace{
				ID:           name,
				Name:         name,
				WorkspaceDir: clean,
			})
		}
	}

	sort.Slice(agents, func(i, j int) bool {
		return agents[i].ID < agents[j].ID
	})
	return agents, nil
}

func extractAgentCandidates(raw json.RawMessage) []openClawAgentCandidate {
	if len(raw) == 0 {
		return nil
	}

	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}

	candidates := make([]openClawAgentCandidate, 0)
	switch typed := parsed.(type) {
	case map[string]any:
		// Handle {"list": [...], "defaults": {...}} format (OpenClaw agents config)
		if listVal, ok := typed["list"]; ok {
			if listArr, ok := listVal.([]any); ok {
				for _, value := range listArr {
					candidate, ok := decodeAgentCandidate("", value)
					if ok {
						candidates = append(candidates, candidate)
					}
				}
			}
		}
		// Also try each key as an agent entry (legacy map format)
		for key, value := range typed {
			if key == "list" || key == "defaults" {
				continue
			}
			candidate, ok := decodeAgentCandidate(key, value)
			if ok {
				candidates = append(candidates, candidate)
			}
		}
	case []any:
		for _, value := range typed {
			candidate, ok := decodeAgentCandidate("", value)
			if ok {
				candidates = append(candidates, candidate)
			}
		}
	}

	return candidates
}

func decodeAgentCandidate(key string, value any) (openClawAgentCandidate, bool) {
	record, ok := value.(map[string]any)
	if !ok {
		return openClawAgentCandidate{}, false
	}

	id := firstNonEmpty(
		lookupString(record, "id"),
		lookupString(record, "slug"),
		lookupString(record, "agent"),
		lookupString(record, "agent_id"),
		strings.TrimSpace(key),
	)
	name := firstNonEmpty(
		lookupString(record, "name"),
		lookupString(record, "display_name"),
		lookupString(record, "displayName"),
		id,
	)

	workspace := firstNonEmpty(
		lookupString(record, "workspace"),
		lookupString(record, "workspace_dir"),
		lookupString(record, "workspaceDir"),
		lookupString(record, "path"),
		lookupString(record, "dir"),
	)
	if workspace == "" {
		if nested, ok := record["workspace"].(map[string]any); ok {
			workspace = firstNonEmpty(
				lookupString(nested, "path"),
				lookupString(nested, "dir"),
				lookupString(nested, "root"),
			)
		}
	}

	if id == "" && workspace == "" {
		return openClawAgentCandidate{}, false
	}
	return openClawAgentCandidate{
		ID:        id,
		Name:      name,
		Workspace: workspace,
	}, true
}

func resolveWorkspacePath(rootDir, workspaceRoot string, candidate openClawAgentCandidate) string {
	if strings.TrimSpace(candidate.Workspace) != "" {
		resolved := resolveConfiguredPath(rootDir, candidate.Workspace)
		if resolved != "" && isDirectory(resolved) {
			return resolved
		}
	}

	id := strings.TrimSpace(candidate.ID)
	if id != "" {
		// Try workspaceRoot/id (e.g., workspaces/main)
		if workspaceRoot != "" {
			path := filepath.Join(workspaceRoot, id)
			if isDirectory(path) {
				return filepath.Clean(path)
			}
		}
		// Try rootDir/workspace-id (e.g., ~/.openclaw/workspace-main)
		// This is the common OpenClaw layout where each agent has workspace-<slug>
		path := filepath.Join(rootDir, "workspace-"+id)
		if isDirectory(path) {
			return filepath.Clean(path)
		}
		// Special case: "main" agent often uses bare "workspace/" directory
		if id == "main" {
			barePath := filepath.Join(rootDir, "workspace")
			if isDirectory(barePath) {
				return filepath.Clean(barePath)
			}
		}
	}

	return ""
}

var skippableWorkspaceDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"scripts":      {},
	"memory":       {},
	"logs":         {},
	"tmp":          {},
	"cache":        {},
}

func isSkippableWorkspaceDir(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if _, ok := skippableWorkspaceDirs[lower]; ok {
		return true
	}
	// Skip hidden directories
	if strings.HasPrefix(lower, ".") {
		return true
	}
	return false
}

func hasAnyIdentityFile(workspaceDir string) bool {
	for _, name := range identityFileNames {
		path := filepath.Join(workspaceDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

func detectWorkspaceRoot(rootDir string) string {
	return detectFirstExistingDir(
		filepath.Join(rootDir, "workspaces"),
		filepath.Join(rootDir, "workspace"),
		filepath.Join(rootDir, "agents"),
	)
}

func readIdentityFile(workspaceDir, fileName string) (string, string, bool, error) {
	return readIdentityFileGuarded(workspaceDir, fileName, nil)
}

func readIdentityFileGuarded(workspaceDir, fileName string, guard *OpenClawSourceGuard) (string, string, bool, error) {
	base := filepath.Clean(workspaceDir)
	target := filepath.Clean(filepath.Join(base, fileName))
	if !isWithinDir(base, target) {
		return "", "", false, nil
	}
	return readIdentityFileWithGuard(base, target, guard)
}

func readIdentityFileWithGuard(_ string, target string, guard *OpenClawSourceGuard) (string, string, bool, error) {
	if guard != nil {
		if err := guard.ValidateReadPath(target); err != nil {
			return "", "", false, err
		}
	}

	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", "", false, nil
	}

	content, err := os.ReadFile(target)
	if err != nil {
		return "", "", false, err
	}

	return normalizeIdentityText(string(content)), target, true, nil
}

func normalizeIdentityText(value string) string {
	normalized := strings.ReplaceAll(value, "\r\n", "\n")
	return strings.TrimSpace(normalized)
}

func resolveConfiguredPath(rootDir, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = expandHomeDir(value)
	if filepath.IsAbs(value) {
		if isDirectory(value) {
			return filepath.Clean(value)
		}
		return ""
	}
	joined := filepath.Join(rootDir, value)
	if isDirectory(joined) {
		return filepath.Clean(joined)
	}
	return ""
}

func expandHomeDir(path string) string {
	path = strings.TrimSpace(path)
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

func isWithinDir(base, target string) bool {
	base = filepath.Clean(base)
	target = filepath.Clean(target)
	if base == target {
		return true
	}
	return strings.HasPrefix(target, base+string(os.PathSeparator))
}

func detectFirstExistingDir(candidates ...string) string {
	for _, candidate := range candidates {
		if isDirectory(candidate) {
			return filepath.Clean(candidate)
		}
	}
	return ""
}

func detectFirstExistingFile(candidates ...string) string {
	for _, candidate := range candidates {
		if isFile(candidate) {
			return filepath.Clean(candidate)
		}
	}
	return ""
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func lookupString(record map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := record[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func lookupInt(record map[string]any, key string) int {
	value, ok := record[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return 0
}
