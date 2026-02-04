package dispatch

import (
	"fmt"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/models"
)

// TaskNode represents a task and its dependency requirements for dispatch.
type TaskNode struct {
	ID        string
	Status    string
	DependsOn []string
}

// CircularDependencyError indicates a circular dependency was detected.
type CircularDependencyError struct {
	Path []string
}

func (e CircularDependencyError) Error() string {
	if len(e.Path) == 0 {
		return "circular dependency detected"
	}
	return fmt.Sprintf("circular dependency: %s", strings.Join(e.Path, " -> "))
}

// DependencyGraph holds tasks and dependency relationships.
type DependencyGraph struct {
	nodes   map[string]TaskNode
	deps    map[string][]string
	reverse map[string][]string
	order   []string
}

// BuildDependencyGraph constructs a dependency graph and validates it for cycles.
func BuildDependencyGraph(tasks []TaskNode) (*DependencyGraph, error) {
	graph := &DependencyGraph{
		nodes:   make(map[string]TaskNode, len(tasks)),
		deps:    make(map[string][]string, len(tasks)),
		reverse: make(map[string][]string, len(tasks)),
		order:   make([]string, 0, len(tasks)),
	}

	for _, task := range tasks {
		if task.ID == "" {
			return nil, fmt.Errorf("task id is required")
		}
		if _, exists := graph.nodes[task.ID]; exists {
			return nil, fmt.Errorf("duplicate task id: %s", task.ID)
		}
		graph.nodes[task.ID] = task
		graph.order = append(graph.order, task.ID)
	}

	for _, task := range tasks {
		if len(task.DependsOn) == 0 {
			continue
		}
		depsCopy := append([]string(nil), task.DependsOn...)
		graph.deps[task.ID] = depsCopy
		for _, dep := range task.DependsOn {
			if dep == "" {
				return nil, fmt.Errorf("task %s has empty dependency", task.ID)
			}
			graph.reverse[dep] = append(graph.reverse[dep], task.ID)
		}
	}

	if err := graph.detectCycles(); err != nil {
		return nil, err
	}

	return graph, nil
}

// CanDispatch reports whether the given task's dependencies are complete.
func (g *DependencyGraph) CanDispatch(taskID string) (bool, error) {
	task, ok := g.nodes[taskID]
	if !ok {
		return false, fmt.Errorf("unknown task: %s", taskID)
	}

	for _, depID := range g.deps[task.ID] {
		dep, exists := g.nodes[depID]
		if !exists {
			return false, nil
		}
		if dep.Status != models.TaskStatusDone {
			return false, nil
		}
	}

	return true, nil
}

// DispatchableTasks returns tasks whose dependencies are complete, preserving input order.
func (g *DependencyGraph) DispatchableTasks() ([]TaskNode, error) {
	ready := make([]TaskNode, 0, len(g.order))
	for _, id := range g.order {
		can, err := g.CanDispatch(id)
		if err != nil {
			return nil, err
		}
		if can {
			ready = append(ready, g.nodes[id])
		}
	}
	return ready, nil
}

func (g *DependencyGraph) detectCycles() error {
	const (
		stateUnvisited = iota
		stateVisiting
		stateDone
	)

	state := make(map[string]int, len(g.nodes))
	stack := make([]string, 0, len(g.nodes))
	indexByID := make(map[string]int, len(g.nodes))

	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case stateVisiting:
			start := indexByID[id]
			path := append([]string(nil), stack[start:]...)
			path = append(path, id)
			return CircularDependencyError{Path: path}
		case stateDone:
			return nil
		}

		state[id] = stateVisiting
		indexByID[id] = len(stack)
		stack = append(stack, id)

		for _, dep := range g.deps[id] {
			if _, exists := g.nodes[dep]; !exists {
				continue
			}
			if err := visit(dep); err != nil {
				return err
			}
		}

		stack = stack[:len(stack)-1]
		state[id] = stateDone
		delete(indexByID, id)
		return nil
	}

	for _, id := range g.order {
		if state[id] == stateUnvisited {
			if err := visit(id); err != nil {
				return err
			}
		}
	}

	return nil
}
