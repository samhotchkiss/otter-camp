//go:build integration

package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestMagicLinkCommandPrintsToken(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	orgRepo := repo.NewOrgRepo(pool)
	userRepo := repo.NewHumanUserRepo(pool)
	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "cli-magic-link-org",
		DisplayName: "CLI Magic Link Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password-123"), 12)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	hash := string(passwordHash)

	_, err = userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          "cli@example.com",
		DisplayName:    "CLI User",
		PasswordHash:   &hash,
		Role:           "admin",
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Setenv("OTTERCAMP_DATABASE_URL", pool.Config().ConnString())
	t.Setenv("OTTERCAMP_DEFAULT_ORG_ID", org.ID.String())

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	exitCode := runWithIO([]string{"magic-link", "--email", "cli@example.com"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}

	token := strings.TrimSpace(stdout.String())
	if token == "" {
		t.Fatal("expected token output")
	}
	if !strings.HasPrefix(token, "mlk_") {
		t.Fatalf("token prefix = %q, want mlk_", token)
	}
}

func runWithIO(args []string, stdout, stderr *bytes.Buffer) int {
	originalStdout := os.Stdout
	originalStderr := os.Stderr

	readOut, writeOut, _ := os.Pipe()
	readErr, writeErr, _ := os.Pipe()

	os.Stdout = writeOut
	os.Stderr = writeErr

	exitCode := run(args)

	_ = writeOut.Close()
	_ = writeErr.Close()
	_, _ = stdout.ReadFrom(readOut)
	_, _ = stderr.ReadFrom(readErr)
	_ = readOut.Close()
	_ = readErr.Close()

	os.Stdout = originalStdout
	os.Stderr = originalStderr

	return exitCode
}
