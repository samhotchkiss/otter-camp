//go:build integration

package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	memoryimporter "github.com/samhotchkiss/otter-camp/internal/memory/importer"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	nativetools "github.com/samhotchkiss/otter-camp/internal/tools/native"
	"golang.org/x/crypto/bcrypt"
)

func TestMemoryItemsRequiresAuth(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	resp := mustJSON(t, http.MethodGet, fixture.URL+"/v1/memory/items", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusUnauthorized, string(resp.Body))
	}
}

func TestMemoryQueryPassiveIncludeRestrictedReturns422(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	resp := mustJSON(t, http.MethodPost, fixture.URL+"/v1/memory/query", map[string]any{
		"query":              "release notes",
		"mode":               "passive",
		"include_restricted": true,
	}, map[string]string{"Authorization": "Bearer " + token})

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusUnprocessableEntity, string(resp.Body))
	}
}

func TestMemoryRecordToolExecutionAppearsInListAndQuery(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	ctx := context.Background()
	agent, err := repo.NewAgentRepo(fixture.Pool).Create(ctx, repo.Agent{
		OrganizationID:       fixture.Org.ID,
		DisplayName:          "Memory Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	executor := nativetools.NewExecutor(nativetools.ExecutorOptions{
		Pool:          fixture.Pool,
		WorkspaceRoot: t.TempDir(),
	})
	execCtx := mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: fixture.Org.ID,
		AgentID:        &agent.ID,
	})
	recorded, err := executor.Execute(execCtx, "memory.record", map[string]any{
		"content": "release retrospective highlighted rollback checklist",
		"scope":   "org",
	})
	if err != nil {
		t.Fatalf("memory.record: %v", err)
	}
	if got := recorded["status"]; got != "stored" {
		t.Fatalf("record status = %v, want stored", got)
	}
	memoryID, ok := recorded["memory_id"].(uuid.UUID)
	if !ok {
		t.Fatalf("memory_id type = %T, want uuid.UUID", recorded["memory_id"])
	}

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	items := mustJSON(t, http.MethodGet, fixture.URL+"/v1/memory/items?status=active", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if items.StatusCode != http.StatusOK {
		t.Fatalf("items status = %d, want %d body=%s", items.StatusCode, http.StatusOK, string(items.Body))
	}
	var itemPayload struct {
		Data []memoryItemRecord `json:"data"`
	}
	if err := json.Unmarshal(items.Body, &itemPayload); err != nil {
		t.Fatalf("unmarshal items: %v", err)
	}
	found := false
	for _, item := range itemPayload.Data {
		if item.ID == memoryID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("recorded memory %s missing from /v1/memory/items body=%s", memoryID, string(items.Body))
	}

	query := mustJSON(t, http.MethodPost, fixture.URL+"/v1/memory/query", map[string]any{
		"query":       "rollback checklist",
		"mode":        "passive",
		"max_results": 5,
	}, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if query.StatusCode != http.StatusOK {
		t.Fatalf("query status = %d, want %d body=%s", query.StatusCode, http.StatusOK, string(query.Body))
	}
	var queryPayload struct {
		Data struct {
			Memories []memoryQueryResultItem `json:"memories"`
		} `json:"data"`
	}
	if err := json.Unmarshal(query.Body, &queryPayload); err != nil {
		t.Fatalf("unmarshal query: %v", err)
	}
	if len(queryPayload.Data.Memories) == 0 {
		t.Fatalf("expected query to return recorded memory body=%s", string(query.Body))
	}
}

func TestMemoryItemsStatusFiltersByOrg(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	ctx := context.Background()
	memoryRepo := repo.NewMemoryRepo(fixture.Pool)
	active, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "org-a active memory",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(9),
		Status:         "active",
		TrustTier:      0.9,
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create active memory: %v", err)
	}
	candidate, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "org-a superseded memory",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(8),
		Status:         "active",
		TrustTier:      0.8,
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create candidate memory: %v", err)
	}
	if _, err := memoryRepo.Supersede(ctx, candidate.ID, active.ID); err != nil {
		t.Fatalf("supersede memory: %v", err)
	}

	otherOrg, err := repo.NewOrgRepo(fixture.Pool).Create(ctx, repo.Organization{Slug: "memory-other-" + uuid.NewString()[:8], DisplayName: "Other"})
	if err != nil {
		t.Fatalf("create other org: %v", err)
	}
	if _, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: otherOrg.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "org-b active memory",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(7),
		Status:         "active",
		TrustTier:      0.9,
		Sensitivity:    "normal",
	}); err != nil {
		t.Fatalf("create other org memory: %v", err)
	}

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	resp := mustJSON(t, http.MethodGet, fixture.URL+"/v1/memory/items?status=active", nil, map[string]string{"Authorization": "Bearer " + token})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	var payload struct {
		Data []memoryItemRecord `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("active items len = %d, want 1 body=%s", len(payload.Data), string(resp.Body))
	}
	if payload.Data[0].ID != active.ID {
		t.Fatalf("active item id = %s, want %s", payload.Data[0].ID, active.ID)
	}
}

func TestMemoryItemSupersessionChain(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	ctx := context.Background()
	memoryRepo := repo.NewMemoryRepo(fixture.Pool)
	memoryA, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "A",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(5),
		Status:         "active",
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create memory A: %v", err)
	}
	memoryB, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "B",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(6),
		Status:         "active",
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create memory B: %v", err)
	}
	memoryC, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "C",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(7),
		Status:         "active",
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create memory C: %v", err)
	}
	if _, err := memoryRepo.Supersede(ctx, memoryA.ID, memoryB.ID); err != nil {
		t.Fatalf("supersede A: %v", err)
	}
	if _, err := memoryRepo.Supersede(ctx, memoryB.ID, memoryC.ID); err != nil {
		t.Fatalf("supersede B: %v", err)
	}

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	resp := mustJSON(t, http.MethodGet, fixture.URL+"/v1/memory/items/"+memoryA.ID.String(), nil, map[string]string{"Authorization": "Bearer " + token})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	var payload struct {
		Data memoryItemDetailRecord `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	chain := payload.Data.SupersessionChain
	if len(chain) != 3 {
		t.Fatalf("chain len = %d, want 3 body=%s", len(chain), string(resp.Body))
	}
	if chain[0].ID != memoryA.ID || chain[1].ID != memoryB.ID || chain[2].ID != memoryC.ID {
		t.Fatalf("chain ids = [%s %s %s], want [%s %s %s]", chain[0].ID, chain[1].ID, chain[2].ID, memoryA.ID, memoryB.ID, memoryC.ID)
	}
}

func TestMemoryTaxonomyNestedCounts(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	ctx := context.Background()
	taxRepo := repo.NewMemoryTaxonomyNodeRepo(fixture.Pool)
	tagRepo := repo.NewMemoryTaxonomyTagRepo(fixture.Pool)
	memoryRepo := repo.NewMemoryRepo(fixture.Pool)

	root, err := taxRepo.Create(ctx, repo.MemoryTaxonomyNode{
		OrganizationID: fixture.Org.ID,
		Slug:           "workflow",
		DisplayName:    "Workflow",
	})
	if err != nil {
		t.Fatalf("create root node: %v", err)
	}
	child, err := taxRepo.Create(ctx, repo.MemoryTaxonomyNode{
		OrganizationID: fixture.Org.ID,
		ParentID:       &root.ID,
		Slug:           "release",
		DisplayName:    "Release",
	})
	if err != nil {
		t.Fatalf("create child node: %v", err)
	}

	activeRoot, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "root memory",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(1),
		Status:         "active",
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create active root memory: %v", err)
	}
	activeChild, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "child memory",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(2),
		Status:         "active",
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create active child memory: %v", err)
	}
	archived, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "archived memory",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(3),
		Status:         "active",
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create archived memory: %v", err)
	}
	if _, err := memoryRepo.Archive(ctx, archived.ID); err != nil {
		t.Fatalf("archive memory: %v", err)
	}

	if _, err := tagRepo.Create(ctx, repo.MemoryTaxonomyTag{MemoryID: activeRoot.ID, TaxonomyNodeID: root.ID, AssignedBy: "manual", Confidence: 1}); err != nil {
		t.Fatalf("tag root: %v", err)
	}
	if _, err := tagRepo.Create(ctx, repo.MemoryTaxonomyTag{MemoryID: activeChild.ID, TaxonomyNodeID: child.ID, AssignedBy: "manual", Confidence: 1}); err != nil {
		t.Fatalf("tag child: %v", err)
	}
	if _, err := tagRepo.Create(ctx, repo.MemoryTaxonomyTag{MemoryID: archived.ID, TaxonomyNodeID: child.ID, AssignedBy: "manual", Confidence: 1}); err != nil {
		t.Fatalf("tag archived child: %v", err)
	}

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	resp := mustJSON(t, http.MethodGet, fixture.URL+"/v1/memory/taxonomy", nil, map[string]string{"Authorization": "Bearer " + token})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	var payload struct {
		Data []memoryTaxonomyNodeRecord `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("tree roots len = %d, want 1 body=%s", len(payload.Data), string(resp.Body))
	}
	if payload.Data[0].ID != root.ID {
		t.Fatalf("root id = %s, want %s", payload.Data[0].ID, root.ID)
	}
	if payload.Data[0].MemoryCount != 1 {
		t.Fatalf("root memory_count = %d, want 1", payload.Data[0].MemoryCount)
	}
	if len(payload.Data[0].Children) != 1 {
		t.Fatalf("root children len = %d, want 1", len(payload.Data[0].Children))
	}
	if payload.Data[0].Children[0].ID != child.ID {
		t.Fatalf("child id = %s, want %s", payload.Data[0].Children[0].ID, child.ID)
	}
	if payload.Data[0].Children[0].MemoryCount != 1 {
		t.Fatalf("child memory_count = %d, want 1", payload.Data[0].Children[0].MemoryCount)
	}
}

func TestMemoryConsolidateValidationAndRBAC(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	adminToken := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	memberToken := loginToken(t, fixture.URL, fixture.Member.Email, "member-password")

	missingTask := mustJSON(t, http.MethodPost, fixture.URL+"/v1/memory/consolidate", map[string]any{
		"run_type": "task_consolidation",
	}, map[string]string{"Authorization": "Bearer " + adminToken})
	if missingTask.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing task status = %d, want %d body=%s", missingTask.StatusCode, http.StatusUnprocessableEntity, string(missingTask.Body))
	}

	forbidden := mustJSON(t, http.MethodPost, fixture.URL+"/v1/memory/consolidate", map[string]any{
		"run_type": "sleep_reflection",
	}, map[string]string{"Authorization": "Bearer " + memberToken})
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("forbidden status = %d, want %d body=%s", forbidden.StatusCode, http.StatusForbidden, string(forbidden.Body))
	}
}

func TestMemoryCompactionRunsAutoFailStalePending(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	ctx := context.Background()
	runs := repo.NewMemoryCompactionRunRepo(fixture.Pool)
	stale, err := runs.Create(ctx, repo.MemoryCompactionRun{
		OrganizationID: fixture.Org.ID,
		RunType:        "task_consolidation",
		Status:         "pending",
		ScopeContext:   `{"task_id":"` + uuid.NewString() + `"}`,
	})
	if err != nil {
		t.Fatalf("create stale run: %v", err)
	}
	if _, err := fixture.Pool.Exec(ctx, `
		UPDATE memory_compaction_run
		SET created_at = $2
		WHERE id = $1
	`, stale.ID, time.Now().UTC().Add(-2*time.Hour)); err != nil {
		t.Fatalf("age stale run: %v", err)
	}

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	resp := mustJSON(t, http.MethodGet, fixture.URL+"/v1/memory/compaction-runs", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	var payload struct {
		Data []memoryCompactionRunRecord `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	found := false
	for _, item := range payload.Data {
		if item.ID != stale.ID {
			continue
		}
		found = true
		if item.Status != "failed" {
			t.Fatalf("stale run status = %q, want failed", item.Status)
		}
		if item.ErrorMessage == nil || strings.TrimSpace(*item.ErrorMessage) != repo.MemoryCompactionPendingTimeoutReason {
			t.Fatalf("stale run error_message = %v, want %q", item.ErrorMessage, repo.MemoryCompactionPendingTimeoutReason)
		}
		break
	}
	if !found {
		t.Fatalf("stale run %s not found in response body=%s", stale.ID, string(resp.Body))
	}
}

func TestMemoryQueryRanksMostRelevantLast(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	ctx := context.Background()
	memoryRepo := repo.NewMemoryRepo(fixture.Pool)
	for i := 1; i <= 20; i++ {
		if _, err := memoryRepo.Create(ctx, repo.Memory{
			OrganizationID: fixture.Org.ID,
			MemoryType:     "semantic",
			Scope:          "org",
			Content:        fmt.Sprintf("memory-%02d", i),
			ContentHash:    fmt.Sprintf("hash-%02d", i),
			Embedding:      embeddingWithSignal(float32(i)),
			Status:         "active",
			TrustTier:      0.8,
			Sensitivity:    "normal",
		}); err != nil {
			t.Fatalf("create memory %d: %v", i, err)
		}
	}

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	resp := mustJSON(t, http.MethodPost, fixture.URL+"/v1/memory/query", map[string]any{
		"query":       "30",
		"mode":        "passive",
		"max_results": 5,
	}, map[string]string{"Authorization": "Bearer " + token})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	var payload struct {
		Data struct {
			Memories []memoryQueryResultItem `json:"memories"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if len(payload.Data.Memories) != 5 {
		t.Fatalf("result len = %d, want 5 body=%s", len(payload.Data.Memories), string(resp.Body))
	}

	want := []string{"memory-16", "memory-17", "memory-18", "memory-19", "memory-20"}
	for i, item := range payload.Data.Memories {
		if item.Content != want[i] {
			t.Fatalf("result[%d].content = %q, want %q", i, item.Content, want[i])
		}
	}
}

func TestMemoryItemsTrustTierMinFilter(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	ctx := context.Background()
	memoryRepo := repo.NewMemoryRepo(fixture.Pool)
	high, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "high trust",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(9),
		Status:         "active",
		TrustTier:      0.9,
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create high trust: %v", err)
	}
	if _, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "low trust",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(2),
		Status:         "active",
		TrustTier:      0.5,
		Sensitivity:    "normal",
	}); err != nil {
		t.Fatalf("create low trust: %v", err)
	}

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	resp := mustJSON(t, http.MethodGet, fixture.URL+"/v1/memory/items?status=active&trust_tier_min=0.8", nil, map[string]string{"Authorization": "Bearer " + token})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(resp.Body))
	}

	var payload struct {
		Data []memoryItemRecord `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("items len = %d, want 1 body=%s", len(payload.Data), string(resp.Body))
	}
	if payload.Data[0].ID != high.ID {
		t.Fatalf("returned id = %s, want %s", payload.Data[0].ID, high.ID)
	}
}

func TestMemoryDedupStatusFilters(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	ctx := context.Background()
	memoryRepo := repo.NewMemoryRepo(fixture.Pool)
	dedupRepo := repo.NewMemoryDedupReviewedRepo(fixture.Pool)

	memoryA, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "duplicate-a",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(4),
		Status:         "active",
		TrustTier:      0.8,
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create memory A: %v", err)
	}
	memoryB, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "duplicate-b",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(5),
		Status:         "active",
		TrustTier:      0.8,
		Sensitivity:    "normal",
	})
	if err != nil {
		t.Fatalf("create memory B: %v", err)
	}
	if _, err := dedupRepo.Create(ctx, repo.MemoryDedupReviewed{
		MemoryIDA:        memoryA.ID,
		MemoryIDB:        memoryB.ID,
		CosineSimilarity: 0.98,
		Decision:         "supersede_a",
		ReviewedBy:       "human",
	}); err != nil {
		t.Fatalf("create dedup review: %v", err)
	}
	if _, err := memoryRepo.Supersede(ctx, memoryA.ID, memoryB.ID); err != nil {
		t.Fatalf("supersede A: %v", err)
	}

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	superseded := mustJSON(t, http.MethodGet, fixture.URL+"/v1/memory/items?status=superseded", nil, map[string]string{"Authorization": "Bearer " + token})
	if superseded.StatusCode != http.StatusOK {
		t.Fatalf("superseded status = %d, want %d body=%s", superseded.StatusCode, http.StatusOK, string(superseded.Body))
	}
	active := mustJSON(t, http.MethodGet, fixture.URL+"/v1/memory/items?status=active", nil, map[string]string{"Authorization": "Bearer " + token})
	if active.StatusCode != http.StatusOK {
		t.Fatalf("active status = %d, want %d body=%s", active.StatusCode, http.StatusOK, string(active.Body))
	}

	var supersededPayload struct {
		Data []memoryItemRecord `json:"data"`
	}
	if err := json.Unmarshal(superseded.Body, &supersededPayload); err != nil {
		t.Fatalf("unmarshal superseded: %v", err)
	}
	if len(supersededPayload.Data) != 1 || supersededPayload.Data[0].ID != memoryA.ID {
		t.Fatalf("superseded data = %+v, want only %s", supersededPayload.Data, memoryA.ID)
	}

	var activePayload struct {
		Data []memoryItemRecord `json:"data"`
	}
	if err := json.Unmarshal(active.Body, &activePayload); err != nil {
		t.Fatalf("unmarshal active: %v", err)
	}
	for _, item := range activePayload.Data {
		if item.ID == memoryA.ID {
			t.Fatalf("active list unexpectedly contains superseded memory %s", memoryA.ID)
		}
	}
}

func TestMemoryPassiveCooldownAcrossRequests(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	ctx := context.Background()
	memoryRepo := repo.NewMemoryRepo(fixture.Pool)
	if _, err := memoryRepo.Create(ctx, repo.Memory{
		OrganizationID: fixture.Org.ID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        "cooldown-memory",
		ContentHash:    uuid.NewString(),
		Embedding:      embeddingWithSignal(10),
		Status:         "active",
		TrustTier:      0.9,
		Sensitivity:    "normal",
	}); err != nil {
		t.Fatalf("create memory: %v", err)
	}

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	for turn := 1; turn <= 6; turn++ {
		resp := mustJSON(t, http.MethodPost, fixture.URL+"/v1/memory/query", map[string]any{
			"query":       "10",
			"mode":        "passive",
			"max_results": 1,
		}, map[string]string{"Authorization": "Bearer " + token})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("turn %d status = %d, want %d body=%s", turn, resp.StatusCode, http.StatusOK, string(resp.Body))
		}

		var payload struct {
			Data struct {
				Memories []memoryQueryResultItem `json:"memories"`
			} `json:"data"`
		}
		if err := json.Unmarshal(resp.Body, &payload); err != nil {
			t.Fatalf("turn %d unmarshal: %v", turn, err)
		}

		switch turn {
		case 1, 6:
			if len(payload.Data.Memories) != 1 {
				t.Fatalf("turn %d memories len = %d, want 1", turn, len(payload.Data.Memories))
			}
		default:
			if len(payload.Data.Memories) != 0 {
				t.Fatalf("turn %d memories len = %d, want 0", turn, len(payload.Data.Memories))
			}
		}
	}
}

func TestMemoryImportRoundTripViaAPI(t *testing.T) {
	t.Setenv("OTTERCAMP_AUTH_MODE", "standard")
	fixture := newMemoryAPITestServer(t)
	defer fixture.Close()

	token := loginToken(t, fixture.URL, fixture.Admin.Email, "admin-password")
	archive := buildMemoryImportArchive(t, 220)

	created := postMemoryImportMultipart(t, fixture.URL, token, archive)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create import status = %d, want %d body=%s", created.StatusCode, http.StatusCreated, string(created.Body))
	}
	importID := jsonPathString(t, created.Body, "data", "import_id")
	if importID == "" {
		t.Fatalf("missing import_id in response: %s", string(created.Body))
	}

	var (
		lastProcessed int = -1
		sawIncrease   bool
	)
	deadline := time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for import completion")
		}

		statusResp := mustJSON(t, http.MethodGet, fixture.URL+"/v1/memory/imports/"+importID, nil, map[string]string{"Authorization": "Bearer " + token})
		if statusResp.StatusCode != http.StatusOK {
			t.Fatalf("status poll = %d, want %d body=%s", statusResp.StatusCode, http.StatusOK, string(statusResp.Body))
		}

		var payload struct {
			Data memoryImportRecord `json:"data"`
		}
		if err := json.Unmarshal(statusResp.Body, &payload); err != nil {
			t.Fatalf("unmarshal status body: %v", err)
		}
		if lastProcessed >= 0 && payload.Data.ProcessedRecords > lastProcessed {
			sawIncrease = true
		}
		lastProcessed = payload.Data.ProcessedRecords

		if payload.Data.Status == "completed" {
			break
		}
		if payload.Data.Status == "failed" {
			t.Fatalf("import failed: %+v", payload.Data)
		}
		time.Sleep(30 * time.Millisecond)
	}

	if !sawIncrease {
		t.Fatal("expected processed_records to increment while import was running")
	}

	itemsResp := mustJSON(t, http.MethodGet, fixture.URL+"/v1/memory/items?status=active&search=import-record-1", nil, map[string]string{"Authorization": "Bearer " + token})
	if itemsResp.StatusCode != http.StatusOK {
		t.Fatalf("items status = %d, want %d body=%s", itemsResp.StatusCode, http.StatusOK, string(itemsResp.Body))
	}
	var itemsPayload struct {
		Data []memoryItemRecord `json:"data"`
	}
	if err := json.Unmarshal(itemsResp.Body, &itemsPayload); err != nil {
		t.Fatalf("unmarshal items body: %v", err)
	}
	if len(itemsPayload.Data) == 0 {
		t.Fatalf("expected imported memories to be queryable, got 0 items")
	}
}

type memoryAPITestFixture struct {
	URL     string
	Pool    *pgxpool.Pool
	Org     repo.Organization
	Admin   repo.HumanUser
	Member  repo.HumanUser
	handler http.Handler

	ts         *httptest.Server
	worker     *jobqueue.Worker
	cancelWork context.CancelFunc
	workerErr  chan error
}

func newMemoryAPITestServer(t *testing.T) *memoryAPITestFixture {
	t.Helper()

	pool := testdb.New(t)
	org, admin := createOrgAndUser(t, pool, "memory-http-org", "memory-admin@example.com", "Memory Admin", "admin", "admin-password")
	_, member := createOrgAndUser(t, pool, "memory-http-org", "memory-member@example.com", "Memory Member", "member", "member-password")

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: org.ID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("new auth service: %v", err)
	}

	retriever, err := memory.NewRetriever(memory.RetrieverOptions{
		Pool:     pool,
		Embedder: memoryAPIEmbedder{},
	})
	if err != nil {
		t.Fatalf("new retriever: %v", err)
	}

	store, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("new fs store: %v", err)
	}
	imp, err := memoryimporter.NewImporter(memoryimporter.ImporterOptions{
		Pool:      pool,
		Store:     store,
		Extractor: &memoryAPIImportExtractor{memoryRepo: repo.NewMemoryRepo(pool), sleepPerBatch: 75 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("new importer: %v", err)
	}

	worker := jobqueue.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), jobqueue.Config{PollInterval: 10 * time.Millisecond})
	imp.RegisterJobs(worker)
	workerCtx, cancel := context.WithCancel(context.Background())
	workerErr := make(chan error, 1)
	go func() {
		workerErr <- worker.Start(workerCtx)
	}()

	memoryRegistrar := NewMemoryRouteRegistrar(MemoryRouteOptions{
		Pool:      pool,
		Retriever: retriever,
		Importer:  imp,
		Store:     store,
		Enqueuer:  worker,
	})

	handler := NewHandlerWithOptions(HandlerOptions{
		Version:     "test-version",
		AuthService: authService,
		Pool:        pool,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		RouteRegistrars: []RouteRegistrar{
			memoryRegistrar,
		},
	})

	ts := httptest.NewServer(handler)
	return &memoryAPITestFixture{
		URL:        ts.URL,
		Pool:       pool,
		Org:        org,
		Admin:      admin,
		Member:     member,
		handler:    handler,
		ts:         ts,
		worker:     worker,
		cancelWork: cancel,
		workerErr:  workerErr,
	}
}

func (f *memoryAPITestFixture) Close() {
	if f == nil {
		return
	}
	if f.cancelWork != nil {
		f.cancelWork()
	}
	if f.worker != nil {
		_ = f.worker.Stop()
	}
	if f.workerErr != nil {
		select {
		case <-f.workerErr:
		case <-time.After(2 * time.Second):
		}
	}
	if f.ts != nil {
		f.ts.Close()
	}
}

type memoryAPIEmbedder struct{}

func (memoryAPIEmbedder) Embed(_ context.Context, _ uuid.UUID, _ string, inputs []string) ([][]float32, error) {
	out := make([][]float32, 0, len(inputs))
	for _, input := range inputs {
		signal := float32(1)
		trimmed := strings.TrimSpace(input)
		if parsed, err := strconv.Atoi(trimmed); err == nil && parsed > 0 {
			signal = float32(parsed)
		}
		out = append(out, embeddingWithSignal(signal))
	}
	return out, nil
}

type memoryAPIImportExtractor struct {
	memoryRepo    *repo.MemoryRepo
	sleepPerBatch time.Duration
}

func (e *memoryAPIImportExtractor) ExtractFromImport(ctx context.Context, orgID, _ uuid.UUID, records []memory.ImportRecord) error {
	for idx, record := range records {
		content := strings.TrimSpace(record.Content)
		if content == "" {
			continue
		}
		memoryType := strings.ToLower(strings.TrimSpace(record.MemoryType))
		if memoryType == "" {
			memoryType = "semantic"
		}
		if _, err := e.memoryRepo.Create(ctx, repo.Memory{
			OrganizationID: orgID,
			MemoryType:     memoryType,
			Scope:          "org",
			Content:        content,
			ContentHash:    fmt.Sprintf("import-%d-%s", idx, uuid.NewString()),
			Embedding:      embeddingWithSignal(5),
			Status:         "active",
			TrustTier:      0.8,
			Sensitivity:    "normal",
		}); err != nil {
			return err
		}
	}
	if e.sleepPerBatch > 0 {
		time.Sleep(e.sleepPerBatch)
	}
	return nil
}

func postMemoryImportMultipart(t *testing.T, baseURL, token string, archive []byte) jsonResponse {
	t.Helper()

	body := bytes.NewBuffer(nil)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "memory.zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(archive); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/memory/import", body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do: %v", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return jsonResponse{StatusCode: resp.StatusCode, Headers: resp.Header.Clone(), Body: respBody}
}

func buildMemoryImportArchive(t *testing.T, count int) []byte {
	t.Helper()

	buf := bytes.NewBuffer(nil)
	zw := zip.NewWriter(buf)
	w, err := zw.Create("memory.jsonl")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	for i := 0; i < count; i++ {
		line := fmt.Sprintf(`{"content":"import-record-%d","memory_type":"semantic"}`, i+1)
		if i > 0 {
			_, _ = w.Write([]byte("\n"))
		}
		_, _ = w.Write([]byte(line))
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func embeddingWithSignal(signal float32) []float32 {
	vector := make([]float32, 1536)
	vector[0] = signal
	vector[1] = 1
	return vector
}
