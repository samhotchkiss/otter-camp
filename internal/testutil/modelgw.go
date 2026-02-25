package testutil

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type MakeProviderOptions struct {
	OrganizationID      uuid.UUID
	ProviderSlug        string
	ProviderDisplayName string
	ProviderAPIBaseURL  string
	ConnectionName      string
	APIKeyRef           string
	FailoverPriority    int
	MaxConcurrent       int
	IsEnabled           bool
}

type ProviderFixture struct {
	Provider   repo.ModelProvider
	Connection repo.ProviderConnection
}

func MakeProvider(t testing.TB, db *pgxpool.Pool, opts MakeProviderOptions) ProviderFixture {
	t.Helper()

	if opts.OrganizationID == uuid.Nil {
		t.Fatal("MakeProvider requires a non-nil organization id")
	}

	ctx := context.Background()
	providerRepo := repo.NewModelProviderRepo(db)
	connectionRepo := repo.NewProviderConnectionRepo(db)

	slug := strings.TrimSpace(opts.ProviderSlug)
	if slug == "" {
		slug = "provider-" + uuid.NewString()[:8]
	}
	displayName := strings.TrimSpace(opts.ProviderDisplayName)
	if displayName == "" {
		displayName = "Provider " + uuid.NewString()[:8]
	}
	apiBaseURL := strings.TrimSpace(opts.ProviderAPIBaseURL)
	if apiBaseURL == "" {
		apiBaseURL = "https://provider.example"
	}
	isEnabled := opts.IsEnabled
	if !opts.IsEnabled {
		isEnabled = true
	}

	provider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        slug,
		DisplayName: displayName,
		APIBaseURL:  apiBaseURL,
		IsEnabled:   isEnabled,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}

	connectionName := strings.TrimSpace(opts.ConnectionName)
	if connectionName == "" {
		connectionName = "Connection " + uuid.NewString()[:8]
	}
	apiKeyRef := strings.TrimSpace(opts.APIKeyRef)
	if apiKeyRef == "" {
		apiKeyRef = "ref:test-provider"
	}

	connection, err := connectionRepo.Create(ctx, repo.ProviderConnection{
		OrganizationID: opts.OrganizationID,
		ProviderID:     provider.ID,
		DisplayName:    connectionName,
		APIKeyRef:      apiKeyRef,
		APIBaseURLOverride: func() *string {
			if strings.TrimSpace(apiBaseURL) == "" {
				return nil
			}
			value := strings.TrimSpace(apiBaseURL)
			return &value
		}(),
		FailoverPriority: opts.FailoverPriority,
		MaxConcurrent:    opts.MaxConcurrent,
		IsEnabled:        isEnabled,
	})
	if err != nil {
		t.Fatalf("create provider connection: %v", err)
	}

	return ProviderFixture{
		Provider:   provider,
		Connection: connection,
	}
}

func MakeModelProfile(t testing.TB, db *pgxpool.Pool, orgID, providerID uuid.UUID) repo.ModelProfile {
	t.Helper()

	if orgID == uuid.Nil {
		t.Fatal("MakeModelProfile requires non-nil org id")
	}
	if providerID == uuid.Nil {
		t.Fatal("MakeModelProfile requires non-nil provider id")
	}

	organizationID := orgID
	profile, err := repo.NewModelProfileRepo(db).Create(context.Background(), repo.ModelProfile{
		LogicalProfileID:    "profile-" + uuid.NewString()[:8],
		OrganizationID:      &organizationID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          providerID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "Profile " + uuid.NewString()[:8],
		ContextWindowTokens: 8192,
		MaxOutputTokens:     1024,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create model profile: %v", err)
	}
	return profile
}

func MakeProfileAssignment(t testing.TB, db *pgxpool.Pool, scopeType string, scopeID uuid.UUID, profileID string) repo.ModelProfileAssignment {
	t.Helper()

	if strings.TrimSpace(scopeType) == "" {
		t.Fatal("MakeProfileAssignment requires scope type")
	}
	if scopeID == uuid.Nil {
		t.Fatal("MakeProfileAssignment requires non-nil scope id")
	}
	if strings.TrimSpace(profileID) == "" {
		t.Fatal("MakeProfileAssignment requires profile id")
	}

	var orgID *uuid.UUID
	if err := db.QueryRow(context.Background(), `
		SELECT organization_id
		FROM model_profile
		WHERE logical_profile_id = $1
		  AND is_current = true
		ORDER BY created_at DESC
		LIMIT 1
	`, strings.TrimSpace(profileID)).Scan(&orgID); err != nil {
		t.Fatalf("lookup profile organization id: %v", err)
	}
	if orgID == nil || *orgID == uuid.Nil {
		t.Fatalf("profile %q has no organization id; org-scoped assignment required", profileID)
	}

	assignment, err := repo.NewModelProfileAssignmentRepo(db).Upsert(context.Background(), repo.ModelProfileAssignment{
		OrganizationID:    *orgID,
		ScopeType:         strings.TrimSpace(scopeType),
		ScopeID:           scopeID,
		LogicalProfileID:  strings.TrimSpace(profileID),
		InvocationPurpose: "agent_turn",
	})
	if err != nil {
		t.Fatalf("upsert model profile assignment: %v", err)
	}
	return assignment
}

type MockHandler struct {
	StatusCode int
	Headers    map[string]string
	Body       string
	Delay      time.Duration
}

type MockProviderFixture struct {
	Handlers []MockHandler
}

func MockProviderServer(t testing.TB, fixture MockProviderFixture) string {
	t.Helper()

	var (
		mu        sync.Mutex
		callCount int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		idx := callCount
		callCount++
		mu.Unlock()

		var handler MockHandler
		if idx < len(fixture.Handlers) {
			handler = fixture.Handlers[idx]
		} else {
			handler = MockHandler{
				StatusCode: http.StatusInternalServerError,
				Body:       `{"error":"mock handler exhausted"}`,
			}
		}

		if handler.Delay > 0 {
			select {
			case <-time.After(handler.Delay):
			case <-r.Context().Done():
				return
			}
		}
		for key, value := range handler.Headers {
			w.Header().Set(key, value)
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}

		status := handler.StatusCode
		if status <= 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)

		body := strings.TrimSpace(handler.Body)
		if body == "" {
			body = `{"ok":true}`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	return server.URL
}

func MustJSONBytes(t testing.TB, payload any) []byte {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return raw
}
