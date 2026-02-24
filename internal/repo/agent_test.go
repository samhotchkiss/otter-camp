package repo

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeRow struct {
	scanFn func(dest ...any) error
}

func (f fakeRow) Scan(dest ...any) error {
	return f.scanFn(dest...)
}

func TestScanAgentNullableFields(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	createdAt := now.Add(-1 * time.Hour)
	updatedAt := now

	t.Run("nullable pointers populated including zero values", func(t *testing.T) {
		agentID := uuid.New()
		orgID := uuid.New()
		createdByID := uuid.New()
		tempProjectID := uuid.New()
		promotedToID := uuid.New()
		defaultProfileID := "high-capability"
		budgetCapTokens := int64(0)
		budgetPeriod := "daily"
		tempTTLSeconds := int32(0)
		tempExpiresAt := now.Add(2 * time.Hour)

		agent, err := scanAgent(fakeRow{scanFn: func(dest ...any) error {
			*dest[0].(*uuid.UUID) = agentID
			*dest[1].(*uuid.UUID) = orgID
			*dest[2].(*string) = "Temp Worker"
			*dest[3].(*string) = "temp"
			*dest[4].(*string) = "active"
			*dest[5].(*string) = "system"
			*dest[6].(*string) = "operator"
			*dest[7].(*string) = "worker"
			*dest[8].(*bool) = false
			*dest[9].(*bool) = false
			*dest[10].(*[]string) = []string{"org", "project"}
			*dest[11].(*[]string) = []string{}
			*dest[12].(*[]string) = []string{"mcp.*"}
			*dest[13].(**string) = &defaultProfileID
			*dest[14].(**int64) = &budgetCapTokens
			*dest[15].(**string) = &budgetPeriod
			*dest[16].(**uuid.UUID) = &tempProjectID
			*dest[17].(**int32) = &tempTTLSeconds
			*dest[18].(**time.Time) = &tempExpiresAt
			*dest[19].(**uuid.UUID) = &promotedToID
			*dest[20].(*string) = "system"
			*dest[21].(*uuid.UUID) = createdByID
			*dest[22].(*time.Time) = createdAt
			*dest[23].(*time.Time) = updatedAt
			return nil
		}})
		if err != nil {
			t.Fatalf("scanAgent returned error: %v", err)
		}

		if agent.DefaultModelProfileID == nil || *agent.DefaultModelProfileID != defaultProfileID {
			t.Fatalf("DefaultModelProfileID = %v, want %q", agent.DefaultModelProfileID, defaultProfileID)
		}
		if agent.BudgetCapTokens == nil || *agent.BudgetCapTokens != 0 {
			t.Fatalf("BudgetCapTokens = %v, want pointer to 0", agent.BudgetCapTokens)
		}
		if agent.BudgetPeriod == nil || *agent.BudgetPeriod != budgetPeriod {
			t.Fatalf("BudgetPeriod = %v, want %q", agent.BudgetPeriod, budgetPeriod)
		}
		if agent.TempProjectID == nil || *agent.TempProjectID != tempProjectID {
			t.Fatalf("TempProjectID = %v, want %s", agent.TempProjectID, tempProjectID)
		}
		if agent.TempTTLSeconds == nil || *agent.TempTTLSeconds != 0 {
			t.Fatalf("TempTTLSeconds = %v, want pointer to 0", agent.TempTTLSeconds)
		}
		if agent.TempExpiresAt == nil || !agent.TempExpiresAt.Equal(tempExpiresAt) {
			t.Fatalf("TempExpiresAt = %v, want %v", agent.TempExpiresAt, tempExpiresAt)
		}
		if agent.PromotedToAgentID == nil || *agent.PromotedToAgentID != promotedToID {
			t.Fatalf("PromotedToAgentID = %v, want %s", agent.PromotedToAgentID, promotedToID)
		}
	})

	t.Run("nullable pointers remain nil", func(t *testing.T) {
		agent, err := scanAgent(fakeRow{scanFn: func(dest ...any) error {
			*dest[0].(*uuid.UUID) = uuid.New()
			*dest[1].(*uuid.UUID) = uuid.New()
			*dest[2].(*string) = "Staff"
			*dest[3].(*string) = "staff"
			*dest[4].(*string) = "active"
			*dest[5].(*string) = "system"
			*dest[6].(*string) = ""
			*dest[7].(*string) = "general"
			*dest[8].(*bool) = true
			*dest[9].(*bool) = false
			*dest[10].(*[]string) = []string{"org", "project", "agent"}
			*dest[11].(*[]string) = []string{}
			*dest[12].(*[]string) = []string{}
			*dest[13].(**string) = nil
			*dest[14].(**int64) = nil
			*dest[15].(**string) = nil
			*dest[16].(**uuid.UUID) = nil
			*dest[17].(**int32) = nil
			*dest[18].(**time.Time) = nil
			*dest[19].(**uuid.UUID) = nil
			*dest[20].(*string) = "system"
			*dest[21].(*uuid.UUID) = uuid.Nil
			*dest[22].(*time.Time) = createdAt
			*dest[23].(*time.Time) = updatedAt
			return nil
		}})
		if err != nil {
			t.Fatalf("scanAgent returned error: %v", err)
		}

		if agent.DefaultModelProfileID != nil {
			t.Fatalf("DefaultModelProfileID = %v, want nil", agent.DefaultModelProfileID)
		}
		if agent.BudgetCapTokens != nil {
			t.Fatalf("BudgetCapTokens = %v, want nil", agent.BudgetCapTokens)
		}
		if agent.BudgetPeriod != nil {
			t.Fatalf("BudgetPeriod = %v, want nil", agent.BudgetPeriod)
		}
		if agent.TempProjectID != nil {
			t.Fatalf("TempProjectID = %v, want nil", agent.TempProjectID)
		}
		if agent.TempTTLSeconds != nil {
			t.Fatalf("TempTTLSeconds = %v, want nil", agent.TempTTLSeconds)
		}
		if agent.TempExpiresAt != nil {
			t.Fatalf("TempExpiresAt = %v, want nil", agent.TempExpiresAt)
		}
		if agent.PromotedToAgentID != nil {
			t.Fatalf("PromotedToAgentID = %v, want nil", agent.PromotedToAgentID)
		}
	})
}
