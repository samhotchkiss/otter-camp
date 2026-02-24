package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	searchTypeProject      = "project"
	searchTypeTask         = "task"
	searchTypeAgent        = "agent"
	searchTypeSession      = "session"
	searchTypeFlowTemplate = "flow_template"
)

var validSearchTypes = map[string]struct{}{
	searchTypeProject:      {},
	searchTypeTask:         {},
	searchTypeAgent:        {},
	searchTypeSession:      {},
	searchTypeFlowTemplate: {},
}

type SearchHandler struct {
	pool *pgxpool.Pool
}

type SearchResult struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func NewSearchHandler(pool *pgxpool.Pool) SearchHandler {
	return SearchHandler{pool: pool}
}

func (h SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	responder := NewResponder(r.Context())
	if h.pool == nil {
		responder.Error(w, http.StatusNotImplemented, ErrCodeNotImplemented, "search service is not configured")
		return
	}

	orgID, ok := OrganizationIDFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, ErrCodeUnauthorized, "authentication required")
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) < 2 {
		responder.Error(w, http.StatusUnprocessableEntity, ErrCodeValidation, "q must be at least 2 characters")
		return
	}

	searchTypes, err := parseSearchTypes(r.URL.Query()["types"])
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, ErrCodeValidation, err.Error())
		return
	}

	limit := parseSearchLimit(r.URL.Query().Get("limit"))
	results, err := h.searchByTypes(r.Context(), orgID.String(), query, limit, searchTypes)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, ErrCodeInternal, "search failed")
		return
	}

	sort.Slice(results, func(i, j int) bool {
		left := strings.ToLower(results[i].Title)
		right := strings.ToLower(results[j].Title)
		if left == right {
			return results[i].ID < results[j].ID
		}
		return left < right
	})

	if len(results) > limit {
		results = results[:limit]
	}

	responder.JSON(w, http.StatusOK, map[string]any{
		"results":       results,
		"query":         query,
		"total_results": len(results),
	})
}

func (h SearchHandler) searchByTypes(ctx context.Context, organizationID, query string, limit int, searchTypes []string) ([]SearchResult, error) {
	results := make([]SearchResult, 0, limit)
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
	)

	pattern := "%" + query + "%"
	for _, typeName := range searchTypes {
		typeName := typeName
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, err := h.searchType(ctx, organizationID, pattern, limit, typeName)
			if isSchemaNotReadyError(err) {
				return
			}
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}

			mu.Lock()
			results = append(results, items...)
			mu.Unlock()
		}()
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

func (h SearchHandler) searchType(ctx context.Context, organizationID, pattern string, limit int, typeName string) ([]SearchResult, error) {
	switch typeName {
	case searchTypeProject:
		return h.searchProjects(ctx, organizationID, pattern, limit)
	case searchTypeTask:
		return h.searchTasks(ctx, organizationID, pattern, limit)
	case searchTypeAgent:
		return h.searchAgents(ctx, organizationID, pattern, limit)
	case searchTypeSession:
		return h.searchSessions(ctx, organizationID, pattern, limit)
	case searchTypeFlowTemplate:
		return h.searchFlowTemplates(ctx, organizationID, pattern, limit)
	default:
		return nil, fmt.Errorf("unsupported search type %q", typeName)
	}
}

func (h SearchHandler) searchProjects(ctx context.Context, organizationID, pattern string, limit int) ([]SearchResult, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT id::text, COALESCE(NULLIF(name, ''), slug) AS title
		FROM project
		WHERE organization_id = $1
		  AND (slug ILIKE $2 OR name ILIKE $2)
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`, organizationID, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSearchRows(rows, searchTypeProject, "/v1/projects/")
}

func (h SearchHandler) searchTasks(ctx context.Context, organizationID, pattern string, limit int) ([]SearchResult, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT t.id::text, t.title
		FROM project_task t
		JOIN project p ON p.id = t.project_id
		WHERE p.organization_id = $1
		  AND t.title ILIKE $2
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT $3
	`, organizationID, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSearchRows(rows, searchTypeTask, "/v1/tasks/")
}

func (h SearchHandler) searchAgents(ctx context.Context, organizationID, pattern string, limit int) ([]SearchResult, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT id::text, name
		FROM agent
		WHERE organization_id = $1
		  AND name ILIKE $2
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`, organizationID, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSearchRows(rows, searchTypeAgent, "/v1/agents/")
}

func (h SearchHandler) searchSessions(ctx context.Context, organizationID, pattern string, limit int) ([]SearchResult, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT id::text, id::text
		FROM chat_session
		WHERE organization_id = $1
		  AND id::text ILIKE $2
		ORDER BY created_at DESC, id DESC
		LIMIT $3
	`, organizationID, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSearchRows(rows, searchTypeSession, "/v1/chat-sessions/")
}

func (h SearchHandler) searchFlowTemplates(ctx context.Context, organizationID, pattern string, limit int) ([]SearchResult, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT ft.id::text, ft.name
		FROM flow_template ft
		LEFT JOIN project p ON p.id = ft.project_id
		WHERE (ft.organization_id = $1 OR p.organization_id = $1)
		  AND ft.name ILIKE $2
		ORDER BY ft.created_at DESC, ft.id DESC
		LIMIT $3
	`, organizationID, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSearchRows(rows, searchTypeFlowTemplate, "/v1/flow-templates/")
}

func scanSearchRows(rows pgx.Rows, typeName, urlPrefix string) ([]SearchResult, error) {
	results := make([]SearchResult, 0)
	for rows.Next() {
		var id string
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		results = append(results, SearchResult{
			Type:  typeName,
			ID:    id,
			Title: title,
			URL:   urlPrefix + id,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func parseSearchLimit(raw string) int {
	const (
		defaultLimit = 10
		maxLimit     = 50
	)
	limit := defaultLimit
	if strings.TrimSpace(raw) != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			limit = parsed
		}
	}
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func parseSearchTypes(rawValues []string) ([]string, error) {
	if len(rawValues) == 0 {
		return []string{
			searchTypeProject,
			searchTypeTask,
			searchTypeAgent,
			searchTypeSession,
			searchTypeFlowTemplate,
		}, nil
	}

	seen := make(map[string]struct{}, len(rawValues))
	types := make([]string, 0, len(rawValues))

	for _, rawValue := range rawValues {
		for _, segment := range strings.Split(rawValue, ",") {
			candidate := strings.ToLower(strings.TrimSpace(segment))
			if candidate == "" {
				continue
			}
			if _, ok := validSearchTypes[candidate]; !ok {
				return nil, fmt.Errorf("unsupported search type %q", candidate)
			}
			if _, exists := seen[candidate]; exists {
				continue
			}
			seen[candidate] = struct{}{}
			types = append(types, candidate)
		}
	}

	if len(types) == 0 {
		return []string{
			searchTypeProject,
			searchTypeTask,
			searchTypeAgent,
			searchTypeSession,
			searchTypeFlowTemplate,
		}, nil
	}

	return types, nil
}

func isSchemaNotReadyError(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "42P01" || pgErr.Code == "42703"
}
