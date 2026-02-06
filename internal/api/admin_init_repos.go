package api

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

// HandleAdminInitRepos initializes git repos for all projects in the org.
// POST /api/admin/init-repos
func HandleAdminInitRepos(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
			return
		}

		if db == nil {
			sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
			return
		}

		orgID := middleware.WorkspaceFromContext(r.Context())
		if orgID == "" {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing org_id"})
			return
		}

		projectStore := store.NewProjectStore(db)
		projects, err := projectStore.List(r.Context())
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
			return
		}

		results := make([]map[string]any, 0, len(projects))
		for _, p := range projects {
			result := map[string]any{
				"id":   p.ID,
				"name": p.Name,
			}

			if err := projectStore.InitProjectRepo(r.Context(), p.ID); err != nil {
				result["status"] = "error"
				result["error"] = err.Error()
				log.Printf("[admin] init repo failed for %s (%s): %v", p.Name, p.ID, err)
			} else {
				repoPath, _ := projectStore.GetRepoPath(r.Context(), p.ID)
				result["status"] = "ok"
				result["repo_path"] = repoPath
				log.Printf("[admin] init repo ok for %s (%s): %s", p.Name, p.ID, repoPath)
			}

			results = append(results, result)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"initialized": len(results),
			"results":     results,
		})
	}
}
