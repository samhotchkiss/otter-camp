//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestAuditEventRepoInsertConstraintsAndFilters(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	auditRepo := NewAuditEventRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "audit-org", DisplayName: "Audit Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	principalID := uuid.New()
	targetID := uuid.New()
	delegatedByType := "human"
	delegatedByID := uuid.New()

	if err := auditRepo.Insert(ctx, AuditEvent{
		OrganizationID:  org.ID,
		EventType:       "auth.login",
		PrincipalType:   "human",
		PrincipalID:     principalID,
		DelegatedByType: &delegatedByType,
		DelegatedByID:   &delegatedByID,
		TargetType:      ptrString("session"),
		TargetID:        &targetID,
		Metadata: map[string]any{
			"ip_address": "127.0.0.1",
		},
	}); err != nil {
		t.Fatalf("insert full audit event: %v", err)
	}

	if err := auditRepo.Insert(ctx, AuditEvent{
		OrganizationID: org.ID,
		EventType:      "auth.logout",
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
	}); err != nil {
		t.Fatalf("insert system principal event: %v", err)
	}

	listed, err := auditRepo.ListByOrg(ctx, org.ID, AuditEventFilters{
		EventType: ptrString("auth.login"),
	}, Pagination{Limit: 25})
	if err != nil {
		t.Fatalf("ListByOrg by event_type: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListByOrg by event_type count = %d, want 1", len(listed))
	}
	if listed[0].PrincipalID != principalID {
		t.Fatalf("principal_id = %s, want %s", listed[0].PrincipalID, principalID)
	}
	if listed[0].DelegatedByType == nil || *listed[0].DelegatedByType != delegatedByType {
		t.Fatalf("delegated_by_type = %v, want %q", listed[0].DelegatedByType, delegatedByType)
	}
	if listed[0].DelegatedByID == nil || *listed[0].DelegatedByID != delegatedByID {
		t.Fatalf("delegated_by_id = %v, want %s", listed[0].DelegatedByID, delegatedByID)
	}
	if listed[0].TargetType == nil || *listed[0].TargetType != "session" {
		t.Fatalf("target_type = %v, want %q", listed[0].TargetType, "session")
	}
	if listed[0].TargetID == nil || *listed[0].TargetID != targetID {
		t.Fatalf("target_id = %v, want %s", listed[0].TargetID, targetID)
	}
	if gotIP, _ := listed[0].Metadata["ip_address"].(string); gotIP != "127.0.0.1" {
		t.Fatalf("metadata ip_address = %v, want %q", listed[0].Metadata["ip_address"], "127.0.0.1")
	}

	if _, err := auditRepo.CountByOrg(ctx, org.ID, AuditEventFilters{
		EventType: ptrString("auth.login"),
	}); err != nil {
		t.Fatalf("CountByOrg by event_type: %v", err)
	}
	principalTypeFiltered, err := auditRepo.ListByOrg(ctx, org.ID, AuditEventFilters{
		PrincipalType: ptrString("system"),
	}, Pagination{Limit: 25})
	if err != nil {
		t.Fatalf("ListByOrg by principal_type: %v", err)
	}
	if len(principalTypeFiltered) != 1 {
		t.Fatalf("ListByOrg by principal_type count = %d, want 1", len(principalTypeFiltered))
	}
	if principalTypeFiltered[0].EventType != "auth.logout" {
		t.Fatalf("principal_type filtered event_type = %q, want %q", principalTypeFiltered[0].EventType, "auth.logout")
	}

	if err := auditRepo.Insert(ctx, AuditEvent{
		OrganizationID: org.ID,
		EventType:      "auth.bad_principal",
		PrincipalType:  "robot",
		PrincipalID:    principalID,
	}); pgErrCode(err) != "23514" {
		t.Fatalf("principal_type CHECK err code = %q, want 23514 (err=%v)", pgErrCode(err), err)
	}

	invalidDelegatedType := "human"
	if err := auditRepo.Insert(ctx, AuditEvent{
		OrganizationID:  org.ID,
		EventType:       "auth.bad_delegation",
		PrincipalType:   "human",
		PrincipalID:     principalID,
		DelegatedByType: &invalidDelegatedType,
	}); pgErrCode(err) != "23514" {
		t.Fatalf("delegation pair CHECK err code = %q, want 23514 (err=%v)", pgErrCode(err), err)
	}
}

func TestAuditEventRepoListByOrgDateRangeAndTargetFilters(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgRepo := NewOrgRepo(pool)
	auditRepo := NewAuditEventRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{Slug: "audit-org-range", DisplayName: "Audit Org Range"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	targetA := uuid.New()
	targetB := uuid.New()
	principalID := uuid.New()

	if err := auditRepo.Insert(ctx, AuditEvent{
		OrganizationID: org.ID,
		EventType:      "auth.range_1",
		PrincipalType:  "human",
		PrincipalID:    principalID,
		TargetType:     ptrString("session"),
		TargetID:       &targetA,
	}); err != nil {
		t.Fatalf("insert range_1: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	midpoint := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)

	if err := auditRepo.Insert(ctx, AuditEvent{
		OrganizationID: org.ID,
		EventType:      "auth.range_2",
		PrincipalType:  "human",
		PrincipalID:    principalID,
		TargetType:     ptrString("session"),
		TargetID:       &targetA,
	}); err != nil {
		t.Fatalf("insert range_2: %v", err)
	}

	if err := auditRepo.Insert(ctx, AuditEvent{
		OrganizationID: org.ID,
		EventType:      "auth.range_3",
		PrincipalType:  "human",
		PrincipalID:    principalID,
		TargetType:     ptrString("session"),
		TargetID:       &targetB,
	}); err != nil {
		t.Fatalf("insert range_3: %v", err)
	}

	inRange, err := auditRepo.ListByOrg(ctx, org.ID, AuditEventFilters{
		CreatedAfter: &midpoint,
	}, Pagination{Limit: 50})
	if err != nil {
		t.Fatalf("ListByOrg by date range: %v", err)
	}
	if len(inRange) != 2 {
		t.Fatalf("ListByOrg date range count = %d, want 2", len(inRange))
	}
	for _, event := range inRange {
		if event.CreatedAt.Before(midpoint) {
			t.Fatalf("event created_at %s was before range start %s", event.CreatedAt, midpoint)
		}
	}

	targetOnly, err := auditRepo.ListByOrg(ctx, org.ID, AuditEventFilters{
		TargetType: ptrString("session"),
		TargetID:   &targetA,
	}, Pagination{Limit: 50})
	if err != nil {
		t.Fatalf("ListByOrg by target_type+target_id: %v", err)
	}
	if len(targetOnly) != 2 {
		t.Fatalf("ListByOrg target filter count = %d, want 2", len(targetOnly))
	}

	count, err := auditRepo.CountByOrg(ctx, org.ID, AuditEventFilters{
		TargetType: ptrString("session"),
		TargetID:   &targetA,
	})
	if err != nil {
		t.Fatalf("CountByOrg target filter: %v", err)
	}
	if count != 2 {
		t.Fatalf("CountByOrg target filter = %d, want 2", count)
	}

	_, err = auditRepo.ListByOrg(ctx, org.ID, AuditEventFilters{
		TargetType: ptrString("session"),
	}, Pagination{Limit: 10})
	if err == nil {
		t.Fatal("expected error when target_type provided without target_id")
	}
}

func pgErrCode(err error) string {
	if err == nil {
		return ""
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func ptrString(s string) *string {
	return &s
}
