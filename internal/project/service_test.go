package project

import (
	"errors"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/scheduling"
)

func TestValidateSlug(t *testing.T) {
	cases := []struct {
		name    string
		slug    string
		wantErr bool
	}{
		{name: "valid simple", slug: "alpha", wantErr: false},
		{name: "valid dashed", slug: "project-123", wantErr: false},
		{name: "invalid uppercase", slug: "Project", wantErr: true},
		{name: "invalid space", slug: "my project", wantErr: true},
		{name: "invalid leading hyphen", slug: "-alpha", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateSlug(tc.slug)
			if tc.wantErr && !errors.Is(err, ErrInvalidSlug) {
				t.Fatalf("validateSlug(%q) err = %v, want ErrInvalidSlug", tc.slug, err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateSlug(%q) err = %v, want nil", tc.slug, err)
			}
		})
	}
}

func TestComputeNextFireAtValidation(t *testing.T) {
	now := time.Date(2026, time.February, 24, 8, 15, 0, 0, time.UTC)
	svc := &service{
		clock:      clock.NewFake(now),
		cronParser: scheduling.NewCronParser(),
	}

	next, err := svc.computeNextFireAt("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("computeNextFireAt valid expression: %v", err)
	}
	want := time.Date(2026, time.February, 24, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next_fire_at = %s, want %s", next, want)
	}

	_, err = svc.computeNextFireAt("not-a-cron")
	if !errors.Is(err, ErrInvalidCronExpression) {
		t.Fatalf("invalid expression err = %v, want ErrInvalidCronExpression", err)
	}
}
