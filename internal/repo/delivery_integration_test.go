//go:build integration

package repo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestProjectRemoteRepoSetDefaultSwitchesDefaultAndEnforcesUnique(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	_, project := seedTaskRepoOrgProject(t, ctx, pool)

	remoteRepo := NewProjectRemoteRepo(pool)

	origin, err := remoteRepo.Create(ctx, ProjectRemote{
		ProjectID: project.ID,
		Name:      "origin",
		URL:       "https://git.example.com/repo.git",
		IsDefault: true,
		Transport: "https",
	})
	if err != nil {
		t.Fatalf("create origin remote: %v", err)
	}
	backup, err := remoteRepo.Create(ctx, ProjectRemote{
		ProjectID: project.ID,
		Name:      "backup",
		URL:       "ssh://git@example.com/repo.git",
		IsDefault: false,
		Transport: "ssh",
	})
	if err != nil {
		t.Fatalf("create backup remote: %v", err)
	}

	if _, err := remoteRepo.Create(ctx, ProjectRemote{
		ProjectID: project.ID,
		Name:      "mirror",
		URL:       "https://git.example.com/mirror.git",
		IsDefault: true,
		Transport: "https",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second default remote create err = %v, want ErrConflict", err)
	}

	if _, err := remoteRepo.SetDefault(ctx, project.ID, backup.ID); err != nil {
		t.Fatalf("SetDefault backup: %v", err)
	}

	originReloaded, err := remoteRepo.GetByID(ctx, origin.ID)
	if err != nil {
		t.Fatalf("GetByID origin: %v", err)
	}
	backupReloaded, err := remoteRepo.GetByID(ctx, backup.ID)
	if err != nil {
		t.Fatalf("GetByID backup: %v", err)
	}

	if originReloaded.IsDefault {
		t.Fatalf("origin is_default = %v, want false", originReloaded.IsDefault)
	}
	if !backupReloaded.IsDefault {
		t.Fatalf("backup is_default = %v, want true", backupReloaded.IsDefault)
	}

	defaultRemote, err := remoteRepo.GetDefault(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	if defaultRemote.ID != backup.ID {
		t.Fatalf("default remote id = %s, want %s", defaultRemote.ID, backup.ID)
	}
}

func TestProjectEnvironmentRepoSetDeployTaskRecordDeploymentAndGetActiveByMode(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedTaskRepoOrgProject(t, ctx, pool)

	remoteRepo := NewProjectRemoteRepo(pool)
	environmentRepo := NewProjectEnvironmentRepo(pool)
	taskRepo := NewProjectTaskRepo(pool)
	template := seedTaskRepoFlowTemplate(t, ctx, pool, org.ID, project.ID)

	remote, err := remoteRepo.Create(ctx, ProjectRemote{
		ProjectID: project.ID,
		Name:      "origin",
		URL:       "https://git.example.com/repo.git",
		IsDefault: true,
		Transport: "https",
	})
	if err != nil {
		t.Fatalf("create remote: %v", err)
	}

	continuousEnv, err := environmentRepo.Create(ctx, ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "production",
		DeliveryMode: "continuous",
		RemoteID:     &remote.ID,
		TargetBranch: "main",
		IsActive:     true,
	})
	if err != nil {
		t.Fatalf("create continuous env: %v", err)
	}
	if _, err := environmentRepo.Create(ctx, ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "paused",
		DeliveryMode: "continuous",
		RemoteID:     &remote.ID,
		TargetBranch: "main",
		IsActive:     false,
	}); err != nil {
		t.Fatalf("create inactive continuous env: %v", err)
	}
	if _, err := environmentRepo.Create(ctx, ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "staging",
		DeliveryMode: "gated",
		RemoteID:     &remote.ID,
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create gated env: %v", err)
	}

	deployTask, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Deploy production",
		WorkStatus:     "queued",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
		CreatedByID:    nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create deploy task: %v", err)
	}

	updatedEnv, err := environmentRepo.SetDeployTask(ctx, continuousEnv.ID, &deployTask.ID)
	if err != nil {
		t.Fatalf("SetDeployTask: %v", err)
	}
	if updatedEnv.DeployTaskID == nil || *updatedEnv.DeployTaskID != deployTask.ID {
		t.Fatalf("deploy_task_id = %v, want %s", updatedEnv.DeployTaskID, deployTask.ID)
	}

	firstDeployedAt := time.Date(2026, 2, 24, 13, 0, 0, 0, time.UTC)
	if _, err := environmentRepo.RecordDeployment(ctx, continuousEnv.ID, "aaaaaaaa", firstDeployedAt); err != nil {
		t.Fatalf("RecordDeployment first: %v", err)
	}
	secondDeployedAt := firstDeployedAt.Add(10 * time.Minute)
	recorded, err := environmentRepo.RecordDeployment(ctx, continuousEnv.ID, "bbbbbbbb", secondDeployedAt)
	if err != nil {
		t.Fatalf("RecordDeployment second: %v", err)
	}
	if recorded.LastDeployedCommit == nil || *recorded.LastDeployedCommit != "bbbbbbbb" {
		t.Fatalf("last_deployed_commit = %v, want bbbbbbbb", recorded.LastDeployedCommit)
	}
	if recorded.LastDeployedAt == nil || !recorded.LastDeployedAt.Equal(secondDeployedAt) {
		t.Fatalf("last_deployed_at = %v, want %v", recorded.LastDeployedAt, secondDeployedAt)
	}

	activeContinuous, err := environmentRepo.GetActiveByMode(ctx, "continuous")
	if err != nil {
		t.Fatalf("GetActiveByMode continuous: %v", err)
	}

	foundContinuous := false
	for _, item := range activeContinuous {
		if item.ID == continuousEnv.ID {
			foundContinuous = true
		}
		if item.Name == "paused" {
			t.Fatalf("inactive environment returned by GetActiveByMode")
		}
	}
	if !foundContinuous {
		t.Fatalf("active continuous environment not returned")
	}

	randomID := uuid.New()
	if _, err := environmentRepo.SetDeployTask(ctx, randomID, &deployTask.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetDeployTask missing err = %v, want ErrNotFound", err)
	}
}
