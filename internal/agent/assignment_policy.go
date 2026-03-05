package agent

import (
	"errors"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const StarterTrioProjectRoleErrorCode = "starter_trio_not_project_staff"

var ErrAssignmentStarterTrioRole = errors.New("starter trio agents (Frank, Lori, Ellie) are setup/governance-only and are not project staff, so they cannot be assigned to project roles")

func ValidateProjectAssignmentTarget(agentRecord repo.Agent, role string) error {
	if projectAssignmentRoleRequiresDedicatedAgent(role) && agentRecord.IsStarterTrio {
		return ErrAssignmentStarterTrioRole
	}
	if role == "project_manager" && !strings.EqualFold(strings.TrimSpace(agentRecord.AgentClass), "staff") {
		return ErrAssignmentPMRequiresStaff
	}
	return nil
}

func projectAssignmentRoleRequiresDedicatedAgent(role string) bool {
	switch role {
	case "project_manager", "worker", "reviewer", "observer":
		return true
	default:
		return false
	}
}
