//go:build integration

package repo

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestMCPConnectionRepoCRUDAndStatusTransitions(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)
	connRepo := NewMCPConnectionRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "mcp-org", DisplayName: "MCP Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "mcp-project",
		DisplayName:    "MCP Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	created, err := connRepo.Create(ctx, MCPConnection{
		OrganizationID:  org.ID,
		ProjectID:       &project.ID,
		DisplayName:     "Primary MCP",
		Slug:            "primary-mcp",
		Transport:       "http",
		TransportConfig: []byte(`{"base_url":"http://mcp.local"}`),
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if created.Status != "configuring" {
		t.Fatalf("created status = %q, want configuring", created.Status)
	}

	byID, err := connRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if byID.ID != created.ID {
		t.Fatalf("GetByID id = %s, want %s", byID.ID, created.ID)
	}

	bySlug, err := connRepo.GetBySlug(ctx, org.ID, created.Slug)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if bySlug.ID != created.ID {
		t.Fatalf("GetBySlug id = %s, want %s", bySlug.ID, created.ID)
	}

	listed, err := connRepo.List(ctx, org.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List len = %d, want 1", len(listed))
	}

	statusUpdated, err := connRepo.SetStatus(ctx, created.ID, "active")
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if statusUpdated.Status != "active" {
		t.Fatalf("status after SetStatus = %q, want active", statusUpdated.Status)
	}

	disabled, err := connRepo.Disable(ctx, created.ID)
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if disabled.IsEnabled {
		t.Fatalf("is_enabled after disable = %v, want false", disabled.IsEnabled)
	}

	enabled, err := connRepo.Enable(ctx, created.ID)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !enabled.IsEnabled {
		t.Fatalf("is_enabled after enable = %v, want true", enabled.IsEnabled)
	}

	healthyAt := time.Now().UTC().Truncate(time.Second)
	healthUpdated, err := connRepo.UpdateLastHealthy(ctx, created.ID, healthyAt)
	if err != nil {
		t.Fatalf("UpdateLastHealthy: %v", err)
	}
	if healthUpdated.LastHealthyAt == nil || !healthUpdated.LastHealthyAt.UTC().Equal(healthyAt) {
		t.Fatalf("last_healthy_at = %v, want %v", healthUpdated.LastHealthyAt, healthyAt)
	}
}

func TestMCPToolCatalogRepoBulkUpsertDiffAndPreservesEnablement(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	connRepo := NewMCPConnectionRepo(pool)
	catalogRepo := NewMCPToolCatalogRepo(pool)
	orgID := seedMCPOrgAndProject(t, ctx, pool)
	connection := seedMCPConnection(t, ctx, connRepo, orgID)

	firstManifest := []MCPToolCatalogEntry{
		{ToolName: "A", Description: "A1"},
		{ToolName: "B", Description: "B1"},
		{ToolName: "C", Description: "C1"},
		{ToolName: "D", Description: "D1"},
		{ToolName: "E", Description: "E1"},
	}
	diff, err := catalogRepo.BulkUpsert(ctx, connection.ID, firstManifest)
	if err != nil {
		t.Fatalf("BulkUpsert first: %v", err)
	}
	if diff.AddedCount != 5 || diff.UpdatedCount != 0 || diff.RemovedCount != 0 {
		t.Fatalf("first diff = %+v, want added=5 updated=0 removed=0", diff)
	}

	entries, err := catalogRepo.ListByConnection(ctx, connection.ID)
	if err != nil {
		t.Fatalf("ListByConnection first: %v", err)
	}
	var bID uuid.UUID
	for _, entry := range entries {
		if entry.ToolName == "B" {
			bID = entry.ID
			break
		}
	}
	if bID == uuid.Nil {
		t.Fatalf("missing tool B entry")
	}
	if _, err := catalogRepo.Enable(ctx, bID); err != nil {
		t.Fatalf("Enable B: %v", err)
	}

	secondManifest := []MCPToolCatalogEntry{
		{ToolName: "B", Description: "B2"},
		{ToolName: "C", Description: "C2"},
		{ToolName: "D", Description: "D2"},
		{ToolName: "F", Description: "F1"},
	}
	diff, err = catalogRepo.BulkUpsert(ctx, connection.ID, secondManifest)
	if err != nil {
		t.Fatalf("BulkUpsert second: %v", err)
	}
	if diff.AddedCount != 1 || diff.UpdatedCount != 3 || diff.RemovedCount != 2 {
		t.Fatalf("second diff = %+v, want added=1 updated=3 removed=2", diff)
	}

	entries, err = catalogRepo.ListByConnection(ctx, connection.ID)
	if err != nil {
		t.Fatalf("ListByConnection second: %v", err)
	}
	if len(entries) != 6 {
		t.Fatalf("catalog entry count = %d, want 6", len(entries))
	}

	state := make(map[string]bool, len(entries))
	for _, entry := range entries {
		state[entry.ToolName] = entry.IsEnabled
	}
	if !state["B"] {
		t.Fatalf("tool B should stay enabled after upsert")
	}
	if state["A"] || state["E"] {
		t.Fatalf("removed tools should be disabled: state=%v", state)
	}
	if state["F"] {
		t.Fatalf("new tool F should default to disabled")
	}
}

func TestMCPConnectionCascadeDeleteRemovesCatalogAndBindings(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	connRepo := NewMCPConnectionRepo(pool)
	catalogRepo := NewMCPToolCatalogRepo(pool)
	bindingRepo := NewMCPSecretBindingRepo(pool)
	orgID := seedMCPOrgAndProject(t, ctx, pool)
	connection := seedMCPConnection(t, ctx, connRepo, orgID)

	if _, err := catalogRepo.BulkUpsert(ctx, connection.ID, []MCPToolCatalogEntry{{ToolName: "tool-a", Description: "desc"}}); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	if _, err := bindingRepo.Create(ctx, MCPSecretBinding{ConnectionID: connection.ID, SecretRef: "ref:test-secret", EnvVarName: "API_KEY"}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	if err := connRepo.Delete(ctx, connection.ID); err != nil {
		t.Fatalf("Delete connection: %v", err)
	}

	var (
		catalogCount int
		bindingCount int
	)
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_tool_catalog WHERE connection_id = $1`, connection.ID).Scan(&catalogCount); err != nil {
		t.Fatalf("count catalog rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM mcp_secret_binding WHERE connection_id = $1`, connection.ID).Scan(&bindingCount); err != nil {
		t.Fatalf("count binding rows: %v", err)
	}
	if catalogCount != 0 || bindingCount != 0 {
		t.Fatalf("cascade delete counts = catalog:%d binding:%d, want 0/0", catalogCount, bindingCount)
	}
}

func seedMCPOrgAndProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "mcp-org-seed", DisplayName: "MCP Seed Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "mcp-seed-project",
		DisplayName:    "MCP Seed Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return org.ID
}

func seedMCPConnection(t *testing.T, ctx context.Context, connRepo *MCPConnectionRepo, orgID uuid.UUID) MCPConnection {
	t.Helper()

	created, err := connRepo.Create(ctx, MCPConnection{
		OrganizationID:  orgID,
		DisplayName:     "Seed Connection",
		Slug:            "seed-connection",
		Transport:       "http",
		TransportConfig: []byte(`{"base_url":"http://localhost"}`),
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	return created
}
