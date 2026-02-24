//go:build integration

package audit

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestAuditServiceRecordAndList(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgRepo := repo.NewOrgRepo(pool)
	auditRepo := repo.NewAuditEventRepo(pool)
	service := NewService(auditRepo, nil)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "audit-service-org",
		DisplayName: "Audit Service Org",
	})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	principalID := uuid.New()
	targetType := "session"
	targetID := uuid.New()
	delegatedType := "human"
	delegatedID := uuid.New()

	if err := service.Record(ctx, Event{
		OrgID:           org.ID,
		EventType:       EventAuthLogin,
		PrincipalType:   "human",
		PrincipalID:     principalID,
		DelegatedByType: &delegatedType,
		DelegatedByID:   &delegatedID,
		TargetType:      &targetType,
		TargetID:        &targetID,
		Metadata: map[string]any{
			"ip": "127.0.0.1",
		},
	}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	filterType := EventAuthLogin
	events, err := auditRepo.ListByOrg(ctx, org.ID, repo.AuditEventFilters{
		EventType: &filterType,
	}, repo.Pagination{Limit: 10})
	if err != nil {
		t.Fatalf("ListByOrg failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("ListByOrg count = %d, want 1", len(events))
	}
	if events[0].DelegatedByType == nil || *events[0].DelegatedByType != delegatedType {
		t.Fatalf("delegated_by_type = %v, want %q", events[0].DelegatedByType, delegatedType)
	}
}

func TestAuditServiceRecordBadOrgReturnsError(t *testing.T) {
	service := NewService(repo.NewAuditEventRepo(testdb.New(t)), nil)
	err := service.Record(context.Background(), Event{
		OrgID:         uuid.New(),
		EventType:     EventAuthLogin,
		PrincipalType: "human",
		PrincipalID:   uuid.New(),
	})
	if err == nil {
		t.Fatal("expected Record to fail for missing organization")
	}
}
