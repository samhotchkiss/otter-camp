package api

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type fakeOpenClawMigrationProgressStore struct {
	rowsByKey map[string]store.MigrationProgress

	listByOrgCalls []string
	getByTypeCalls []string

	startPhaseInputs []store.StartMigrationProgressInput
	setStatusInputs  []store.SetMigrationProgressStatusInput

	updateStatusByOrgInputs []fakeOpenClawUpdateStatusByOrgCall
}

type fakeOpenClawUpdateStatusByOrgCall struct {
	OrgID      string
	FromStatus store.MigrationProgressStatus
	ToStatus   store.MigrationProgressStatus
}

func newFakeOpenClawMigrationProgressStore(
	rowsByOrg map[string][]store.MigrationProgress,
) *fakeOpenClawMigrationProgressStore {
	out := &fakeOpenClawMigrationProgressStore{
		rowsByKey: make(map[string]store.MigrationProgress),
	}
	for orgID, rows := range rowsByOrg {
		for _, row := range rows {
			key := fakeOpenClawMigrationProgressKey(orgID, row.MigrationType)
			cloned := row
			cloned.OrgID = orgID
			out.rowsByKey[key] = cloned
		}
	}
	return out
}

func fakeOpenClawMigrationProgressKey(orgID, migrationType string) string {
	return strings.TrimSpace(orgID) + "|" + strings.TrimSpace(migrationType)
}

func (f *fakeOpenClawMigrationProgressStore) ListByOrg(_ context.Context, orgID string) ([]store.MigrationProgress, error) {
	f.listByOrgCalls = append(f.listByOrgCalls, orgID)

	rows := make([]store.MigrationProgress, 0)
	for _, row := range f.rowsByKey {
		if strings.TrimSpace(row.OrgID) != strings.TrimSpace(orgID) {
			continue
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].MigrationType < rows[j].MigrationType
	})
	return rows, nil
}

func (f *fakeOpenClawMigrationProgressStore) GetByType(
	_ context.Context,
	orgID,
	migrationType string,
) (*store.MigrationProgress, error) {
	key := fakeOpenClawMigrationProgressKey(orgID, migrationType)
	f.getByTypeCalls = append(f.getByTypeCalls, key)

	row, ok := f.rowsByKey[key]
	if !ok {
		return nil, nil
	}
	cloned := row
	return &cloned, nil
}

func (f *fakeOpenClawMigrationProgressStore) StartPhase(
	_ context.Context,
	input store.StartMigrationProgressInput,
) (*store.MigrationProgress, error) {
	f.startPhaseInputs = append(f.startPhaseInputs, input)

	row := store.MigrationProgress{
		OrgID:         input.OrgID,
		MigrationType: strings.TrimSpace(input.MigrationType),
		Status:        store.MigrationProgressStatusRunning,
		TotalItems:    input.TotalItems,
	}
	if input.CurrentLabel != nil {
		row.CurrentLabel = strings.TrimSpace(*input.CurrentLabel)
	}
	f.rowsByKey[fakeOpenClawMigrationProgressKey(input.OrgID, input.MigrationType)] = row
	cloned := row
	return &cloned, nil
}

func (f *fakeOpenClawMigrationProgressStore) SetStatus(
	_ context.Context,
	input store.SetMigrationProgressStatusInput,
) (*store.MigrationProgress, error) {
	f.setStatusInputs = append(f.setStatusInputs, input)

	key := fakeOpenClawMigrationProgressKey(input.OrgID, input.MigrationType)
	row, ok := f.rowsByKey[key]
	if !ok {
		return nil, fmt.Errorf("migration progress phase not found: %w", store.ErrNotFound)
	}

	row.Status = input.Status
	if input.CurrentLabel != nil {
		row.CurrentLabel = strings.TrimSpace(*input.CurrentLabel)
	}
	if input.Error != nil {
		errMsg := strings.TrimSpace(*input.Error)
		row.Error = &errMsg
	}
	f.rowsByKey[key] = row
	cloned := row
	return &cloned, nil
}

func (f *fakeOpenClawMigrationProgressStore) UpdateStatusByOrg(
	_ context.Context,
	orgID string,
	fromStatus store.MigrationProgressStatus,
	toStatus store.MigrationProgressStatus,
) (int, error) {
	f.updateStatusByOrgInputs = append(f.updateStatusByOrgInputs, fakeOpenClawUpdateStatusByOrgCall{
		OrgID:      orgID,
		FromStatus: fromStatus,
		ToStatus:   toStatus,
	})

	updated := 0
	for key, row := range f.rowsByKey {
		if strings.TrimSpace(row.OrgID) != strings.TrimSpace(orgID) {
			continue
		}
		if row.Status != fromStatus {
			continue
		}
		row.Status = toStatus
		f.rowsByKey[key] = row
		updated++
	}
	return updated, nil
}

func (f *fakeOpenClawMigrationProgressStore) DeleteByOrgAndTypes(
	_ context.Context,
	orgID string,
	migrationTypes []string,
) (int, error) {
	orgID = strings.TrimSpace(orgID)
	types := make(map[string]struct{}, len(migrationTypes))
	for _, t := range migrationTypes {
		trimmed := strings.TrimSpace(t)
		if trimmed != "" {
			types[trimmed] = struct{}{}
		}
	}
	deleted := 0
	for key, row := range f.rowsByKey {
		if strings.TrimSpace(row.OrgID) != orgID {
			continue
		}
		if _, ok := types[strings.TrimSpace(row.MigrationType)]; !ok {
			continue
		}
		delete(f.rowsByKey, key)
		deleted++
	}
	return deleted, nil
}
