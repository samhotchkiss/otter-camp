package repo

import (
	"strings"
	"testing"
)

func TestListSubtreeQueryUsesRecursiveCTE(t *testing.T) {
	query := strings.ToUpper(listSubtreeQuery)
	if !strings.Contains(query, "WITH RECURSIVE") {
		t.Fatal("expected recursive CTE in ListSubtree query")
	}
	if !strings.Contains(query, "WHERE DEPTH > 0") {
		t.Fatal("expected descendants-only filter in ListSubtree query")
	}
}
