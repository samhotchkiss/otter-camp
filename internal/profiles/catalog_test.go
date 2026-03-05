package profiles

import (
	"path/filepath"
	"runtime"
	"testing"
)

func testCatalogDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "agent-profiles")
}

func TestLoadCatalog(t *testing.T) {
	catalog, err := LoadCatalog(testCatalogDir(t))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(catalog.entries) < 200 {
		t.Fatalf("expected >200 profiles, got %d", len(catalog.entries))
	}
}

func TestSearch(t *testing.T) {
	catalog, err := LoadCatalog(testCatalogDir(t))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	t.Run("by_category", func(t *testing.T) {
		results := catalog.Search("engineering", "", "", 0)
		if len(results) == 0 {
			t.Fatal("expected engineering profiles, got 0")
		}
		for _, r := range results {
			if r.Category != "engineering" {
				t.Fatalf("expected category engineering, got %q", r.Category)
			}
		}
	})

	t.Run("by_query", func(t *testing.T) {
		results := catalog.Search("", "", "llm", 0)
		if len(results) == 0 {
			t.Fatal("expected results for query 'llm', got 0")
		}
		found := false
		for _, r := range results {
			if r.RoleID == "ai-llm-specialist" {
				found = true
			}
		}
		if !found {
			t.Fatal("expected ai-llm-specialist in results for query 'llm'")
		}
	})

	t.Run("limit", func(t *testing.T) {
		results := catalog.Search("", "", "", 5)
		if len(results) != 5 {
			t.Fatalf("expected 5 results, got %d", len(results))
		}
	})
}

func TestGetByRoleID(t *testing.T) {
	catalog, err := LoadCatalog(testCatalogDir(t))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	detail, ok := catalog.GetByRoleID("ai-llm-specialist")
	if !ok {
		t.Fatal("expected to find ai-llm-specialist")
	}
	if detail.DisplayName != "River Bustamante" {
		t.Fatalf("expected River Bustamante, got %q", detail.DisplayName)
	}
	if detail.IdentitySummary == "" {
		t.Fatal("expected non-empty identity summary")
	}
	if detail.Soul == "" {
		t.Fatal("expected non-empty soul")
	}
}

func TestCategories(t *testing.T) {
	catalog, err := LoadCatalog(testCatalogDir(t))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	cats := catalog.Categories()
	if len(cats) < 10 {
		t.Fatalf("expected >=10 categories, got %d: %v", len(cats), cats)
	}
	// Verify sorted
	for i := 1; i < len(cats); i++ {
		if cats[i] < cats[i-1] {
			t.Fatalf("categories not sorted: %q before %q", cats[i-1], cats[i])
		}
	}
}
