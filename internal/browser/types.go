package browser

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	StatusPending               = "pending"
	StatusInProgress            = "in_progress"
	StatusCompleted             = "completed"
	StatusFailed                = "failed"
	StatusBlockedByDomainPolicy = "blocked_by_domain_policy"
)

var (
	ErrNotFound           = errors.New("browser: not found")
	ErrConflict           = errors.New("browser: conflict")
	ErrDomainPolicyDenied = errors.New("browser: domain policy denied")
	ErrSessionRevoked     = errors.New("browser: session revoked")
	ErrSessionNotActive   = errors.New("browser: session is not active")
)

type BrowserSession struct {
	ID                  uuid.UUID
	TaskID              uuid.UUID
	ProjectID           uuid.UUID
	AgentID             uuid.UUID
	OrganizationID      uuid.UUID
	Status              string
	BrowserType         string
	CurrentURL          *string
	UserAgent           *string
	CredentialsInjected []string
	IdleSince           *time.Time
	RevokedAt           *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type BrowserAction struct {
	ID                   uuid.UUID
	BrowserSessionID     uuid.UUID
	RunID                uuid.UUID
	RunStepID            uuid.UUID
	ActionType           string
	Input                json.RawMessage
	Output               json.RawMessage
	ScreenshotArtifactID *uuid.UUID
	Status               string
	ErrorMessage         *string
	URLAtExecution       *string
	StartedAt            *time.Time
	CompletedAt          *time.Time
	DurationMS           *int
	CreatedAt            time.Time
}

type BrowserHandoff struct {
	ID                              uuid.UUID
	BrowserSessionID                uuid.UUID
	InboxItemID                     uuid.UUID
	RunID                           uuid.UUID
	AgentID                         uuid.UUID
	TargetUserID                    uuid.UUID
	Reason                          string
	PreHandoffScreenshotArtifactID  *uuid.UUID
	PostHandoffScreenshotArtifactID *uuid.UUID
	Status                          string
	ExpiresAt                       time.Time
	CompletedAt                     *time.Time
	CreatedAt                       time.Time
}

type ActionCompletion struct {
	Status               string
	Output               json.RawMessage
	ErrorMessage         *string
	ScreenshotArtifactID *uuid.UUID
	URLAtExecution       *string
	StartedAt            *time.Time
	CompletedAt          *time.Time
	DurationMS           *int
}

type HandoffStatusUpdate struct {
	Status                          string
	PostHandoffScreenshotArtifactID *uuid.UUID
	CompletedAt                     *time.Time
}

type ExecuteActionInput struct {
	ActionType string
	Input      map[string]any
}

type ExecuteActionOutput struct {
	ActionID             uuid.UUID
	Output               map[string]any
	ScreenshotArtifactID *uuid.UUID
	URLAtExecution       *string
}

type BrowserCredential struct {
	Slug  string
	Value string
}
