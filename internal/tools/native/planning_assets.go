package native

import (
	"bytes"
	"context"

	"github.com/samhotchkiss/otter-camp/internal/planningasset"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
)

func (e *NativeToolExecutor) planningAssetService() *planningasset.Service {
	return planningasset.New(planningasset.Options{
		Artifacts:    e.planningAssets,
		Environments: e.environments,
	})
}

func (e *NativeToolExecutor) syncPlanningArtifacts(ctx context.Context, taskRecord repo.ProjectTask, actor executionActor) (repo.ProjectTask, taskplan.Plan, error) {
	service := e.planningAssetService()
	plan, err := service.SyncTask(ctx, taskRecord, planningasset.Actor{
		Type: actor.createdByType,
		ID:   actor.createdByPtr,
	})
	if err != nil {
		return taskRecord, taskplan.Plan{}, err
	}
	if !plan.HasSelection() {
		return taskRecord, plan, nil
	}

	metadata := taskplan.ApplyMetadata(taskRecord.Metadata, plan)
	if bytes.Equal(metadata, taskRecord.Metadata) {
		return taskRecord, plan, nil
	}
	taskRecord.Metadata = metadata
	return taskRecord, plan, nil
}
