package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

const (
	defaultDatabaseURL   = "postgres://otter:camp@localhost:5432/ottercamp?sslmode=disable"
	defaultOrgName       = "My Workspace"
	defaultOrgSlug       = "my-workspace"
	defaultUserName      = "Admin"
	defaultUserEmail     = "admin@localhost"
	defaultUserSubject   = "local-admin"
	defaultUserIssuer    = "otter.camp"
	localTokenPrefix     = "oc_local_"
	localTokenLength     = 32
	defaultSessionWindow = 365 * 24 * time.Hour
)

type seedResult struct {
	Created bool
	OrgID   string
	UserID  string
	Token   string
}

type seedStore interface {
	CountOrganizations(ctx context.Context) (int, error)
	CreateOrganization(ctx context.Context, name string, slug string) (string, error)
	CreateUser(ctx context.Context, orgID string, name string, email string, subject string, issuer string) (string, error)
	CreateSession(ctx context.Context, orgID string, userID string, token string, expiresAt time.Time) error
	CreateProject(ctx context.Context, orgID string, name string, description string) (string, error)
	SetProjectRepoPath(ctx context.Context, projectID string, repoPath string) error
}

type sqlSeedStore struct {
	db *sql.DB
}

func (s *sqlSeedStore) CountOrganizations(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM organizations`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *sqlSeedStore) CreateOrganization(ctx context.Context, name string, slug string) (string, error) {
	var orgID string
	err := s.db.QueryRowContext(
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

func (s *sqlSeedStore) CreateUser(ctx context.Context, orgID string, name string, email string, subject string, issuer string) (string, error) {
	var userID string
	err := s.db.QueryRowContext(
		ctx,
		`INSERT INTO users (org_id, display_name, email, subject, issuer)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (org_id, issuer, subject) DO UPDATE
		   SET display_name = EXCLUDED.display_name, email = EXCLUDED.email
		 RETURNING id`,
		orgID,
		name,
		email,
		subject,
		issuer,
	).Scan(&userID)
	return userID, err
}

func (s *sqlSeedStore) CreateSession(ctx context.Context, orgID string, userID string, token string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO sessions (org_id, user_id, token, expires_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (token) DO UPDATE
		   SET org_id = EXCLUDED.org_id,
		       user_id = EXCLUDED.user_id,
		       expires_at = EXCLUDED.expires_at`,
		orgID,
		userID,
		token,
		expiresAt,
	)
	return err
}

func (s *sqlSeedStore) CreateProject(ctx context.Context, orgID string, name string, description string) (string, error) {
	var projectID string
	err := s.db.QueryRowContext(
		ctx,
		`INSERT INTO projects (org_id, name, description, status)
		 VALUES ($1, $2, $3, 'active')
		 ON CONFLICT DO NOTHING
		 RETURNING id`,
		orgID,
		name,
		description,
	).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		// Already exists — look it up
		err = s.db.QueryRowContext(
			ctx,
			`SELECT id FROM projects WHERE org_id = $1 AND name = $2`,
			orgID, name,
		).Scan(&projectID)
	}
	return projectID, err
}

func (s *sqlSeedStore) SetProjectRepoPath(ctx context.Context, projectID string, repoPath string) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE projects SET local_repo_path = $1 WHERE id = $2`,
		repoPath,
		projectID,
	)
	return err
}

func seedDefaultWorkspace(ctx context.Context, store seedStore, now time.Time, token string) (seedResult, error) {
	orgCount, err := store.CountOrganizations(ctx)
	if err != nil {
		return seedResult{}, fmt.Errorf("count organizations: %w", err)
	}
	if orgCount > 0 {
		return seedResult{Created: false, Token: strings.TrimSpace(token)}, nil
	}

	if strings.TrimSpace(token) == "" {
		token, err = generateLocalToken()
		if err != nil {
			return seedResult{}, fmt.Errorf("generate token: %w", err)
		}
	}

	orgID, err := store.CreateOrganization(ctx, defaultOrgName, defaultOrgSlug)
	if err != nil {
		return seedResult{}, fmt.Errorf("create organization: %w", err)
	}
	userID, err := store.CreateUser(ctx, orgID, defaultUserName, defaultUserEmail, defaultUserSubject, defaultUserIssuer)
	if err != nil {
		return seedResult{}, fmt.Errorf("create user: %w", err)
	}
	expiresAt := now.Add(defaultSessionWindow)
	if err := store.CreateSession(ctx, orgID, userID, token, expiresAt); err != nil {
		return seedResult{}, fmt.Errorf("create session: %w", err)
	}

	// Create the Agent Files project (stores agent identity files).
	projectID, err := store.CreateProject(ctx, orgID, "Agent Files",
		"Stores agent identity files (SOUL.md, IDENTITY.md, memory) managed by OtterCamp.")
	if err != nil {
		return seedResult{}, fmt.Errorf("create Agent Files project: %w", err)
	}

	repoRoot := strings.TrimSpace(os.Getenv("GIT_REPO_ROOT"))
	if repoRoot == "" {
		repoRoot = "./data/repos"
	}
	repoPath := filepath.Join(repoRoot, orgID, projectID+".git")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		return seedResult{}, fmt.Errorf("create Agent Files repo dir: %w", err)
	}
	initCmd := exec.CommandContext(ctx, "git", "init", "--bare", repoPath)
	if out, err := initCmd.CombinedOutput(); err != nil {
		return seedResult{}, fmt.Errorf("git init --bare Agent Files repo: %w\n%s", err, out)
	}
	if err := store.SetProjectRepoPath(ctx, projectID, repoPath); err != nil {
		return seedResult{}, fmt.Errorf("set Agent Files repo path: %w", err)
	}

	return seedResult{
		Created: true,
		OrgID:   orgID,
		UserID:  userID,
		Token:   token,
	}, nil
}

func generateLocalToken() (string, error) {
	entropy := make([]byte, localTokenLength)
	if _, err := rand.Read(entropy); err != nil {
		return "", err
	}
	suffix := base64.RawURLEncoding.EncodeToString(entropy)
	if len(suffix) > localTokenLength {
		suffix = suffix[:localTokenLength]
	}
	return localTokenPrefix + suffix, nil
}

func databaseURLFromEnv() string {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return defaultDatabaseURL
	}
	return databaseURL
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := sql.Open("postgres", databaseURLFromEnv())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	token := strings.TrimSpace(os.Getenv("LOCAL_AUTH_TOKEN"))
	result, err := seedDefaultWorkspace(ctx, &sqlSeedStore{db: db}, time.Now().UTC(), token)
	if err != nil {
		return err
	}

	if !result.Created {
		fmt.Println("Workspace already exists, skipping seed.")
		return nil
	}

	fmt.Printf("Created workspace %q (org_id=%s)\n", defaultOrgName, result.OrgID)
	fmt.Printf("Created user %q (user_id=%s)\n", defaultUserName, result.UserID)
	fmt.Printf("Created \"Agent Files\" project with git repo\n")
	fmt.Printf("Auth token: %s\n", result.Token)
	return nil
}

func main() {
	if err := run(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			log.Fatalf("seed timeout: %v", err)
		}
		log.Fatalf("seed failed: %v", err)
	}
}
