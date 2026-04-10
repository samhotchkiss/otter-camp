package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestMemoryRoutesRegistered(t *testing.T) {
	registrar := NewMemoryRouteRegistrar(MemoryRouteOptions{})
	router := chi.NewRouter()
	registrar.RegisterRoutes(router)

	routes := make(map[string]struct{})
	if err := chi.Walk(router, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[strings.ToUpper(method)+" "+route] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	required := []string{
		"POST /memory/query",
		"GET /memory/items",
		"GET /memory/items/{id}",
		"GET /memory/entities",
		"GET /memory/entities/{id}",
		"GET /memory/taxonomy",
		"POST /memory/import",
		"GET /memory/imports/{id}",
		"GET /memory/imports",
		"POST /memory/consolidate",
	}
	for _, key := range required {
		if _, ok := routes[key]; !ok {
			t.Fatalf("missing route %q", key)
		}
	}
}

func TestQueryMemoryPassiveIncludeRestrictedReturns422(t *testing.T) {
	h := memoryHandlers{retriever: fakeMemoryRetriever{}}

	req := httptest.NewRequest(http.MethodPost, "/v1/memory/query", strings.NewReader(`{"query":"hello","mode":"passive","include_restricted":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: uuid.New(),
		Role:           "admin",
	}))

	rr := httptest.NewRecorder()
	h.queryMemory(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
	}
}

func TestBuildTaxonomyTreeHandlesOrphans(t *testing.T) {
	rootID := uuid.New()
	childID := uuid.New()
	orphanID := uuid.New()
	missingParentID := uuid.New()

	nodes := []repo.MemoryTaxonomyNode{
		{ID: rootID, Slug: "root", DisplayName: "Root"},
		{ID: childID, ParentID: &rootID, Slug: "child", DisplayName: "Child"},
		{ID: orphanID, ParentID: &missingParentID, Slug: "orphan", DisplayName: "Orphan"},
	}
	counts := map[uuid.UUID]int{rootID: 2, childID: 1, orphanID: 3}

	tree, flat := buildTaxonomyTree(nodes, counts)
	if len(tree) != 2 {
		t.Fatalf("tree roots len = %d, want 2", len(tree))
	}
	if len(flat) != 3 {
		t.Fatalf("flat len = %d, want 3", len(flat))
	}

	var root memoryTaxonomyNodeRecord
	foundRoot := false
	for _, item := range tree {
		if item.ID == rootID {
			root = item
			foundRoot = true
			break
		}
	}
	if !foundRoot {
		t.Fatalf("root %s not found", rootID)
	}
	if len(root.Children) != 1 || root.Children[0].ID != childID {
		t.Fatalf("root children = %+v, want child %s", root.Children, childID)
	}
}

func TestGetMemoryItemIncludesSupersessionChain(t *testing.T) {
	orgID := uuid.New()
	memoryA := repo.Memory{ID: uuid.New(), OrganizationID: orgID, MemoryType: "semantic", Scope: "org", Status: "superseded", CreatedAt: time.Now().Add(-2 * time.Hour)}
	memoryB := repo.Memory{ID: uuid.New(), OrganizationID: orgID, MemoryType: "semantic", Scope: "org", Status: "superseded", CreatedAt: time.Now().Add(-time.Hour)}
	memoryC := repo.Memory{ID: uuid.New(), OrganizationID: orgID, MemoryType: "semantic", Scope: "org", Status: "active", CreatedAt: time.Now()}

	h := memoryHandlers{
		memories:     fakeMemoryRepo{items: map[uuid.UUID]repo.Memory{memoryA.ID: memoryA}},
		taxonomyTags: fakeMemoryTaxonomyTagRepo{},
		mentions:     fakeMemoryEntityMentionRepo{},
		supersessions: fakeSupersessionReader{
			chain: []repo.Memory{memoryA, memoryB, memoryC},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/memory/items/"+memoryA.ID.String(), nil)
	req = withRouteParams(req, map[string]string{"id": memoryA.ID.String()})
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: orgID,
		Role:           "admin",
	}))

	rr := httptest.NewRecorder()
	h.getMemoryItem(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var envelope map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, _ := envelope["data"].(map[string]any)
	chain, _ := data["supersession_chain"].([]any)
	if len(chain) != 3 {
		t.Fatalf("supersession_chain len = %d, want 3 body=%s", len(chain), rr.Body.String())
	}

	ids := make([]string, 0, len(chain))
	for _, raw := range chain {
		item, _ := raw.(map[string]any)
		ids = append(ids, item["id"].(string))
	}
	if ids[0] != memoryA.ID.String() || ids[1] != memoryB.ID.String() || ids[2] != memoryC.ID.String() {
		t.Fatalf("chain ids = %v, want [%s %s %s]", ids, memoryA.ID, memoryB.ID, memoryC.ID)
	}
}

func TestGetMemoryItemIncludesSources(t *testing.T) {
	orgID := uuid.New()
	memoryID := uuid.New()
	sourceID := uuid.New()
	importID := uuid.New()
	sessionID := uuid.New()
	createdAt := time.Now().UTC().Round(time.Second)
	item := repo.Memory{ID: memoryID, OrganizationID: orgID, MemoryType: "semantic", Scope: "org", Status: "active", CreatedAt: createdAt, UpdatedAt: createdAt}

	h := memoryHandlers{
		memories:     fakeMemoryRepo{items: map[uuid.UUID]repo.Memory{memoryID: item}},
		sources:      fakeMemorySourceRepo{items: []repo.MemorySource{{ID: uuid.New(), MemoryID: memoryID, SourceType: "chat_message", SourceID: &sourceID, ImportID: &importID, SessionID: &sessionID, CreatedAt: createdAt}}},
		taxonomyTags: fakeMemoryTaxonomyTagRepo{},
		mentions:     fakeMemoryEntityMentionRepo{},
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/memory/items/"+memoryID.String(), nil)
	req = withRouteParams(req, map[string]string{"id": memoryID.String()})
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{
		UserID:         uuid.New(),
		OrganizationID: orgID,
		Role:           "admin",
	}))

	rr := httptest.NewRecorder()
	h.getMemoryItem(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var envelope map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, _ := envelope["data"].(map[string]any)
	sources, _ := data["sources"].([]any)
	if len(sources) != 1 {
		t.Fatalf("sources len = %d, want 1 body=%s", len(sources), rr.Body.String())
	}
	record, _ := sources[0].(map[string]any)
	if got := record["memory_id"]; got != memoryID.String() {
		t.Fatalf("sources[0].memory_id = %v, want %s", got, memoryID)
	}
	if got := record["source_type"]; got != "chat_message" {
		t.Fatalf("sources[0].source_type = %v, want chat_message", got)
	}
	if got := record["source_id"]; got != sourceID.String() {
		t.Fatalf("sources[0].source_id = %v, want %s", got, sourceID)
	}
	if got := record["import_id"]; got != importID.String() {
		t.Fatalf("sources[0].import_id = %v, want %s", got, importID)
	}
	if got := record["session_id"]; got != sessionID.String() {
		t.Fatalf("sources[0].session_id = %v, want %s", got, sessionID)
	}
}

type fakeMemoryRetriever struct{}

func (fakeMemoryRetriever) Query(context.Context, memory.RetrievalRequest) (memory.RetrievalResult, error) {
	return memory.RetrievalResult{}, nil
}

type fakeMemoryRepo struct {
	items map[uuid.UUID]repo.Memory
}

func (f fakeMemoryRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Memory, error) {
	item, ok := f.items[id]
	if !ok {
		return repo.Memory{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeMemorySourceRepo struct {
	items []repo.MemorySource
}

func (f fakeMemorySourceRepo) ListByMemory(_ context.Context, memoryID uuid.UUID) ([]repo.MemorySource, error) {
	items := make([]repo.MemorySource, 0, len(f.items))
	for _, item := range f.items {
		if item.MemoryID == memoryID {
			items = append(items, item)
		}
	}
	return items, nil
}

type fakeMemoryTaxonomyTagRepo struct{}

func (fakeMemoryTaxonomyTagRepo) ListByMemory(context.Context, uuid.UUID) ([]repo.MemoryTaxonomyTag, error) {
	return nil, nil
}

type fakeMemoryEntityMentionRepo struct{}

func (fakeMemoryEntityMentionRepo) ListByMemory(context.Context, uuid.UUID) ([]repo.MemoryEntityMention, error) {
	return nil, nil
}

type fakeSupersessionReader struct {
	chain []repo.Memory
}

func (f fakeSupersessionReader) GetSupersessionChain(context.Context, uuid.UUID) ([]repo.Memory, error) {
	return append([]repo.Memory(nil), f.chain...), nil
}
