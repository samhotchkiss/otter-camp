package memory

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type EllieRetrievalQualityEventRecorder interface {
	RecordEvent(ctx context.Context, input store.CreateEllieRetrievalQualityEventInput) (*store.EllieRetrievalQualityEvent, error)
}

type EllieRetrievalQualityStoreSink struct {
	Store EllieRetrievalQualityEventRecorder
}

func NewEllieRetrievalQualityStoreSink(store EllieRetrievalQualityEventRecorder) *EllieRetrievalQualityStoreSink {
	return &EllieRetrievalQualityStoreSink{Store: store}
}

func (s *EllieRetrievalQualityStoreSink) Record(ctx context.Context, signal EllieRetrievalQualitySignal) error {
	if s == nil || s.Store == nil {
		return nil
	}
	projectID := optionalUUIDPtr(signal.ProjectID)
	roomID := optionalUUIDPtr(signal.RoomID)
	metadata := map[string]any{
		"injected_item_ids":   append([]string(nil), signal.InjectedItemIDs...),
		"referenced_item_ids": append([]string(nil), signal.ReferencedItemIDs...),
		"missed_item_ids":     append([]string(nil), signal.MissedItemIDs...),
	}
	encodedMetadata, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = s.Store.RecordEvent(ctx, store.CreateEllieRetrievalQualityEventInput{
		OrgID:           strings.TrimSpace(signal.OrgID),
		ProjectID:       projectID,
		RoomID:          roomID,
		Query:           strings.TrimSpace(signal.Query),
		TierUsed:        signal.TierUsed,
		InjectedCount:   signal.InjectedCount,
		ReferencedCount: signal.ReferencedCount,
		MissedCount:     signal.MissedCount,
		NoInformation:   signal.NoInformation,
		Metadata:        encodedMetadata,
	})
	return err
}

func optionalUUIDPtr(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
