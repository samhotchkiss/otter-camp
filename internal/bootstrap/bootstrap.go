package bootstrap

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

type State struct {
	OrganizationID uuid.UUID
}

type StepFunc func(ctx context.Context, state *State) error

type step struct {
	name string
	fn   StepFunc
}

type Bootstrapper struct {
	mu    sync.RWMutex
	steps []step
}

func NewBootstrapper() *Bootstrapper {
	return &Bootstrapper{
		steps: []step{
			{
				name: "create-starter-trio",
				fn: func(context.Context, *State) error {
					return nil
				},
			},
		},
	}
}

func (b *Bootstrapper) RegisterStep(name string, fn StepFunc) {
	if b == nil || name == "" || fn == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	for i := range b.steps {
		if b.steps[i].name == name {
			b.steps[i].fn = fn
			return
		}
	}

	b.steps = append(b.steps, step{name: name, fn: fn})
}

func (b *Bootstrapper) Run(ctx context.Context, state *State) error {
	if b == nil {
		return fmt.Errorf("bootstrapper is nil")
	}
	if state == nil {
		state = &State{}
	}

	steps := b.snapshot()
	for _, s := range steps {
		if err := runStep(ctx, s.fn, state); err != nil {
			return fmt.Errorf("bootstrap step %q: %w", s.name, err)
		}
	}

	return nil
}

func (b *Bootstrapper) StepNames() []string {
	if b == nil {
		return nil
	}

	steps := b.snapshot()
	names := make([]string, 0, len(steps))
	for _, s := range steps {
		names = append(names, s.name)
	}
	return names
}

func (b *Bootstrapper) snapshot() []step {
	b.mu.RLock()
	defer b.mu.RUnlock()

	steps := make([]step, len(b.steps))
	copy(steps, b.steps)
	return steps
}

func runStep(ctx context.Context, fn StepFunc, state *State) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return fn(ctx, state)
}
