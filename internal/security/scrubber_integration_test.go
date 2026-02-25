//go:build integration

package security

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestScrubber_Invariant1_NotInPrompts(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := makeSecurityOrgID(t, pool)
	provider := mustCreateModelProvider(t, pool)

	secretValue := "sk-abc123def456ghi789jklmnopqrst"
	makeSecuritySecret(t, pool, orgID, "prompt-secret", secretValue)

	scrubber := NewSecretScrubber()
	rawPrompt := "System prompt includes " + secretValue + " and must be scrubbed."
	scrubbedPrompt := scrubber.Scrub(rawPrompt)
	if strings.Contains(scrubbedPrompt, secretValue) {
		t.Fatalf("scrubbed prompt leaked secret: %q", scrubbedPrompt)
	}

	metadata := json.RawMessage(`{"prompt":"` + scrubbedPrompt + `"}`)
	created, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    orgID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "completed",
		ModelName:         "integration-model",
		Metadata:          metadata,
	})
	if err != nil {
		t.Fatalf("create model_invocation: %v", err)
	}

	var storedMetadata string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM model_invocation WHERE id = $1`, created.ID).Scan(&storedMetadata); err != nil {
		t.Fatalf("query metadata text: %v", err)
	}
	if strings.Contains(storedMetadata, secretValue) {
		t.Fatalf("model_invocation metadata leaked secret: %s", storedMetadata)
	}
}

func TestScrubber_Invariant2_NotInAPIResponse(t *testing.T) {
	secretValue := "Bearer abcdefghijklmnopqrstuvwxy12345"
	sanitizer := NewOutputSanitizer(NewSecretScrubber())

	handler := OutputSanitizerMiddleware(sanitizer)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"transport_config": map[string]any{
					"authorization": secretValue,
				},
			},
		})
	}))

	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET sanitized response: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	if strings.Contains(text, secretValue) {
		t.Fatalf("API response leaked raw secret: %s", text)
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("API response missing [REDACTED]: %s", text)
	}
}

func TestScrubber_Invariant3_NotInAuditEvent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := makeSecurityOrgID(t, pool)
	user := makeSecurityUser(t, pool, orgID, "admin")

	testCases := []struct {
		eventType string
		metadata  map[string]any
		rawSecret string
	}{
		{
			eventType: "integration.audit.secret.bearer",
			metadata: map[string]any{
				"auth": "Bearer abcdefghijklmnopqrstuvwxy12345",
			},
			rawSecret: "Bearer abcdefghijklmnopqrstuvwxy12345",
		},
		{
			eventType: "integration.audit.secret.jwt",
			metadata: map[string]any{
				"token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc123.def456",
			},
			rawSecret: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc123.def456",
		},
		{
			eventType: "integration.audit.secret.openai-env",
			metadata: map[string]any{
				"payload": "OPENAI_API_KEY=sk-abc123def456ghi789jklmnopqrst",
			},
			rawSecret: "OPENAI_API_KEY=sk-abc123def456ghi789jklmnopqrst",
		},
		{
			eventType: "integration.audit.secret.anthropic-env",
			metadata: map[string]any{
				"payload": "ANTHROPIC_API_KEY=sk-ant-api03-9p6m3s4wX0bNEjDZUlSmvBe",
			},
			rawSecret: "ANTHROPIC_API_KEY=sk-ant-api03-9p6m3s4wX0bNEjDZUlSmvBe",
		},
	}

	auditRepo := repo.NewAuditEventRepo(pool)
	for _, tc := range testCases {
		if err := auditRepo.Insert(ctx, repo.AuditEvent{
			OrganizationID: orgID,
			EventType:      tc.eventType,
			PrincipalType:  "human",
			PrincipalID:    user.ID,
			Metadata:       tc.metadata,
		}); err != nil {
			t.Fatalf("insert audit event %s: %v", tc.eventType, err)
		}

		var metadataText string
		if err := pool.QueryRow(ctx, `
			SELECT metadata::text
			FROM audit_event
			WHERE organization_id = $1
			  AND event_type = $2
			ORDER BY created_at DESC
			LIMIT 1
		`, orgID, tc.eventType).Scan(&metadataText); err != nil {
			t.Fatalf("query audit metadata %s: %v", tc.eventType, err)
		}
		if strings.Contains(metadataText, tc.rawSecret) {
			t.Fatalf("audit metadata leaked raw secret for %s: %s", tc.eventType, metadataText)
		}
		if !strings.Contains(metadataText, "[REDACTED]") && !strings.Contains(metadataText, "[JWT_REDACTED]") {
			t.Fatalf("audit metadata missing redaction marker for %s: %s", tc.eventType, metadataText)
		}
	}
}

func TestScrubber_Invariant4_NotInLogs(t *testing.T) {
	secretValue := "Authorization: Bearer abcdefghijklmnopqrstuvwxy12345"
	var logBuf bytes.Buffer
	base := slog.NewTextHandler(&logBuf, nil)
	logger := slog.New(NewScrubbingHandler(base, NewSecretScrubber()))

	logger.Info("request log", "token", secretValue)

	output := logBuf.String()
	if strings.Contains(output, secretValue) {
		t.Fatalf("log output leaked secret: %s", output)
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("log output missing [REDACTED]: %s", output)
	}
}

func TestScrubber_Invariant5_NotInMemory(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgID := makeSecurityOrgID(t, pool)

	rawSecret := "sk-abc123def456ghi789jklmnopqrst"
	rawContent := "remember this key " + rawSecret
	scrubbedContent := NewSecretScrubber().Scrub(rawContent)

	_, err := repo.NewMemoryRepo(pool).Create(ctx, repo.Memory{
		OrganizationID: orgID,
		MemoryType:     "semantic",
		Scope:          "org",
		Content:        scrubbedContent,
		ContentHash:    hashSecurityContent(scrubbedContent),
		Status:         "active",
	})
	if err != nil {
		t.Fatalf("create memory row: %v", err)
	}

	var leakedRows int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM memory
		WHERE organization_id = $1
		  AND content LIKE $2
	`, orgID, "%"+rawSecret+"%").Scan(&leakedRows); err != nil {
		t.Fatalf("count leaked memory rows: %v", err)
	}
	if leakedRows != 0 {
		t.Fatalf("memory rows containing raw secret = %d, want 0", leakedRows)
	}
}

func TestScrubber_KnownPatterns(t *testing.T) {
	scrubber := NewSecretScrubber()
	input := strings.Join([]string{
		"$secret.db-password",
		"OPENAI_API_KEY=sk-abc123def456ghi789jklmnopqrst",
		"sk-ant-api03-9p6m3s4wX0bNEjDZUlSmvBe",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxy12345",
		"token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc123.def456",
	}, "\n")

	output := scrubber.Scrub(input)
	if strings.Contains(output, "$secret.db-password") {
		t.Fatalf("secret slug pattern not scrubbed: %s", output)
	}
	if strings.Contains(output, "sk-abc123def456ghi789jklmnopqrst") {
		t.Fatalf("OpenAI key pattern not scrubbed: %s", output)
	}
	if strings.Contains(output, "sk-ant-api03-9p6m3s4wX0bNEjDZUlSmvBe") {
		t.Fatalf("Anthropic key pattern not scrubbed: %s", output)
	}
	if strings.Contains(strings.ToLower(output), "bearer abcdefghijklmnopqrstuvwxy12345") {
		t.Fatalf("bearer token pattern not scrubbed: %s", output)
	}
	if strings.Contains(output, "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc123.def456") {
		t.Fatalf("JWT pattern not scrubbed: %s", output)
	}
}

func TestScrubber_NoFalsePositives(t *testing.T) {
	samples := []string{
		"the api key section is empty",
		"token bucket algorithm description",
		"keyboard input handling notes",
		"store this safely without secrets",
		"project monkey has no credentials",
	}
	scrubber := NewSecretScrubber()
	for idx, sample := range samples {
		got := scrubber.Scrub(sample)
		if got != sample {
			t.Fatalf("sample %d was unexpectedly scrubbed: got=%q want=%q", idx, got, sample)
		}
	}
}

func mustCreateModelProvider(t *testing.T, pool *pgxpool.Pool) repo.ModelProvider {
	t.Helper()
	provider, err := repo.NewModelProviderRepo(pool).Create(context.Background(), repo.ModelProvider{
		Slug:        "security-scrubber-provider-" + uuid.NewString()[:8],
		DisplayName: "Security Scrubber Provider",
		APIBaseURL:  "https://provider.example/v1",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	return provider
}
