package task

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNoUnexpectedDirectQueuedTaskCreationInNonTestCode(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))

	allowed := map[string]struct{}{
		filepath.Clean("internal/delivery/service.go"):       {},
		filepath.Clean("internal/delivery/deploy_worker.go"): {},
		filepath.Clean("internal/delivery/rollback.go"):      {},
		filepath.Clean("internal/tools/native/mutation_tools.go"): {},
	}

	var offenders []string
	err := filepath.WalkDir(filepath.Join(repoRoot, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.Clean(rel)

		srcBytes, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		src := string(srcBytes)
		if !strings.Contains(src, `WorkStatus:          "queued"`) && !strings.Contains(src, `WorkStatus:      "queued"`) {
			return nil
		}
		if _, ok := allowed[rel]; ok {
			return nil
		}
		offenders = append(offenders, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal tree: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("unexpected direct queued task creation sites: %s", strings.Join(offenders, ", "))
	}
}
