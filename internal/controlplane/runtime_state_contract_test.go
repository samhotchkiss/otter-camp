package controlplane

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestRuntimeContractFromStateAndRunPrefersCurrentRunIdentity(t *testing.T) {
	taskID := uuid.New()
	oldSessionID := uuid.New()
	newSessionID := uuid.New()
	oldFlowNodeID := uuid.New()
	newFlowNodeID := uuid.New()
	oldExecutionID := uuid.New()
	newExecutionID := uuid.New()

	stateMetadata, err := json.Marshal(map[string]any{
		"status":                 "resumable",
		"task_id":                taskID.String(),
		"session_id":             oldSessionID.String(),
		"flow_node_id":           oldFlowNodeID.String(),
		"flow_node_execution_id": oldExecutionID.String(),
		"provider_session_id":    "old-provider",
		"wakeup_source":          "supervisor",
		"wakeup_kind":            "supervisor_recovery",
	})
	if err != nil {
		t.Fatalf("marshal state metadata: %v", err)
	}

	runMetadata, err := json.Marshal(map[string]any{
		"flow_node_execution_id": newExecutionID.String(),
		"provider_session_id":    "new-provider",
		"execution_wakeup": map[string]any{
			"source": "task_queue_processor",
			"kind":   "flow_transition",
		},
	})
	if err != nil {
		t.Fatalf("marshal run metadata: %v", err)
	}

	contract := runtimeContractFromStateAndRun(RuntimeState{
		Metadata: stateMetadata,
	}, Run{
		TaskID:     &taskID,
		SessionID:  &newSessionID,
		FlowNodeID: &newFlowNodeID,
		Metadata:   runMetadata,
	})

	if contract.TaskID == nil || *contract.TaskID != taskID {
		t.Fatalf("task_id = %v, want %s", contract.TaskID, taskID)
	}
	if contract.SessionID == nil || *contract.SessionID != newSessionID {
		t.Fatalf("session_id = %v, want %s", contract.SessionID, newSessionID)
	}
	if contract.FlowNodeID == nil || *contract.FlowNodeID != newFlowNodeID {
		t.Fatalf("flow_node_id = %v, want %s", contract.FlowNodeID, newFlowNodeID)
	}
	if contract.FlowNodeExecutionID == nil || *contract.FlowNodeExecutionID != newExecutionID {
		t.Fatalf("flow_node_execution_id = %v, want %s", contract.FlowNodeExecutionID, newExecutionID)
	}
	if contract.ProviderSessionID != "new-provider" {
		t.Fatalf("provider_session_id = %q, want new-provider", contract.ProviderSessionID)
	}
	if contract.WakeupSource != "task_queue_processor" {
		t.Fatalf("wakeup_source = %q, want task_queue_processor", contract.WakeupSource)
	}
	if contract.WakeupKind != "flow_transition" {
		t.Fatalf("wakeup_kind = %q, want flow_transition", contract.WakeupKind)
	}
}
