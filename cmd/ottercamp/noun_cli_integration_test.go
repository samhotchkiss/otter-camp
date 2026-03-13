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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	agentsvc "github.com/samhotchkiss/otter-camp/internal/agent"
	authsvc "github.com/samhotchkiss/otter-camp/internal/auth"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/server"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/turn"
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
	chatService, err := chat.NewService(chat.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewService chat: %v", err)
	}
	relauncher, err := turn.NewBootstrapRelauncher(turn.BootstrapRelauncherOptions{
		Pool:     pool,
		Chat:     chatService,
		Events:   bus,
		Enqueuer: jobqueue.New(pool, logger, jobqueue.Config{}),
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("NewBootstrapRelauncher: %v", err)
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
			server.NewProjectRouteRegistrar(projectService, repo.NewSkillRepo(pool), relauncher),
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

func TestProjectArchiveIntegrationArchivesProjectAndPrintsID(t *testing.T) {
	fixture := newNounCLIIntegrationFixture(t)
	defer fixture.Close()

	restore := setChatCLIEnvForTest(t, fixture.ServerURL, fixture.APIKey, "quiet")
	defer restore()

	projectRecord, err := repo.NewProjectRepo(fixture.Pool).Create(context.Background(), repo.Project{
		OrganizationID: fixture.Org.ID,
		Slug:           "cli-archive-target",
		DisplayName:    "CLI Archive Target",
		DeliveryMode:   "gated",
		CreatedByType:  "human_user",
		CreatedByID:    fixture.User.ID,
	})
	if err != nil {
		t.Fatalf("seed project archive target: %v", err)
	}

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runProjectArchive([]string{"--project-id", projectRecord.ID.String(), "--output", "quiet"})
	})
	if code != 0 {
		t.Fatalf("project archive exit=%d stderr=%q", code, stderr)
	}

	archivedID, err := uuid.Parse(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatalf("project archive output %q is not uuid: %v", stdout, err)
	}
	if archivedID != projectRecord.ID {
		t.Fatalf("archived id = %s, want %s", archivedID, projectRecord.ID)
	}

	archived, err := repo.NewProjectRepo(fixture.Pool).GetByID(context.Background(), archivedID)
	if err != nil {
		t.Fatalf("GetByID archived project %s: %v", archivedID, err)
	}
	if archived.Status != "archived" {
		t.Fatalf("status = %q, want %q", archived.Status, "archived")
	}
}

func TestProjectRelaunchIntegrationCreatesRestartProjectAndPrintsID(t *testing.T) {
	fixture := newNounCLIIntegrationFixture(t)
	defer fixture.Close()

	restore := setChatCLIEnvForTest(t, fixture.ServerURL, fixture.APIKey, "quiet")
	defer restore()

	projectRepo := repo.NewProjectRepo(fixture.Pool)
	recordedAt := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	projectRecord, err := projectRepo.Create(context.Background(), repo.Project{
		OrganizationID: fixture.Org.ID,
		Slug:           "cli-archived-bootstrap-source",
		DisplayName:    "CLI Archived Bootstrap Source",
		DeliveryMode:   "gated",
		Settings: json.RawMessage(`{
			"automatic_failure":{
				"action":"archive",
				"source":"project_bootstrap",
				"failure_class":"bootstrap_runtime",
				"failure_reason":"cli relaunch integration test",
				"recorded_at":"` + recordedAt + `"
			},
			"bootstrap_restart_bundle":{
				"operator_brief":"Restart from the CLI integration bootstrap bundle.",
				"source_project_id":"PENDING"
			}
		}`),
		CreatedByType: "human_user",
		CreatedByID:   fixture.User.ID,
	})
	if err != nil {
		t.Fatalf("seed archived bootstrap source: %v", err)
	}
	projectRecord.Settings = json.RawMessage(strings.ReplaceAll(string(projectRecord.Settings), `"PENDING"`, `"`+projectRecord.ID.String()+`"`))
	if updated, updateErr := projectRepo.Update(context.Background(), projectRecord); updateErr != nil {
		t.Fatalf("update archived bootstrap source settings: %v", updateErr)
	} else {
		projectRecord = updated
	}
	if err := projectRepo.Archive(context.Background(), projectRecord.ID); err != nil {
		t.Fatalf("archive bootstrap source: %v", err)
	}

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runProjectRelaunch([]string{"--project-id", projectRecord.ID.String(), "--output", "quiet"})
	})
	if code != 0 {
		t.Fatalf("project relaunch exit=%d stderr=%q", code, stderr)
	}

	restartedID, err := uuid.Parse(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatalf("project relaunch output %q is not uuid: %v", stdout, err)
	}
	if restartedID == projectRecord.ID {
		t.Fatalf("project relaunch returned archived project id %s", restartedID)
	}

	restarted, err := projectRepo.GetByID(context.Background(), restartedID)
	if err != nil {
		t.Fatalf("GetByID restarted project %s: %v", restartedID, err)
	}
	if restarted.OrganizationID != fixture.Org.ID {
		t.Fatalf("organization_id = %s, want %s", restarted.OrganizationID, fixture.Org.ID)
	}
	if restarted.Status != "active" {
		t.Fatalf("status = %q, want %q", restarted.Status, "active")
	}
	archived, err := projectRepo.GetByID(context.Background(), projectRecord.ID)
	if err != nil {
		t.Fatalf("GetByID archived project %s: %v", projectRecord.ID, err)
	}
	if !strings.Contains(string(archived.Settings), restartedID.String()) {
		t.Fatalf("archived settings = %s, want restart project id %s", string(archived.Settings), restartedID)
	}
}
