package api

import (
	"encoding/json"

	"github.com/samhotchkiss/otter-camp/internal/ws"
)

type taskEvent struct {
	Type ws.MessageType `json:"type"`
	Task Task           `json:"task"`
}

type taskStatusEvent struct {
	Type           ws.MessageType `json:"type"`
	Task           Task           `json:"task"`
	PreviousStatus string         `json:"previous_status"`
}

func broadcastTaskCreated(hub *ws.Hub, task Task) {
	broadcastTaskEvent(hub, ws.MessageTaskCreated, task)
}

func broadcastTaskUpdated(hub *ws.Hub, task Task) {
	broadcastTaskEvent(hub, ws.MessageTaskUpdated, task)
}

func broadcastTaskStatusChanged(hub *ws.Hub, task Task, previousStatus string) {
	if hub == nil {
		return
	}

	payload, err := json.Marshal(taskStatusEvent{
		Type:           ws.MessageTaskStatusChanged,
		Task:           task,
		PreviousStatus: previousStatus,
	})
	if err != nil {
		return
	}

	hub.Broadcast(task.OrgID, payload)
}

func broadcastTaskEvent(hub *ws.Hub, messageType ws.MessageType, task Task) {
	if hub == nil {
		return
	}

	payload, err := json.Marshal(taskEvent{
		Type: messageType,
		Task: task,
	})
	if err != nil {
		return
	}

	hub.Broadcast(task.OrgID, payload)
}
