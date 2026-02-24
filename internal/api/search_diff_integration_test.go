//go:build integration

package api

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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestSearchHandlerProjectTypeFiltersByOrg(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := repo.NewOrgRepo(pool)
	orgA, err := orgRepo.Create(context.Background(), repo.Organization{Slug: "search-org-a", DisplayName: "search-org-a"})
	if err != nil {
		t.Fatalf("create org A: %v", err)
	}
	orgB, err := orgRepo.Create(context.Background(), repo.Organization{Slug: "search-org-b", DisplayName: "search-org-b"})
	if err != nil {
		t.Fatalf("create org B: %v", err)
	}

	ensureProjectTable(t, pool)
	projectA := uuid.New()
	projectB := uuid.New()
	now := time.Now().UTC()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO project (id, organization_id, slug, display_name, delivery_mode, created_by_type, created_by_id, created_at)
		VALUES
			($1, $2, $3, $4, 'gated', 'system', $5, $6),
			($7, $8, $9, $10, 'gated', 'system', $11, $12)
	`, projectA, orgA.ID, "proj-alpha", "Proj Alpha", uuid.Nil, now, projectB, orgB.ID, "proj-beta", "Proj Beta", uuid.Nil, now); err != nil {
		t.Fatalf("seed projects: %v", err)
	}

	handler := NewSearchHandler(pool)
	req := httptest.NewRequest(http.MethodGet, "/v1/search?q=proj&types=project", nil)
	req = req.WithContext(WithOrganizationID(req.Context(), orgA.ID))
	rr := httptest.NewRecorder()
	handler.Search(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data := payload["data"].(map[string]any)
	results := data["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("results len = %d, want %d body=%s", len(results), 1, rr.Body.String())
	}

	item := results[0].(map[string]any)
	if got := item["type"]; got != "project" {
		t.Fatalf("result type = %v, want %q", got, "project")
	}
	if got := item["id"]; got != projectA.String() {
		t.Fatalf("result id = %v, want %q", got, projectA.String())
	}
}

func TestSearchHandlerShortQueryReturns422(t *testing.T) {
	handler := NewSearchHandler(testdb.New(t))

	req := httptest.NewRequest(http.MethodGet, "/v1/search?q=a", nil)
	req = req.WithContext(WithOrganizationID(req.Context(), uuid.New()))
	rr := httptest.NewRecorder()
	handler.Search(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusUnprocessableEntity, rr.Body.String())
	}
}

func TestDiffHandlerNoRemoteAndSuccess(t *testing.T) {
	pool := testdb.New(t)
	orgRepo := repo.NewOrgRepo(pool)
	org, err := orgRepo.Create(context.Background(), repo.Organization{Slug: "diff-org", DisplayName: "diff-org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	ensureProjectTable(t, pool)
	ensureTaskAndRemoteTables(t, pool)

	projectID := uuid.New()
	taskID := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO project (id, organization_id, slug, display_name, delivery_mode, default_branch, created_by_type, created_by_id, created_at)
		VALUES ($1, $2, $3, $4, 'gated', $5, 'system', $6, now())
	`, projectID, org.ID, "proj-diff", "Project Diff", "main", uuid.Nil); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO project_task (id, project_id, title, branch_name, created_at)
		VALUES ($1, $2, $3, $4, now())
	`, taskID, projectID, "Task Diff", "feature/diff"); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	noRemoteHandler := NewDiffHandler(pool, fakeGitService{})
	router := chi.NewRouter()
	router.Get("/v1/tasks/{id}/diff", noRemoteHandler.GetTaskDiff)

	noRemoteReq := httptest.NewRequest(http.MethodGet, "/v1/tasks/"+taskID.String()+"/diff", nil)
	noRemoteReq = noRemoteReq.WithContext(WithOrganizationID(noRemoteReq.Context(), org.ID))
	noRemoteRR := httptest.NewRecorder()
	router.ServeHTTP(noRemoteRR, noRemoteReq)

	if noRemoteRR.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no remote status = %d, want %d body=%s", noRemoteRR.Code, http.StatusUnprocessableEntity, noRemoteRR.Body.String())
	}

	if _, err := pool.Exec(context.Background(), `
		INSERT INTO project_remote (id, project_id, created_at)
		VALUES ($1, $2, now())
	`, uuid.New(), projectID); err != nil {
		t.Fatalf("seed project_remote: %v", err)
	}

	withRemoteHandler := NewDiffHandler(pool, fakeGitService{
		diff: GitDiff{
			BaseBranch: "main",
			DiffStat: GitDiffStat{
				FilesChanged: 3,
				Insertions:   45,
				Deletions:    12,
			},
			DiffText: strings.Repeat("diff --git a/x b/x\n", 10),
		},
	})
	router = chi.NewRouter()
	router.Get("/v1/tasks/{id}/diff", withRemoteHandler.GetTaskDiff)

	diffReq := httptest.NewRequest(http.MethodGet, "/v1/tasks/"+taskID.String()+"/diff", nil)
	diffReq = diffReq.WithContext(WithOrganizationID(diffReq.Context(), org.ID))
	diffRR := httptest.NewRecorder()
	router.ServeHTTP(diffRR, diffReq)

	if diffRR.Code != http.StatusOK {
		t.Fatalf("diff status = %d, want %d body=%s", diffRR.Code, http.StatusOK, diffRR.Body.String())
	}
}

func ensureProjectTable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS project (
			id uuid PRIMARY KEY,
			organization_id uuid NOT NULL REFERENCES organization(id) ON DELETE CASCADE,
			slug text NOT NULL,
			display_name text NOT NULL,
			delivery_mode text NOT NULL DEFAULT 'gated',
			default_branch text NOT NULL DEFAULT 'main',
			created_by_type text NOT NULL DEFAULT 'system',
			created_by_id uuid NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
			created_at timestamptz NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		t.Fatalf("create project table: %v", err)
	}
}

func ensureTaskAndRemoteTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS project_task (
			id uuid PRIMARY KEY,
			project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
			title text NOT NULL,
			branch_name text NOT NULL,
			created_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		t.Fatalf("create project_task table: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS project_remote (
			id uuid PRIMARY KEY,
			project_id uuid NOT NULL REFERENCES project(id) ON DELETE CASCADE,
			created_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		t.Fatalf("create project_remote table: %v", err)
	}
}

type fakeGitService struct {
	diff GitDiff
	err  error
}

func (f fakeGitService) Diff(context.Context, uuid.UUID, string, string) (GitDiff, error) {
	return f.diff, f.err
}
