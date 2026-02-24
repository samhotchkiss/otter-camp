package audit

const (
	EventAuthLogin          = "auth.login"
	EventAuthLogout         = "auth.logout"
	EventAuthLoginFailed    = "auth.login_failed"
	EventAuthSessionRevoked = "auth.session_revoked"
	EventAPIKeyIssued       = "apikey.issued"
	EventAPIKeyRevoked      = "apikey.revoked"
)
