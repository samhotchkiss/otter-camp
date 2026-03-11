package task

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNoUnexpectedDirectLiveTaskStatusCreationInNonTestCode(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))

	allowedQueued := map[string]struct{}{
		filepath.Clean("internal/delivery/service.go"):       {},
		filepath.Clean("internal/delivery/deploy_worker.go"): {},
		filepath.Clean("internal/delivery/rollback.go"):      {},
		filepath.Clean("internal/tools/native/mutation_tools.go"): {},
	}
	allowedLowLevelStatusMutation := map[string]struct{}{
		filepath.Clean("internal/tools/native/mutation_tools.go"): {},
	}
	allowedSetFlowNode := map[string]struct{}{
		filepath.Clean("internal/flow/execution_service.go"):      {},
		filepath.Clean("internal/tools/native/mutation_tools.go"): {},
	}
	allowedGenericTaskUpdate := map[string]struct{}{
		filepath.Clean("internal/delivery/rollback.go"):      {},
		filepath.Clean("internal/task/service.go"):           {},
		filepath.Clean("internal/tools/native/mutation_tools.go"): {},
	}
	forbiddenStatuses := []string{"queued", "in_progress", "blocked", "review", "done", "cancelled"}

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
		for _, status := range forbiddenStatuses {
			if !strings.Contains(src, `WorkStatus:          "`+status+`"`) && !strings.Contains(src, `WorkStatus:      "`+status+`"`) {
				continue
			}
			if status == "queued" {
				if _, ok := allowedQueued[rel]; ok {
					continue
				}
			}
			offenders = append(offenders, rel+":"+status)
		}
		if strings.Contains(src, "e.tasks.UpdateStatus(ctx, taskID, targetStatus)") {
			if _, ok := allowedLowLevelStatusMutation[rel]; !ok {
				offenders = append(offenders, rel+":low-level-status-update")
			}
		}
		if strings.Contains(src, ".SetFlowNode(ctx,") {
			if _, ok := allowedSetFlowNode[rel]; !ok {
				offenders = append(offenders, rel+":set-flow-node")
			}
		}
		if strings.Contains(src, ".tasks.Update(ctx,") {
			if _, ok := allowedGenericTaskUpdate[rel]; !ok {
				offenders = append(offenders, rel+":generic-task-update")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal tree: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("unexpected direct live task status creation sites: %s", strings.Join(offenders, ", "))
	}
}
