package dispatch

import "testing"

func TestQueuePriorityOrder(t *testing.T) {
	var q Queue

	if err := q.Add(Item{ID: "p2", Priority: "P2"}); err != nil {
		t.Fatalf("add p2: %v", err)
	}
	if err := q.Add(Item{ID: "p0", Priority: "P0"}); err != nil {
		t.Fatalf("add p0: %v", err)
	}
	if err := q.Add(Item{ID: "p1", Priority: "P1"}); err != nil {
		t.Fatalf("add p1: %v", err)
	}

	item, ok := q.Next()
	if !ok || item.ID != "p0" {
		t.Fatalf("expected p0 first, got ok=%v item=%v", ok, item)
	}
	if !q.Ack(item.ID) {
		t.Fatalf("expected ack for p0")
	}

	item, ok = q.Next()
	if !ok || item.ID != "p1" {
		t.Fatalf("expected p1 next, got ok=%v item=%v", ok, item)
	}
	if !q.Ack(item.ID) {
		t.Fatalf("expected ack for p1")
	}

	item, ok = q.Next()
	if !ok || item.ID != "p2" {
		t.Fatalf("expected p2 last, got ok=%v item=%v", ok, item)
	}
	if !q.Ack(item.ID) {
		t.Fatalf("expected ack for p2")
	}

	item, ok = q.Next()
	if ok || item != nil {
		t.Fatalf("expected empty queue, got ok=%v item=%v", ok, item)
	}
}

func TestQueueFIFOWithinPriority(t *testing.T) {
	var q Queue

	items := []string{"a", "b", "c"}
	for _, id := range items {
		if err := q.Add(Item{ID: id, Priority: "P1"}); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}

	for _, id := range items {
		item, ok := q.Next()
		if !ok || item.ID != id {
			t.Fatalf("expected %s, got ok=%v item=%v", id, ok, item)
		}
		if !q.Ack(item.ID) {
			t.Fatalf("expected ack for %s", id)
		}
	}
}

func TestQueueIdempotentPickup(t *testing.T) {
	var q Queue

	if err := q.Add(Item{ID: "one", Priority: "P0"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	item1, ok := q.Next()
	if !ok || item1.ID != "one" {
		t.Fatalf("expected first pickup, got ok=%v item=%v", ok, item1)
	}
	item2, ok := q.Next()
	if !ok || item2.ID != "one" {
		t.Fatalf("expected idempotent pickup, got ok=%v item=%v", ok, item2)
	}

	if !q.Ack("one") {
		t.Fatalf("expected ack for one")
	}

	item3, ok := q.Next()
	if ok || item3 != nil {
		t.Fatalf("expected empty queue after ack, got ok=%v item=%v", ok, item3)
	}
}

func TestQueueRejectsInvalidPriority(t *testing.T) {
	var q Queue

	if err := q.Add(Item{ID: "bad", Priority: "P5"}); err == nil {
		t.Fatalf("expected error for invalid priority")
	}
}
