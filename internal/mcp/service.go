package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var (
	slugPattern          = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	ErrInvalidSlug       = errors.New("invalid slug")
	ErrConnectionFailed  = errors.New("mcp connection is failed")
	ErrConnectionOrg     = errors.New("mcp connection does not belong to organization")
	ErrResolverRequired  = errors.New("secret resolver is required")
	ErrTransportRequired = errors.New("mcp transport is required")
)

type MCPConnection = repo.MCPConnection
type MCPToolCatalogEntry = repo.MCPToolCatalogEntry
type MCPSecretBinding = repo.MCPSecretBinding

type CreateConnectionRequest struct {
	OrganizationID  uuid.UUID
	ProjectID       *uuid.UUID
	DisplayName     string
	Slug            string
	Transport       string
	TransportConfig json.RawMessage
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
}

type SecretResolver interface {
	ResolveRef(ctx context.Context, orgID uuid.UUID, ref string) (string, error)
}

type EventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type ConnectionRepository interface {
	Create(ctx context.Context, connection repo.MCPConnection) (repo.MCPConnection, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.MCPConnection, error)
	List(ctx context.Context, organizationID uuid.UUID) ([]repo.MCPConnection, error)
	SetStatus(ctx context.Context, id uuid.UUID, status string) (repo.MCPConnection, error)
	Update(ctx context.Context, connection repo.MCPConnection) (repo.MCPConnection, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type ToolCatalogRepository interface {
	BulkUpsert(ctx context.Context, connectionID uuid.UUID, manifest []repo.MCPToolCatalogEntry) (repo.MCPToolCatalogDiff, error)
	GetByID(ctx context.Context, id uuid.UUID) (repo.MCPToolCatalogEntry, error)
	Enable(ctx context.Context, id uuid.UUID) (repo.MCPToolCatalogEntry, error)
	Disable(ctx context.Context, id uuid.UUID) (repo.MCPToolCatalogEntry, error)
}

type SecretBindingRepository interface {
	GetByConnection(ctx context.Context, connectionID uuid.UUID) ([]repo.MCPSecretBinding, error)
}

type service struct {
	connections ConnectionRepository
	catalog     ToolCatalogRepository
	bindings    SecretBindingRepository
	resolver    SecretResolver
	transport   Transport
	eventBus    EventPublisher
}

type ServiceOptions struct {
	Connections ConnectionRepository
	Catalog     ToolCatalogRepository
	Bindings    SecretBindingRepository
	Resolver    SecretResolver
	Transport   Transport
	EventBus    EventPublisher
}

func NewService(opts ServiceOptions) (MCPService, error) {
	if opts.Connections == nil || opts.Catalog == nil || opts.Bindings == nil {
		return nil, fmt.Errorf("mcp repositories are required")
	}
	if opts.Resolver == nil {
		return nil, ErrResolverRequired
	}
	if opts.Transport == nil {
		return nil, ErrTransportRequired
	}
	return &service{
		connections: opts.Connections,
		catalog:     opts.Catalog,
		bindings:    opts.Bindings,
		resolver:    opts.Resolver,
		transport:   opts.Transport,
		eventBus:    opts.EventBus,
	}, nil
}

func (s *service) CreateConnection(ctx context.Context, req CreateConnectionRequest) (*MCPConnection, error) {
	normalizedSlug, err := normalizeSlug(req.Slug)
	if err != nil {
		return nil, err
	}

	created, err := s.connections.Create(ctx, repo.MCPConnection{
		OrganizationID:  req.OrganizationID,
		ProjectID:       req.ProjectID,
		DisplayName:     strings.TrimSpace(req.DisplayName),
		Slug:            normalizedSlug,
		Transport:       strings.TrimSpace(req.Transport),
		TransportConfig: req.TransportConfig,
		Status:          "configuring",
		IsEnabled:       true,
		CreatedByType:   strings.TrimSpace(req.CreatedByType),
		CreatedByID:     req.CreatedByID,
	})
	if err != nil {
		return nil, err
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

	resolvedConfig, err := resolveTransportConfigRefs(ctx, connection.OrganizationID, connection.TransportConfig, s.resolver)
	if err != nil {
		return err
	}

	secretBindings, err := s.ResolveSecretBindings(ctx, connID)
	if err != nil {
		return err
	}

	manifest, err := s.transport.ListTools(ctx, connection, resolvedConfig, secretBindings)
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
		payload, marshalErr := json.Marshal(map[string]any{
			"connection_id": connID,
			"added_count":   diff.AddedCount,
			"updated_count": diff.UpdatedCount,
			"removed_count": diff.RemovedCount,
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
