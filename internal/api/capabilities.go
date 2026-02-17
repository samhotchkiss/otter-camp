package api

import "strings"

const (
	RoleOwner      = "owner"
	RoleMaintainer = "maintainer"
	RoleMember     = "member"
	RoleViewer     = "viewer"
)

const (
	CapabilityGitHubManualSync       = "github.sync.manual"
	CapabilityGitHubConflictResolve  = "github.conflict.resolve"
	CapabilityGitHubPublish          = "github.publish"
	CapabilityGitHubIntegrationAdmin = "github.integration.manage"
	CapabilityAdminConfigManage      = "admin.config.manage"
	CapabilityOpenClawMigrationManage = "openclaw.migration.manage"
)

var roleCapabilityMatrix = map[string]map[string]struct{}{
	RoleOwner: {
		CapabilityGitHubManualSync:       {},
		CapabilityGitHubConflictResolve:  {},
		CapabilityGitHubPublish:          {},
		CapabilityGitHubIntegrationAdmin: {},
		CapabilityAdminConfigManage:      {},
		CapabilityOpenClawMigrationManage: {},
	},
	RoleMaintainer: {
		CapabilityGitHubManualSync:      {},
		CapabilityGitHubConflictResolve: {},
		CapabilityGitHubPublish:         {},
		CapabilityOpenClawMigrationManage: {},
	},
	RoleMember: {
		CapabilityGitHubManualSync: {},
	},
	RoleViewer: {},
}

func normalizeRole(role string) string {
	trimmed := strings.TrimSpace(strings.ToLower(role))
	if trimmed == "" {
		return RoleViewer
	}
	if _, ok := roleCapabilityMatrix[trimmed]; ok {
		return trimmed
	}
	return RoleViewer
}

func roleAllowsCapability(role, capability string) bool {
	capability = strings.TrimSpace(capability)
	if capability == "" {
		return false
	}

	caps := roleCapabilityMatrix[normalizeRole(role)]
	_, ok := caps[capability]
	return ok
}
