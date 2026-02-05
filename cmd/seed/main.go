package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
)

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
		VALUES (
			'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11',
			'Sam Hotchkiss',
			'sam',
			NOW(),
			NOW()
		)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, updated_at = NOW()
	`)
	if err != nil {
		log.Fatal("Failed to create organization: ", err)
	}

	fmt.Println("✅ Organization 'sam' created/updated!")

	// Verify
	var name string
	err = db.QueryRow("SELECT name FROM organizations WHERE slug = 'sam'").Scan(&name)
	if err != nil {
		log.Fatal("Failed to verify: ", err)
	}
	fmt.Printf("✅ Verified organization: %s\n", name)
}
