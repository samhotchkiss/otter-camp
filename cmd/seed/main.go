package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

const orgID = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Insert Sam's organization
	_, err = db.Exec(`
		INSERT INTO organizations (id, name, slug, created_at, updated_at)
		VALUES ($1, 'Sam Hotchkiss', 'sam', NOW(), NOW())
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, updated_at = NOW()
	`, orgID)
	if err != nil {
		log.Fatal("Failed to create organization: ", err)
	}
	fmt.Println("✅ Organization 'sam' created/updated!")

	// Seed projects
	projects := []struct {
		name        string
		description string
		status      string
		repoURL     string
	}{
		{"Pearl Proxy", "Memory and routing infrastructure", "active", "https://github.com/The-Trawl/pearl"},
		{"Otter Camp", "Task management for AI-assisted workflows", "active", "https://github.com/samhotchkiss/otter-camp"},
		{"ItsAlive", "Static site deployment platform", "active", "https://github.com/The-Trawl/itsalive"},
		{"Three Stones", "Educational content and presentations", "archived", ""},
		{"OpenClaw", "AI agent runtime and orchestration", "active", "https://github.com/openclaw/openclaw"},
	}

	for _, p := range projects {
		var repoURL interface{} = p.repoURL
		if p.repoURL == "" {
			repoURL = nil
		}

		// Check if project exists
		var exists bool
		err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM projects WHERE org_id = $1 AND name = $2)", orgID, p.name).Scan(&exists)
		if err != nil {
			log.Printf("⚠️  Failed to check project %s: %v", p.name, err)
			continue
		}

		if exists {
			// Update existing
			_, err = db.Exec(`
				UPDATE projects SET description = $1, status = $2, repo_url = $3, updated_at = NOW()
				WHERE org_id = $4 AND name = $5
			`, p.description, p.status, repoURL, orgID, p.name)
		} else {
			// Insert new
			_, err = db.Exec(`
				INSERT INTO projects (org_id, name, description, status, repo_url, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
			`, orgID, p.name, p.description, p.status, repoURL)
		}

		if err != nil {
			log.Printf("⚠️  Failed to create/update project %s: %v", p.name, err)
		} else {
			fmt.Printf("✅ Project '%s' created/updated\n", p.name)
		}
	}

	// Count projects
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM projects WHERE org_id = $1", orgID).Scan(&count)
	if err != nil {
		log.Fatal("Failed to count projects: ", err)
	}
	fmt.Printf("✅ Total projects: %d\n", count)
}
