//go:build integration

package security

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestAudit_Login_Recorded(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := makeSecurityOrgID(t, pool)
	user := makeSecurityUser(t, pool, orgID, "admin")
	auditRepo := repo.NewAuditEventRepo(pool)

	if err := auditRepo.Insert(ctx, repo.AuditEvent{
		OrganizationID: orgID,
		EventType:      "login",
		PrincipalType:  "human",
		PrincipalID:    user.ID,
		Metadata: map[string]any{
			"ip_address": "127.0.0.1",
			"user_agent": "integration-test-agent",
		},
	}); err != nil {
		t.Fatalf("insert login event: %v", err)
	}
	if err := auditRepo.Insert(ctx, repo.AuditEvent{
		OrganizationID: orgID,
		EventType:      "login_failed",
		PrincipalType:  "human",
		PrincipalID:    user.ID,
		Metadata: map[string]any{
			"ip_address": "127.0.0.1",
			"user_agent": "integration-test-agent",
		},
	}); err != nil {
		t.Fatalf("insert login_failed event: %v", err)
	}

	events, err := auditRepo.ListByOrg(ctx, orgID, repo.AuditEventFilters{}, repo.Pagination{Limit: 20})
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("audit event count = %d, want at least 2", len(events))
	}
	var (
		foundLogin bool
		foundFail  bool
	)
	for _, event := range events {
		switch event.EventType {
		case "login":
			foundLogin = true
			if event.Metadata["ip_address"] == nil || event.Metadata["user_agent"] == nil {
				t.Fatalf("login metadata missing ip/user-agent: %#v", event.Metadata)
			}
		case "login_failed":
			foundFail = true
		}
	}
	if !foundLogin || !foundFail {
		t.Fatalf("missing expected login events foundLogin=%v foundFail=%v", foundLogin, foundFail)
	}
}

func TestAudit_APIKey_Issuance(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := makeSecurityOrgID(t, pool)
	user := makeSecurityUser(t, pool, orgID, "admin")
	auditRepo := repo.NewAuditEventRepo(pool)

	rawKey := "sk-abc123def456ghi789jklmnopqrst"
	if err := auditRepo.Insert(ctx, repo.AuditEvent{
		OrganizationID: orgID,
		EventType:      "api_key_created",
		PrincipalType:  "human",
		PrincipalID:    user.ID,
		Metadata: map[string]any{
			"key_id":  uuid.NewString(),
			"name":    "CI Key",
			"raw_key": rawKey,
		},
	}); err != nil {
		t.Fatalf("insert api_key_created event: %v", err)
	}

	var metadataText string
	if err := pool.QueryRow(ctx, `
		SELECT metadata::text
		FROM audit_event
		WHERE organization_id = $1
		  AND event_type = 'api_key_created'
		ORDER BY created_at DESC
		LIMIT 1
	`, orgID).Scan(&metadataText); err != nil {
		t.Fatalf("query api key metadata: %v", err)
	}
	if strings.Contains(metadataText, rawKey) {
		t.Fatalf("audit metadata leaked raw api key: %s", metadataText)
	}
	if !strings.Contains(metadataText, "CI Key") {
		t.Fatalf("audit metadata missing key name: %s", metadataText)
	}
}

func TestAudit_AgentTransition_Recorded(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := makeSecurityOrgID(t, pool)
	user := makeSecurityUser(t, pool, orgID, "admin")
	agent := mustCreateAuditAgent(t, pool, orgID)
	auditRepo := repo.NewAuditEventRepo(pool)

	if err := auditRepo.Insert(ctx, repo.AuditEvent{
		OrganizationID: orgID,
		EventType:      "agent_activated",
		PrincipalType:  "human",
		PrincipalID:    user.ID,
		Metadata: map[string]any{
			"agent_id": agent.ID.String(),
			"from":     "draft",
			"to":       "active",
		},
	}); err != nil {
		t.Fatalf("insert agent transition event: %v", err)
	}

	var (
		principalType string
		principalID   uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT principal_type, principal_id
		FROM audit_event
		WHERE organization_id = $1
		  AND event_type = 'agent_activated'
		ORDER BY created_at DESC
		LIMIT 1
	`, orgID).Scan(&principalType, &principalID); err != nil {
		t.Fatalf("query agent transition event: %v", err)
	}
	if principalType != "human" || principalID != user.ID {
		t.Fatalf("principal fields = (%q,%s), want (human,%s)", principalType, principalID, user.ID)
	}
}

func TestAudit_PolicyDecision_Recorded(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := makeSecurityOrgID(t, pool)
	user := makeSecurityUser(t, pool, orgID, "admin")

	if err := repo.NewAuditEventRepo(pool).Insert(ctx, repo.AuditEvent{
		OrganizationID: orgID,
		EventType:      "capability_denied",
		PrincipalType:  "human",
		PrincipalID:    user.ID,
		Metadata: map[string]any{
			"capability":   "system.file.write",
			"policy_layer": "org",
		},
	}); err != nil {
		t.Fatalf("insert capability_denied: %v", err)
	}

	var metadataText string
	if err := pool.QueryRow(ctx, `
		SELECT metadata::text
		FROM audit_event
		WHERE organization_id = $1
		  AND event_type = 'capability_denied'
		ORDER BY created_at DESC
		LIMIT 1
	`, orgID).Scan(&metadataText); err != nil {
		t.Fatalf("query capability_denied metadata: %v", err)
	}
	if !strings.Contains(metadataText, "system.file.write") || !strings.Contains(metadataText, "org") {
		t.Fatalf("capability/policy_layer missing from metadata: %s", metadataText)
	}
}

func TestAudit_RunCreated_Recorded(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := makeSecurityOrgID(t, pool)
	user := makeSecurityUser(t, pool, orgID, "admin")
	runID := uuid.New()

	if err := repo.NewAuditEventRepo(pool).Insert(ctx, repo.AuditEvent{
		OrganizationID: orgID,
		EventType:      "run_created",
		PrincipalType:  "human",
		PrincipalID:    user.ID,
		Metadata: map[string]any{
			"run_id": runID.String(),
		},
	}); err != nil {
		t.Fatalf("insert run_created: %v", err)
	}

	var metadataText string
	if err := pool.QueryRow(ctx, `
		SELECT metadata::text
		FROM audit_event
		WHERE organization_id = $1
		  AND event_type = 'run_created'
		ORDER BY created_at DESC
		LIMIT 1
	`, orgID).Scan(&metadataText); err != nil {
		t.Fatalf("query run_created metadata: %v", err)
	}
	if !strings.Contains(metadataText, runID.String()) {
		t.Fatalf("run_id missing from metadata: %s", metadataText)
	}
}

func TestAudit_SecretAccess_Recorded(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := makeSecurityOrgID(t, pool)
	user := makeSecurityUser(t, pool, orgID, "admin")
	secretValue := "sk-abc123def456ghi789jklmnopqrst"
	slug := makeSecuritySecret(t, pool, orgID, "db-password", secretValue)

	if err := repo.NewAuditEventRepo(pool).Insert(ctx, repo.AuditEvent{
		OrganizationID: orgID,
		EventType:      "secret_accessed",
		PrincipalType:  "human",
		PrincipalID:    user.ID,
		Metadata: map[string]any{
			"secret_slug":  slug,
			"secret_value": secretValue,
		},
	}); err != nil {
		t.Fatalf("insert secret_accessed event: %v", err)
	}

	var metadataText string
	if err := pool.QueryRow(ctx, `
		SELECT metadata::text
		FROM audit_event
		WHERE organization_id = $1
		  AND event_type = 'secret_accessed'
		ORDER BY created_at DESC
		LIMIT 1
	`, orgID).Scan(&metadataText); err != nil {
		t.Fatalf("query secret_accessed metadata: %v", err)
	}
	if !strings.Contains(metadataText, slug) {
		t.Fatalf("secret slug missing from metadata: %s", metadataText)
	}
	if strings.Contains(metadataText, secretValue) {
		t.Fatalf("secret value leaked in metadata: %s", metadataText)
	}
}

func TestAudit_OrganizationIsolation(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgA := makeSecurityOrgID(t, pool)
	orgB := makeSecurityOrgID(t, pool)
	userA := makeSecurityUser(t, pool, orgA, "admin")
	userB := makeSecurityUser(t, pool, orgB, "admin")

	makeSecurityAuditEvent(t, pool, orgA, "orgA.event", "human", userA.ID)
	makeSecurityAuditEvent(t, pool, orgB, "orgB.event", "human", userB.ID)

	eventsB, err := repo.NewAuditEventRepo(pool).ListByOrg(ctx, orgB, repo.AuditEventFilters{}, repo.Pagination{Limit: 50})
	if err != nil {
		t.Fatalf("ListByOrg orgB: %v", err)
	}
	if len(eventsB) == 0 {
		t.Fatal("expected orgB audit events")
	}
	for _, event := range eventsB {
		if event.OrganizationID != orgB {
			t.Fatalf("found cross-org event organization_id=%s in orgB results", event.OrganizationID)
		}
	}
}

func TestAudit_DelegatedAction(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := makeSecurityOrgID(t, pool)
	human := makeSecurityUser(t, pool, orgID, "admin")
	agent := mustCreateAuditAgent(t, pool, orgID)
	delegatedByType := "human"

	if err := repo.NewAuditEventRepo(pool).Insert(ctx, repo.AuditEvent{
		OrganizationID:  orgID,
		EventType:       "delegated.action",
		PrincipalType:   "agent",
		PrincipalID:     agent.ID,
		DelegatedByType: &delegatedByType,
		DelegatedByID:   &human.ID,
		Metadata: map[string]any{
			"source": "integration-test",
		},
	}); err != nil {
		t.Fatalf("insert delegated.action event: %v", err)
	}

	var (
		principalType string
		principalID   uuid.UUID
		delegatedType *string
		delegatedByID *uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT principal_type, principal_id, delegated_by_type, delegated_by_id
		FROM audit_event
		WHERE organization_id = $1
		  AND event_type = 'delegated.action'
		ORDER BY created_at DESC
		LIMIT 1
	`, orgID).Scan(&principalType, &principalID, &delegatedType, &delegatedByID); err != nil {
		t.Fatalf("query delegated.action event: %v", err)
	}
	if principalType != "agent" || principalID != agent.ID {
		t.Fatalf("principal mismatch type=%q id=%s want agent/%s", principalType, principalID, agent.ID)
	}
	if delegatedType == nil || *delegatedType != "human" {
		t.Fatalf("delegated_by_type = %v, want human", delegatedType)
	}
	if delegatedByID == nil || *delegatedByID != human.ID {
		t.Fatalf("delegated_by_id = %v, want %s", delegatedByID, human.ID)
	}
}

func makeSecurityAuditEvent(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, action, principalType string, principalID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO audit_event (organization_id, event_type, principal_type, principal_id, metadata)
		VALUES ($1, $2, $3, $4, '{}'::jsonb)
		RETURNING id
	`, orgID, action, principalType, principalID).Scan(&id); err != nil {
		t.Fatalf("insert audit_event: %v", err)
	}
	return id
}

func mustCreateAuditAgent(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) repo.Agent {
	t.Helper()
	agent, err := repo.NewAgentRepo(pool).Create(context.Background(), repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Audit Agent " + uuid.NewString()[:8],
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "prompt",
		OperatorInstructions: "",
		AgentType:            "worker",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org"},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent
}
