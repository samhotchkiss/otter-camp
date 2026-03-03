//go:build integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/server"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"golang.org/x/crypto/bcrypt"
)

type nounCLIIntegrationFixture struct {
	ServerURL string
	Server    *httptest.Server
	Pool      *pgxpool.Pool
	Org       repo.Organization
	User      repo.HumanUser
	APIKey    string
}

func (f *nounCLIIntegrationFixture) Close() {
	if f == nil {
		return
	}
	if f.Server != nil {
		f.Server.Close()
	}
}

func newNounCLIIntegrationFixture(t *testing.T) *nounCLIIntegrationFixture {
	t.Helper()

	pool := testdb.New(t)
	org, user := createCLIOrgAndUser(t, pool)

	authService, err := authsvc.NewService(authsvc.Options{
		Users:        repo.NewHumanUserRepo(pool),
		Sessions:     repo.NewAuthSessionRepo(pool),
		APIKeys:      repo.NewAPIKeyRepo(pool),
		DefaultOrgID: org.ID,
		AuthMode:     "standard",
		BcryptCost:   bcrypt.MinCost,
	})
	if err != nil {
		t.Fatalf("NewService auth: %v", err)
	}

	issued, err := authService.IssueAPIKey(context.Background(), user.ID, "noun-cli-test", []string{
		"read:agents",
		"write:agents",
		"read:projects",
		"write:projects",
		"admin:auth",
	}, nil)
	if err != nil {
		t.Fatalf("IssueAPIKey: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := eventbus.New(pool, logger, eventbus.Config{})

	agentService, err := agentsvc.NewService(agentsvc.Options{
		Pool:   pool,
		Agents: repo.NewAgentRepo(pool),
		Events: bus,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewService agent: %v", err)
	}

	projectService, err := projectsvc.NewService(projectsvc.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewService project: %v", err)
	}

	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Version:     "test-version",
		Logger:      logger,
		AuthService: authService,
		Pool:        pool,
		RouteRegistrars: []server.RouteRegistrar{
			server.NewAgentRouteRegistrar(
				agentService,
				repo.NewAgentProfileTemplateRepo(pool),
				nil,
				repo.NewAgentRepo(pool),
				repo.NewProjectRepo(pool),
				repo.NewSkillRepo(pool),
				repo.NewAgentProjectAssignmentRepo(pool),
				repo.NewAgentSkillAttachmentRepo(pool),
				repo.NewToolDefinitionRepo(pool),
			),
			server.NewProjectRouteRegistrar(projectService, repo.NewSkillRepo(pool)),
		},
	})

	ts := httptest.NewServer(handler)
	return &nounCLIIntegrationFixture{
		ServerURL: ts.URL,
		Server:    ts,
		Pool:      pool,
		Org:       org,
		User:      user,
		APIKey:    issued.RawKey,
	}
}

func TestAgentListIntegrationReturnsSeededAgents(t *testing.T) {
	fixture := newNounCLIIntegrationFixture(t)
	defer fixture.Close()

	restore := setChatCLIEnvForTest(t, fixture.ServerURL, fixture.APIKey, "json")
	defer restore()

	agentRepo := repo.NewAgentRepo(fixture.Pool)
	created, err := agentRepo.Create(context.Background(), repo.Agent{
		OrganizationID:       fixture.Org.ID,
		DisplayName:          "CLI Integration Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runAgentList([]string{"--output", "json"})
	})
	if code != 0 {
		t.Fatalf("agent list exit=%d stderr=%q", code, stderr)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("unmarshal stdout: %v stdout=%q", err, stdout)
	}
	itemsRaw, ok := payload["data"].([]any)
	if !ok {
		t.Fatalf("payload data type = %T, want []any", payload["data"])
	}
	if len(itemsRaw) == 0 {
		t.Fatalf("agent list returned no rows: %s", stdout)
	}
	if !strings.Contains(stdout, created.ID.String()) {
		t.Fatalf("agent list output missing seeded id %s: %s", created.ID, stdout)
	}
}

func TestProjectCreateIntegrationCreatesProjectAndPrintsID(t *testing.T) {
	fixture := newNounCLIIntegrationFixture(t)
	defer fixture.Close()

	restore := setChatCLIEnvForTest(t, fixture.ServerURL, fixture.APIKey, "quiet")
	defer restore()

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runProjectCreate([]string{"--name", "CLI Integration Project", "--output", "quiet"})
	})
	if code != 0 {
		t.Fatalf("project create exit=%d stderr=%q", code, stderr)
	}

	projectID, err := uuid.Parse(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatalf("project create output %q is not uuid: %v", stdout, err)
	}

	projectRecord, err := repo.NewProjectRepo(fixture.Pool).GetByID(context.Background(), projectID)
	if err != nil {
		t.Fatalf("GetByID project %s: %v", projectID, err)
	}
	if projectRecord.OrganizationID != fixture.Org.ID {
		t.Fatalf("organization_id = %s, want %s", projectRecord.OrganizationID, fixture.Org.ID)
	}
	if projectRecord.DisplayName != "CLI Integration Project" {
		t.Fatalf("display_name = %q, want %q", projectRecord.DisplayName, "CLI Integration Project")
	}
}
