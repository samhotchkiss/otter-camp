//go:build integration

package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestServiceRecordInsertsAndReturnsFKViolation(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	orgRepo := repo.NewOrgRepo(pool)
	auditRepo := repo.NewAuditEventRepo(pool)
	service := NewService(auditRepo, nil)

	org, err := orgRepo.Create(ctx, repo.Organization{Slug: "audit-service-org", DisplayName: "Audit Service Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	principalID := uuid.New()
	delegatedByType := "human"
	delegatedByID := uuid.New()
	targetType := "session"
	targetID := uuid.New()
	eventType := EventAuthLoginFailed

	err = service.Record(ctx, Event{
		OrgID:           org.ID,
		EventType:       eventType,
		PrincipalType:   "human",
		PrincipalID:     principalID,
		DelegatedByType: &delegatedByType,
		DelegatedByID:   &delegatedByID,
		TargetType:      &targetType,
		TargetID:        &targetID,
		Metadata: map[string]any{
			"ip_address": "10.0.0.1",
		},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	listed, err := auditRepo.ListByOrg(ctx, org.ID, repo.AuditEventFilters{
		EventType: &eventType,
	}, repo.Pagination{Limit: 20})
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListByOrg count = %d, want 1", len(listed))
	}
	if listed[0].PrincipalID != principalID {
		t.Fatalf("principal_id = %s, want %s", listed[0].PrincipalID, principalID)
	}

	if err := service.Record(ctx, Event{
		OrgID:         uuid.New(),
		EventType:     EventAuthLogin,
		PrincipalType: "human",
		PrincipalID:   principalID,
	}); pgErrCode(err) != "23503" {
		t.Fatalf("foreign key error code = %q, want 23503 (err=%v)", pgErrCode(err), err)
	}

	if err := service.Record(ctx, Event{
		OrgID:         org.ID,
		EventType:     EventAuthLogout,
		PrincipalType: "system",
		PrincipalID:   SystemPrincipalID,
	}); err != nil {
		t.Fatalf("system principal record failed: %v", err)
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
