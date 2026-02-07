package dispatch

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/webhook"
)

// DeliveryStatus tracks the latest delivery status for a task.
type DeliveryStatus struct {
	TaskID         string
	URL            string
	Attempts       int
	Delivered      bool
	LastError      string
	LastStatusCode int
	LastAttempt    time.Time
	NextAttempt    time.Time
}

// DeliveryRequest represents a webhook delivery request.
type DeliveryRequest struct {
	TaskID  string
	URL     string
	Payload []byte
	Secret  string
}

// WebhookDispatcherOptions configures WebhookDispatcher behavior.
type WebhookDispatcherOptions struct {
	Client          *http.Client
	Secret          string
	MaxRetries      int
	SignatureHeader string
	Backoff         func(retry int) time.Duration
	Sleep           func(time.Duration)
	Now             func() time.Time
	Nonce           func() string
}

// WebhookDispatcher delivers signed webhooks with retries and status tracking.
type WebhookDispatcher struct {
	client          *http.Client
	secret          string
	maxRetries      int
	signatureHeader string
	backoff         func(retry int) time.Duration
	sleep           func(time.Duration)
	now             func() time.Time
	nonce           func() string

	mu       sync.RWMutex
	statuses map[string]*DeliveryStatus
}

// NewWebhookDispatcher creates a new WebhookDispatcher with defaults.
func NewWebhookDispatcher(opts WebhookDispatcherOptions) *WebhookDispatcher {
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	maxRetries := opts.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	signatureHeader := strings.TrimSpace(opts.SignatureHeader)
	if signatureHeader == "" {
		signatureHeader = webhook.SignatureHeader
	}

	backoff := opts.Backoff
	if backoff == nil {
		backoff = defaultBackoff
	}

	sleep := opts.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	nonce := opts.Nonce
	if nonce == nil {
		nonce = randomNonce
	}

	return &WebhookDispatcher{
		client:          client,
		secret:          strings.TrimSpace(opts.Secret),
		maxRetries:      maxRetries,
		signatureHeader: signatureHeader,
		backoff:         backoff,
		sleep:           sleep,
		now:             now,
		nonce:           nonce,
		statuses:        make(map[string]*DeliveryStatus),
	}
}

// Deliver sends a webhook and retries on failure with exponential backoff.
func (d *WebhookDispatcher) Deliver(ctx context.Context, req DeliveryRequest) (*DeliveryStatus, error) {
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		return nil, errors.New("task id is required")
	}
	url := strings.TrimSpace(req.URL)
	if url == "" {
		return nil, errors.New("webhook url is required")
	}

	secret := strings.TrimSpace(req.Secret)
	if secret == "" {
		secret = d.secret
	}

	status := &DeliveryStatus{
		TaskID: taskID,
		URL:    url,
	}
	d.setStatus(status)

	var lastErr error

	for attempt := 0; attempt <= d.maxRetries; attempt++ {
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			status.LastError = lastErr.Error()
			status.Delivered = false
			status.NextAttempt = time.Time{}
			d.setStatus(status)
			return d.copyStatus(status), lastErr
		}

		status.Attempts = attempt + 1
		status.LastAttempt = d.now()

		statusCode, responseBody, err := d.send(ctx, url, req.Payload, secret)
		status.LastStatusCode = statusCode

		if err == nil && statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
			status.Delivered = true
			status.LastError = ""
			status.NextAttempt = time.Time{}
			d.setStatus(status)
			return d.copyStatus(status), nil
		}

		if err != nil {
			lastErr = err
		} else {
			lastErr = newStatusError(statusCode, responseBody)
		}
		status.Delivered = false
		status.LastError = lastErr.Error()

		if attempt == d.maxRetries {
			status.NextAttempt = time.Time{}
			d.setStatus(status)
			return d.copyStatus(status), lastErr
		}

		wait := d.backoff(attempt + 1)
		if wait < 0 {
			wait = 0
		}
		status.NextAttempt = d.now().Add(wait)
		d.setStatus(status)
		if wait > 0 {
			d.sleep(wait)
		}
	}

	return d.copyStatus(status), lastErr
}

// Status returns the latest delivery status for a task.
func (d *WebhookDispatcher) Status(taskID string) (*DeliveryStatus, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	status, ok := d.statuses[taskID]
	if !ok {
		return nil, false
	}
	return d.copyStatus(status), true
}

func (d *WebhookDispatcher) setStatus(status *DeliveryStatus) {
	d.mu.Lock()
	defer d.mu.Unlock()

	copyStatus := *status
	d.statuses[status.TaskID] = &copyStatus
}

func (d *WebhookDispatcher) copyStatus(status *DeliveryStatus) *DeliveryStatus {
	if status == nil {
		return nil
	}
	copyStatus := *status
	return &copyStatus
}

func (d *WebhookDispatcher) send(ctx context.Context, url string, payload []byte, secret string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, "", err
	}

	req.Header.Set("Content-Type", "application/json")

	if secret != "" {
		req.Header.Set(d.signatureHeader, webhook.Sign(payload, secret))
		req.Header.Set(webhook.TimestampHeader, fmt.Sprintf("%d", d.now().Unix()))
		req.Header.Set(webhook.NonceHeader, d.nonce())
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return resp.StatusCode, strings.TrimSpace(string(body)), nil
}

func defaultBackoff(retry int) time.Duration {
	if retry <= 0 {
		return 0
	}
	delay := time.Second << (retry - 1)
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func newStatusError(statusCode int, responseBody string) error {
	if responseBody == "" {
		return fmt.Errorf("webhook responded with status %d", statusCode)
	}
	return fmt.Errorf("webhook responded with status %d: %s", statusCode, responseBody)
}

func randomNonce() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
