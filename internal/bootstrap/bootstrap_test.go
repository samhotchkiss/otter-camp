package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestBootstrapperRegisterStepOverridesStubAndPreservesOrder(t *testing.T) {
	b := New(Options{})

	executed := make([]string, 0, len(defaultStepOrder))
	for _, stepName := range defaultStepOrder {
		name := stepName
		b.RegisterStep(name, func(context.Context, *BootstrapState) error {
			executed = append(executed, name)
			return nil
		})
	}

	b.RegisterStep("create-agents", func(context.Context, *BootstrapState) error {
		executed = append(executed, "custom-create-agents")
		return nil
	})

	if err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(executed) != len(defaultStepOrder) {
		t.Fatalf("executed step count = %d, want %d", len(executed), len(defaultStepOrder))
	}

	for i, stepName := range defaultStepOrder {
		expected := stepName
		if stepName == stepNameCreateAgents {
			expected = "custom-create-agents"
		}
		if executed[i] != expected {
			t.Fatalf("step[%d] = %q, want %q", i, executed[i], expected)
		}
	}
}

func TestBootstrapperRegisterStepAliasOverridesCreateAgents(t *testing.T) {
	b := New(Options{})

	executed := make([]string, 0, len(defaultStepOrder))
	for _, stepName := range defaultStepOrder {
		name := stepName
		b.RegisterStep(name, func(context.Context, *BootstrapState) error {
			executed = append(executed, name)
			return nil
		})
	}

	b.RegisterStep("create-starter-trio", func(context.Context, *BootstrapState) error {
		executed = append(executed, "custom-starter-trio")
		return nil
	})

	if err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if executed[6] != "custom-starter-trio" {
		t.Fatalf("step 7 = %q, want custom-starter-trio", executed[6])
	}
}

func TestBootstrapperRunRecoversStepPanic(t *testing.T) {
	b := New(Options{})

	for _, stepName := range defaultStepOrder {
		b.RegisterStep(stepName, func(context.Context, *BootstrapState) error {
			return nil
		})
	}

	b.RegisterStep(stepNameSeedModels, func(context.Context, *BootstrapState) error {
		panic("boom")
	})

	err := b.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "panic recovered") {
		t.Fatalf("error = %q, want panic recovered text", err)
	}
}

func TestAdminUserExists(t *testing.T) {
	users := []repo.HumanUser{{Role: "member"}, {Role: "Admin"}}
	if !adminUserExists(users) {
		t.Fatal("adminUserExists returned false, want true")
	}

	users = []repo.HumanUser{{Role: "member"}}
	if adminUserExists(users) {
		t.Fatal("adminUserExists returned true, want false")
	}
}

func TestReadAdminCredentials(t *testing.T) {
	t.Setenv("OTTERCAMP_ADMIN_EMAIL", "")
	t.Setenv("OTTERCAMP_ADMIN_PASSWORD", "")
	_, _, configured, err := readAdminCredentials()
	if err != nil {
		t.Fatalf("readAdminCredentials returned error: %v", err)
	}
	if configured {
		t.Fatal("configured = true, want false")
	}

	t.Setenv("OTTERCAMP_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("OTTERCAMP_ADMIN_PASSWORD", "secret")
	email, password, configured, err := readAdminCredentials()
	if err != nil {
		t.Fatalf("readAdminCredentials returned error: %v", err)
	}
	if !configured {
		t.Fatal("configured = false, want true")
	}
	if email != "admin@example.com" || password != "secret" {
		t.Fatalf("unexpected credentials email=%q password=%q", email, password)
	}

	t.Setenv("OTTERCAMP_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("OTTERCAMP_ADMIN_PASSWORD", "")
	_, _, _, err = readAdminCredentials()
	if err == nil {
		t.Fatal("expected error when only one credential is set")
	}
}

func TestSkillNeedsUpsert(t *testing.T) {
	orgID := uuid.New()
	existing := repo.Skill{
		OrganizationID: orgID,
		Slug:           "summarize",
		DisplayName:    "Summarize",
		Description:    "Summarize text",
		FilePath:       "skills/summarize.md",
		IsActive:       true,
	}
	desired := existing

	if skillNeedsUpsert(existing, desired) {
		t.Fatal("skillNeedsUpsert returned true for identical skills")
	}

	desired.Description = "Updated"
	if !skillNeedsUpsert(existing, desired) {
		t.Fatal("skillNeedsUpsert returned false for changed description")
	}
}

func TestModelProfileNeedsDeprecation(t *testing.T) {
	providerID := uuid.New()
	temp := 0.7
	current := repo.ModelProfile{
		ProviderID:          providerID,
		ModelName:           "claude-sonnet-4-5",
		ContextWindowTokens: 200000,
		MaxOutputTokens:     8192,
		SupportsStreaming:   true,
		SupportsVision:      true,
		Temperature:         &temp,
		InvocationPurpose:   "agent_turn",
	}
	seed := modelProfileSeed{
		ProviderID:          providerID,
		ModelName:           "claude-sonnet-4-5",
		ContextWindowTokens: 200000,
		MaxOutputTokens:     8192,
		SupportsStreaming:   true,
		SupportsVision:      true,
		Temperature:         ptrFloat64(0.7),
		InvocationPurpose:   "agent_turn",
	}

	if modelProfileNeedsDeprecation(current, seed) {
		t.Fatal("modelProfileNeedsDeprecation returned true for equivalent profile")
	}

	seed.ModelName = "claude-opus-4-5"
	if !modelProfileNeedsDeprecation(current, seed) {
		t.Fatal("modelProfileNeedsDeprecation returned false for changed model")
	}
}
