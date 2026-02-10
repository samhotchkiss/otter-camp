package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const (
	DeployMethodNone       = "none"
	DeployMethodGitHubPush = "github_push"
	DeployMethodCLICommand = "cli_command"
)

type DeployConfig struct {
	ID            string     `json:"id"`
	OrgID         string     `json:"org_id"`
	ProjectID     string     `json:"project_id"`
	DeployMethod  string     `json:"deploy_method"`
	GitHubRepoURL *string    `json:"github_repo_url,omitempty"`
	GitHubBranch  string     `json:"github_branch"`
	CLICommand    *string    `json:"cli_command,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

type UpsertDeployConfigInput struct {
	ProjectID     string
	DeployMethod  string
	GitHubRepoURL *string
	GitHubBranch  string
	CLICommand    *string
}

type DeployConfigStore struct {
	db *sql.DB
}

func NewDeployConfigStore(db *sql.DB) *DeployConfigStore {
	return &DeployConfigStore{db: db}
}

func normalizeDeployConfigInput(input UpsertDeployConfigInput) (UpsertDeployConfigInput, error) {
	method := strings.ToLower(strings.TrimSpace(input.DeployMethod))
	if method == "" {
		method = DeployMethodNone
	}
	switch method {
	case DeployMethodNone, DeployMethodGitHubPush, DeployMethodCLICommand:
	default:
		return UpsertDeployConfigInput{}, fmt.Errorf("%w: invalid deploy_method", ErrValidation)
	}

	repoURL := normalizeOptionalDeployText(input.GitHubRepoURL)
	branch := strings.TrimSpace(input.GitHubBranch)
	if branch == "" {
		branch = "main"
	}
	command := normalizeOptionalDeployText(input.CLICommand)

	switch method {
	case DeployMethodNone:
		repoURL = nil
		command = nil
	case DeployMethodGitHubPush:
		command = nil
	case DeployMethodCLICommand:
		if command == nil {
			return UpsertDeployConfigInput{}, fmt.Errorf("%w: cli_command is required for deploy_method cli_command", ErrValidation)
		}
		repoURL = nil
	}

	return UpsertDeployConfigInput{
		ProjectID:     strings.TrimSpace(input.ProjectID),
		DeployMethod:  method,
		GitHubRepoURL: repoURL,
		GitHubBranch:  branch,
		CLICommand:    command,
	}, nil
}

func normalizeOptionalDeployText(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func (s *DeployConfigStore) Upsert(ctx context.Context, input UpsertDeployConfigInput) (*DeployConfig, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	input.ProjectID = strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(input.ProjectID) {
		return nil, fmt.Errorf("%w: invalid project_id", ErrValidation)
	}

	normalized, err := normalizeDeployConfigInput(input)
	if err != nil {
		return nil, err
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := ensureDeployProjectVisible(ctx, conn, normalized.ProjectID); err != nil {
		return nil, err
	}

	row := conn.QueryRowContext(
		ctx,
		`INSERT INTO project_deploy_config (
			org_id, project_id, deploy_method, github_repo_url, github_branch, cli_command
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (project_id)
		DO UPDATE SET
			deploy_method = EXCLUDED.deploy_method,
			github_repo_url = EXCLUDED.github_repo_url,
			github_branch = EXCLUDED.github_branch,
			cli_command = EXCLUDED.cli_command
		RETURNING id, org_id, project_id, deploy_method, github_repo_url, github_branch, cli_command, created_at, updated_at`,
		workspaceID,
		normalized.ProjectID,
		normalized.DeployMethod,
		nullableString(normalized.GitHubRepoURL),
		normalized.GitHubBranch,
		nullableString(normalized.CLICommand),
	)

	config, err := scanDeployConfig(row)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert deploy config: %w", err)
	}
	return &config, nil
}

func (s *DeployConfigStore) GetByProject(ctx context.Context, projectID string) (*DeployConfig, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("%w: invalid project_id", ErrValidation)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := ensureDeployProjectVisible(ctx, conn, projectID); err != nil {
		return nil, err
	}

	row := conn.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, deploy_method, github_repo_url, github_branch, cli_command, created_at, updated_at
			FROM project_deploy_config
			WHERE project_id = $1`,
		projectID,
	)
	config, err := scanDeployConfig(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &DeployConfig{
				OrgID:         workspaceID,
				ProjectID:     projectID,
				DeployMethod:  DeployMethodNone,
				GitHubBranch:  "main",
				GitHubRepoURL: nil,
				CLICommand:    nil,
			}, nil
		}
		return nil, fmt.Errorf("failed to get deploy config: %w", err)
	}
	return &config, nil
}

func ensureDeployProjectVisible(ctx context.Context, conn *sql.Conn, projectID string) error {
	var exists bool
	if err := conn.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1)`,
		projectID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("failed to validate project: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func scanDeployConfig(scanner interface{ Scan(...any) error }) (DeployConfig, error) {
	var cfg DeployConfig
	var githubRepoURL sql.NullString
	var githubBranch sql.NullString
	var cliCommand sql.NullString
	var createdAt time.Time
	var updatedAt time.Time

	if err := scanner.Scan(
		&cfg.ID,
		&cfg.OrgID,
		&cfg.ProjectID,
		&cfg.DeployMethod,
		&githubRepoURL,
		&githubBranch,
		&cliCommand,
		&createdAt,
		&updatedAt,
	); err != nil {
		return DeployConfig{}, err
	}

	if githubRepoURL.Valid {
		cfg.GitHubRepoURL = &githubRepoURL.String
	}
	if githubBranch.Valid && strings.TrimSpace(githubBranch.String) != "" {
		cfg.GitHubBranch = strings.TrimSpace(githubBranch.String)
	} else {
		cfg.GitHubBranch = "main"
	}
	if cliCommand.Valid {
		cfg.CLICommand = &cliCommand.String
	}
	cfg.CreatedAt = &createdAt
	cfg.UpdatedAt = &updatedAt
	return cfg, nil
}
