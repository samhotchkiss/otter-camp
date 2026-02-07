package dispatch

import (
	"fmt"
	"sync"
)

var priorityOrder = []string{"P0", "P1", "P2", "P3"}

var allowedPriorities = map[string]struct{}{
	"P0": {},
	"P1": {},
	"P2": {},
	"P3": {},
}

// Item represents a dispatchable entry in the queue.
type Item struct {
	ID       string
	Priority string
}

// Queue is an in-memory dispatch queue with priority ordering and FIFO per priority.
type Queue struct {
	mu       sync.Mutex
	pending  map[string][]*Item
	inflight *Item
}

// Add inserts an item into the queue.
func (q *Queue) Add(item Item) error {
	if _, ok := allowedPriorities[item.Priority]; !ok {
		return fmt.Errorf("invalid priority: %s", item.Priority)
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.pending == nil {
		q.pending = make(map[string][]*Item)
	}

	copyItem := item
	q.pending[item.Priority] = append(q.pending[item.Priority], &copyItem)
	return nil
}

// Next returns the next item to dispatch, honoring priority and FIFO order.
// If an item is already in-flight, it is returned again (idempotent pickup).
func (q *Queue) Next() (*Item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.inflight != nil {
		copyItem := *q.inflight
		return &copyItem, true
	}

	if q.pending == nil {
		return nil, false
	}

	for _, priority := range priorityOrder {
		items := q.pending[priority]
		if len(items) == 0 {
			continue
		}
		item := items[0]
		q.pending[priority] = items[1:]
		q.inflight = item
		copyItem := *item
		return &copyItem, true
	}

	return nil, false
}

// Ack acknowledges the in-flight item, removing it from the queue.
func (q *Queue) Ack(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.inflight == nil {
		return false
	}
	if q.inflight.ID != id {
		return false
	}
	q.inflight = nil
	return true
}
