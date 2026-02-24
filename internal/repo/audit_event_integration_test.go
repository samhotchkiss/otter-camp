//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestAuditEventRepoInsertListFiltersAndConstraints(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	auditRepo := NewAuditEventRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{
		Slug:        "audit-org",
		DisplayName: "Audit Org",
	})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}

	principalID := uuid.New()
	targetID := uuid.New()
	eventTypeLogin := "auth.login"
	eventTypeLogout := "auth.logout"
	targetType := "session"
	delegatedType := "human"
	delegatedID := uuid.New()

	beforeInsert := time.Now().UTC()
	if err := auditRepo.Insert(ctx, AuditEvent{
		OrganizationID: org.ID,
		EventType:      eventTypeLogin,
		PrincipalType:  "human",
		PrincipalID:    principalID,
		TargetType:     &targetType,
		TargetID:       &targetID,
		Metadata:       []byte(`{"ip":"127.0.0.1"}`),
	}); err != nil {
		t.Fatalf("insert event 1 failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	midpoint := time.Now().UTC()

	if err := auditRepo.Insert(ctx, AuditEvent{
		OrganizationID:  org.ID,
		EventType:       eventTypeLogout,
		PrincipalType:   "human",
		PrincipalID:     principalID,
		DelegatedByType: &delegatedType,
		DelegatedByID:   &delegatedID,
		Metadata:        []byte(`{"reason":"manual"}`),
	}); err != nil {
		t.Fatalf("insert event 2 failed: %v", err)
	}

	systemActorID := uuid.MustParse("00000000-0000-0000-0000-000000000000")
	if err := auditRepo.Insert(ctx, AuditEvent{
		OrganizationID: org.ID,
		EventType:      "auth.login_failed",
		PrincipalType:  "system",
		PrincipalID:    systemActorID,
		Metadata:       []byte(`{"source":"bootstrap"}`),
	}); err != nil {
		t.Fatalf("insert system event failed: %v", err)
	}

	byType, err := auditRepo.ListByOrg(ctx, org.ID, AuditEventFilters{
		EventType: &eventTypeLogin,
	}, Pagination{Limit: 50})
	if err != nil {
		t.Fatalf("ListByOrg by type failed: %v", err)
	}
	if len(byType) != 1 || byType[0].EventType != eventTypeLogin {
		t.Fatalf("ListByOrg by event_type returned %+v", byType)
	}

	byDate, err := auditRepo.ListByOrg(ctx, org.ID, AuditEventFilters{
		CreatedFrom: &midpoint,
	}, Pagination{Limit: 50})
	if err != nil {
		t.Fatalf("ListByOrg by date failed: %v", err)
	}
	if len(byDate) == 0 {
		t.Fatal("expected date-range filter to return rows")
	}
	for _, item := range byDate {
		if item.CreatedAt.Before(midpoint) {
			t.Fatalf("date filter returned row before midpoint: %s < %s", item.CreatedAt, midpoint)
		}
	}

	byTarget, err := auditRepo.ListByOrg(ctx, org.ID, AuditEventFilters{
		TargetType: &targetType,
		TargetID:   &targetID,
	}, Pagination{Limit: 50})
	if err != nil {
		t.Fatalf("ListByOrg by target failed: %v", err)
	}
	if len(byTarget) != 1 {
		t.Fatalf("ListByOrg by target count = %d, want 1", len(byTarget))
	}
	if byTarget[0].TargetType == nil || *byTarget[0].TargetType != targetType {
		t.Fatalf("target type = %v, want %q", byTarget[0].TargetType, targetType)
	}

	count, err := auditRepo.CountByOrg(ctx, org.ID, AuditEventFilters{
		CreatedFrom: &beforeInsert,
	})
	if err != nil {
		t.Fatalf("CountByOrg failed: %v", err)
	}
	if count < 3 {
		t.Fatalf("CountByOrg = %d, want >= 3", count)
	}

	if err := auditRepo.Insert(ctx, AuditEvent{
		OrganizationID: org.ID,
		EventType:      "auth.login",
		PrincipalType:  "robot",
		PrincipalID:    principalID,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid principal_type error = %v, want ErrConflict", err)
	}

	if err := auditRepo.Insert(ctx, AuditEvent{
		OrganizationID:  org.ID,
		EventType:       "auth.login",
		PrincipalType:   "human",
		PrincipalID:     principalID,
		DelegatedByType: &delegatedType,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid delegation pairing error = %v, want ErrConflict", err)
	}
}
