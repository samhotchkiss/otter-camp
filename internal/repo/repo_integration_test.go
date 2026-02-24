//go:build integration

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestOrgRepoCRUDAndGetBySlug(t *testing.T) {
	pool := testdb.New(t)
	repo := NewOrgRepo(pool)

	created, err := repo.Create(context.Background(), Organization{
		Slug:        "acme-tools",
		DisplayName: "Acme Tools",
		Settings: OrganizationSettings{
			Agents: OrganizationAgentsSettings{MaxConcurrentTemps: 4},
		},
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	byID, err := repo.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if byID.Slug != "acme-tools" {
		t.Fatalf("slug = %q, want %q", byID.Slug, "acme-tools")
	}

	bySlug, err := repo.GetBySlug(context.Background(), "acme-tools")
	if err != nil {
		t.Fatalf("GetBySlug failed: %v", err)
	}
	if bySlug.ID != created.ID {
		t.Fatalf("GetBySlug ID = %s, want %s", bySlug.ID, created.ID)
	}

	updated, err := repo.UpdateSettings(context.Background(), created.ID, OrganizationSettings{
		Redaction: OrganizationRedactionSettings{Enabled: true, Policy: "strict"},
	})
	if err != nil {
		t.Fatalf("UpdateSettings failed: %v", err)
	}
	if updated.Settings.Agents.MaxConcurrentTemps != 0 {
		t.Fatal("UpdateSettings should fully replace settings")
	}

	if err := repo.Archive(context.Background(), created.ID); err != nil {
		t.Fatalf("Archive failed: %v", err)
	}
}

func TestOrgRepoGetBySlugNotFoundAndUniqueConflict(t *testing.T) {
	pool := testdb.New(t)
	repo := NewOrgRepo(pool)

	if _, err := repo.GetBySlug(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBySlug missing error = %v, want ErrNotFound", err)
	}

	_, err := repo.Create(context.Background(), Organization{Slug: "dup-org", DisplayName: "First"})
	if err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	_, err = repo.Create(context.Background(), Organization{Slug: "dup-org", DisplayName: "Second"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate slug error = %v, want ErrConflict", err)
	}
}

func TestModelProviderRepoCRUDAndUniqueConflict(t *testing.T) {
	pool := testdb.New(t)
	repo := NewModelProviderRepo(pool)

	created, err := repo.Create(context.Background(), ModelProvider{
		Slug:              "openai",
		DisplayName:       "OpenAI",
		APIBaseURL:        "https://api.openai.com/v1",
		SupportedFeatures: []string{"streaming", "tool_use"},
		IsEnabled:         true,
		Metadata:          json.RawMessage(`{"priority":1}`),
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	bySlug, err := repo.GetBySlug(context.Background(), "openai")
	if err != nil {
		t.Fatalf("GetBySlug failed: %v", err)
	}
	if bySlug.ID != created.ID {
		t.Fatalf("GetBySlug ID = %s, want %s", bySlug.ID, created.ID)
	}

	toggled, err := repo.SetEnabled(context.Background(), created.ID, false)
	if err != nil {
		t.Fatalf("SetEnabled failed: %v", err)
	}
	if toggled.IsEnabled {
		t.Fatal("expected provider to be disabled")
	}

	_, err = repo.Create(context.Background(), ModelProvider{
		Slug:        "openai",
		DisplayName: "Duplicate",
		APIBaseURL:  "https://duplicate.example",
		IsEnabled:   true,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate slug error = %v, want ErrConflict", err)
	}
}

func TestToolDefinitionRepoCRUDListAndBulkUpsert(t *testing.T) {
	pool := testdb.New(t)
	repo := NewToolDefinitionRepo(pool)

	created, err := repo.Create(context.Background(), ToolDefinition{
		Name:        "file.read",
		DisplayName: "File Read",
		Description: "Read a file",
		ToolTier:    "tier1",
		ToolDomain:  "file",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if _, err := repo.GetByName(context.Background(), created.Name); err != nil {
		t.Fatalf("GetByName failed: %v", err)
	}

	if _, err := repo.SetEnabled(context.Background(), created.ID, false); err != nil {
		t.Fatalf("SetEnabled failed: %v", err)
	}

	insertSet := []ToolDefinition{
		{Name: "cli.execute", DisplayName: "CLI Execute", Description: "Run command", ToolTier: "tier2", ToolDomain: "cli", IsEnabled: true},
		{Name: "browser.navigate", DisplayName: "Browser Navigate", Description: "Navigate browser", ToolTier: "tier2", ToolDomain: "browser", IsEnabled: true},
		{Name: "memory.search", DisplayName: "Memory Search", Description: "Search memory", ToolTier: "tier1", ToolDomain: "memory", IsEnabled: true},
		{Name: "project.create", DisplayName: "Project Create", Description: "Create project", ToolTier: "tier1", ToolDomain: "project", IsEnabled: true},
		{Name: "agent.list", DisplayName: "Agent List", Description: "List agents", ToolTier: "tier1", ToolDomain: "agent", IsEnabled: true},
	}
	if _, err := repo.BulkUpsert(context.Background(), insertSet); err != nil {
		t.Fatalf("BulkUpsert insert set failed: %v", err)
	}

	updateSet := []ToolDefinition{
		{Name: "cli.execute", DisplayName: "CLI Execute v2", Description: "Run shell command", ToolTier: "tier2", ToolDomain: "cli", IsEnabled: true},
		{Name: "browser.navigate", DisplayName: "Browser Navigate v2", Description: "Navigate URL", ToolTier: "tier2", ToolDomain: "browser", IsEnabled: false},
		{Name: "memory.search", DisplayName: "Memory Search v2", Description: "Lookup memory", ToolTier: "tier1", ToolDomain: "memory", IsEnabled: true},
		{Name: "system.health", DisplayName: "System Health", Description: "Read health", ToolTier: "tier1", ToolDomain: "system", IsEnabled: true},
		{Name: "chat.send", DisplayName: "Chat Send", Description: "Send chat", ToolTier: "tier1", ToolDomain: "chat", IsEnabled: true},
	}
	if _, err := repo.BulkUpsert(context.Background(), updateSet); err != nil {
		t.Fatalf("BulkUpsert update set failed: %v", err)
	}

	allTier1, err := repo.ListByTier(context.Background(), "tier1")
	if err != nil {
		t.Fatalf("ListByTier failed: %v", err)
	}
	if len(allTier1) == 0 {
		t.Fatal("expected tier1 tools")
	}

	cliTools, err := repo.ListByDomain(context.Background(), "cli")
	if err != nil {
		t.Fatalf("ListByDomain failed: %v", err)
	}
	if len(cliTools) != 1 || cliTools[0].DisplayName != "CLI Execute v2" {
		t.Fatalf("unexpected cli tool list: %+v", cliTools)
	}

	var total int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM tool_definition`).Scan(&total); err != nil {
		t.Fatalf("count tools failed: %v", err)
	}
	if total != 8 {
		t.Fatalf("tool_definition count = %d, want 8", total)
	}

	_, err = repo.Create(context.Background(), ToolDefinition{
		Name:        "bad.tier",
		DisplayName: "Bad Tier",
		Description: "Invalid",
		ToolTier:    "tier3",
		ToolDomain:  "system",
		IsEnabled:   true,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid tier error = %v, want ErrConflict", err)
	}
}

func TestMemoryTaxonomyRepoSubtreeDeleteRestrictAndConflict(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	nodeRepo := NewMemoryTaxonomyNodeRepo(pool)

	org, err := orgRepo.Create(context.Background(), Organization{Slug: "memory-org", DisplayName: "Memory Org"})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	root, err := nodeRepo.Create(context.Background(), MemoryTaxonomyNode{
		OrganizationID: org.ID,
		Slug:           "root",
		DisplayName:    "Root",
	})
	if err != nil {
		t.Fatalf("create root failed: %v", err)
	}

	child1, err := nodeRepo.Create(context.Background(), MemoryTaxonomyNode{
		OrganizationID: org.ID,
		ParentID:       &root.ID,
		Slug:           "child-1",
		DisplayName:    "Child 1",
	})
	if err != nil {
		t.Fatalf("create child1 failed: %v", err)
	}
	child2, err := nodeRepo.Create(context.Background(), MemoryTaxonomyNode{
		OrganizationID: org.ID,
		ParentID:       &root.ID,
		Slug:           "child-2",
		DisplayName:    "Child 2",
	})
	if err != nil {
		t.Fatalf("create child2 failed: %v", err)
	}

	_, _ = nodeRepo.Create(context.Background(), MemoryTaxonomyNode{OrganizationID: org.ID, ParentID: &child1.ID, Slug: "g11", DisplayName: "G11"})
	_, _ = nodeRepo.Create(context.Background(), MemoryTaxonomyNode{OrganizationID: org.ID, ParentID: &child1.ID, Slug: "g12", DisplayName: "G12"})
	_, _ = nodeRepo.Create(context.Background(), MemoryTaxonomyNode{OrganizationID: org.ID, ParentID: &child2.ID, Slug: "g21", DisplayName: "G21"})
	_, _ = nodeRepo.Create(context.Background(), MemoryTaxonomyNode{OrganizationID: org.ID, ParentID: &child2.ID, Slug: "g22", DisplayName: "G22"})

	descendants, err := nodeRepo.ListSubtree(context.Background(), root.ID)
	if err != nil {
		t.Fatalf("ListSubtree failed: %v", err)
	}
	if len(descendants) != 6 {
		t.Fatalf("descendants count = %d, want 6", len(descendants))
	}

	if err := nodeRepo.Delete(context.Background(), root.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete root with children error = %v, want ErrConflict", err)
	}

	_, err = nodeRepo.Create(context.Background(), MemoryTaxonomyNode{
		OrganizationID: org.ID,
		ParentID:       &root.ID,
		Slug:           "child-1",
		DisplayName:    "Duplicate Child",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate sibling slug error = %v, want ErrConflict", err)
	}

	gotBySlug, err := nodeRepo.GetBySlug(context.Background(), org.ID, &root.ID, "child-1")
	if err != nil {
		t.Fatalf("GetBySlug failed: %v", err)
	}
	if gotBySlug.ID != child1.ID {
		t.Fatalf("GetBySlug ID = %s, want %s", gotBySlug.ID, child1.ID)
	}

	children, err := nodeRepo.ListChildren(context.Background(), org.ID, &root.ID)
	if err != nil {
		t.Fatalf("ListChildren failed: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("children count = %d, want 2", len(children))
	}
}

func TestAllReposListRoundTrip(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	providerRepo := NewModelProviderRepo(pool)
	toolRepo := NewToolDefinitionRepo(pool)
	nodeRepo := NewMemoryTaxonomyNodeRepo(pool)

	org, err := orgRepo.Create(context.Background(), Organization{Slug: "list-org", DisplayName: "List Org"})
	if err != nil {
		t.Fatalf("org create failed: %v", err)
	}

	if _, err := providerRepo.Create(context.Background(), ModelProvider{Slug: "anthropic", DisplayName: "Anthropic", APIBaseURL: "https://api.anthropic.com", IsEnabled: true}); err != nil {
		t.Fatalf("provider create failed: %v", err)
	}

	if _, err := toolRepo.Create(context.Background(), ToolDefinition{Name: "file.write", DisplayName: "File Write", Description: "Write file", ToolTier: "tier2", ToolDomain: "file", IsEnabled: true}); err != nil {
		t.Fatalf("tool create failed: %v", err)
	}

	if _, err := nodeRepo.Create(context.Background(), MemoryTaxonomyNode{OrganizationID: org.ID, Slug: "root", DisplayName: "Root"}); err != nil {
		t.Fatalf("node create failed: %v", err)
	}

	orgs, err := orgRepo.List(context.Background())
	if err != nil || len(orgs) != 1 {
		t.Fatalf("org list failed, len=%d err=%v", len(orgs), err)
	}

	providers, err := providerRepo.List(context.Background())
	if err != nil || len(providers) != 1 {
		t.Fatalf("provider list failed, len=%d err=%v", len(providers), err)
	}

	if _, err := nodeRepo.GetByID(context.Background(), uuid.Nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID missing error = %v, want ErrNotFound", err)
	}
}
