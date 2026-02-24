//go:build integration

package mcp

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestRefreshCatalogIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgID := seedMCPOrgAndProject(t, ctx, pool)
	connRepo := repo.NewMCPConnectionRepo(pool)
	catalogRepo := repo.NewMCPToolCatalogRepo(pool)
	bindingRepo := repo.NewMCPSecretBindingRepo(pool)
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})

	service, err := NewService(ServiceOptions{
		Connections: connRepo,
		Catalog:     catalogRepo,
		Bindings:    bindingRepo,
		Resolver: &integrationResolver{values: map[string]string{
			"ref:catalog-token": "token-value",
		}},
		Transport: MockTransport{Tools: []ToolManifest{
			{ToolName: "tool.alpha", Description: "alpha", InputSchema: []byte(`{"type":"object"}`)},
			{ToolName: "tool.beta", Description: "beta", InputSchema: []byte(`{"type":"object"}`)},
		}},
		EventBus: bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	created, err := service.CreateConnection(ctx, CreateConnectionRequest{
		OrganizationID:  orgID,
		DisplayName:     "Catalog Connection",
		Slug:            "catalog-connection",
		Transport:       "http",
		TransportConfig: []byte(`{"api_key":"ref:catalog-token"}`),
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	if err := service.RefreshCatalog(ctx, created.ID); err != nil {
		t.Fatalf("RefreshCatalog: %v", err)
	}

	var (
		catalogCount int
		enabledCount int
		status       string
		eventCount   int
	)
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_tool_catalog WHERE connection_id = $1`, created.ID).Scan(&catalogCount); err != nil {
		t.Fatalf("count catalog rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_tool_catalog WHERE connection_id = $1 AND is_enabled = true`, created.ID).Scan(&enabledCount); err != nil {
		t.Fatalf("count enabled rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM mcp_connection WHERE id = $1`, created.ID).Scan(&status); err != nil {
		t.Fatalf("load connection status: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM domain_event WHERE organization_id = $1 AND event_type = 'mcp.catalog.changed'`, orgID).Scan(&eventCount); err != nil {
		t.Fatalf("count domain events: %v", err)
	}

	if catalogCount != 2 {
		t.Fatalf("catalog row count = %d, want 2", catalogCount)
	}
	if enabledCount != 0 {
		t.Fatalf("enabled row count = %d, want 0 (default deny)", enabledCount)
	}
	if status != "active" {
		t.Fatalf("connection status = %q, want active", status)
	}
	if eventCount != 1 {
		t.Fatalf("domain event count = %d, want 1", eventCount)
	}
}

func TestResolveSecretBindingsIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgID := seedMCPOrgAndProject(t, ctx, pool)
	connRepo := repo.NewMCPConnectionRepo(pool)
	catalogRepo := repo.NewMCPToolCatalogRepo(pool)
	bindingRepo := repo.NewMCPSecretBindingRepo(pool)

	service, err := NewService(ServiceOptions{
		Connections: connRepo,
		Catalog:     catalogRepo,
		Bindings:    bindingRepo,
		Resolver: &integrationResolver{values: map[string]string{
			"ref:mcp-key":   "secret-key",
			"ref:mcp-token": "secret-token",
		}},
		Transport: MockTransport{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	created, err := service.CreateConnection(ctx, CreateConnectionRequest{
		OrganizationID: orgID,
		DisplayName:    "Resolve Connection",
		Slug:           "resolve-connection",
		Transport:      "http",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}

	if _, err := bindingRepo.Create(ctx, repo.MCPSecretBinding{ConnectionID: created.ID, SecretRef: "ref:mcp-key", EnvVarName: "MCP_KEY"}); err != nil {
		t.Fatalf("create binding MCP_KEY: %v", err)
	}
	if _, err := bindingRepo.Create(ctx, repo.MCPSecretBinding{ConnectionID: created.ID, SecretRef: "ref:mcp-token", EnvVarName: "MCP_TOKEN"}); err != nil {
		t.Fatalf("create binding MCP_TOKEN: %v", err)
	}

	resolved, err := service.ResolveSecretBindings(ctx, created.ID)
	if err != nil {
		t.Fatalf("ResolveSecretBindings: %v", err)
	}
	if resolved["MCP_KEY"] != "secret-key" || resolved["MCP_TOKEN"] != "secret-token" {
		t.Fatalf("resolved map = %#v", resolved)
	}
}

func seedMCPOrgAndProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "mcp-service-org", DisplayName: "MCP Service Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "mcp-service-project",
		DisplayName:    "MCP Service Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return org.ID
}

type integrationResolver struct {
	values map[string]string
}

func (r *integrationResolver) ResolveRef(_ context.Context, _ uuid.UUID, ref string) (string, error) {
	if value, ok := r.values[ref]; ok {
		return value, nil
	}
	return ref, nil
}
