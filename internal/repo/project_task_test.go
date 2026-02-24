package repo

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTaskNumberAllocatorConcurrentUnique(t *testing.T) {
	t.Parallel()

	allocator := &inMemoryTaskNumberAllocator{
		nextByProject: map[uuid.UUID]int{},
	}
	projectID := uuid.New()

	const workers = 32
	var wg sync.WaitGroup
	wg.Add(workers)

	results := make(chan int, workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			results <- allocator.Next(projectID)
		}()
	}
	wg.Wait()
	close(results)

	seen := make(map[int]struct{}, workers)
	for number := range results {
		if _, exists := seen[number]; exists {
			t.Fatalf("duplicate task number generated: %d", number)
		}
		seen[number] = struct{}{}
	}
	if len(seen) != workers {
		t.Fatalf("unique task number count = %d, want %d", len(seen), workers)
	}
}

func TestInboxFilterTargetBroadcastAndActed(t *testing.T) {
	t.Parallel()

	orgID := uuid.New()
	userID := uuid.New()
	otherUserID := uuid.New()

	targeted := InboxItem{ID: uuid.New(), OrganizationID: orgID, TargetUserID: &userID, IsActed: false}
	broadcast := InboxItem{ID: uuid.New(), OrganizationID: orgID, TargetUserID: nil, IsActed: false}
	acted := InboxItem{ID: uuid.New(), OrganizationID: orgID, TargetUserID: &userID, IsActed: true}
	otherTarget := InboxItem{ID: uuid.New(), OrganizationID: orgID, TargetUserID: &otherUserID, IsActed: false}

	filtered := filterInboxItemsForUser([]InboxItem{targeted, broadcast, acted, otherTarget}, orgID, userID, false)
	if len(filtered) != 2 {
		t.Fatalf("filtered len = %d, want 2", len(filtered))
	}

	filteredWithActed := filterInboxItemsForUser([]InboxItem{targeted, broadcast, acted, otherTarget}, orgID, userID, true)
	if len(filteredWithActed) != 3 {
		t.Fatalf("filtered with acted len = %d, want 3", len(filteredWithActed))
	}
}

func TestMergeQueuePositionAssignmentSequentialAndRequeue(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	assigner := &inMemoryMergeQueuePositionAssigner{
		entries: map[uuid.UUID][]MergeQueueEntry{},
	}

	first := assigner.Enqueue(projectID, uuid.New())
	second := assigner.Enqueue(projectID, uuid.New())
	third := assigner.Enqueue(projectID, uuid.New())
	if first.Position != 1 || second.Position != 2 || third.Position != 3 {
		t.Fatalf("positions = [%d %d %d], want [1 2 3]", first.Position, second.Position, third.Position)
	}

	third.Status = "failed"
	now := time.Now().UTC()
	third.ArchivedAt = &now
	assigner.Replace(projectID, third)

	requeued := assigner.Enqueue(projectID, third.TaskID)
	if requeued.Position != 4 {
		t.Fatalf("requeue position = %d, want 4", requeued.Position)
	}
}

type inMemoryTaskNumberAllocator struct {
	mu            sync.Mutex
	nextByProject map[uuid.UUID]int
}

func (a *inMemoryTaskNumberAllocator) Next(projectID uuid.UUID) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := a.nextByProject[projectID] + 1
	a.nextByProject[projectID] = next
	return next
}

type inMemoryMergeQueuePositionAssigner struct {
	mu      sync.Mutex
	entries map[uuid.UUID][]MergeQueueEntry
}

func (a *inMemoryMergeQueuePositionAssigner) Enqueue(projectID, taskID uuid.UUID) MergeQueueEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	position := 1
	for _, entry := range a.entries[projectID] {
		if entry.Position >= position {
			position = entry.Position + 1
		}
	}

	entry := MergeQueueEntry{
		ID:        uuid.New(),
		ProjectID: projectID,
		TaskID:    taskID,
		Position:  position,
		Status:    "queued",
		Metadata:  jsonRaw(`{}`),
	}
	a.entries[projectID] = append(a.entries[projectID], entry)
	return entry
}

func (a *inMemoryMergeQueuePositionAssigner) Replace(projectID uuid.UUID, replacement MergeQueueEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entries := a.entries[projectID]
	for i := range entries {
		if entries[i].ID == replacement.ID {
			entries[i] = replacement
			a.entries[projectID] = entries
			return
		}
	}
}

func filterInboxItemsForUser(items []InboxItem, organizationID, userID uuid.UUID, includeActed bool) []InboxItem {
	out := make([]InboxItem, 0, len(items))
	for _, item := range items {
		if item.OrganizationID != organizationID {
			continue
		}
		if !includeActed && item.IsActed {
			continue
		}
		if item.TargetUserID != nil && *item.TargetUserID != userID {
			continue
		}
		out = append(out, item)
	}
	return out
}

func jsonRaw(value string) []byte {
	return []byte(value)
}
