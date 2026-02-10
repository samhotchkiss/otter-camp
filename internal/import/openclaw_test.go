package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
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
