package clock

import (
	"sync"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/config"
)

type Clock interface {
	Now() time.Time
}

type Real struct{}

func (Real) Now() time.Time {
	return time.Now()
}

type Fake struct {
	mu  sync.RWMutex
	now time.Time
}

func NewFake(now time.Time) *Fake {
	return &Fake{now: now}
}

func (f *Fake) Now() time.Time {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.now
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func New(mode config.Mode) Clock {
	if mode == config.ModeTest {
		return NewFake(time.Now())
	}

	return Real{}
}
