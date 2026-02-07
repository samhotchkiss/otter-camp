package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

type GitHubInstallation struct {
	ID             string          `json:"id"`
	OrgID          string          `json:"org_id"`
	InstallationID int64           `json:"installation_id"`
	AccountLogin   string          `json:"account_login"`
	AccountType    string          `json:"account_type"`
	Permissions    json.RawMessage `json:"permissions"`
	ConnectedAt    time.Time       `json:"connected_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type UpsertGitHubInstallationInput struct {
	InstallationID int64
	AccountLogin   string
	AccountType    string
	Permissions    json.RawMessage
}

type GitHubInstallationStore struct {
	db *sql.DB
}

func NewGitHubInstallationStore(db *sql.DB) *GitHubInstallationStore {
	return &GitHubInstallationStore{db: db}
}

const githubInstallationColumns = `
	id,
	org_id,
	installation_id,
	account_login,
	account_type,
	permissions,
	connected_at,
	updated_at
`

func (s *GitHubInstallationStore) Upsert(
	ctx context.Context,
	input UpsertGitHubInstallationInput,
) (*GitHubInstallation, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	if input.InstallationID <= 0 {
		return nil, fmt.Errorf("installation_id must be greater than zero")
	}

	accountLogin := strings.TrimSpace(input.AccountLogin)
	if accountLogin == "" {
		return nil, fmt.Errorf("account_login is required")
	}

	accountType := strings.TrimSpace(input.AccountType)
	if accountType == "" {
		return nil, fmt.Errorf("account_type is required")
	}

	permissions := input.Permissions
	if len(permissions) == 0 {
		permissions = json.RawMessage("{}")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `
		INSERT INTO github_installations (
			org_id,
			installation_id,
			account_login,
			account_type,
			permissions
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (org_id)
		DO UPDATE SET
			installation_id = EXCLUDED.installation_id,
			account_login = EXCLUDED.account_login,
			account_type = EXCLUDED.account_type,
			permissions = EXCLUDED.permissions
		RETURNING` + githubInstallationColumns

	installation, err := scanGitHubInstallation(conn.QueryRowContext(
		ctx,
		query,
		workspaceID,
		input.InstallationID,
		accountLogin,
		accountType,
		permissions,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert github installation: %w", err)
	}

	return &installation, nil
}

func (s *GitHubInstallationStore) GetByOrg(ctx context.Context) (*GitHubInstallation, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `SELECT` + githubInstallationColumns + ` FROM github_installations WHERE org_id = $1`
	installation, err := scanGitHubInstallation(conn.QueryRowContext(ctx, query, workspaceID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get github installation by org: %w", err)
	}

	return &installation, nil
}

func (s *GitHubInstallationStore) GetByInstallationID(
	ctx context.Context,
	installationID int64,
) (*GitHubInstallation, error) {
	if installationID <= 0 {
		return nil, fmt.Errorf("installation_id must be greater than zero")
	}

	query := `SELECT` + githubInstallationColumns + ` FROM github_installations WHERE installation_id = $1`
	installation, err := scanGitHubInstallation(s.db.QueryRowContext(ctx, query, installationID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get github installation by installation_id: %w", err)
	}

	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(ctx))
	if workspaceID != "" && installation.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	return &installation, nil
}

func scanGitHubInstallation(scanner interface{ Scan(dest ...any) error }) (GitHubInstallation, error) {
	var installation GitHubInstallation
	if err := scanner.Scan(
		&installation.ID,
		&installation.OrgID,
		&installation.InstallationID,
		&installation.AccountLogin,
		&installation.AccountType,
		&installation.Permissions,
		&installation.ConnectedAt,
		&installation.UpdatedAt,
	); err != nil {
		return installation, err
	}

	if len(installation.Permissions) == 0 {
		installation.Permissions = json.RawMessage("{}")
	}

	return installation, nil
}
