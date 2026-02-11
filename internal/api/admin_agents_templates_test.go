package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildCreateAgentTemplateInput(t *testing.T) {
	t.Parallel()

	t.Run("uses profile defaults with placeholder replacement", func(t *testing.T) {
		t.Parallel()
		req := adminAgentCreateRequest{
			DisplayName: "Marcus",
			ProfileID:   "marcus",
		}
		templateInput, err := buildCreateAgentTemplateInput(req)
		require.NoError(t, err)
		require.Contains(t, templateInput.Soul, "Marcus")
		require.Contains(t, templateInput.Identity, "Marcus")
		require.NotEmpty(t, templateInput.Avatar)
	})

	t.Run("prefers custom soul and identity overrides", func(t *testing.T) {
		t.Parallel()
		req := adminAgentCreateRequest{
			DisplayName: "Rory",
			ProfileID:   "rory",
			Soul:        "# SOUL\nCustom soul",
			Identity:    "# IDENTITY\nCustom identity",
			Avatar:      "https://example.com/custom.png",
		}
		templateInput, err := buildCreateAgentTemplateInput(req)
		require.NoError(t, err)
		require.Equal(t, "# SOUL\nCustom soul", templateInput.Soul)
		require.Equal(t, "# IDENTITY\nCustom identity", templateInput.Identity)
		require.Equal(t, "https://example.com/custom.png", templateInput.Avatar)
	})

	t.Run("rejects unknown profile", func(t *testing.T) {
		t.Parallel()
		req := adminAgentCreateRequest{DisplayName: "Unknown", ProfileID: "does-not-exist"}
		_, err := buildCreateAgentTemplateInput(req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "profileId")
	})

	t.Run("falls back to default templates when no profile selected", func(t *testing.T) {
		t.Parallel()
		req := adminAgentCreateRequest{DisplayName: "Start From Scratch"}
		templateInput, err := buildCreateAgentTemplateInput(req)
		require.NoError(t, err)
		require.Contains(t, templateInput.Soul, "Start From Scratch")
		require.Contains(t, templateInput.Identity, "Start From Scratch")
	})

	t.Run("supports newly shipped frontend profile ids", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name      string
			profileID string
		}{
			{name: "Kit", profileID: "kit"},
			{name: "Jules", profileID: "jules"},
			{name: "Avery", profileID: "avery"},
			{name: "Sloane", profileID: "sloane"},
			{name: "Rowan", profileID: "rowan"},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.profileID, func(t *testing.T) {
				t.Parallel()
				req := adminAgentCreateRequest{
					DisplayName: tc.name,
					ProfileID:   tc.profileID,
				}
				templateInput, err := buildCreateAgentTemplateInput(req)
				require.NoError(t, err)
				require.Contains(t, templateInput.Soul, tc.name)
				require.Contains(t, templateInput.Identity, tc.name)
				require.NotEmpty(t, templateInput.Avatar)
			})
		}
	})
}

func TestBuildCreateAgentConfigPatchOmitsChannelAndHeartbeat(t *testing.T) {
	t.Parallel()

	patch, err := buildCreateAgentConfigPatch("riley", "gpt-5.2-codex")
	require.NoError(t, err)
	require.JSONEq(t, `{"agents":{"riley":{"enabled":true,"model":{"primary":"gpt-5.2-codex"}}}}`, string(patch))
}

func TestShouldDispatchOpenClawAgentConfigMutation(t *testing.T) {
	t.Parallel()

	require.True(t, shouldDispatchOpenClawAgentConfigMutation("chameleon"))
	require.True(t, shouldDispatchOpenClawAgentConfigMutation("elephant"))
	require.True(t, shouldDispatchOpenClawAgentConfigMutation(" CHAMELEON "))
	require.False(t, shouldDispatchOpenClawAgentConfigMutation("main"))
	require.False(t, shouldDispatchOpenClawAgentConfigMutation("writer"))
}

func TestRenderNewAgentToolsTemplateIncludesOtterCampCommands(t *testing.T) {
	t.Parallel()

	tools := renderNewAgentToolsTemplate()
	require.Contains(t, tools, "otter project create")
	require.Contains(t, tools, "otter issue create")
	require.Contains(t, tools, "otter issue ask")
	require.Contains(t, tools, "otter issue respond")
}
