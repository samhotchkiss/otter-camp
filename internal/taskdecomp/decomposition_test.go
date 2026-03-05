package taskdecomp

import (
	"strings"
	"testing"
)

func TestAnalyzeFlagsOversizedMultiDeliverableSpecs(t *testing.T) {
	description := strings.Join([]string{
		"1. Migrate all 36 posts from legacy markdown into the new CMS model including author mapping and canonical slug preservation.",
		"2. Rewrite media links and upload all referenced assets into object storage with stable paths and redirects.",
		"3. Rebuild taxonomy mapping for tags/categories and validate URL parity for existing inbound links.",
	}, "\n")

	plan := Analyze("Migrate legacy blog", &description)
	if !plan.RequiresDecomposition {
		t.Fatal("RequiresDecomposition = false, want true")
	}
	if plan.PrimaryDeliverable == "" {
		t.Fatal("PrimaryDeliverable is empty, want non-empty")
	}
	if len(plan.ChildDeliverables) < 1 {
		t.Fatalf("ChildDeliverables len = %d, want >= 1", len(plan.ChildDeliverables))
	}
}

func TestAnalyzeSkipsSmallSingleDeliverableSpecs(t *testing.T) {
	description := "Implement pagination controls for the project list view."

	plan := Analyze("Add pagination", &description)
	if plan.RequiresDecomposition {
		t.Fatal("RequiresDecomposition = true, want false")
	}
}
