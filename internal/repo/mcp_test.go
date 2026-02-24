package repo

import (
	"testing"

	"github.com/google/uuid"
)

func TestPlanCatalogDiffRefreshScenario(t *testing.T) {
	existing := map[string]MCPToolCatalogEntry{
		"A": {ID: uuid.New(), ToolName: "A", IsEnabled: true},
		"B": {ID: uuid.New(), ToolName: "B", IsEnabled: true},
		"C": {ID: uuid.New(), ToolName: "C", IsEnabled: false},
	}
	manifest := []MCPToolCatalogEntry{
		{ToolName: "B", Description: "B2"},
		{ToolName: "C", Description: "C2"},
		{ToolName: "D", Description: "D1"},
	}

	added, updated, removed, err := planCatalogDiff(existing, manifest)
	if err != nil {
		t.Fatalf("planCatalogDiff: %v", err)
	}

	if len(added) != 1 || added[0].ToolName != "D" {
		t.Fatalf("added = %+v, want [D]", added)
	}
	if len(removed) != 1 || removed[0].ToolName != "A" {
		t.Fatalf("removed = %+v, want [A]", removed)
	}
	if len(updated) != 2 {
		t.Fatalf("updated len = %d, want 2", len(updated))
	}

	updatedByName := map[string]MCPToolCatalogEntry{}
	for _, entry := range updated {
		updatedByName[entry.ToolName] = entry
	}
	if !updatedByName["B"].IsEnabled {
		t.Fatalf("updated B should preserve enabled=true")
	}
	if updatedByName["C"].IsEnabled {
		t.Fatalf("updated C should preserve enabled=false")
	}
}

func TestPlanCatalogDiffRejectsEmptyToolName(t *testing.T) {
	_, _, _, err := planCatalogDiff(map[string]MCPToolCatalogEntry{}, []MCPToolCatalogEntry{{ToolName: " "}})
	if err == nil {
		t.Fatalf("expected error for empty tool_name")
	}
}
