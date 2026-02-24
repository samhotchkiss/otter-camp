package memory

import (
	"sync"

	"github.com/google/uuid"
)

type cooldownTracker struct {
	mu          sync.Mutex
	lastSeen    map[uuid.UUID]map[uuid.UUID]int
	sessionTurn map[uuid.UUID]int
}

func newCooldownTracker() *cooldownTracker {
	return &cooldownTracker{
		lastSeen:    make(map[uuid.UUID]map[uuid.UUID]int),
		sessionTurn: make(map[uuid.UUID]int),
	}
}

func (t *cooldownTracker) turn(sessionID uuid.UUID, requested int) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	if requested > 0 {
		if requested > t.sessionTurn[sessionID] {
			t.sessionTurn[sessionID] = requested
		}
		return requested
	}
	t.sessionTurn[sessionID]++
	return t.sessionTurn[sessionID]
}

func (t *cooldownTracker) shouldInclude(sessionID, memoryID uuid.UUID, turn int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	lastByMemory := t.lastSeen[sessionID]
	if len(lastByMemory) == 0 {
		return true
	}
	lastTurn, ok := lastByMemory[memoryID]
	if !ok {
		return true
	}
	return turn-lastTurn >= 5
}

func (t *cooldownTracker) markInjected(sessionID uuid.UUID, memoryIDs []uuid.UUID, turn int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	lastByMemory := t.lastSeen[sessionID]
	if lastByMemory == nil {
		lastByMemory = make(map[uuid.UUID]int)
		t.lastSeen[sessionID] = lastByMemory
	}
	for _, memoryID := range memoryIDs {
		lastByMemory[memoryID] = turn
	}
}
