package api

import "testing"

func TestRoleAllowsCapabilityMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		role       string
		capability string
		allowed    bool
	}{
		{name: "owner can run manual sync", role: RoleOwner, capability: CapabilityGitHubManualSync, allowed: true},
		{name: "owner can resolve conflicts", role: RoleOwner, capability: CapabilityGitHubConflictResolve, allowed: true},
		{name: "owner can publish", role: RoleOwner, capability: CapabilityGitHubPublish, allowed: true},
		{name: "owner can manage integration", role: RoleOwner, capability: CapabilityGitHubIntegrationAdmin, allowed: true},
		{name: "maintainer can run manual sync", role: RoleMaintainer, capability: CapabilityGitHubManualSync, allowed: true},
		{name: "maintainer can resolve conflicts", role: RoleMaintainer, capability: CapabilityGitHubConflictResolve, allowed: true},
		{name: "maintainer can publish", role: RoleMaintainer, capability: CapabilityGitHubPublish, allowed: true},
		{name: "maintainer cannot manage integration", role: RoleMaintainer, capability: CapabilityGitHubIntegrationAdmin, allowed: false},
		{name: "member can run manual sync", role: RoleMember, capability: CapabilityGitHubManualSync, allowed: true},
		{name: "member cannot resolve conflicts", role: RoleMember, capability: CapabilityGitHubConflictResolve, allowed: false},
		{name: "member cannot publish", role: RoleMember, capability: CapabilityGitHubPublish, allowed: false},
		{name: "viewer cannot run manual sync", role: RoleViewer, capability: CapabilityGitHubManualSync, allowed: false},
		{name: "unknown role denied", role: "nobody", capability: CapabilityGitHubManualSync, allowed: false},
		{name: "empty capability denied", role: RoleOwner, capability: "", allowed: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := roleAllowsCapability(tc.role, tc.capability); got != tc.allowed {
				t.Fatalf("roleAllowsCapability(%q,%q) = %v, want %v", tc.role, tc.capability, got, tc.allowed)
			}
		})
	}
}
