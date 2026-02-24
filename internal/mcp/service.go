package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var (
	slugPattern             = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	ErrInvalidSlug          = errors.New("invalid slug")
	ErrInvalidSecretBinding = errors.New("invalid secret binding")
	ErrConnectionFailed     = errors.New("mcp connection is failed")
	ErrConnectionOrg        = errors.New("mcp connection does not belong to organization")
	ErrResolverRequired     = errors.New("secret resolver is required")
	ErrTransportFactory     = errors.New("mcp transport factory is required")
)

const (
	defaultPerCallTimeout      = 30 * time.Second
	defaultHealthCheckTimeout  = 10 * time.Second
	defaultRecoveryTimeout     = 60 * time.Second
	defaultFailureThreshold    = 5
	defaultMaxConsecutiveOpens = 3
	defaultActiveInterval      = 30 * time.Second
	defaultDegradedInterval    = 120 * time.Second
	defaultSchedulerTick       = 5 * time.Second
)

type MCPConnection = repo.MCPConnection
type MCPToolCatalogEntry = repo.MCPToolCatalogEntry
type MCPSecretBinding = repo.MCPSecretBinding

type SecretBindingInput struct {
	SecretRef  string
	EnvVarName string
}

type CreateConnectionRequest struct {
	OrganizationID  uuid.UUID
	ProjectID       *uuid.UUID
	DisplayName     string
	Slug            string
	Transport       string
	TransportConfig json.RawMessage
	IsEnabled       *bool
	SecretBindings  []SecretBindingInput
	CreatedByType   string
	CreatedByID     uuid.UUID
}

type UpdateConnectionRequest struct {
	DisplayName     *string
	Slug            *string
	Transport       *string
	TransportConfig json.RawMessage
	IsEnabled       *bool
}

type MCPConnectionFilter struct {
	ProjectID       *uuid.UUID
	Status          string
	Transport       string
	IncludeDisabled bool
}

type ToolIntent string

const (
	ToolIntentRead  ToolIntent = "read"
	ToolIntentWrite ToolIntent = "write"
)

type CallToolRequest struct {
	ConnectionID uuid.UUID
	Tool         string
	Input        json.RawMessage
	Intent       ToolIntent
	SafeToRetry  *bool
}

type DiscoverRequest struct {
	OrganizationID uuid.UUID
	AgentID        uuid.UUID
	ConnectionID   uuid.UUID
	Filter         string
}

type MCPService interface {
	CreateConnection(ctx context.Context, req CreateConnectionRequest) (*MCPConnection, error)
	GetConnection(ctx context.Context, orgID, connID uuid.UUID) (*MCPConnection, error)
	ListConnections(ctx context.Context, orgID uuid.UUID, filter MCPConnectionFilter) ([]*MCPConnection, error)
	UpdateConnection(ctx context.Context, orgID, connID uuid.UUID, req UpdateConnectionRequest) (*MCPConnection, error)
	DeleteConnection(ctx context.Context, orgID, connID uuid.UUID) error

	RefreshCatalog(ctx context.Context, connID uuid.UUID) error
	EnableTool(ctx context.Context, connID, entryID uuid.UUID) error
	DisableTool(ctx context.Context, connID, entryID uuid.UUID) error

	ResolveSecretBindings(ctx context.Context, connID uuid.UUID) (map[string]string, error)
	Discover(ctx context.Context, req DiscoverRequest) ([]MCPToolCatalogEntry, error)

	CallTool(ctx context.Context, req CallToolRequest) (json.RawMessage, error)
	RunHealthChecks(ctx context.Context) error
	StartHealthScheduler(ctx context.Context)
	CircuitState(connID uuid.UUID) CBState
}

type SecretResolver interface {
	ResolveRef(ctx context.Context, orgID uuid.UUID, ref string) (string, error)
}

type EventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type ConnectionAccessChecker interface {
	CanAccessConnection(ctx context.Context, agentID uuid.UUID, connection repo.MCPConnection) (bool, error)
}

type ConnectionRepository interface {
	Create(ctx context.Context, connection repo.MCPConnection) (repo.MCPConnection, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.MCPConnection, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.MCPConnection, error)
	ListEnabled(ctx context.Context) ([]repo.MCPConnection, error)
	SetStatus(ctx context.Context, id uuid.UUID, status string) (repo.MCPConnection, error)
	UpdateLastHealthy(ctx context.Context, id uuid.UUID, lastHealthyAt time.Time) (repo.MCPConnection, error)
	Update(ctx context.Context, connection repo.MCPConnection) (repo.MCPConnection, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type ToolCatalogRepository interface {
	BulkUpsert(ctx context.Context, connectionID uuid.UUID, manifest []repo.MCPToolCatalogEntry) (repo.MCPToolCatalogDiff, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.MCPToolCatalogEntry, error)
	Enable(ctx context.Context, id uuid.UUID) (repo.MCPToolCatalogEntry, error)
	Disable(ctx context.Context, id uuid.UUID) (repo.MCPToolCatalogEntry, error)
	GetEnabled(ctx context.Context, connectionID uuid.UUID) ([]repo.MCPToolCatalogEntry, error)
	ListByConnection(ctx context.Context, connectionID uuid.UUID) ([]repo.MCPToolCatalogEntry, error)
}

type SecretBindingRepository interface {
	Create(ctx context.Context, binding repo.MCPSecretBinding) (repo.MCPSecretBinding, error)
	GetByConnection(ctx context.Context, connectionID uuid.UUID) ([]repo.MCPSecretBinding, error)
}

type runtimeConfig struct {
	PerCallTimeout      time.Duration
	HealthCheckTimeout  time.Duration
	RecoveryTimeout     time.Duration
	FailureThreshold    int
	MaxConsecutiveOpens int
	ActiveInterval      time.Duration
	DegradedInterval    time.Duration
	SchedulerTick       time.Duration
}

type service struct {
	connections ConnectionRepository
	catalog     ToolCatalogRepository
	bindings    SecretBindingRepository
	resolver    SecretResolver
	eventBus    EventPublisher

	transportFactory TransportFactory
	accessChecker    ConnectionAccessChecker

	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
	randMu   sync.Mutex
	randFunc func() float64

	defaults runtimeConfig

	breakersMu sync.Mutex
	breakers   map[uuid.UUID]*CircuitBreaker

	healthMu         sync.Mutex
	lastHealthChecks map[uuid.UUID]time.Time
	schedulerOnce    sync.Once
}

type ServiceOptions struct {
	Connections ConnectionRepository
	Catalog     ToolCatalogRepository
	Bindings    SecretBindingRepository
	Resolver    SecretResolver
	EventBus    EventPublisher

	TransportFactory TransportFactory
	AccessChecker    ConnectionAccessChecker

	Now      func() time.Time
	Sleep    func(ctx context.Context, d time.Duration) error
	RandFunc func() float64

	DefaultPerCallTimeout      time.Duration
	DefaultHealthCheckTimeout  time.Duration
	DefaultRecoveryTimeout     time.Duration
	DefaultFailureThreshold    int
	DefaultMaxConsecutiveOpens int
	DefaultActiveInterval      time.Duration
	DefaultDegradedInterval    time.Duration
	DefaultSchedulerTick       time.Duration
}

func NewService(opts ServiceOptions) (MCPService, error) {
	if opts.Connections == nil || opts.Catalog == nil || opts.Bindings == nil {
		return nil, fmt.Errorf("mcp repositories are required")
	}
	if opts.Resolver == nil {
		return nil, ErrResolverRequired
	}
	if opts.TransportFactory == nil {
		return nil, ErrTransportFactory
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Sleep == nil {
		opts.Sleep = defaultSleep
	}
	if opts.RandFunc == nil {
		randSource := rand.New(rand.NewSource(time.Now().UnixNano()))
		opts.RandFunc = randSource.Float64
	}

	defaults := runtimeConfig{
		PerCallTimeout:      defaultPerCallTimeout,
		HealthCheckTimeout:  defaultHealthCheckTimeout,
		RecoveryTimeout:     defaultRecoveryTimeout,
		FailureThreshold:    defaultFailureThreshold,
		MaxConsecutiveOpens: defaultMaxConsecutiveOpens,
		ActiveInterval:      defaultActiveInterval,
		DegradedInterval:    defaultDegradedInterval,
		SchedulerTick:       defaultSchedulerTick,
	}
	if opts.DefaultPerCallTimeout > 0 {
		defaults.PerCallTimeout = opts.DefaultPerCallTimeout
	}
	if opts.DefaultHealthCheckTimeout > 0 {
		defaults.HealthCheckTimeout = opts.DefaultHealthCheckTimeout
	}
	if opts.DefaultRecoveryTimeout > 0 {
		defaults.RecoveryTimeout = opts.DefaultRecoveryTimeout
	}
	if opts.DefaultFailureThreshold > 0 {
		defaults.FailureThreshold = opts.DefaultFailureThreshold
	}
	if opts.DefaultMaxConsecutiveOpens > 0 {
		defaults.MaxConsecutiveOpens = opts.DefaultMaxConsecutiveOpens
	}
	if opts.DefaultActiveInterval > 0 {
		defaults.ActiveInterval = opts.DefaultActiveInterval
	}
	if opts.DefaultDegradedInterval > 0 {
		defaults.DegradedInterval = opts.DefaultDegradedInterval
	}
	if opts.DefaultSchedulerTick > 0 {
		defaults.SchedulerTick = opts.DefaultSchedulerTick
	}

	return &service{
		connections:      opts.Connections,
		catalog:          opts.Catalog,
		bindings:         opts.Bindings,
		resolver:         opts.Resolver,
		eventBus:         opts.EventBus,
		transportFactory: opts.TransportFactory,
		accessChecker:    opts.AccessChecker,
		now:              opts.Now,
		sleep:            opts.Sleep,
		randFunc:         opts.RandFunc,
		defaults:         defaults,
		breakers:         make(map[uuid.UUID]*CircuitBreaker),
		lastHealthChecks: make(map[uuid.UUID]time.Time),
	}, nil
}

func (s *service) CreateConnection(ctx context.Context, req CreateConnectionRequest) (*MCPConnection, error) {
	normalizedSlug, err := normalizeSlug(req.Slug)
	if err != nil {
		return nil, err
	}
	enabled := true
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}

	created, err := s.connections.Create(ctx, repo.MCPConnection{
		OrganizationID:  req.OrganizationID,
		ProjectID:       req.ProjectID,
		DisplayName:     strings.TrimSpace(req.DisplayName),
		Slug:            normalizedSlug,
		Transport:       strings.TrimSpace(req.Transport),
		TransportConfig: req.TransportConfig,
		Status:          "configuring",
		IsEnabled:       enabled,
		CreatedByType:   strings.TrimSpace(req.CreatedByType),
		CreatedByID:     req.CreatedByID,
	})
	if err != nil {
		return nil, err
	}

	for _, binding := range req.SecretBindings {
		secretRef := strings.TrimSpace(binding.SecretRef)
		envVarName := strings.TrimSpace(binding.EnvVarName)
		if secretRef == "" || envVarName == "" {
			_ = s.connections.Delete(ctx, created.ID)
			return nil, ErrInvalidSecretBinding
		}
		if _, err := s.bindings.Create(ctx, repo.MCPSecretBinding{
			ConnectionID: created.ID,
			SecretRef:    secretRef,
			EnvVarName:   envVarName,
		}); err != nil {
			_ = s.connections.Delete(ctx, created.ID)
			return nil, err
		}
	}

	return &created, nil
}

func (s *service) GetConnection(ctx context.Context, orgID, connID uuid.UUID) (*MCPConnection, error) {
	connection, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return nil, err
	}
	if connection.OrganizationID != orgID {
		return nil, ErrConnectionOrg
	}
	return &connection, nil
}

func (s *service) ListConnections(ctx context.Context, orgID uuid.UUID, filter MCPConnectionFilter) ([]*MCPConnection, error) {
	all, err := s.connections.List(ctx, orgID)
	if err != nil {
		return nil, err
	}

	filtered := make([]*MCPConnection, 0, len(all))
	for i := range all {
		item := all[i]
		if filter.ProjectID != nil {
			if item.ProjectID == nil || *item.ProjectID != *filter.ProjectID {
				continue
			}
		}
		if strings.TrimSpace(filter.Status) != "" && item.Status != strings.TrimSpace(filter.Status) {
			continue
		}
		if strings.TrimSpace(filter.Transport) != "" && item.Transport != strings.TrimSpace(filter.Transport) {
			continue
		}
		if !filter.IncludeDisabled && !item.IsEnabled {
			continue
		}
		copyItem := item
		filtered = append(filtered, &copyItem)
	}
	return filtered, nil
}

func (s *service) UpdateConnection(ctx context.Context, orgID, connID uuid.UUID, req UpdateConnectionRequest) (*MCPConnection, error) {
	existing, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return nil, err
	}
	if existing.OrganizationID != orgID {
		return nil, ErrConnectionOrg
	}

	updated := existing
	if req.DisplayName != nil {
		updated.DisplayName = strings.TrimSpace(*req.DisplayName)
	}
	if req.Slug != nil {
		normalizedSlug, slugErr := normalizeSlug(*req.Slug)
		if slugErr != nil {
			return nil, slugErr
		}
		updated.Slug = normalizedSlug
	}
	if req.Transport != nil {
		updated.Transport = strings.TrimSpace(*req.Transport)
	}
	if len(req.TransportConfig) > 0 {
		updated.TransportConfig = req.TransportConfig
	}
	if req.IsEnabled != nil {
		updated.IsEnabled = *req.IsEnabled
		if *req.IsEnabled && existing.Status == "failed" {
			updated.Status = "configuring"
		}
	}

	stored, err := s.connections.Update(ctx, updated)
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

func (s *service) DeleteConnection(ctx context.Context, orgID, connID uuid.UUID) error {
	connection, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return err
	}
	if connection.OrganizationID != orgID {
		return ErrConnectionOrg
	}
	return s.connections.Delete(ctx, connID)
}

func (s *service) RefreshCatalog(ctx context.Context, connID uuid.UUID) error {
	connection, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return err
	}
	if connection.Status == "failed" {
		return ErrConnectionFailed
	}

	runtime := s.runtimeConfigForConnection(connection)
	resolvedConfig, err := resolveTransportConfigRefs(ctx, connection.OrganizationID, connection.TransportConfig, s.resolver)
	if err != nil {
		return err
	}
	secretBindings, err := s.ResolveSecretBindings(ctx, connID)
	if err != nil {
		return err
	}
	transport, err := s.transportFactory.New(ctx, connection, resolvedConfig, secretBindings)
	if err != nil {
		return err
	}
	defer transport.Close()

	callCtx, cancel := context.WithTimeout(ctx, runtime.PerCallTimeout)
	manifest, err := transport.DiscoverTools(callCtx)
	cancel()
	if err != nil {
		return err
	}

	entries := make([]repo.MCPToolCatalogEntry, 0, len(manifest))
	for _, tool := range manifest {
		entries = append(entries, repo.MCPToolCatalogEntry{
			ConnectionID: connID,
			ToolName:     strings.TrimSpace(tool.ToolName),
			Description:  tool.Description,
			InputSchema:  tool.InputSchema,
			Metadata:     tool.Metadata,
		})
	}

	diff, err := s.catalog.BulkUpsert(ctx, connID, entries)
	if err != nil {
		return err
	}

	if connection.Status == "configuring" {
		if _, err := s.connections.SetStatus(ctx, connID, "active"); err != nil {
			return err
		}
	}

	if s.eventBus != nil {
		now := s.now().UTC()
		payload, marshalErr := json.Marshal(map[string]any{
			"organization_id": connection.OrganizationID,
			"connection_id":   connID,
			"added_count":     diff.AddedCount,
			"updated_count":   diff.UpdatedCount,
			"removed_count":   diff.RemovedCount,
			"timestamp":       now,
		})
		if marshalErr != nil {
			return fmt.Errorf("marshal mcp catalog changed event: %w", marshalErr)
		}
		if err := s.eventBus.Publish(ctx, nil, eventbus.DomainEvent{
			OrganizationID: connection.OrganizationID,
			EventType:      "mcp.catalog.changed",
			ActorType:      "system",
			Payload:        payload,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (s *service) EnableTool(ctx context.Context, connID, entryID uuid.UUID) error {
	entry, err := s.catalog.GetByID(ctx, entryID)
	if err != nil {
		return err
	}
	if entry.ConnectionID != connID {
		return repo.ErrNotFound
	}
	_, err = s.catalog.Enable(ctx, entryID)
	return err
}

func (s *service) DisableTool(ctx context.Context, connID, entryID uuid.UUID) error {
	entry, err := s.catalog.GetByID(ctx, entryID)
	if err != nil {
		return err
	}
	if entry.ConnectionID != connID {
		return repo.ErrNotFound
	}
	_, err = s.catalog.Disable(ctx, entryID)
	return err
}

func (s *service) ResolveSecretBindings(ctx context.Context, connID uuid.UUID) (map[string]string, error) {
	connection, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return nil, err
	}

	bindings, err := s.bindings.GetByConnection(ctx, connID)
	if err != nil {
		return nil, err
	}

	resolved := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		value, resolveErr := s.resolver.ResolveRef(ctx, connection.OrganizationID, binding.SecretRef)
		if resolveErr != nil {
			return nil, resolveErr
		}
		resolved[binding.EnvVarName] = value
	}
	return resolved, nil
}

func (s *service) Discover(ctx context.Context, req DiscoverRequest) ([]MCPToolCatalogEntry, error) {
	connection, err := s.connections.GetByID(ctx, req.ConnectionID)
	if err != nil {
		return nil, err
	}
	if req.OrganizationID != uuid.Nil && connection.OrganizationID != req.OrganizationID {
		return nil, ErrConnectionOrg
	}
	if !s.hasDiscoverAccess(ctx, req.AgentID, connection) {
		return []MCPToolCatalogEntry{}, nil
	}

	entries, err := s.catalog.GetEnabled(ctx, req.ConnectionID)
	if err != nil {
		return nil, err
	}

	filter := strings.ToLower(strings.TrimSpace(req.Filter))
	if filter == "" {
		return entries, nil
	}

	filtered := make([]MCPToolCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.ToolName), filter) || strings.Contains(strings.ToLower(entry.Description), filter) {
			filtered = append(filtered, entry)
		}
	}
	return filtered, nil
}

func (s *service) hasDiscoverAccess(ctx context.Context, agentID uuid.UUID, connection repo.MCPConnection) bool {
	if connection.ProjectID == nil {
		return true
	}
	if agentID == uuid.Nil {
		return false
	}
	if s.accessChecker == nil {
		return false
	}
	allowed, err := s.accessChecker.CanAccessConnection(ctx, agentID, connection)
	if err != nil {
		return false
	}
	return allowed
}

func (s *service) CallTool(ctx context.Context, req CallToolRequest) (json.RawMessage, error) {
	connection, err := s.connections.GetByID(ctx, req.ConnectionID)
	if err != nil {
		return nil, err
	}
	if connection.Status == "failed" {
		return nil, ErrConnectionFailed
	}

	runtime := s.runtimeConfigForConnection(connection)
	breaker := s.breakerForConnection(connection.ID, runtime)
	if err := breaker.BeforeCall(s.now()); err != nil {
		return nil, err
	}

	resolvedConfig, err := resolveTransportConfigRefs(ctx, connection.OrganizationID, connection.TransportConfig, s.resolver)
	if err != nil {
		return nil, err
	}
	secretBindings, err := s.ResolveSecretBindings(ctx, req.ConnectionID)
	if err != nil {
		return nil, err
	}
	transport, err := s.transportFactory.New(ctx, connection, resolvedConfig, secretBindings)
	if err != nil {
		return nil, err
	}
	defer transport.Close()

	safeToRetry, err := s.shouldRetryWrite(ctx, req)
	if err != nil {
		return nil, err
	}
	attempts := retryAttempts(req.Intent, safeToRetry)

	baseBackoff := 100 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, runtime.PerCallTimeout)
		result, callErr := transport.Call(callCtx, req.Tool, req.Input)
		cancel()
		if callErr == nil {
			transition := breaker.RecordSuccess(s.now())
			if err := s.applyCircuitTransition(ctx, req.ConnectionID, transition); err != nil {
				return nil, err
			}
			return normalizeRawJSON(result), nil
		}
		lastErr = callErr
		if attempt == attempts || !isTransientError(callErr) {
			break
		}

		delay := jitteredBackoff(baseBackoff, attempt-1, s.randFloat64())
		if sleepErr := s.sleep(ctx, delay); sleepErr != nil {
			return nil, sleepErr
		}
	}

	transition := breaker.RecordFailure(s.now())
	if err := s.applyCircuitTransition(ctx, req.ConnectionID, transition); err != nil {
		return nil, err
	}
	return nil, lastErr
}

func (s *service) shouldRetryWrite(ctx context.Context, req CallToolRequest) (bool, error) {
	if req.Intent != ToolIntentWrite {
		return true, nil
	}
	if req.SafeToRetry != nil {
		return *req.SafeToRetry, nil
	}

	entries, err := s.catalog.ListByConnection(ctx, req.ConnectionID)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.ToolName != req.Tool {
			continue
		}
		var metadata map[string]any
		if len(entry.Metadata) == 0 {
			return false, nil
		}
		if err := json.Unmarshal(entry.Metadata, &metadata); err != nil {
			return false, nil
		}
		if raw, ok := metadata["safe_to_retry"]; ok {
			if value, ok := raw.(bool); ok {
				return value, nil
			}
		}
		return false, nil
	}

	return false, nil
}

func retryAttempts(intent ToolIntent, safeToRetry bool) int {
	switch intent {
	case ToolIntentWrite:
		if safeToRetry {
			return 3
		}
		return 1
	case ToolIntentRead:
		return 3
	default:
		return 1
	}
}

func (s *service) RunHealthChecks(ctx context.Context) error {
	connections, err := s.connections.ListEnabled(ctx)
	if err != nil {
		return err
	}

	now := s.now()
	for _, connection := range connections {
		runtime := s.runtimeConfigForConnection(connection)
		if !s.shouldRunHealthCheck(connection, runtime, now) {
			continue
		}
		if err := s.runConnectionHealthCheck(ctx, connection, runtime, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) StartHealthScheduler(ctx context.Context) {
	s.schedulerOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(s.defaults.SchedulerTick)
			defer ticker.Stop()

			for {
				_ = s.RunHealthChecks(ctx)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

func (s *service) CircuitState(connID uuid.UUID) CBState {
	s.breakersMu.Lock()
	breaker := s.breakers[connID]
	s.breakersMu.Unlock()
	if breaker == nil {
		return CBClosed
	}
	return breaker.Snapshot(s.now()).State
}

func (s *service) shouldRunHealthCheck(connection repo.MCPConnection, runtime runtimeConfig, now time.Time) bool {
	interval := runtime.ActiveInterval
	if connection.Status == "degraded" {
		interval = runtime.DegradedInterval
	}

	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	last := s.lastHealthChecks[connection.ID]
	if !last.IsZero() && now.Sub(last) < interval {
		return false
	}
	s.lastHealthChecks[connection.ID] = now
	return true
}

func (s *service) runConnectionHealthCheck(ctx context.Context, connection repo.MCPConnection, runtime runtimeConfig, now time.Time) error {
	breaker := s.breakerForConnection(connection.ID, runtime)
	if err := breaker.BeginProbe(now); err != nil {
		return nil
	}

	resolvedConfig, err := resolveTransportConfigRefs(ctx, connection.OrganizationID, connection.TransportConfig, s.resolver)
	if err != nil {
		transition := breaker.RecordFailure(now)
		return s.applyCircuitTransition(ctx, connection.ID, transition)
	}
	secretBindings, err := s.ResolveSecretBindings(ctx, connection.ID)
	if err != nil {
		transition := breaker.RecordFailure(now)
		return s.applyCircuitTransition(ctx, connection.ID, transition)
	}
	transport, err := s.transportFactory.New(ctx, connection, resolvedConfig, secretBindings)
	if err != nil {
		transition := breaker.RecordFailure(now)
		return s.applyCircuitTransition(ctx, connection.ID, transition)
	}
	defer transport.Close()

	healthCtx, cancel := context.WithTimeout(ctx, runtime.HealthCheckTimeout)
	err = transport.HealthCheck(healthCtx)
	cancel()
	if err != nil {
		transition := breaker.RecordFailure(now)
		return s.applyCircuitTransition(ctx, connection.ID, transition)
	}

	if _, err := s.connections.UpdateLastHealthy(ctx, connection.ID, now); err != nil {
		return err
	}
	transition := breaker.RecordSuccess(now)
	return s.applyCircuitTransition(ctx, connection.ID, transition)
}

func (s *service) breakerForConnection(connectionID uuid.UUID, runtime runtimeConfig) *CircuitBreaker {
	s.breakersMu.Lock()
	defer s.breakersMu.Unlock()

	breaker, ok := s.breakers[connectionID]
	if !ok {
		breaker = NewCircuitBreaker(connectionID, CircuitBreakerConfig{
			FailureThreshold:    runtime.FailureThreshold,
			RecoveryTimeout:     runtime.RecoveryTimeout,
			MaxConsecutiveOpens: runtime.MaxConsecutiveOpens,
		})
		s.breakers[connectionID] = breaker
	}
	return breaker
}

func (s *service) applyCircuitTransition(ctx context.Context, connectionID uuid.UUID, transition CircuitTransition) error {
	if !transition.Changed {
		return nil
	}

	switch transition.To {
	case CBClosed:
		_, err := s.connections.SetStatus(ctx, connectionID, "active")
		return err
	case CBOpen:
		status := "degraded"
		if transition.PermanentlyOpen {
			status = "failed"
		}
		_, err := s.connections.SetStatus(ctx, connectionID, status)
		return err
	default:
		return nil
	}
}

func (s *service) runtimeConfigForConnection(connection repo.MCPConnection) runtimeConfig {
	cfg := s.defaults
	var raw map[string]any
	if err := json.Unmarshal(normalizeRawJSON(connection.TransportConfig), &raw); err != nil {
		return cfg
	}

	if v, ok := intConfig(raw, "per_call_timeout_ms"); ok && v > 0 {
		cfg.PerCallTimeout = time.Duration(v) * time.Millisecond
	}
	if v, ok := intConfig(raw, "health_check_timeout_ms"); ok && v > 0 {
		cfg.HealthCheckTimeout = time.Duration(v) * time.Millisecond
	}
	if v, ok := intConfig(raw, "recovery_timeout_ms"); ok && v > 0 {
		cfg.RecoveryTimeout = time.Duration(v) * time.Millisecond
	}
	if v, ok := intConfig(raw, "failure_threshold"); ok && v > 0 {
		cfg.FailureThreshold = v
	}
	if v, ok := intConfig(raw, "max_consecutive_opens"); ok && v > 0 {
		cfg.MaxConsecutiveOpens = v
	}
	if v, ok := intConfig(raw, "health_active_interval_ms", "health_check_active_interval_ms"); ok && v > 0 {
		cfg.ActiveInterval = time.Duration(v) * time.Millisecond
	}
	if v, ok := intConfig(raw, "health_degraded_interval_ms", "health_check_degraded_interval_ms"); ok && v > 0 {
		cfg.DegradedInterval = time.Duration(v) * time.Millisecond
	}
	if v, ok := intConfig(raw, "health_scheduler_tick_ms"); ok && v > 0 {
		cfg.SchedulerTick = time.Duration(v) * time.Millisecond
	}
	return cfg
}

func (s *service) randFloat64() float64 {
	s.randMu.Lock()
	defer s.randMu.Unlock()
	return s.randFunc()
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func jitteredBackoff(base time.Duration, power int, randValue float64) time.Duration {
	delay := base * time.Duration(1<<power)
	factor := 0.8 + (randValue * 0.4)
	return time.Duration(float64(delay) * factor)
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	return false
}

func normalizeSlug(slug string) (string, error) {
	trimmed := strings.TrimSpace(slug)
	if !slugPattern.MatchString(trimmed) {
		return "", fmt.Errorf("%w: %q", ErrInvalidSlug, slug)
	}
	return trimmed, nil
}

func resolveTransportConfigRefs(ctx context.Context, orgID uuid.UUID, raw json.RawMessage, resolver SecretResolver) (map[string]any, error) {
	config := make(map[string]any)
	if len(raw) == 0 {
		return config, nil
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("unmarshal transport config: %w", err)
	}

	resolved, err := resolveConfigValue(ctx, orgID, config, resolver)
	if err != nil {
		return nil, err
	}
	resolvedMap, ok := resolved.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("transport config must be an object")
	}
	return resolvedMap, nil
}

func resolveConfigValue(ctx context.Context, orgID uuid.UUID, value any, resolver SecretResolver) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			resolved, err := resolveConfigValue(ctx, orgID, nested, resolver)
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(typed))
		for _, nested := range typed {
			resolved, err := resolveConfigValue(ctx, orgID, nested, resolver)
			if err != nil {
				return nil, err
			}
			out = append(out, resolved)
		}
		return out, nil
	case string:
		if strings.HasPrefix(typed, "ref:") {
			resolved, err := resolver.ResolveRef(ctx, orgID, typed)
			if err != nil {
				return nil, err
			}
			return resolved, nil
		}
		return typed, nil
	default:
		return typed, nil
	}
}

func intConfig(config map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		value, ok := config[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int(typed), true
		case int:
			return typed, true
		case json.Number:
			n, err := typed.Int64()
			if err == nil {
				return int(n), true
			}
		}
	}
	return 0, false
}
