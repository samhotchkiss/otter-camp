package gateway

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

const (
	defaultGlobalSlots        = 50
	defaultPerConnectionSlots = 10
)

type ConcurrencyManager struct {
	mu sync.Mutex
	cv *sync.Cond

	globalLimit      int
	defaultConnLimit int
	globalActive     int
	connActive       map[uuid.UUID]int
	connLimit        map[uuid.UUID]int
}

func NewConcurrencyManager(globalLimit int, perConnectionLimit map[uuid.UUID]int) *ConcurrencyManager {
	if globalLimit <= 0 {
		globalLimit = defaultGlobalSlots
	}

	manager := &ConcurrencyManager{
		globalLimit:      globalLimit,
		defaultConnLimit: defaultPerConnectionSlots,
		connActive:       make(map[uuid.UUID]int),
		connLimit:        make(map[uuid.UUID]int, len(perConnectionLimit)),
	}
	for key, value := range perConnectionLimit {
		if value > 0 {
			manager.connLimit[key] = value
		}
	}
	if manager.defaultConnLimit > manager.globalLimit {
		manager.defaultConnLimit = manager.globalLimit
	}

	manager.cv = sync.NewCond(&manager.mu)
	return manager
}

func (m *ConcurrencyManager) Reserve(ctx context.Context, connectionID uuid.UUID) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.cv.Broadcast()
			m.mu.Unlock()
		case <-done:
		}
	}()
	defer close(done)

	m.mu.Lock()
	defer m.mu.Unlock()

	for {
		if release, ok := m.tryReserveLocked(connectionID); ok {
			return release, nil
		}

		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m.cv.Wait()
	}
}

func (m *ConcurrencyManager) TryReserve(connectionID uuid.UUID) (func(), bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tryReserveLocked(connectionID)
}

func (m *ConcurrencyManager) SetConnectionLimit(connectionID uuid.UUID, maxConcurrent int) {
	if maxConcurrent <= 0 {
		return
	}

	m.mu.Lock()
	m.connLimit[connectionID] = maxConcurrent
	m.cv.Broadcast()
	m.mu.Unlock()
}

func (m *ConcurrencyManager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.globalActive
}

func (m *ConcurrencyManager) IsAtGlobalCapacity() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.globalActive >= m.globalLimit
}

func (m *ConcurrencyManager) GlobalLimit() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.globalLimit
}

func (m *ConcurrencyManager) tryReserveLocked(connectionID uuid.UUID) (func(), bool) {
	if m.globalActive >= m.globalLimit {
		return nil, false
	}
	limit := m.connectionLimitLocked(connectionID)
	if m.connActive[connectionID] >= limit {
		return nil, false
	}

	m.globalActive++
	m.connActive[connectionID]++

	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()

			if m.globalActive > 0 {
				m.globalActive--
			}
			if active := m.connActive[connectionID]; active <= 1 {
				delete(m.connActive, connectionID)
			} else {
				m.connActive[connectionID] = active - 1
			}
			m.cv.Broadcast()
		})
	}, true
}

func (m *ConcurrencyManager) connectionLimitLocked(connectionID uuid.UUID) int {
	if limit, ok := m.connLimit[connectionID]; ok && limit > 0 {
		return limit
	}
	return m.defaultConnLimit
}
