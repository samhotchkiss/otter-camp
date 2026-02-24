package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestRefreshCatalogDiffLogicAndEvent(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	connID := uuid.New()

	connRepo := &fakeConnectionRepo{
		byID: map[uuid.UUID]repo.MCPConnection{
			connID: {
				ID:             connID,
				OrganizationID: orgID,
				Status:         "configuring",
				IsEnabled:      true,
				Transport:      "http",
			},
		},
	}
	catalogRepo := &fakeCatalogRepo{
		byConnection: map[uuid.UUID]map[string]repo.MCPToolCatalogEntry{
			connID: {
				"A": {ID: uuid.New(), ConnectionID: connID, ToolName: "A", IsEnabled: true},
				"B": {ID: uuid.New(), ConnectionID: connID, ToolName: "B", IsEnabled: true},
				"C": {ID: uuid.New(), ConnectionID: connID, ToolName: "C", IsEnabled: false},
			},
		},
	}
	bindingsRepo := &fakeBindingRepo{}
	resolver := &fakeResolver{values: map[string]string{}}
	publisher := &fakePublisher{}

	svc, err := NewService(ServiceOptions{
		Connections: connRepo,
		Catalog:     catalogRepo,
		Bindings:    bindingsRepo,
		Resolver:    resolver,
		Transport: MockTransport{Tools: []ToolManifest{
			{ToolName: "B", Description: "B2"},
			{ToolName: "C", Description: "C2"},
			{ToolName: "D", Description: "D1"},
		}},
		EventBus: publisher,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if err := svc.RefreshCatalog(ctx, connID); err != nil {
		t.Fatalf("RefreshCatalog: %v", err)
	}

	state := catalogRepo.byConnection[connID]
	if state["A"].IsEnabled {
		t.Fatalf("removed tool A should be disabled")
	}
	if !state["B"].IsEnabled {
		t.Fatalf("existing tool B should preserve enabled=true")
	}
	if state["C"].IsEnabled {
		t.Fatalf("existing tool C should preserve enabled=false")
	}
	if state["D"].IsEnabled {
		t.Fatalf("new tool D should default to disabled")
	}

	if got := connRepo.byID[connID].Status; got != "active" {
		t.Fatalf("connection status after refresh = %q, want active", got)
	}

	if len(publisher.events) != 1 {
		t.Fatalf("published event count = %d, want 1", len(publisher.events))
	}
	event := publisher.events[0]
	if event.EventType != "mcp.catalog.changed" {
		t.Fatalf("event type = %q, want mcp.catalog.changed", event.EventType)
	}
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["added_count"] != float64(1) || payload["updated_count"] != float64(2) || payload["removed_count"] != float64(1) {
		t.Fatalf("payload counts = %#v, want added=1 updated=2 removed=1", payload)
	}
}

func TestResolveSecretBindings(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	connID := uuid.New()

	connRepo := &fakeConnectionRepo{byID: map[uuid.UUID]repo.MCPConnection{
		connID: {ID: connID, OrganizationID: orgID, Status: "active", IsEnabled: true},
	}}
	bindingsRepo := &fakeBindingRepo{rows: map[uuid.UUID][]repo.MCPSecretBinding{
		connID: {
			{ID: uuid.New(), ConnectionID: connID, SecretRef: "ref:alpha", EnvVarName: "ALPHA"},
			{ID: uuid.New(), ConnectionID: connID, SecretRef: "ref:beta", EnvVarName: "BETA"},
		},
	}}
	resolver := &fakeResolver{values: map[string]string{"ref:alpha": "a1", "ref:beta": "b1"}}
	catalogRepo := &fakeCatalogRepo{byConnection: map[uuid.UUID]map[string]repo.MCPToolCatalogEntry{}}

	svc, err := NewService(ServiceOptions{
		Connections: connRepo,
		Catalog:     catalogRepo,
		Bindings:    bindingsRepo,
		Resolver:    resolver,
		Transport:   MockTransport{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	resolved, err := svc.ResolveSecretBindings(ctx, connID)
	if err != nil {
		t.Fatalf("ResolveSecretBindings: %v", err)
	}
	if resolved["ALPHA"] != "a1" || resolved["BETA"] != "b1" {
		t.Fatalf("resolved map = %#v", resolved)
	}

	resolver.errFor = map[string]error{"ref:beta": errors.New("boom")}
	if _, err := svc.ResolveSecretBindings(ctx, connID); err == nil {
		t.Fatalf("expected resolver error to propagate")
	}
}

func TestResolveTransportConfigRefs(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	resolver := &fakeResolver{values: map[string]string{
		"ref:api-key":    "secret-key",
		"ref:nested-key": "nested-secret",
	}}

	resolved, err := resolveTransportConfigRefs(ctx, orgID, []byte(`{"api_key":"ref:api-key","plain":"value","nested":{"k":"ref:nested-key"}}`), resolver)
	if err != nil {
		t.Fatalf("resolveTransportConfigRefs: %v", err)
	}

	if resolved["api_key"] != "secret-key" {
		t.Fatalf("api_key = %v, want secret-key", resolved["api_key"])
	}
	if resolved["plain"] != "value" {
		t.Fatalf("plain = %v, want value", resolved["plain"])
	}
	nested, ok := resolved["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested = %#v, want map", resolved["nested"])
	}
	if nested["k"] != "nested-secret" {
		t.Fatalf("nested.k = %v, want nested-secret", nested["k"])
	}
}

type fakeConnectionRepo struct {
	byID map[uuid.UUID]repo.MCPConnection
}

func (f *fakeConnectionRepo) Create(_ context.Context, connection repo.MCPConnection) (repo.MCPConnection, error) {
	if connection.ID == uuid.Nil {
		connection.ID = uuid.New()
	}
	if f.byID == nil {
		f.byID = make(map[uuid.UUID]repo.MCPConnection)
	}
	f.byID[connection.ID] = connection
	return connection, nil
}

func (f *fakeConnectionRepo) GetByID(_ context.Context, id uuid.UUID) (repo.MCPConnection, error) {
	connection, ok := f.byID[id]
	if !ok {
		return repo.MCPConnection{}, repo.ErrNotFound
	}
	return connection, nil
}

func (f *fakeConnectionRepo) List(_ context.Context, organizationID uuid.UUID) ([]repo.MCPConnection, error) {
	rows := make([]repo.MCPConnection, 0)
	for _, connection := range f.byID {
		if connection.OrganizationID == organizationID {
			rows = append(rows, connection)
		}
	}
	return rows, nil
}

func (f *fakeConnectionRepo) SetStatus(_ context.Context, id uuid.UUID, status string) (repo.MCPConnection, error) {
	connection, ok := f.byID[id]
	if !ok {
		return repo.MCPConnection{}, repo.ErrNotFound
	}
	connection.Status = status
	f.byID[id] = connection
	return connection, nil
}

func (f *fakeConnectionRepo) Update(_ context.Context, connection repo.MCPConnection) (repo.MCPConnection, error) {
	if _, ok := f.byID[connection.ID]; !ok {
		return repo.MCPConnection{}, repo.ErrNotFound
	}
	f.byID[connection.ID] = connection
	return connection, nil
}

func (f *fakeConnectionRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.byID[id]; !ok {
		return repo.ErrNotFound
	}
	delete(f.byID, id)
	return nil
}

type fakeCatalogRepo struct {
	byConnection map[uuid.UUID]map[string]repo.MCPToolCatalogEntry
}

func (f *fakeCatalogRepo) BulkUpsert(_ context.Context, connectionID uuid.UUID, manifest []repo.MCPToolCatalogEntry) (repo.MCPToolCatalogDiff, error) {
	if f.byConnection == nil {
		f.byConnection = make(map[uuid.UUID]map[string]repo.MCPToolCatalogEntry)
	}
	if _, ok := f.byConnection[connectionID]; !ok {
		f.byConnection[connectionID] = make(map[string]repo.MCPToolCatalogEntry)
	}

	entries := f.byConnection[connectionID]
	seen := make(map[string]struct{}, len(manifest))
	diff := repo.MCPToolCatalogDiff{}
	for _, item := range manifest {
		name := item.ToolName
		seen[name] = struct{}{}
		if existing, ok := entries[name]; ok {
			existing.Description = item.Description
			existing.InputSchema = item.InputSchema
			existing.Metadata = item.Metadata
			entries[name] = existing
			diff.UpdatedCount++
			continue
		}
		item.ID = uuid.New()
		item.ConnectionID = connectionID
		item.IsEnabled = false
		entries[name] = item
		diff.AddedCount++
	}
	for name, existing := range entries {
		if _, ok := seen[name]; ok {
			continue
		}
		existing.IsEnabled = false
		entries[name] = existing
		diff.RemovedCount++
	}
	return diff, nil
}

func (f *fakeCatalogRepo) GetByID(_ context.Context, id uuid.UUID) (repo.MCPToolCatalogEntry, error) {
	for _, entries := range f.byConnection {
		for _, entry := range entries {
			if entry.ID == id {
				return entry, nil
			}
		}
	}
	return repo.MCPToolCatalogEntry{}, repo.ErrNotFound
}

func (f *fakeCatalogRepo) Enable(_ context.Context, id uuid.UUID) (repo.MCPToolCatalogEntry, error) {
	return f.setEnabled(id, true)
}

func (f *fakeCatalogRepo) Disable(_ context.Context, id uuid.UUID) (repo.MCPToolCatalogEntry, error) {
	return f.setEnabled(id, false)
}

func (f *fakeCatalogRepo) setEnabled(id uuid.UUID, enabled bool) (repo.MCPToolCatalogEntry, error) {
	for connID, entries := range f.byConnection {
		for name, entry := range entries {
			if entry.ID == id {
				entry.IsEnabled = enabled
				entries[name] = entry
				f.byConnection[connID] = entries
				return entry, nil
			}
		}
	}
	return repo.MCPToolCatalogEntry{}, repo.ErrNotFound
}

type fakeBindingRepo struct {
	rows map[uuid.UUID][]repo.MCPSecretBinding
}

func (f *fakeBindingRepo) GetByConnection(_ context.Context, connectionID uuid.UUID) ([]repo.MCPSecretBinding, error) {
	if f.rows == nil {
		return nil, nil
	}
	rows := f.rows[connectionID]
	out := make([]repo.MCPSecretBinding, len(rows))
	copy(out, rows)
	return out, nil
}

type fakeResolver struct {
	values map[string]string
	errFor map[string]error
}

func (f *fakeResolver) ResolveRef(_ context.Context, _ uuid.UUID, ref string) (string, error) {
	if err, ok := f.errFor[ref]; ok {
		return "", err
	}
	if value, ok := f.values[ref]; ok {
		return value, nil
	}
	return ref, nil
}

type fakePublisher struct {
	events []eventbus.DomainEvent
}

func (f *fakePublisher) Publish(_ context.Context, _ pgx.Tx, event eventbus.DomainEvent) error {
	f.events = append(f.events, event)
	return nil
}
