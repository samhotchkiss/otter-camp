package memory

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeEllieRetrievalQualityRecorder struct {
	called    int
	lastInput store.CreateEllieRetrievalQualityEventInput
	err       error
}

func (f *fakeEllieRetrievalQualityRecorder) RecordEvent(
	_ context.Context,
	input store.CreateEllieRetrievalQualityEventInput,
) (*store.EllieRetrievalQualityEvent, error) {
	f.called += 1
	f.lastInput = input
	if f.err != nil {
		return nil, f.err
	}
	return &store.EllieRetrievalQualityEvent{ID: 1}, nil
}

func TestEllieRetrievalQualityStoreSinkRecordNilStoreReturnsError(t *testing.T) {
	var sink *EllieRetrievalQualityStoreSink
	err := sink.Record(context.Background(), EllieRetrievalQualitySignal{
		OrgID: "00000000-0000-0000-0000-000000000001",
		Query: "test query",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "not configured")
}

func TestEllieRetrievalQualityStoreSinkRecordMapsSignalToStoreInput(t *testing.T) {
	recorder := &fakeEllieRetrievalQualityRecorder{}
	sink := NewEllieRetrievalQualityStoreSink(recorder)
	signal := EllieRetrievalQualitySignal{
		OrgID:             " 00000000-0000-0000-0000-000000000001 ",
		ProjectID:         " 00000000-0000-0000-0000-000000000002 ",
		RoomID:            " 00000000-0000-0000-0000-000000000003 ",
		Query:             "  retrieval query  ",
		TierUsed:          2,
		InjectedCount:     4,
		ReferencedCount:   3,
		MissedCount:       1,
		NoInformation:     false,
		InjectedItemIDs:   []string{"mem-1", "mem-2"},
		ReferencedItemIDs: []string{"mem-1"},
		MissedItemIDs:     []string{"mem-3"},
	}

	err := sink.Record(context.Background(), signal)
	require.NoError(t, err)
	require.Equal(t, 1, recorder.called)
	require.Equal(t, "00000000-0000-0000-0000-000000000001", recorder.lastInput.OrgID)
	require.NotNil(t, recorder.lastInput.ProjectID)
	require.Equal(t, "00000000-0000-0000-0000-000000000002", *recorder.lastInput.ProjectID)
	require.NotNil(t, recorder.lastInput.RoomID)
	require.Equal(t, "00000000-0000-0000-0000-000000000003", *recorder.lastInput.RoomID)
	require.Equal(t, "retrieval query", recorder.lastInput.Query)
	require.Equal(t, 2, recorder.lastInput.TierUsed)
	require.Equal(t, 4, recorder.lastInput.InjectedCount)
	require.Equal(t, 3, recorder.lastInput.ReferencedCount)
	require.Equal(t, 1, recorder.lastInput.MissedCount)
	require.False(t, recorder.lastInput.NoInformation)

	var metadata map[string][]string
	err = json.Unmarshal(recorder.lastInput.Metadata, &metadata)
	require.NoError(t, err)
	require.Equal(t, []string{"mem-1", "mem-2"}, metadata["injected_item_ids"])
	require.Equal(t, []string{"mem-1"}, metadata["referenced_item_ids"])
	require.Equal(t, []string{"mem-3"}, metadata["missed_item_ids"])
}

func TestEllieRetrievalQualityStoreSinkRecordPropagatesStoreError(t *testing.T) {
	recorder := &fakeEllieRetrievalQualityRecorder{err: errors.New("write failed")}
	sink := NewEllieRetrievalQualityStoreSink(recorder)

	err := sink.Record(context.Background(), EllieRetrievalQualitySignal{
		OrgID: "00000000-0000-0000-0000-000000000001",
		Query: "query",
	})
	require.ErrorContains(t, err, "write failed")
}

func TestOptionalUUIDPtr(t *testing.T) {
	require.Nil(t, optionalUUIDPtr(""))
	require.Nil(t, optionalUUIDPtr("   "))

	value := optionalUUIDPtr(" 00000000-0000-0000-0000-000000000001 ")
	require.NotNil(t, value)
	require.Equal(t, "00000000-0000-0000-0000-000000000001", *value)
}
