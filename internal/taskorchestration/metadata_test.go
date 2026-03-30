package taskorchestration

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
)

func TestApplyMergesParentIntegrationMetadata(t *testing.T) {
	childA := uuid.New()
	childB := uuid.New()
	metadata := taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{
		RequiresDecomposition: true,
		PrimaryDeliverable:    "Ship the launch",
		Deliverables:          []string{"Ship the launch", "Landing page", "Billing flow"},
	}, "Ship the launch end to end.", []uuid.UUID{childA, childB})

	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	updated, err := Apply(metadata, Update{
		ChildVerifications: []ChildVerification{
			NewChildVerification(childA, "Verified the landing page output against the brief.", now),
			NewChildVerification(childB, "Verified the billing flow handoff.", now),
		},
		IntegrationCheck:  NewIntegrationCheck("passed", "Ran the launch smoke test across both child deliverables.", now),
		OutcomeAssessment: NewOutcomeAssessment(true, "The launch bundle now satisfies the parent outcome.", now),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	state, ok := Parse(updated)
	if !ok {
		t.Fatal("Parse ok = false, want true")
	}
	if len(state.ChildVerifications) != 2 {
		t.Fatalf("child_verifications len = %d, want 2", len(state.ChildVerifications))
	}
	if state.IntegrationCheck == nil || state.IntegrationCheck.Status != "passed" {
		t.Fatalf("integration_check = %+v, want passed", state.IntegrationCheck)
	}
	if state.OutcomeAssessment == nil || !state.OutcomeAssessment.Satisfied {
		t.Fatalf("outcome_assessment = %+v, want satisfied", state.OutcomeAssessment)
	}
}

func TestValidateCompletionRejectsMissingParentChecks(t *testing.T) {
	parentID := uuid.New()
	childID := uuid.New()
	parent := repo.ProjectTask{
		ID:       parentID,
		Metadata: taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{RequiresDecomposition: true, PrimaryDeliverable: "Primary", Deliverables: []string{"Primary", "Child"}}, "source", []uuid.UUID{childID}),
	}
	child := repo.ProjectTask{ID: childID, TaskNumber: 2, Title: "Billing child", WorkStatus: "done"}

	err := ValidateCompletion(parent, []repo.ProjectTask{child})
	if !errors.Is(err, ErrParentCompletionRequirements) {
		t.Fatalf("ValidateCompletion err = %v, want ErrParentCompletionRequirements", err)
	}
	if !strings.Contains(err.Error(), "verify child outputs") {
		t.Fatalf("error = %v, want missing child verification detail", err)
	}
	if !strings.Contains(err.Error(), "integration or end-to-end") {
		t.Fatalf("error = %v, want integration detail", err)
	}
}

func TestValidateCompletionAllowsVerifiedIntegratedParent(t *testing.T) {
	parentID := uuid.New()
	childID := uuid.New()
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	parent := repo.ProjectTask{
		ID:         parentID,
		TaskNumber: 1,
		Title:      "Launch parent",
		Metadata:   taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{RequiresDecomposition: true, PrimaryDeliverable: "Primary", Deliverables: []string{"Primary", "Child"}}, "source", []uuid.UUID{childID}),
	}
	metadata, err := Apply(parent.Metadata, Update{
		ChildVerifications: []ChildVerification{NewChildVerification(childID, "Verified the child output.", now)},
		IntegrationCheck:   NewIntegrationCheck("passed", "Ran the integration smoke test.", now),
		OutcomeAssessment:  NewOutcomeAssessment(true, "The parent outcome is satisfied.", now),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	parent.Metadata = metadata

	child := repo.ProjectTask{ID: childID, TaskNumber: 2, Title: "Billing child", WorkStatus: "done"}
	if err := ValidateCompletion(parent, []repo.ProjectTask{child}); err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
}

func TestValidateCompletionAllowsBlockedProceduralChildrenWithSatisfiedParent(t *testing.T) {
	parentID := uuid.New()
	blockedChildID := uuid.New()
	doneChildID := uuid.New()
	now := time.Date(2026, 3, 29, 3, 50, 0, 0, time.UTC)
	description := "Crawl technonymous.org and produce a JSON index file at `content/technonymous-index.json`."
	parent := repo.ProjectTask{
		ID:         parentID,
		TaskNumber: 44,
		Title:      "Produce content/technonymous-index.json by crawling technonymous.org",
		Metadata: taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{
			RequiresDecomposition: true,
			PrimaryDeliverable:    "content/technonymous-index.json",
			Deliverables: []string{
				"content/technonymous-index.json",
				"Use browser tools to navigate to https://technonymous.org",
				"Verify content/technonymous-index.json delivered — close parent OC-44",
			},
		}, description, []uuid.UUID{blockedChildID, doneChildID}),
	}
	metadata, err := Apply(parent.Metadata, Update{
		ChildVerifications: []ChildVerification{
			NewChildVerification(doneChildID, "Verified the deliverable and confirmed the parent can close.", now),
		},
		IntegrationCheck:  NewIntegrationCheck("passed", "Verified the delivered index against downstream scrape work.", now),
		OutcomeAssessment: NewOutcomeAssessment(true, "The parent deliverable is satisfied.", now),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	parent.Metadata = metadata

	blockedChild := repo.ProjectTask{
		ID:         blockedChildID,
		TaskNumber: 45,
		Title:      "Use browser tools to navigate to https://technonymous.org",
		WorkStatus: "blocked",
	}
	doneDescription := "Confirm the parent deliverable is complete and the parent can close."
	doneChild := repo.ProjectTask{
		ID:          doneChildID,
		TaskNumber:  75,
		Title:       "Verify content/technonymous-index.json delivered — close parent OC-44",
		Description: &doneDescription,
		WorkStatus:  "done",
	}

	if err := ValidateCompletion(parent, []repo.ProjectTask{blockedChild, doneChild}); err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
}

func TestValidateCompletionAllowsBlockedChildrenAfterCompletedCloseoutProof(t *testing.T) {
	parentID := uuid.New()
	blockedChildID := uuid.New()
	doneAID := uuid.New()
	doneBID := uuid.New()
	closeoutChildID := uuid.New()
	now := time.Date(2026, 3, 29, 8, 45, 0, 0, time.UTC)
	description := "Scrape and import technonymous.org posts from URL index."
	parent := repo.ProjectTask{
		ID:         parentID,
		TaskNumber: 34,
		Title:      "Scrape and import technonymous.org posts from URL index",
		Metadata: taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{
			RequiresDecomposition: true,
			PrimaryDeliverable:    "content/posts/{slug}.md",
			Deliverables: []string{
				"content/posts/{slug}.md",
				"Fetch posts 13-24 from content/technonymous-index.json and save as markdown in content/posts/",
				"Fetch posts 25-35 from content/technonymous-index.json and save as markdown in content/posts/",
				"Close out OC-34: verify content/posts/ contains all scraped posts and mark parent complete",
			},
		}, description, []uuid.UUID{blockedChildID, doneAID, doneBID, closeoutChildID}),
	}
	metadata, err := Apply(parent.Metadata, Update{
		ChildVerifications: []ChildVerification{
			NewChildVerification(doneAID, "Verified posts 13-24 landed in content/posts/.", now),
			NewChildVerification(doneBID, "Verified posts 25-35 landed in content/posts/.", now),
			NewChildVerification(closeoutChildID, "Verified the closeout child confirmed all 35 posts exist and the parent can close.", now),
		},
		IntegrationCheck:  NewIntegrationCheck("passed", "Confirmed the full scrape-import workstream is complete end to end.", now),
		OutcomeAssessment: NewOutcomeAssessment(true, "The scrape-import parent outcome is satisfied.", now),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	parent.Metadata = metadata

	blockedChild := repo.ProjectTask{
		ID:         blockedChildID,
		TaskNumber: 81,
		Title:      "Scrape all technonymous.org posts from content/technonymous-index.json and save as markdown files in content/posts/",
		WorkStatus: "blocked",
	}
	doneChildA := repo.ProjectTask{
		ID:         doneAID,
		TaskNumber: 66,
		Title:      "Fetch posts 13–24 from content/technonymous-index.json and save as markdown in content/posts/",
		WorkStatus: "done",
	}
	doneChildB := repo.ProjectTask{
		ID:         doneBID,
		TaskNumber: 67,
		Title:      "Fetch posts 25–35 from content/technonymous-index.json and save as markdown in content/posts/",
		WorkStatus: "done",
	}
	closeoutDescription := "All 35 posts exist in content/posts/ and the parent can close."
	closeoutChild := repo.ProjectTask{
		ID:          closeoutChildID,
		TaskNumber:  80,
		Title:       "Close out OC-34: verify content/posts/ contains all scraped posts and mark parent complete",
		Description: &closeoutDescription,
		WorkStatus:  "done",
	}

	if err := ValidateCompletion(parent, []repo.ProjectTask{blockedChild, doneChildA, doneChildB, closeoutChild}); err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
}

func TestValidateCompletionAllowsBlockedChildrenWhenOutcomeAlreadySatisfied(t *testing.T) {
	parentID := uuid.New()
	blockedAID := uuid.New()
	blockedBID := uuid.New()
	doneAID := uuid.New()
	doneBID := uuid.New()
	doneCID := uuid.New()
	now := time.Date(2026, 3, 29, 16, 40, 0, 0, time.UTC)
	description := "Append Overview & Purpose to planning/sambot-feature-spec.md."
	parent := repo.ProjectTask{
		ID:         parentID,
		TaskNumber: 113,
		Title:      "Append Overview & Purpose section",
		Metadata: taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{
			RequiresDecomposition: true,
			PrimaryDeliverable:    "planning/sambot-feature-spec.md",
			Deliverables: []string{
				"planning/sambot-feature-spec.md",
				"Mission statement child",
				"Replacement child A",
				"Replacement child B",
			},
		}, description, []uuid.UUID{blockedAID, blockedBID, doneAID, doneBID, doneCID}),
	}
	metadata, err := Apply(parent.Metadata, Update{
		ChildVerifications: []ChildVerification{
			NewChildVerification(doneAID, "Verified mission statement content exists in planning/sambot-feature-spec.md.", now),
			NewChildVerification(doneBID, "Verified replacement child content already landed in planning/sambot-feature-spec.md.", now),
			NewChildVerification(doneCID, "Verified final replacement child completed the Overview & Purpose deliverable.", now),
		},
		IntegrationCheck:  NewIntegrationCheck("passed", "Reviewed the delivered Overview & Purpose section end to end.", now),
		OutcomeAssessment: NewOutcomeAssessment(true, "The parent Overview & Purpose outcome is already satisfied.", now),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	parent.Metadata = metadata

	blockedChildA := repo.ProjectTask{
		ID:         blockedAID,
		TaskNumber: 114,
		Title:      "Append replacement child A",
		WorkStatus: "blocked",
	}
	blockedChildB := repo.ProjectTask{
		ID:         blockedBID,
		TaskNumber: 129,
		Title:      "Append replacement child B",
		WorkStatus: "blocked",
	}
	doneChildA := repo.ProjectTask{
		ID:         doneAID,
		TaskNumber: 115,
		Title:      "Mission statement child",
		WorkStatus: "done",
	}
	doneChildB := repo.ProjectTask{
		ID:         doneBID,
		TaskNumber: 124,
		Title:      "Replacement child A",
		WorkStatus: "done",
	}
	doneChildC := repo.ProjectTask{
		ID:         doneCID,
		TaskNumber: 130,
		Title:      "Replacement child B",
		WorkStatus: "done",
	}

	if err := ValidateCompletion(parent, []repo.ProjectTask{blockedChildA, blockedChildB, doneChildA, doneChildB, doneChildC}); err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
}

func TestValidateCompletionStillRejectsActiveChildAfterCompletedCloseoutProof(t *testing.T) {
	parentID := uuid.New()
	activeChildID := uuid.New()
	closeoutChildID := uuid.New()
	now := time.Date(2026, 3, 29, 8, 50, 0, 0, time.UTC)
	description := "Scrape and import technonymous.org posts from URL index."
	parent := repo.ProjectTask{
		ID:         parentID,
		TaskNumber: 34,
		Title:      "Scrape and import technonymous.org posts from URL index",
		Metadata: taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{
			RequiresDecomposition: true,
			PrimaryDeliverable:    "content/posts/{slug}.md",
			Deliverables: []string{
				"content/posts/{slug}.md",
				"Close out OC-34: verify content/posts/ contains all scraped posts and mark parent complete",
			},
		}, description, []uuid.UUID{activeChildID, closeoutChildID}),
	}
	metadata, err := Apply(parent.Metadata, Update{
		ChildVerifications: []ChildVerification{
			NewChildVerification(closeoutChildID, "Verified the closeout child confirmed the parent can close.", now),
		},
		IntegrationCheck:  NewIntegrationCheck("passed", "Confirmed the current completed work is coherent.", now),
		OutcomeAssessment: NewOutcomeAssessment(true, "The parent outcome looks satisfied pending the live active child lane.", now),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	parent.Metadata = metadata

	activeChild := repo.ProjectTask{
		ID:         activeChildID,
		TaskNumber: 81,
		Title:      "Scrape all technonymous.org posts from content/technonymous-index.json and save as markdown files in content/posts/",
		WorkStatus: "in_progress",
	}
	closeoutDescription := "All 35 posts exist in content/posts/ and the parent can close."
	closeoutChild := repo.ProjectTask{
		ID:          closeoutChildID,
		TaskNumber:  80,
		Title:       "Close out OC-34: verify content/posts/ contains all scraped posts and mark parent complete",
		Description: &closeoutDescription,
		WorkStatus:  "done",
	}

	err = ValidateCompletion(parent, []repo.ProjectTask{activeChild, closeoutChild})
	if !errors.Is(err, ErrParentCompletionRequirements) {
		t.Fatalf("ValidateCompletion err = %v, want ErrParentCompletionRequirements", err)
	}
	if !strings.Contains(err.Error(), "all child tasks must complete before the parent can finish integration") {
		t.Fatalf("error = %v, want active-child completion detail", err)
	}
}

func TestValidateCompletionAllowsVerifiedSatisfiedParentWithoutCompletedDirectChild(t *testing.T) {
	parentID := uuid.New()
	blockedChildID := uuid.New()
	cancelledChildID := uuid.New()
	now := time.Date(2026, 3, 29, 22, 20, 0, 0, time.UTC)
	description := "Write sambot/api.js — a complete, working Express.js API server for SamBot."
	parent := repo.ProjectTask{
		ID:         parentID,
		TaskNumber: 200,
		Title:      "Write SamBot technical architecture spec — planning/sambot-tech-architecture.md (replaces blocked OC-198)",
		Metadata: taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{
			RequiresDecomposition: true,
			PrimaryDeliverable:    "planning/sambot-tech-architecture.md",
			Deliverables: []string{
				"planning/sambot-tech-architecture.md",
				"Replacement child A",
				"Replacement child B",
			},
		}, description, []uuid.UUID{blockedChildID, cancelledChildID}),
	}
	metadata, err := Apply(parent.Metadata, Update{
		ChildVerifications: []ChildVerification{
			NewChildVerification(blockedChildID, "Verified the superseding architecture spec already exists on disk and satisfies the parent deliverable.", now),
		},
		IntegrationCheck:  NewIntegrationCheck("passed", "Reviewed the delivered architecture spec end to end.", now),
		OutcomeAssessment: NewOutcomeAssessment(true, "The parent architecture outcome is already satisfied.", now),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	parent.Metadata = metadata

	blockedChild := repo.ProjectTask{
		ID:         blockedChildID,
		TaskNumber: 212,
		Title:      "Replacement child A",
		WorkStatus: "blocked",
	}
	cancelledChild := repo.ProjectTask{
		ID:         cancelledChildID,
		TaskNumber: 205,
		Title:      "Replacement child B",
		WorkStatus: "cancelled",
	}

	if err := ValidateCompletion(parent, []repo.ProjectTask{blockedChild, cancelledChild}); err != nil {
		t.Fatalf("ValidateCompletion: %v", err)
	}
}

func TestValidateCompletionStillRequiresVerificationWhenNoCompletedDirectChildRemains(t *testing.T) {
	parentID := uuid.New()
	blockedChildID := uuid.New()
	cancelledChildID := uuid.New()
	now := time.Date(2026, 3, 29, 22, 25, 0, 0, time.UTC)
	description := "Write sambot/api.js — a complete, working Express.js API server for SamBot."
	parent := repo.ProjectTask{
		ID:         parentID,
		TaskNumber: 200,
		Title:      "Write SamBot technical architecture spec — planning/sambot-tech-architecture.md (replaces blocked OC-198)",
		Metadata: taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{
			RequiresDecomposition: true,
			PrimaryDeliverable:    "planning/sambot-tech-architecture.md",
			Deliverables: []string{
				"planning/sambot-tech-architecture.md",
				"Replacement child A",
				"Replacement child B",
			},
		}, description, []uuid.UUID{blockedChildID, cancelledChildID}),
	}
	metadata, err := Apply(parent.Metadata, Update{
		IntegrationCheck:  NewIntegrationCheck("passed", "Reviewed the delivered architecture spec end to end.", now),
		OutcomeAssessment: NewOutcomeAssessment(true, "The parent architecture outcome is already satisfied.", now),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	parent.Metadata = metadata

	blockedChild := repo.ProjectTask{
		ID:         blockedChildID,
		TaskNumber: 212,
		Title:      "Replacement child A",
		WorkStatus: "blocked",
	}
	cancelledChild := repo.ProjectTask{
		ID:         cancelledChildID,
		TaskNumber: 205,
		Title:      "Replacement child B",
		WorkStatus: "cancelled",
	}

	err = ValidateCompletion(parent, []repo.ProjectTask{blockedChild, cancelledChild})
	if !errors.Is(err, ErrParentCompletionRequirements) {
		t.Fatalf("ValidateCompletion err = %v, want ErrParentCompletionRequirements", err)
	}
	if !strings.Contains(err.Error(), "no completed child tasks are available for parent verification") {
		t.Fatalf("error = %v, want missing verification detail", err)
	}
}

func TestValidateCompletionAllowsSupersedingOutputVerificationWhenNoExecutableChildRemains(t *testing.T) {
	parentID := uuid.New()
	blockedChildID := uuid.New()
	cancelledChildID := uuid.New()
	supersedingTaskID := uuid.New()
	now := time.Date(2026, 3, 30, 0, 45, 0, 0, time.UTC)
	description := "Write templates/template-08-replace.html — a complete standalone HTML deliverable."
	parent := repo.ProjectTask{
		ID:         parentID,
		TaskNumber: 243,
		Title:      "Write templates/template-08-replace.html — Dark Mode Editorial layout (final replacement)",
		Metadata: taskdecomp.ApplyMetadata(json.RawMessage(`{}`), taskdecomp.Plan{
			RequiresDecomposition: true,
			PrimaryDeliverable:    "templates/template-08-replace.html",
			Deliverables: []string{
				"templates/template-08-replace.html",
				"Replacement child A",
				"Replacement child B",
			},
		}, description, []uuid.UUID{blockedChildID, cancelledChildID}),
	}
	metadata, err := Apply(parent.Metadata, Update{
		ChildVerifications: []ChildVerification{
			NewChildVerification(supersedingTaskID, "Superseding task OC-242 already delivered templates/template-08-replace.html as the final complete file.", now),
		},
		IntegrationCheck:  NewIntegrationCheck("passed", "Reviewed the delivered template end to end.", now),
		OutcomeAssessment: NewOutcomeAssessment(true, "The final replacement template is already satisfied by the superseding output.", now),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	parent.Metadata = metadata

	blockedChild := repo.ProjectTask{
		ID:         blockedChildID,
		TaskNumber: 244,
		Title:      "Replacement child A",
		WorkStatus: "blocked",
	}
	cancelledChild := repo.ProjectTask{
		ID:         cancelledChildID,
		TaskNumber: 241,
		Title:      "Replacement child B",
		WorkStatus: "cancelled",
	}

	if err := ValidateCompletion(parent, []repo.ProjectTask{blockedChild, cancelledChild}); err != nil {
		t.Fatalf("ValidateCompletion err = %v, want nil for superseding-output verification", err)
	}
}
