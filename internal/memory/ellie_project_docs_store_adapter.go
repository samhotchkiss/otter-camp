package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type EllieProjectDocsStoreAdapter struct {
	Store *store.EllieProjectDocsStore
}

func (a *EllieProjectDocsStoreAdapter) ListActiveProjectDocs(
	ctx context.Context,
	orgID, projectID string,
) ([]EllieProjectDocStoreRecord, error) {
	if a == nil || a.Store == nil {
		return nil, fmt.Errorf("project docs store adapter is not configured")
	}
	docs, err := a.Store.ListActiveProjectDocs(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]EllieProjectDocStoreRecord, 0, len(docs))
	for _, doc := range docs {
		path := strings.TrimSpace(doc.FilePath)
		hash := strings.TrimSpace(doc.ContentHash)
		if path == "" || hash == "" {
			continue
		}
		out = append(out, EllieProjectDocStoreRecord{
			FilePath:    path,
			ContentHash: hash,
		})
	}
	return out, nil
}

func (a *EllieProjectDocsStoreAdapter) UpsertProjectDoc(
	ctx context.Context,
	input EllieProjectDocStoreUpsertInput,
) (string, error) {
	if a == nil || a.Store == nil {
		return "", fmt.Errorf("project docs store adapter is not configured")
	}
	return a.Store.UpsertProjectDoc(ctx, store.UpsertEllieProjectDocInput{
		OrgID:            strings.TrimSpace(input.OrgID),
		ProjectID:        strings.TrimSpace(input.ProjectID),
		FilePath:         strings.TrimSpace(input.FilePath),
		Title:            strings.TrimSpace(input.Title),
		Summary:          strings.TrimSpace(input.Summary),
		SummaryEmbedding: input.SummaryEmbedding,
		ContentHash:      strings.TrimSpace(input.ContentHash),
	})
}

func (a *EllieProjectDocsStoreAdapter) MarkProjectDocsInactiveExcept(
	ctx context.Context,
	orgID, projectID string,
	keepPaths []string,
) (int, error) {
	if a == nil || a.Store == nil {
		return 0, fmt.Errorf("project docs store adapter is not configured")
	}
	return a.Store.MarkProjectDocsInactiveExcept(ctx, orgID, projectID, keepPaths)
}

