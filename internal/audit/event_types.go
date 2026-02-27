package audit

const (
	EventAuthLogin          = "auth.login"
	EventAuthLogout         = "auth.logout"
	EventAuthLoginFailed    = "auth.login_failed"
	EventAuthSessionRevoked = "auth.session_revoked"
	EventAPIKeyCreated      = "api_key.created"
	EventAPIKeyDeleted      = "api_key.deleted"
	EventAPIKeyRotated      = "api_key.rotated"
	EventUserRoleChanged    = "user.role_changed"
	EventAgentCreated       = "agent.created"
	EventAgentUpdated       = "agent.updated"
	EventAgentDeleted       = "agent.deleted"
)
