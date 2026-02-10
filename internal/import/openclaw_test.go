package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenClawAgentImportDetectsInstallationAndParsesIdentities(t *testing.T) {
	root := t.TempDir()
	mainWorkspace := filepath.Join(root, "workspaces", "main")
	secondWorkspace := filepath.Join(root, "workspaces", "2b")
	require.NoError(t, os.MkdirAll(mainWorkspace, 0o755))
	require.NoError(t, os.MkdirAll(secondWorkspace, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sessions"), 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(mainWorkspace, "SOUL.md"), []byte("  Chief of Staff  \n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mainWorkspace, "IDENTITY.md"), []byte("Frank\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mainWorkspace, "MEMORY.md"), []byte("Long term memory"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mainWorkspace, "TOOLS.md"), []byte("tool_a\ntool_b"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(secondWorkspace, "IDENTITY.md"), []byte("Derek"), 0o644))

	config := map[string]any{
		"gateway": map[string]any{
			"host":  "127.0.0.1",
			"port":  18791,
			"token": "secret-token",
		},
		"sessions_dir":   "./sessions",
		"workspaces_dir": "./workspaces",
		"agents": map[string]any{
			"main": map[string]any{
				"name":          "Frank",
				"workspace_dir": "./workspaces/main",
			},
			"2b": map[string]any{
				"display_name": "Derek",
				"workspace":    "./workspaces/2b",
			},
		},
	}
	configBytes, err := json.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "openclaw.json"), configBytes, 0o644))

	install, err := DetectOpenClawInstallation(DetectOpenClawOptions{HomeDir: root})
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(root), install.RootDir)
	require.Equal(t, filepath.Join(root, "openclaw.json"), install.ConfigPath)
	require.Equal(t, filepath.Join(root, "sessions"), install.SessionsDir)
	require.Equal(t, filepath.Join(root, "workspaces"), install.WorkspacesDir)
	require.Equal(t, "127.0.0.1", install.Gateway.Host)
	require.Equal(t, 18791, install.Gateway.Port)
	require.Equal(t, "secret-token", install.Gateway.Token)
	require.Len(t, install.Agents, 2)

	identities, err := ImportOpenClawAgentIdentities(install)
	require.NoError(t, err)
	require.Len(t, identities, 2)

	byID := map[string]ImportedAgentIdentity{}
	for _, item := range identities {
		byID[item.ID] = item
	}

	main := byID["main"]
	require.Equal(t, "Frank", main.Name)
	require.Equal(t, "Chief of Staff", main.Soul)
	require.Equal(t, "Frank", main.Identity)
	require.Equal(t, "Long term memory", main.Memory)
	require.Equal(t, "tool_a\ntool_b", main.Tools)
	require.Len(t, main.SourceFiles, 4)

	second := byID["2b"]
	require.Equal(t, "Derek", second.Name)
	require.Equal(t, "", second.Soul)
	require.Equal(t, "Derek", second.Identity)
	require.Equal(t, "", second.Memory)
	require.Equal(t, "", second.Tools)
	require.Len(t, second.SourceFiles, 1)
}

func TestOpenClawAgentImportFallsBackToWorkspaceDiscovery(t *testing.T) {
	root := t.TempDir()
	frankWorkspace := filepath.Join(root, "workspaces", "frank")
	novaWorkspace := filepath.Join(root, "workspaces", "nova")
	require.NoError(t, os.MkdirAll(frankWorkspace, 0o755))
	require.NoError(t, os.MkdirAll(novaWorkspace, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(frankWorkspace, "SOUL.md"), []byte("Frank soul"), 0o644))

	install, err := DetectOpenClawInstallation(DetectOpenClawOptions{HomeDir: root})
	require.NoError(t, err)
	require.Equal(t, "", install.ConfigPath)
	require.Equal(t, filepath.Join(root, "workspaces"), install.WorkspacesDir)
	require.Len(t, install.Agents, 2)

	identities, err := ImportOpenClawAgentIdentities(install)
	require.NoError(t, err)
	require.Len(t, identities, 2)

	byID := map[string]ImportedAgentIdentity{}
	for _, item := range identities {
		byID[item.ID] = item
	}
	require.Equal(t, "Frank soul", byID["frank"].Soul)
	require.Equal(t, "", byID["nova"].Soul)
}

func TestOpenClawAgentImportSkipsNonRegularIdentityFiles(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspaces", "main")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(workspace, "SOUL.md"), 0o755))

	config := map[string]any{
		"workspaces_dir": "./workspaces",
		"agents": map[string]any{
			"main": map[string]any{
				"workspace": "./workspaces/main",
			},
		},
	}
	configBytes, err := json.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "openclaw.json"), configBytes, 0o644))

	install, err := DetectOpenClawInstallation(DetectOpenClawOptions{HomeDir: root})
	require.NoError(t, err)
	require.Len(t, install.Agents, 1)

	identities, err := ImportOpenClawAgentIdentities(install)
	require.NoError(t, err)
	require.Len(t, identities, 1)
	require.Equal(t, "", identities[0].Soul)
}

func TestEnsureOpenClawRequiredAgents(t *testing.T) {
	t.Run("adds missing memory and chameleon agents without mutating existing entries", func(t *testing.T) {
		root := t.TempDir()
		configPath := filepath.Join(root, "openclaw.json")
		config := map[string]any{
			"gateway": map[string]any{
				"host": "127.0.0.1",
				"port": 18791,
			},
			"agents": map[string]any{
				"list": []any{
					map[string]any{
						"id":        "main",
						"name":      "Main Agent",
						"workspace": "~/.openclaw/workspace",
						"model":     "anthropic/claude-sonnet-4-20250514",
						"default":   true,
					},
				},
			},
		}
		raw, err := json.Marshal(config)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(configPath, raw, 0o644))

		result, err := EnsureOpenClawRequiredAgents(&OpenClawInstallation{
			RootDir:    root,
			ConfigPath: configPath,
		}, EnsureOpenClawRequiredAgentsOptions{
			IncludeChameleon: true,
		})
		require.NoError(t, err)
		require.True(t, result.Updated)
		require.True(t, result.AddedMemoryAgent)
		require.True(t, result.AddedChameleon)

		updatedRaw, err := os.ReadFile(configPath)
		require.NoError(t, err)
		var updated map[string]any
		require.NoError(t, json.Unmarshal(updatedRaw, &updated))

		agentsObj, ok := updated["agents"].(map[string]any)
		require.True(t, ok)
		list, ok := agentsObj["list"].([]any)
		require.True(t, ok)
		require.Len(t, list, 3)

		entryByID := map[string]map[string]any{}
		for _, item := range list {
			record, ok := item.(map[string]any)
			require.True(t, ok)
			id, _ := record["id"].(string)
			entryByID[id] = record
		}

		main := entryByID["main"]
		require.Equal(t, "Main Agent", main["name"])
		require.Equal(t, "~/.openclaw/workspace", main["workspace"])
		require.Equal(t, "anthropic/claude-sonnet-4-20250514", main["model"])
		require.Equal(t, true, main["default"])

		memoryAgent := entryByID["memory-agent"]
		require.Equal(t, "Memory Agent", memoryAgent["name"])
		require.Equal(t, "anthropic/claude-sonnet-4-20250514", memoryAgent["model"])
		require.Equal(t, "~/.openclaw/workspace-memory-agent", memoryAgent["workspace"])
		require.Equal(t, "low", memoryAgent["thinking"])
		channels, ok := memoryAgent["channels"].([]any)
		require.True(t, ok)
		require.Len(t, channels, 0)

		chameleon := entryByID["chameleon"]
		require.Equal(t, "Chameleon", chameleon["name"])
		require.Equal(t, "~/.openclaw/workspace-chameleon", chameleon["workspace"])

		require.DirExists(t, filepath.Join(root, "workspace-memory-agent"))
		soulRaw, err := os.ReadFile(filepath.Join(root, "workspace-memory-agent", "SOUL.md"))
		require.NoError(t, err)
		require.Contains(t, string(soulRaw), "# Memory Agent")

		stateRaw, err := os.ReadFile(filepath.Join(root, "workspace-memory-agent", "memory-agent-state.json"))
		require.NoError(t, err)
		require.True(t, strings.Contains(string(stateRaw), "\"file_offsets\""))

		second, err := EnsureOpenClawRequiredAgents(&OpenClawInstallation{
			RootDir:    root,
			ConfigPath: configPath,
		}, EnsureOpenClawRequiredAgentsOptions{
			IncludeChameleon: true,
		})
		require.NoError(t, err)
		require.False(t, second.Updated)
		require.False(t, second.AddedMemoryAgent)
		require.False(t, second.AddedChameleon)
	})

	t.Run("can add only memory agent when chameleon is disabled", func(t *testing.T) {
		root := t.TempDir()
		configPath := filepath.Join(root, "openclaw.json")
		config := map[string]any{
			"agents": map[string]any{
				"list": []any{
					map[string]any{
						"id":   "main",
						"name": "Main Agent",
					},
				},
			},
		}
		raw, err := json.Marshal(config)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(configPath, raw, 0o644))

		result, err := EnsureOpenClawRequiredAgents(&OpenClawInstallation{
			RootDir:    root,
			ConfigPath: configPath,
		}, EnsureOpenClawRequiredAgentsOptions{
			IncludeChameleon: false,
		})
		require.NoError(t, err)
		require.True(t, result.Updated)
		require.True(t, result.AddedMemoryAgent)
		require.False(t, result.AddedChameleon)

		updatedRaw, err := os.ReadFile(configPath)
		require.NoError(t, err)
		var updated map[string]any
		require.NoError(t, json.Unmarshal(updatedRaw, &updated))
		agentsObj, ok := updated["agents"].(map[string]any)
		require.True(t, ok)
		list, ok := agentsObj["list"].([]any)
		require.True(t, ok)
		require.Len(t, list, 2)
		require.True(t, listHasAgentID(list, "memory-agent"))
		require.False(t, listHasAgentID(list, "chameleon"))
	})
}
