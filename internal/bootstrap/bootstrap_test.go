package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestRegisterStepOverridesStubAndPreservesOrder(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var order []string

	b := &Bootstrapper{
		logger: logger,
		steps: []BootstrapStep{
			{Number: 1, Name: "run-migrations", Fn: func(context.Context, *BootstrapState) error { order = append(order, "run-migrations"); return nil }},
			{Number: 2, Name: "create-agents", Fn: func(context.Context, *BootstrapState) error { order = append(order, "old-agents"); return nil }},
			{Number: 3, Name: "record-bootstrap-audit-event", Fn: func(context.Context, *BootstrapState) error {
				order = append(order, "record-bootstrap-audit-event")
				return nil
			}},
		},
	}

	b.RegisterStep("create-agents", func(context.Context, *BootstrapState) error {
		order = append(order, "new-agents")
		return nil
	})

	if err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := []string{"run-migrations", "new-agents", "record-bootstrap-audit-event"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("step order = %#v, want %#v", order, want)
	}
}

func TestRunRecoversStepPanic(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := &Bootstrapper{
		logger: logger,
		steps: []BootstrapStep{
			{
				Number: 1,
				Name:   "panic-step",
				Fn: func(context.Context, *BootstrapState) error {
					panic("boom")
				},
			},
		},
	}

	err := b.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil, want panic recovery error")
	}
	if !strings.Contains(err.Error(), "panic recovered") {
		t.Fatalf("Run() error = %q, want panic recovered message", err)
	}
}

func TestOrganizationBySlug(t *testing.T) {
	t.Parallel()

	org := repo.Organization{ID: uuid.New(), Slug: "default"}
	tests := []struct {
		name      string
		lookupErr error
		org       repo.Organization
		wantFound bool
		wantErr   bool
	}{
		{
			name:      "found",
			org:       org,
			wantFound: true,
		},
		{
			name:      "not found",
			lookupErr: repo.ErrNotFound,
			wantFound: false,
		},
		{
			name:      "lookup error",
			lookupErr: errors.New("boom"),
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, found, err := organizationBySlug(context.Background(), fakeOrgLookup{
				org: tc.org,
				err: tc.lookupErr,
			}, "default")
			if tc.wantErr {
				if err == nil {
					t.Fatal("organizationBySlug() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("organizationBySlug() error = %v", err)
			}
			if found != tc.wantFound {
				t.Fatalf("organizationBySlug() found = %t, want %t", found, tc.wantFound)
			}
		})
	}
}

func TestAdminUserExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		users   []repo.HumanUser
		listErr error
		want    bool
		wantErr bool
	}{
		{
			name:  "admin present",
			users: []repo.HumanUser{{Role: "member"}, {Role: "admin"}},
			want:  true,
		},
		{
			name:  "admin absent",
			users: []repo.HumanUser{{Role: "member"}},
			want:  false,
		},
		{
			name:    "list error",
			listErr: errors.New("boom"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := adminUserExists(context.Background(), fakeUserLister{
				users: tc.users,
				err:   tc.listErr,
			}, uuid.New())
			if tc.wantErr {
				if err == nil {
					t.Fatal("adminUserExists() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("adminUserExists() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("adminUserExists() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestBootstrapAuditEventExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		queryErr   error
		queryValue int
		want       bool
		wantErr    bool
	}{
		{
			name:       "event exists",
			queryValue: 1,
			want:       true,
		},
		{
			name:     "event missing",
			queryErr: pgx.ErrNoRows,
			want:     false,
		},
		{
			name:     "query error",
			queryErr: errors.New("boom"),
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := bootstrapAuditEventExists(context.Background(), fakeQueryRower{
				row: fakeRow{
					value: tc.queryValue,
					err:   tc.queryErr,
				},
			}, uuid.New())
			if tc.wantErr {
				if err == nil {
					t.Fatal("bootstrapAuditEventExists() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("bootstrapAuditEventExists() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("bootstrapAuditEventExists() = %t, want %t", got, tc.want)
			}
		})
	}
}

type fakeOrgLookup struct {
	org repo.Organization
	err error
}

func (f fakeOrgLookup) GetBySlug(context.Context, string) (repo.Organization, error) {
	if f.err != nil {
		return repo.Organization{}, f.err
	}
	return f.org, nil
}

type fakeUserLister struct {
	users []repo.HumanUser
	err   error
}

func (f fakeUserLister) List(context.Context, uuid.UUID) ([]repo.HumanUser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.users, nil
}

type fakeQueryRower struct {
	row fakeRow
}

func (f fakeQueryRower) QueryRow(context.Context, string, ...any) pgx.Row {
	return f.row
}

type fakeRow struct {
	value int
	err   error
}

func (f fakeRow) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	if len(dest) != 1 {
		return errors.New("unexpected destination count")
	}
	ptr, ok := dest[0].(*int)
	if !ok {
		return errors.New("destination is not *int")
	}
	*ptr = f.value
	return nil
}
