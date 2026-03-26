package native

import (
	"context"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func (m *mockTaskRepo) GetByProjectAndNumber(_ context.Context, projectID uuid.UUID, taskNumber int) (repo.ProjectTask, error) {
	for _, task := range m.listByProjectTasks {
		if task.ProjectID == projectID && task.TaskNumber == taskNumber {
			return task, nil
		}
	}
	for _, task := range m.createdTasks {
		if task.ProjectID == projectID && task.TaskNumber == taskNumber {
			return task, nil
		}
	}
	if m.task.ProjectID == projectID && m.task.TaskNumber == taskNumber {
		return m.task, nil
	}
	return repo.ProjectTask{}, repo.ErrNotFound
}
