package turn

import "testing"

func TestShouldSeedBootstrapRestartTaskTree(t *testing.T) {
	if shouldSeedBootstrapRestartTaskTree(projectAutomaticFailureRecord{FailureClass: projectBootstrapFailureCompoundParent}) {
		t.Fatal("compound parent failures should rebuild the task tree instead of reusing the archived backlog")
	}
	if !shouldSeedBootstrapRestartTaskTree(projectAutomaticFailureRecord{FailureClass: projectBootstrapFailureRuntime}) {
		t.Fatal("runtime restart failures should continue seeding the archived task tree")
	}
}
