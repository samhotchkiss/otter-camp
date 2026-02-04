package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidateImportData_Valid(t *testing.T) {
	data := ExportData{
		Version:    "1.0",
		ExportedAt: time.Now(),
		OrgID:      "550e8400-e29b-41d4-a716-446655440000",
		Tasks: []ExportTask{
			{
				ID:     "task-1",
				Title:  "Test Task",
				Status: "queued",
			},
		},
		Projects: []ExportProject{
			{
				ID:   "project-1",
				Name: "Test Project",
			},
		},
		Agents: []ExportAgent{
			{
				ID:          "agent-1",
				Slug:        "test-agent",
				DisplayName: "Test Agent",
			},
		},
	}

	result := validateImportData(data)

	assert.True(t, result.Valid)
	assert.Empty(t, result.Errors)
	assert.Equal(t, 1, result.TaskCount)
	assert.Equal(t, 1, result.ProjectCount)
	assert.Equal(t, 1, result.AgentCount)
}

func TestValidateImportData_MissingVersion(t *testing.T) {
	data := ExportData{
		OrgID: "550e8400-e29b-41d4-a716-446655440000",
	}

	result := validateImportData(data)

	assert.False(t, result.Valid)
	assert.Contains(t, result.Errors, "missing version field")
}

func TestValidateImportData_MissingTaskID(t *testing.T) {
	data := ExportData{
		Version: "1.0",
		Tasks: []ExportTask{
			{
				Title: "Task without ID",
			},
		},
	}

	result := validateImportData(data)

	assert.False(t, result.Valid)
	assert.Contains(t, result.Errors, "task[0]: missing id")
}

func TestValidateImportData_MissingTaskTitle(t *testing.T) {
	data := ExportData{
		Version: "1.0",
		Tasks: []ExportTask{
			{
				ID: "task-1",
			},
		},
	}

	result := validateImportData(data)

	assert.False(t, result.Valid)
	assert.Contains(t, result.Errors, "task[0]: missing title")
}

func TestValidateImportData_MissingProjectID(t *testing.T) {
	data := ExportData{
		Version: "1.0",
		Projects: []ExportProject{
			{
				Name: "Project without ID",
			},
		},
	}

	result := validateImportData(data)

	assert.False(t, result.Valid)
	assert.Contains(t, result.Errors, "project[0]: missing id")
}

func TestValidateImportData_MissingAgentSlug(t *testing.T) {
	data := ExportData{
		Version: "1.0",
		Agents: []ExportAgent{
			{
				ID: "agent-1",
			},
		},
	}

	result := validateImportData(data)

	assert.False(t, result.Valid)
	assert.Contains(t, result.Errors, "agent[0]: missing slug")
}

func TestValidateImportData_Warnings(t *testing.T) {
	data := ExportData{
		Version: "1.0",
		// Missing org_id
		Tasks: []ExportTask{
			{
				ID:    "task-1",
				Title: "Test Task",
				// Missing status
			},
		},
		Agents: []ExportAgent{
			{
				ID:   "agent-1",
				Slug: "test-agent",
				// Missing display_name
			},
		},
	}

	result := validateImportData(data)

	assert.True(t, result.Valid)
	assert.Contains(t, result.Warnings, "missing org_id in export (will use target workspace)")
	assert.Contains(t, result.Warnings, "task[0]: missing status (will default to 'queued')")
	assert.Contains(t, result.Warnings, "agent[0]: missing display_name (will use slug)")
}

func TestValidateImportData_UnknownVersion(t *testing.T) {
	data := ExportData{
		Version: "2.0",
		Tasks: []ExportTask{
			{
				ID:    "task-1",
				Title: "Test Task",
			},
		},
	}

	result := validateImportData(data)

	assert.True(t, result.Valid) // Unknown version is a warning, not an error
	assert.Contains(t, result.Warnings, "unknown version: 2.0 (expected 1.0)")
}

func TestExportDataJSONSerialization(t *testing.T) {
	desc := "Test description"
	data := ExportData{
		Version:    "1.0",
		ExportedAt: time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC),
		OrgID:      "550e8400-e29b-41d4-a716-446655440000",
		Tasks: []ExportTask{
			{
				ID:          "task-1",
				Title:       "Test Task",
				Description: &desc,
				Status:      "queued",
				Priority:    "P1",
				Context:     json.RawMessage(`{"key": "value"}`),
				CreatedAt:   time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC),
				UpdatedAt:   time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC),
			},
		},
		TaskCount:  1,
		TotalItems: 1,
	}

	bytes, err := json.Marshal(data)
	assert.NoError(t, err)

	var decoded ExportData
	err = json.Unmarshal(bytes, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, data.Version, decoded.Version)
	assert.Equal(t, data.OrgID, decoded.OrgID)
	assert.Len(t, decoded.Tasks, 1)
	assert.Equal(t, "Test Task", decoded.Tasks[0].Title)
	assert.Equal(t, "Test description", *decoded.Tasks[0].Description)
}

func TestValidationResult_TotalItems(t *testing.T) {
	data := ExportData{
		Version: "1.0",
		Tasks: []ExportTask{
			{ID: "t1", Title: "Task 1"},
			{ID: "t2", Title: "Task 2"},
		},
		Projects: []ExportProject{
			{ID: "p1", Name: "Project 1"},
		},
		Agents: []ExportAgent{
			{ID: "a1", Slug: "agent-1", DisplayName: "Agent 1"},
			{ID: "a2", Slug: "agent-2", DisplayName: "Agent 2"},
			{ID: "a3", Slug: "agent-3", DisplayName: "Agent 3"},
		},
		Activities: []ExportActivity{
			{ID: "act1", Action: "created"},
		},
	}

	result := validateImportData(data)

	assert.Equal(t, 2, result.TaskCount)
	assert.Equal(t, 1, result.ProjectCount)
	assert.Equal(t, 3, result.AgentCount)
	assert.Equal(t, 1, result.ActivityCount)
	assert.Equal(t, 7, result.TotalItems) // 2+1+3+1
}
