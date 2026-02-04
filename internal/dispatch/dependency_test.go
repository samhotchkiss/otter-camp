package dispatch

import (
	"errors"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/models"
)

func TestDependencyGraphDispatchableTasks(t *testing.T) {
	tasks := []TaskNode{
		{ID: "A", Status: models.TaskStatusDone},
		{ID: "B", Status: models.TaskStatusQueued, DependsOn: []string{"A"}},
		{ID: "C", Status: models.TaskStatusQueued, DependsOn: []string{"B"}},
		{ID: "D", Status: models.TaskStatusQueued},
	}

	graph, err := BuildDependencyGraph(tasks)
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	canB, err := graph.CanDispatch("B")
	if err != nil {
		t.Fatalf("can dispatch B: %v", err)
	}
	if !canB {
		t.Fatalf("expected B to be dispatchable")
	}

	canC, err := graph.CanDispatch("C")
	if err != nil {
		t.Fatalf("can dispatch C: %v", err)
	}
	if canC {
		t.Fatalf("expected C to be blocked by B")
	}

	dispatchable, err := graph.DispatchableTasks()
	if err != nil {
		t.Fatalf("dispatchable tasks: %v", err)
	}

	if len(dispatchable) != 3 {
		t.Fatalf("expected 3 dispatchable tasks, got %d", len(dispatchable))
	}
	if dispatchable[0].ID != "A" || dispatchable[1].ID != "B" || dispatchable[2].ID != "D" {
		t.Fatalf("unexpected dispatchable order: %v", dispatchable)
	}
}

func TestDependencyGraphMissingDependency(t *testing.T) {
	tasks := []TaskNode{
		{ID: "A", Status: models.TaskStatusQueued, DependsOn: []string{"missing"}},
	}

	graph, err := BuildDependencyGraph(tasks)
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	can, err := graph.CanDispatch("A")
	if err != nil {
		t.Fatalf("can dispatch A: %v", err)
	}
	if can {
		t.Fatalf("expected A to be blocked by missing dependency")
	}
}

func TestDependencyGraphDetectsCycle(t *testing.T) {
	tasks := []TaskNode{
		{ID: "A", Status: models.TaskStatusQueued, DependsOn: []string{"B"}},
		{ID: "B", Status: models.TaskStatusQueued, DependsOn: []string{"C"}},
		{ID: "C", Status: models.TaskStatusQueued, DependsOn: []string{"A"}},
	}

	_, err := BuildDependencyGraph(tasks)
	if err == nil {
		t.Fatalf("expected cycle detection error")
	}

	var cycleErr CircularDependencyError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("expected CircularDependencyError, got %T", err)
	}
	if len(cycleErr.Path) == 0 {
		t.Fatalf("expected cycle path")
	}
}
