package migrate

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
)

func TestLoadMigrationsOrdersByVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"0002_second.sql": {Data: []byte("select 2;")},
		"0001_first.sql":  {Data: []byte("select 1;")},
	}

	migrations, err := LoadMigrations(fsys)
	if err != nil {
		t.Fatalf("LoadMigrations returned error: %v", err)
	}

	if len(migrations) != 2 {
		t.Fatalf("len(migrations) = %d, want 2", len(migrations))
	}

	if migrations[0].Version != 1 || migrations[1].Version != 2 {
		t.Fatalf("unexpected order: %+v", migrations)
	}
}

func TestPlanPendingUsesAppliedVersionProvider(t *testing.T) {
	provider := mockAppliedProvider{
		applied: map[int]struct{}{
			1: {},
		},
	}

	all := []Migration{{Version: 1}, {Version: 2}, {Version: 3}}
	pending, err := PlanPending(context.Background(), provider, all)
	if err != nil {
		t.Fatalf("PlanPending returned error: %v", err)
	}

	if len(pending) != 2 || pending[0].Version != 2 || pending[1].Version != 3 {
		t.Fatalf("unexpected pending migrations: %+v", pending)
	}
}

func TestPlanPendingPropagatesProviderError(t *testing.T) {
	expected := errors.New("boom")
	_, err := PlanPending(context.Background(), mockAppliedProvider{err: expected}, nil)
	if !errors.Is(err, expected) {
		t.Fatalf("PlanPending error = %v, want %v", err, expected)
	}
}

type mockAppliedProvider struct {
	applied map[int]struct{}
	err     error
}

func (m mockAppliedProvider) AppliedVersions(context.Context) (map[int]struct{}, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.applied, nil
}
