package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/syncmetrics"
)

const defaultRateLimitBackoff = time.Minute

type Option func(*Client)

type Client struct {
	httpClient *http.Client
	baseURL    *url.URL
	budgets    map[JobType]JobBudget
	now        func() time.Time

	mu        sync.Mutex
	used      map[JobType]int
	rateLimit RateLimitState
}

func NewClient(baseURL string, options ...Option) (*Client, error) {
	parsedBaseURL, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parse github base url: %w", err)
	}
	if parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" {
		return nil, fmt.Errorf("github base url must include scheme and host")
	}

	client := &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    parsedBaseURL,
		budgets:    defaultJobBudgets(),
		now:        time.Now,
		used:       make(map[JobType]int),
	}

	for _, option := range options {
		option(client)
	}

	return client, nil
}

func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		if httpClient != nil {
			client.httpClient = httpClient
		}
	}
}

func WithJobBudgets(budgets map[JobType]JobBudget) Option {
	return func(client *Client) {
		if len(budgets) == 0 {
			return
		}
		client.budgets = make(map[JobType]JobBudget, len(budgets))
		for jobType, budget := range budgets {
			client.budgets[jobType] = budget
		}
	}
}

func WithClock(now func() time.Time) Option {
	return func(client *Client) {
		if now != nil {
			client.now = now
		}
	}
}

func defaultJobBudgets() map[JobType]JobBudget {
	return map[JobType]JobBudget{
		JobTypeSync: {
			MaxRequests:     600,
			ReserveRequests: 50,
		},
		JobTypeImport: {
			MaxRequests:     5000,
			ReserveRequests: 200,
		},
		JobTypeWebhook: {
			MaxRequests:     200,
			ReserveRequests: 20,
		},
	}
}

func (c *Client) NewRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	if strings.TrimSpace(method) == "" {
		return nil, fmt.Errorf("method is required")
	}
	requestURL, err := c.resolveURL(endpoint)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	return req, nil
}

func (c *Client) Do(ctx context.Context, jobType JobType, req *http.Request) (*Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	if err := c.reserveBudget(jobType); err != nil {
		return nil, err
	}

	request := req.Clone(ctx)
	if !request.URL.IsAbs() {
		resolvedURL, err := c.resolveURL(request.URL.String())
		if err != nil {
			return nil, err
		}
		parsedURL, err := url.Parse(resolvedURL)
		if err != nil {
			return nil, err
		}
		request.URL = parsedURL
	}

	rawResponse, err := c.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer rawResponse.Body.Close()

	body, err := io.ReadAll(rawResponse.Body)
	if err != nil {
		return nil, err
	}

	rateLimit := parseRateLimitHeaders(rawResponse.Header)
	c.setRateLimit(rateLimit)
	if !rateLimit.IsZero() {
		syncmetrics.RecordQuota(string(jobType), rateLimit.Limit, rateLimit.Remaining, rateLimit.ResetAt)
	}
	budget := c.BudgetState(jobType)

	if isRateLimited, secondary := isRateLimitResponse(rawResponse.StatusCode, rawResponse.Header, body); isRateLimited {
		retryAfter := retryAfterForRateLimit(c.now(), rawResponse.Header, rateLimit, secondary)
		syncmetrics.RecordThrottle(string(jobType))
		return nil, &RateLimitError{
			StatusCode: rawResponse.StatusCode,
			RetryAfter: retryAfter,
			Secondary:  secondary,
			RateLimit:  rateLimit,
			Message:    strings.TrimSpace(string(body)),
		}
	}

	if rawResponse.StatusCode >= http.StatusBadRequest {
		return nil, &HTTPError{StatusCode: rawResponse.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	return &Response{
		StatusCode: rawResponse.StatusCode,
		Headers:    rawResponse.Header.Clone(),
		Body:       body,
		RateLimit:  rateLimit,
		Budget:     budget,
		NextPage:   parseNextPage(rawResponse.Header.Get("Link")),
	}, nil
}

func (c *Client) BudgetState(jobType JobType) BudgetState {
	c.mu.Lock()
	defer c.mu.Unlock()

	budget := c.resolveBudget(jobType)
	used := c.used[jobType]
	jobRemaining := unlimitedRemaining(budget.MaxRequests, used)
	apiRemaining := -1
	if !c.rateLimit.IsZero() {
		apiRemaining = c.rateLimit.Remaining - budget.ReserveRequests
		if apiRemaining < 0 {
			apiRemaining = 0
		}
	}

	return BudgetState{
		Job:                jobType,
		Used:               used,
		MaxRequests:        budget.MaxRequests,
		JobRemaining:       jobRemaining,
		APIRemaining:       apiRemaining,
		EffectiveRemaining: minRemaining(jobRemaining, apiRemaining),
		QuotaResetAt:       optionalTime(c.rateLimit.ResetAt),
		LastRateLimit:      c.rateLimit,
	}
}

func (c *Client) CurrentRateLimit() RateLimitState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rateLimit
}

func (c *Client) resolveURL(endpoint string) (string, error) {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return "", fmt.Errorf("endpoint is required")
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed, nil
	}

	relative, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parse endpoint %q: %w", endpoint, err)
	}

	return c.baseURL.ResolveReference(relative).String(), nil
}

func (c *Client) reserveBudget(jobType JobType) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	budget := c.resolveBudget(jobType)
	used := c.used[jobType]
	if budget.MaxRequests > 0 && used >= budget.MaxRequests {
		return &BudgetExceededError{
			Job:         jobType,
			Used:        used,
			MaxRequests: budget.MaxRequests,
			RateLimit:   c.rateLimit,
		}
	}

	if !c.rateLimit.IsZero() {
		if c.rateLimit.Remaining <= budget.ReserveRequests && !c.rateLimit.ResetAt.IsZero() && c.now().Before(c.rateLimit.ResetAt) {
			return &PauseError{
				ResumeAt: c.rateLimit.ResetAt,
				Reason:   fmt.Sprintf("quota low for %s (remaining=%d reserve=%d)", jobType, c.rateLimit.Remaining, budget.ReserveRequests),
			}
		}
	}

	c.used[jobType] = used + 1
	return nil
}

func (c *Client) resolveBudget(jobType JobType) JobBudget {
	if budget, ok := c.budgets[jobType]; ok {
		return budget
	}
	return JobBudget{}
}

func (c *Client) setRateLimit(rateLimit RateLimitState) {
	if rateLimit.IsZero() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rateLimit = rateLimit
}

func parseRateLimitHeaders(header http.Header) RateLimitState {
	state := RateLimitState{
		Resource: strings.TrimSpace(header.Get("X-RateLimit-Resource")),
	}

	if limit, err := parseIntHeader(header.Get("X-RateLimit-Limit")); err == nil {
		state.Limit = limit
	}
	if remaining, err := parseIntHeader(header.Get("X-RateLimit-Remaining")); err == nil {
		state.Remaining = remaining
	}
	if resetUnix, err := parseIntHeader(header.Get("X-RateLimit-Reset")); err == nil && resetUnix > 0 {
		state.ResetAt = time.Unix(int64(resetUnix), 0).UTC()
	}

	return state
}

func parseIntHeader(raw string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(raw))
}

func isRateLimitResponse(statusCode int, headers http.Header, body []byte) (bool, bool) {
	bodyText := strings.ToLower(string(body))

	if statusCode == http.StatusTooManyRequests {
		secondary := strings.Contains(bodyText, "secondary rate limit")
		return true, secondary
	}

	if statusCode != http.StatusForbidden {
		return false, false
	}

	if strings.Contains(bodyText, "secondary rate limit") {
		return true, true
	}
	if strings.Contains(bodyText, "rate limit") {
		return true, false
	}
	if strings.TrimSpace(headers.Get("X-RateLimit-Remaining")) == "0" {
		return true, false
	}

	return false, false
}

func retryAfterForRateLimit(now time.Time, headers http.Header, rateLimit RateLimitState, secondary bool) time.Duration {
	retryAfterHeader := strings.TrimSpace(headers.Get("Retry-After"))
	if retryAfterHeader != "" {
		if seconds, err := strconv.Atoi(retryAfterHeader); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		if dateValue, err := http.ParseTime(retryAfterHeader); err == nil {
			wait := dateValue.Sub(now)
			if wait > 0 {
				return wait
			}
		}
	}

	if !rateLimit.ResetAt.IsZero() && rateLimit.ResetAt.After(now) {
		return rateLimit.ResetAt.Sub(now)
	}

	if secondary {
		return 30 * time.Second
	}

	return defaultRateLimitBackoff
}

func parseNextPage(linkHeader string) string {
	for _, linkPart := range strings.Split(linkHeader, ",") {
		part := strings.TrimSpace(linkPart)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start == -1 || end == -1 || end <= start+1 {
			continue
		}
		return strings.TrimSpace(part[start+1 : end])
	}
	return ""
}

func unlimitedRemaining(max, used int) int {
	if max <= 0 {
		return -1
	}
	remaining := max - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

func minRemaining(left, right int) int {
	if left < 0 {
		return right
	}
	if right < 0 {
		return left
	}
	if left < right {
		return left
	}
	return right
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copyValue := value
	return &copyValue
}
