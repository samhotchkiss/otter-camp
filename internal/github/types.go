package github

import (
	"fmt"
	"net/http"
	"time"
)

type JobType string

const (
	JobTypeSync    JobType = "sync"
	JobTypeImport  JobType = "import"
	JobTypeWebhook JobType = "webhook"
)

type JobBudget struct {
	MaxRequests     int
	ReserveRequests int
}

type RateLimitState struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"reset_at"`
	Resource  string    `json:"resource,omitempty"`
}

func (s RateLimitState) IsZero() bool {
	return s.Limit == 0 && s.Remaining == 0 && s.ResetAt.IsZero() && s.Resource == ""
}

type BudgetState struct {
	Job                JobType        `json:"job"`
	Used               int            `json:"used"`
	MaxRequests        int            `json:"max_requests"`
	JobRemaining       int            `json:"job_remaining"`
	APIRemaining       int            `json:"api_remaining"`
	EffectiveRemaining int            `json:"effective_remaining"`
	QuotaResetAt       *time.Time     `json:"quota_reset_at,omitempty"`
	LastRateLimit      RateLimitState `json:"last_rate_limit"`
}

type Response struct {
	StatusCode int            `json:"status_code"`
	Headers    http.Header    `json:"-"`
	Body       []byte         `json:"-"`
	RateLimit  RateLimitState `json:"rate_limit"`
	Budget     BudgetState    `json:"budget"`
	NextPage   string         `json:"next_page,omitempty"`
}

type PaginationCheckpoint struct {
	NextURL       string         `json:"next_url,omitempty"`
	LastRateLimit RateLimitState `json:"last_rate_limit"`
	PausedUntil   *time.Time     `json:"paused_until,omitempty"`
	PauseReason   string         `json:"pause_reason,omitempty"`
}

type RateLimitError struct {
	StatusCode int
	RetryAfter time.Duration
	Secondary  bool
	RateLimit  RateLimitState
	Message    string
}

func (e *RateLimitError) Error() string {
	kind := "primary"
	if e.Secondary {
		kind = "secondary"
	}
	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s rate limit exceeded (status=%d, retry_after=%s)", kind, e.StatusCode, e.RetryAfter)
	}
	return fmt.Sprintf("%s rate limit exceeded (status=%d)", kind, e.StatusCode)
}

type PauseError struct {
	ResumeAt time.Time
	Reason   string
}

func (e *PauseError) Error() string {
	return fmt.Sprintf("job paused until %s: %s", e.ResumeAt.UTC().Format(time.RFC3339), e.Reason)
}

type BudgetExceededError struct {
	Job         JobType
	Used        int
	MaxRequests int
	RateLimit   RateLimitState
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("job budget exhausted for %s (%d/%d requests used)", e.Job, e.Used, e.MaxRequests)
}

type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("github api request failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("github api request failed with status %d: %s", e.StatusCode, e.Body)
}
