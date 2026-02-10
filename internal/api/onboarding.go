package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	onboardingDefaultProjectName = "Getting Started"
	onboardingDefaultIssueTitle  = "Welcome to Otter Camp"
	onboardingDefaultIssueBody   = "Welcome to Otter Camp.\n\nStart by creating your first project issue, inviting agents, and connecting your workflows."
	onboardingUserIssuer         = "otter.local"
	onboardingMaxBodyBytes       = 32 * 1024
)

type OnboardingBootstrapRequest struct {
	Name             string `json:"name"`
	Email            string `json:"email"`
	OrganizationName string `json:"organization_name"`
	Organization     string `json:"organization"`
	OrgName          string `json:"org_name"`
}

type OnboardingBootstrapResponse struct {
	OrgID       string    `json:"org_id"`
	OrgSlug     string    `json:"org_slug"`
	UserID      string    `json:"user_id"`
	Token       string    `json:"token"`
	ExpiresAt   time.Time `json:"expires_at"`
	ProjectID   string    `json:"project_id"`
	ProjectName string    `json:"project_name"`
	IssueID     string    `json:"issue_id"`
	IssueNumber int64     `json:"issue_number"`
	IssueTitle  string    `json:"issue_title"`
}

func HandleOnboardingBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	db, err := getAuthDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database unavailable"})
		return
	}
	locked, err := onboardingSetupLocked(r.Context(), db)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to evaluate onboarding state"})
		return
	}
	if locked {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "onboarding already completed"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, onboardingMaxBodyBytes)
	var req OnboardingBootstrapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			sendJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "name is required"})
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "email is required"})
		return
	}
	if !looksLikeEmail(email) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid email"})
		return
	}

	orgName := strings.TrimSpace(req.OrganizationName)
	if orgName == "" {
		orgName = strings.TrimSpace(req.Organization)
	}
	if orgName == "" {
		orgName = strings.TrimSpace(req.OrgName)
	}
	if orgName == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "organization_name is required"})
		return
	}

	orgSlug := slugifyOrg(orgName)
	if orgSlug == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "organization_name must contain letters or numbers"})
		return
	}

	resp, err := bootstrapOnboarding(r.Context(), db, name, email, orgName, orgSlug)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to bootstrap onboarding"})
		return
	}

	sendJSON(w, http.StatusOK, resp)
}

func bootstrapOnboarding(ctx context.Context, db *sql.DB, name, email, orgName, orgSlug string) (OnboardingBootstrapResponse, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return OnboardingBootstrapResponse{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	orgID, err := upsertOnboardingOrg(ctx, tx, orgName, orgSlug)
	if err != nil {
		return OnboardingBootstrapResponse{}, err
	}

	userID, err := upsertOnboardingUser(ctx, tx, orgID, name, email)
	if err != nil {
		return OnboardingBootstrapResponse{}, err
	}

	token, expiresAt, err := createOnboardingSession(ctx, tx, orgID, userID)
	if err != nil {
		return OnboardingBootstrapResponse{}, err
	}

	projectID, err := ensureOnboardingProject(ctx, tx, orgID)
	if err != nil {
		return OnboardingBootstrapResponse{}, err
	}

	issueID, issueNumber, err := ensureOnboardingIssue(ctx, tx, orgID, projectID)
	if err != nil {
		return OnboardingBootstrapResponse{}, err
	}

	if err := tx.Commit(); err != nil {
		return OnboardingBootstrapResponse{}, err
	}

	return OnboardingBootstrapResponse{
		OrgID:       orgID,
		OrgSlug:     orgSlug,
		UserID:      userID,
		Token:       token,
		ExpiresAt:   expiresAt,
		ProjectID:   projectID,
		ProjectName: onboardingDefaultProjectName,
		IssueID:     issueID,
		IssueNumber: issueNumber,
		IssueTitle:  onboardingDefaultIssueTitle,
	}, nil
}

func upsertOnboardingOrg(ctx context.Context, tx *sql.Tx, name, slug string) (string, error) {
	var orgID string
	err := tx.QueryRowContext(
		ctx,
		`INSERT INTO organizations (name, slug)
		 VALUES ($1, $2)
		 ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name
		 RETURNING id`,
		name,
		slug,
	).Scan(&orgID)
	return orgID, err
}

func upsertOnboardingUser(ctx context.Context, tx *sql.Tx, orgID, name, email string) (string, error) {
	subject := "local:" + strings.ToLower(strings.TrimSpace(email))
	if subject == "local:" {
		return "", errors.New("invalid email subject")
	}

	var userID string
	err := tx.QueryRowContext(
		ctx,
		`INSERT INTO users (org_id, subject, issuer, display_name, email, role)
		 VALUES ($1, $2, $3, $4, $5, 'owner')
		 ON CONFLICT (org_id, issuer, subject)
		 DO UPDATE SET
			display_name = EXCLUDED.display_name,
			email = EXCLUDED.email,
			updated_at = NOW()
		 RETURNING id`,
		orgID,
		subject,
		onboardingUserIssuer,
		name,
		email,
	).Scan(&userID)
	return userID, err
}

func createOnboardingSession(ctx context.Context, tx *sql.Tx, orgID, userID string) (string, time.Time, error) {
	rawToken, err := generateRandomToken(32)
	if err != nil {
		return "", time.Time{}, err
	}

	token := "oc_sess_" + rawToken
	expiresAt := time.Now().UTC().Add(sessionTTL())

	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO sessions (org_id, user_id, token, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		orgID,
		userID,
		token,
		expiresAt,
	)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func ensureOnboardingProject(ctx context.Context, tx *sql.Tx, orgID string) (string, error) {
	var projectID string
	err := tx.QueryRowContext(
		ctx,
		`SELECT id
		 FROM projects
		 WHERE org_id = $1
		   AND name = $2
		 ORDER BY created_at ASC
		 LIMIT 1`,
		orgID,
		onboardingDefaultProjectName,
	).Scan(&projectID)
	if err == nil {
		return projectID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	err = tx.QueryRowContext(
		ctx,
		`INSERT INTO projects (org_id, name, status)
		 VALUES ($1, $2, 'active')
		 RETURNING id`,
		orgID,
		onboardingDefaultProjectName,
	).Scan(&projectID)
	if err != nil {
		return "", err
	}
	return projectID, nil
}

func ensureOnboardingIssue(ctx context.Context, tx *sql.Tx, orgID, projectID string) (string, int64, error) {
	var issueID string
	var issueNumber int64
	err := tx.QueryRowContext(
		ctx,
		`SELECT id, issue_number
		 FROM project_issues
		 WHERE org_id = $1
		   AND project_id = $2
		   AND title = $3
		 ORDER BY issue_number ASC
		 LIMIT 1`,
		orgID,
		projectID,
		onboardingDefaultIssueTitle,
	).Scan(&issueID, &issueNumber)
	if err == nil {
		return issueID, issueNumber, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}

	if err := tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(issue_number), 0) + 1
		 FROM project_issues
		 WHERE project_id = $1`,
		projectID,
	).Scan(&issueNumber); err != nil {
		return "", 0, err
	}

	err = tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issues (org_id, project_id, issue_number, title, body, state, origin)
		 VALUES ($1, $2, $3, $4, $5, 'open', 'local')
		 RETURNING id`,
		orgID,
		projectID,
		issueNumber,
		onboardingDefaultIssueTitle,
		onboardingDefaultIssueBody,
	).Scan(&issueID)
	if err != nil {
		return "", 0, err
	}
	return issueID, issueNumber, nil
}

func onboardingSetupLocked(ctx context.Context, db *sql.DB) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM organizations LIMIT 1)`).Scan(&exists)
	return exists, err
}

func looksLikeEmail(value string) bool {
	value = strings.TrimSpace(value)
	at := strings.Index(value, "@")
	if at <= 0 || at >= len(value)-1 {
		return false
	}
	domain := value[at+1:]
	return strings.Contains(domain, ".")
}
