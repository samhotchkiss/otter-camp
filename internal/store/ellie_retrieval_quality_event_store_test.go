package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEllieRetrievalQualityEventStoreRecordAndAggregate(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-quality-event-org")
	projectID := createTestProject(t, db, orgID, "Ellie Quality Signals")
	eventStore := NewEllieRetrievalQualityEventStore(db)

	_, err := eventStore.RecordEvent(context.Background(), CreateEllieRetrievalQualityEventInput{
		OrgID:           orgID,
		ProjectID:       &projectID,
		RoomID:          nil,
		Query:           "database decisions",
		TierUsed:        2,
		InjectedCount:   4,
		ReferencedCount: 3,
		MissedCount:     1,
		NoInformation:   false,
	})
	require.NoError(t, err)

	_, err = eventStore.RecordEvent(context.Background(), CreateEllieRetrievalQualityEventInput{
		OrgID:           orgID,
		ProjectID:       &projectID,
		RoomID:          nil,
		Query:           "deployment rollbacks",
		TierUsed:        3,
		InjectedCount:   2,
		ReferencedCount: 1,
		MissedCount:     2,
		NoInformation:   false,
	})
	require.NoError(t, err)

	projectAgg, err := eventStore.AggregateByProject(context.Background(), orgID, projectID)
	require.NoError(t, err)
	require.Equal(t, 2, projectAgg.EventCount)
	require.Equal(t, 6, projectAgg.TotalInjected)
	require.Equal(t, 4, projectAgg.TotalReferenced)
	require.Equal(t, 3, projectAgg.TotalMissed)
	require.InDelta(t, 4.0/6.0, projectAgg.Precision, 0.0001)
	require.InDelta(t, 4.0/7.0, projectAgg.Recall, 0.0001)

	orgAgg, err := eventStore.AggregateOrgWide(context.Background(), orgID)
	require.NoError(t, err)
	require.Equal(t, projectAgg.EventCount, orgAgg.EventCount)
	require.Equal(t, projectAgg.TotalInjected, orgAgg.TotalInjected)
	require.Equal(t, projectAgg.TotalReferenced, orgAgg.TotalReferenced)
	require.Equal(t, projectAgg.TotalMissed, orgAgg.TotalMissed)
}
