package memory

import (
	"context"
	"encoding/json"
	"fmt"
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
		return fmt.Errorf("ellie retrieval quality store is not configured")
	}
	projectID := optionalUUIDPtr(signal.ProjectID)
	roomID := optionalUUIDPtr(signal.RoomID)
	metadata := map[string]any{
		"injected_item_ids":   nonNilStrings(signal.InjectedItemIDs),
		"referenced_item_ids": nonNilStrings(signal.ReferencedItemIDs),
		"missed_item_ids":     nonNilStrings(signal.MissedItemIDs),
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

func nonNilStrings(value []string) []string {
	if value == nil {
		return []string{}
	}
	return append([]string(nil), value...)
}
