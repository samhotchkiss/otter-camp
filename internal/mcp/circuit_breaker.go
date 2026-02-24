package mcp

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrCircuitOpen = errors.New("mcp circuit is open")

type CBState string

const (
	CBClosed   CBState = "closed"
	CBOpen     CBState = "open"
	CBHalfOpen CBState = "half-open"
)

type CircuitBreakerConfig struct {
	FailureThreshold    int
	RecoveryTimeout     time.Duration
	MaxConsecutiveOpens int
}

func defaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold:    5,
		RecoveryTimeout:     60 * time.Second,
		MaxConsecutiveOpens: 3,
	}
}

func normalizeCircuitBreakerConfig(cfg CircuitBreakerConfig) CircuitBreakerConfig {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.RecoveryTimeout <= 0 {
		cfg.RecoveryTimeout = 60 * time.Second
	}
	if cfg.MaxConsecutiveOpens <= 0 {
		cfg.MaxConsecutiveOpens = 3
	}
	return cfg
}

type CircuitBreaker struct {
	connectionID uuid.UUID

	state        CBState
	failureCount int
	successCount int

	lastFailureAt time.Time
	openedAt      time.Time

	consecutiveOpens int
	permanentlyOpen  bool
	probeInFlight    bool

	cfg CircuitBreakerConfig
	mu  sync.Mutex
}

type CircuitTransition struct {
	From            CBState
	To              CBState
	Changed         bool
	PermanentlyOpen bool
}

type CircuitSnapshot struct {
	ConnectionID      uuid.UUID
	State             CBState
	FailureCount      int
	SuccessCount      int
	LastFailureAt     time.Time
	OpenedAt          time.Time
	ConsecutiveOpens  int
	PermanentlyOpen   bool
	ProbeInFlight     bool
	FailureThreshold  int
	RecoveryTimeout   time.Duration
	MaxConsecutiveMax int
}

func NewCircuitBreaker(connectionID uuid.UUID, cfg CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		connectionID: connectionID,
		state:        CBClosed,
		cfg:          normalizeCircuitBreakerConfig(cfg),
	}
}

func (c *CircuitBreaker) BeforeCall(now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.maybeEnterHalfOpen(now)
	if c.state == CBClosed {
		return nil
	}
	return ErrCircuitOpen
}

func (c *CircuitBreaker) BeginProbe(now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.maybeEnterHalfOpen(now)

	switch c.state {
	case CBClosed:
		return nil
	case CBOpen:
		return ErrCircuitOpen
	case CBHalfOpen:
		if c.probeInFlight {
			return ErrCircuitOpen
		}
		c.probeInFlight = true
		return nil
	default:
		return ErrCircuitOpen
	}
}

func (c *CircuitBreaker) RecordSuccess(_ time.Time) CircuitTransition {
	c.mu.Lock()
	defer c.mu.Unlock()

	prev := c.state
	c.probeInFlight = false
	c.failureCount = 0
	c.successCount++

	if c.state == CBHalfOpen {
		c.state = CBClosed
		c.permanentlyOpen = false
		c.consecutiveOpens = 0
	}

	return CircuitTransition{
		From:            prev,
		To:              c.state,
		Changed:         prev != c.state,
		PermanentlyOpen: c.permanentlyOpen,
	}
}

func (c *CircuitBreaker) RecordFailure(now time.Time) CircuitTransition {
	c.mu.Lock()
	defer c.mu.Unlock()

	prev := c.state
	c.lastFailureAt = now.UTC()
	c.successCount = 0
	c.probeInFlight = false

	switch c.state {
	case CBClosed:
		c.failureCount++
		if c.failureCount >= c.cfg.FailureThreshold {
			c.open(now)
		}
	case CBHalfOpen:
		c.failureCount++
		c.open(now)
	case CBOpen:
		// Keep the circuit open and refresh the failure timestamp.
	default:
		c.open(now)
	}

	return CircuitTransition{
		From:            prev,
		To:              c.state,
		Changed:         prev != c.state,
		PermanentlyOpen: c.permanentlyOpen,
	}
}

func (c *CircuitBreaker) Snapshot(now time.Time) CircuitSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.maybeEnterHalfOpen(now)
	return CircuitSnapshot{
		ConnectionID:      c.connectionID,
		State:             c.state,
		FailureCount:      c.failureCount,
		SuccessCount:      c.successCount,
		LastFailureAt:     c.lastFailureAt,
		OpenedAt:          c.openedAt,
		ConsecutiveOpens:  c.consecutiveOpens,
		PermanentlyOpen:   c.permanentlyOpen,
		ProbeInFlight:     c.probeInFlight,
		FailureThreshold:  c.cfg.FailureThreshold,
		RecoveryTimeout:   c.cfg.RecoveryTimeout,
		MaxConsecutiveMax: c.cfg.MaxConsecutiveOpens,
	}
}

func (c *CircuitBreaker) maybeEnterHalfOpen(now time.Time) {
	if c.state != CBOpen || c.permanentlyOpen {
		return
	}
	if c.openedAt.IsZero() {
		return
	}
	if now.Sub(c.openedAt) >= c.cfg.RecoveryTimeout {
		c.state = CBHalfOpen
		c.probeInFlight = false
	}
}

func (c *CircuitBreaker) open(now time.Time) {
	if c.state != CBOpen {
		c.consecutiveOpens++
	}
	c.state = CBOpen
	c.openedAt = now.UTC()
	if c.consecutiveOpens >= c.cfg.MaxConsecutiveOpens {
		c.permanentlyOpen = true
	}
}
