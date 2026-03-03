package model

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestDailyRollupJobRunUpsertsAgentAndProjectRowsIdempotently(t *testing.T) {
	orgID := uuid.New()
	agentA := uuid.New()
	agentB := uuid.New()
	projectID := uuid.New()
	rollupDay := time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC)

	source := &fakeRollupAggregationSource{
		orgID: orgID,
		rowsByDimension: map[string][][]any{
			"agent_id": {
				{orgID, &agentA, "claude-opus-4-6", json.RawMessage(`{}`), 3, int64(100), int64(50)},
				{orgID, &agentB, "claude-haiku-3-5", json.RawMessage(`{}`), 2, int64(40), int64(20)},
			},
			"project_id": {
				{orgID, &projectID, "claude-opus-4-6", json.RawMessage(`{}`), 3, int64(100), int64(50)},
				{orgID, &projectID, "claude-haiku-3-5", json.RawMessage(`{}`), 2, int64(40), int64(20)},
			},
		},
	}
	writer := &fakeModelUsageRollupWriter{store: map[string]repo.ModelUsageRollup{}}
	events := &fakeDomainEventWriter{}
	job := &DailyRollupJob{
		source:  source,
		rollups: writer,
		events:  events,
		now:     func() time.Time { return rollupDay.Add(24 * time.Hour) },
	}

	if err := job.Run(context.Background(), jobqueue.Job{}); err != nil {
		t.Fatalf("Run first pass: %v", err)
	}
	if err := job.Run(context.Background(), jobqueue.Job{}); err != nil {
		t.Fatalf("Run second pass: %v", err)
	}

	if len(writer.store) != 3 {
		t.Fatalf("rollup row count = %d, want 3", len(writer.store))
	}
	assertRollupTotals(t, writer, orgID, "agent", &agentA, 100, 50, 525000000)
	assertRollupTotals(t, writer, orgID, "agent", &agentB, 40, 20, 11200000)
	assertRollupTotals(t, writer, orgID, "project", &projectID, 140, 70, 536200000)

	if len(events.events) == 0 {
		t.Fatal("expected model.usage_rollup.completed domain event")
	}
	latest := events.events[len(events.events)-1]
	if latest.EventType != "model.usage_rollup.completed" {
		t.Fatalf("event_type = %q, want model.usage_rollup.completed", latest.EventType)
	}
	var payload map[string]any
	if err := json.Unmarshal(latest.Payload, &payload); err != nil {
		t.Fatalf("decode event payload: %v", err)
	}
	if payload["org_id"] != orgID.String() {
		t.Fatalf("payload.org_id = %v, want %s", payload["org_id"], orgID)
	}
	if payload["row_count"] != float64(3) {
		t.Fatalf("payload.row_count = %v, want 3", payload["row_count"])
	}
}

func assertRollupTotals(t *testing.T, writer *fakeModelUsageRollupWriter, orgID uuid.UUID, rollupType string, rollupID *uuid.UUID, input, output, cost int64) {
	t.Helper()
	item, ok := writer.store[rollupStoreKey(orgID, rollupType, rollupID)]
	if !ok {
		t.Fatalf("missing rollup row for type=%s id=%v", rollupType, rollupID)
	}
	if item.TotalInputTokens != input {
		t.Fatalf("%s input_tokens = %d, want %d", rollupType, item.TotalInputTokens, input)
	}
	if item.TotalOutputTokens != output {
		t.Fatalf("%s output_tokens = %d, want %d", rollupType, item.TotalOutputTokens, output)
	}
	if item.TotalCostMicrocents != cost {
		t.Fatalf("%s total_cost_microcents = %d, want %d", rollupType, item.TotalCostMicrocents, cost)
	}
}

type fakeRollupAggregationSource struct {
	orgID           uuid.UUID
	rowsByDimension map[string][][]any
}

func (f *fakeRollupAggregationSource) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	for dimension, rows := range f.rowsByDimension {
		if strings.Contains(sql, dimension) {
			return &fakeRollupRows{rows: rows}, nil
		}
	}
	return &fakeRollupRows{rows: nil}, nil
}

type fakeRollupRows struct {
	idx  int
	rows [][]any
}

func (f *fakeRollupRows) Close()                                       {}
func (f *fakeRollupRows) Err() error                                   { return nil }
func (f *fakeRollupRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (f *fakeRollupRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (f *fakeRollupRows) Values() ([]any, error)                       { return nil, nil }
func (f *fakeRollupRows) RawValues() [][]byte                          { return nil }
func (f *fakeRollupRows) Conn() *pgx.Conn                              { return nil }

func (f *fakeRollupRows) Next() bool {
	if f.idx >= len(f.rows) {
		return false
	}
	f.idx++
	return true
}

func (f *fakeRollupRows) Scan(dest ...any) error {
	row := f.rows[f.idx-1]
	for i := range dest {
		switch d := dest[i].(type) {
		case *uuid.UUID:
			*d = row[i].(uuid.UUID)
		case **uuid.UUID:
			if row[i] == nil {
				*d = nil
				continue
			}
			value := *(row[i].(*uuid.UUID))
			*d = &value
		case *int:
			*d = row[i].(int)
		case *int64:
			*d = row[i].(int64)
		case *string:
			*d = row[i].(string)
		case *json.RawMessage:
			raw := row[i].(json.RawMessage)
			*d = append((*d)[:0], raw...)
		default:
			return nil
		}
	}
	return nil
}

func TestEstimateCostMicrocents(t *testing.T) {
	t.Parallel()

	got := estimateCostMicrocents(500, 200, 2.0, 4.0)
	if got != 1800000 {
		t.Fatalf("estimateCostMicrocents = %d, want 1800000", got)
	}
}

func TestResolveRollupModelCostsUsesModelFallback(t *testing.T) {
	t.Parallel()

	input, output := resolveRollupModelCosts("claude-haiku-3-5", json.RawMessage(`{}`))
	if input != 80 || output != 400 {
		t.Fatalf("fallback costs = (%v,%v), want (80,400)", input, output)
	}
}

type fakeModelUsageRollupWriter struct {
	store map[string]repo.ModelUsageRollup
}

func (f *fakeModelUsageRollupWriter) Upsert(_ context.Context, rollup repo.ModelUsageRollup) (repo.ModelUsageRollup, error) {
	f.store[rollupStoreKey(rollup.OrganizationID, rollup.RollupType, rollup.RollupID)] = rollup
	return rollup, nil
}

func rollupStoreKey(orgID uuid.UUID, rollupType string, rollupID *uuid.UUID) string {
	id := "nil"
	if rollupID != nil {
		id = rollupID.String()
	}
	return orgID.String() + "|" + rollupType + "|" + id
}

type fakeDomainEventWriter struct {
	events []eventbus.DomainEvent
}

func (f *fakeDomainEventWriter) Publish(_ context.Context, _ pgx.Tx, event eventbus.DomainEvent) error {
	f.events = append(f.events, event)
	return nil
}
