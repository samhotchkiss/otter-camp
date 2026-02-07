package dispatch

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// DepStatus represents the lifecycle status of a task for dependency resolution.
type DepStatus string

const (
	StatusQueued     DepStatus = "queued"
	StatusDispatched DepStatus = "dispatched"
	StatusInProgress DepStatus = "in_progress"
	StatusBlocked    DepStatus = "blocked"
	StatusReview     DepStatus = "review"
	StatusDone       DepStatus = "done"
	StatusCancelled  DepStatus = "cancelled"
)

// Dependencies maps a task ID to the list of task IDs it depends on.
type Dependencies map[string][]string

// MissingDependencyStatusError indicates a dependency with no known status.
type MissingDependencyStatusError struct {
	TaskID       string
	DependencyID string
}

func (e *MissingDependencyStatusError) Error() string {
	return fmt.Sprintf("missing status for dependency %s of %s", e.DependencyID, e.TaskID)
}

// CircularDependencyError indicates a detected cycle in the dependency graph.
type CircularDependencyError struct {
	Cycle []string
}

func (e *CircularDependencyError) Error() string {
	return fmt.Sprintf("circular dependency detected: %s", strings.Join(e.Cycle, " -> "))
}

// CanDispatch determines if a task's dependencies are satisfied.
func CanDispatch(taskID string, deps Dependencies, status map[string]DepStatus) (bool, error) {
	if status == nil {
		return false, errors.New("status map is nil")
	}
	if err := DetectCircular(deps); err != nil {
		return false, err
	}

	for _, dep := range deps[taskID] {
		depStatus, ok := status[dep]
		if !ok {
			return false, &MissingDependencyStatusError{TaskID: taskID, DependencyID: dep}
		}
		if depStatus != StatusDone {
			return false, nil
		}
	}
	return true, nil
}

// OnComplete marks a task complete and returns dependent tasks now eligible for dispatch.
func OnComplete(completedID string, deps Dependencies, status map[string]DepStatus) ([]string, error) {
	if status == nil {
		return nil, errors.New("status map is nil")
	}
	status[completedID] = StatusDone

	if err := DetectCircular(deps); err != nil {
		return nil, err
	}

	ready := make([]string, 0)
	for taskID, depList := range deps {
		if status[taskID] == StatusDone {
			continue
		}
		if !contains(depList, completedID) {
			continue
		}
		can, err := CanDispatch(taskID, deps, status)
		if err != nil {
			return nil, err
		}
		if can {
			ready = append(ready, taskID)
		}
	}
	sort.Strings(ready)
	return ready, nil
}

// DetectCircular returns an error if the dependency graph contains a cycle.
func DetectCircular(deps Dependencies) error {
	if deps == nil {
		return nil
	}

	nodes := make(map[string]struct{}, len(deps))
	for taskID, depList := range deps {
		nodes[taskID] = struct{}{}
		for _, dep := range depList {
			nodes[dep] = struct{}{}
		}
	}

	nodeList := make([]string, 0, len(nodes))
	for node := range nodes {
		nodeList = append(nodeList, node)
	}
	sort.Strings(nodeList)

	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)

	colors := make(map[string]uint8, len(nodes))
	stack := make([]string, 0, len(nodes))
	stackIndex := make(map[string]int, len(nodes))

	var visit func(string) error
	visit = func(node string) error {
		switch colors[node] {
		case visiting:
			if idx, ok := stackIndex[node]; ok {
				cycle := append([]string{}, stack[idx:]...)
				cycle = append(cycle, node)
				return &CircularDependencyError{Cycle: cycle}
			}
			return &CircularDependencyError{Cycle: []string{node}}
		case visited:
			return nil
		}

		colors[node] = visiting
		stackIndex[node] = len(stack)
		stack = append(stack, node)

		depsForNode := append([]string(nil), deps[node]...)
		sort.Strings(depsForNode)
		for _, dep := range depsForNode {
			if err := visit(dep); err != nil {
				return err
			}
		}

		stack = stack[:len(stack)-1]
		delete(stackIndex, node)
		colors[node] = visited
		return nil
	}

	for _, node := range nodeList {
		if colors[node] == unvisited {
			if err := visit(node); err != nil {
				return err
			}
		}
	}

	return nil
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
