package clock

import (
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/config"
)

func TestRealNowNearCurrentTime(t *testing.T) {
	r := Real{}
	before := time.Now()
	now := r.Now()
	after := time.Now()

	if now.Before(before) || now.After(after) {
		t.Fatalf("real now %v outside expected range [%v, %v]", now, before, after)
	}
}

func TestFakeClockFrozenAndAdvance(t *testing.T) {
	start := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	f := NewFake(start)

	if got := f.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}

	f.Advance(90 * time.Second)
	want := start.Add(90 * time.Second)
	if got := f.Now(); !got.Equal(want) {
		t.Fatalf("Now() after Advance = %v, want %v", got, want)
	}
}

func TestNewSelectsClockByMode(t *testing.T) {
	if _, ok := New(config.ModeTest).(*Fake); !ok {
		t.Fatal("New(test) did not return *Fake")
	}

	if _, ok := New(config.ModeProduction).(Real); !ok {
		t.Fatal("New(production) did not return Real")
	}
}
