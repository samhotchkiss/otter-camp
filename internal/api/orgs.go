package api

import (
	"database/sql"
	"net/http"
)

type OrgResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// HandleOrgsList returns orgs available to the authenticated user.
// GET /api/orgs
func HandleOrgsList(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
			return
		}

		if db == nil {
			sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
			return
		}

		identity, err := requireSessionIdentity(r.Context(), db, r)
		if err != nil {
			sendJSON(w, http.StatusUnauthorized, errorResponse{Error: err.Error()})
			return
		}

		rows, err := db.QueryContext(
			r.Context(),
			`SELECT o.id::text, o.name, o.slug
			 FROM organizations o
			 JOIN users u ON u.org_id = o.id
			 WHERE u.id = $1
			 ORDER BY o.name`,
			identity.UserID,
		)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load orgs"})
			return
		}
		defer rows.Close()

		orgs := []OrgResponse{}
		for rows.Next() {
			var o OrgResponse
			if err := rows.Scan(&o.ID, &o.Name, &o.Slug); err != nil {
				continue
			}
			orgs = append(orgs, o)
		}

		sendJSON(w, http.StatusOK, map[string]interface{}{
			"orgs":  orgs,
			"total": len(orgs),
		})
	}
}
