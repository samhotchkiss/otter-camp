package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

type execApprovalWebhookFields struct {
	ExternalID  *string
	AgentID     *string
	TaskID      *string
	Command     string
	Cwd         *string
	Shell       *string
	Args        json.RawMessage
	Env         json.RawMessage
	Message     *string
	CallbackURL *string
	Request     json.RawMessage
}

func isExecApprovalEvent(event string) bool {
	normalized := strings.ToLower(strings.TrimSpace(event))
	if normalized == "" {
		return false
	}
	if strings.HasPrefix(normalized, "exec.approval") {
		return true
	}
	if strings.HasPrefix(normalized, "exec_approval") {
		return true
	}
	return false
}

func handleExecApprovalWebhook(ctx context.Context, db *sql.DB, hub *ws.Hub, orgID string, body []byte) (*ExecApprovalRequest, error) {
	fields, err := parseExecApprovalWebhookPayload(body)
	if err != nil {
		return nil, err
	}

	conn, err := store.WithWorkspaceID(ctx, db, orgID)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	inserted, err := insertExecApprovalRequest(ctx, conn, orgID, fields)
	if err != nil {
		return nil, err
	}

	if inserted != nil && hub != nil {
		broadcastExecApprovalRequested(hub, orgID, *inserted)
	}

	return inserted, nil
}

func insertExecApprovalRequest(ctx context.Context, q store.Querier, orgID string, fields execApprovalWebhookFields) (*ExecApprovalRequest, error) {
	var externalArg interface{}
	if fields.ExternalID != nil && strings.TrimSpace(*fields.ExternalID) != "" {
		externalArg = strings.TrimSpace(*fields.ExternalID)
	}

	var agentArg interface{}
	if fields.AgentID != nil && uuidRegex.MatchString(*fields.AgentID) {
		agentArg = strings.TrimSpace(*fields.AgentID)
	}

	var taskArg interface{}
	if fields.TaskID != nil && uuidRegex.MatchString(*fields.TaskID) {
		taskArg = strings.TrimSpace(*fields.TaskID)
	}

	command := strings.TrimSpace(fields.Command)
	if command == "" {
		command = "(unknown command)"
	}

	row := q.QueryRowContext(
		ctx,
		`INSERT INTO exec_approval_requests (
			org_id, external_id, agent_id, task_id, status, command, cwd, shell, args, env, message, callback_url, request
		) VALUES (
			$1, $2, $3, $4, 'pending', $5, $6, $7, $8, $9, $10, $11, $12
		)
		ON CONFLICT (org_id, external_id) WHERE external_id IS NOT NULL DO NOTHING
		RETURNING id, org_id, external_id, agent_id, task_id, status, command, cwd, shell, args, env, message, callback_url, request, response, created_at, resolved_at`,
		orgID,
		externalArg,
		agentArg,
		taskArg,
		command,
		fields.Cwd,
		fields.Shell,
		nullRawMessage(fields.Args),
		nullRawMessage(fields.Env),
		fields.Message,
		fields.CallbackURL,
		nullRawMessage(fields.Request),
	)

	approval, err := scanExecApprovalRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return &approval, nil
}

func nullRawMessage(value json.RawMessage) interface{} {
	if len(value) == 0 || string(value) == "null" {
		return nil
	}
	return value
}

func broadcastExecApprovalRequested(hub *ws.Hub, orgID string, approval ExecApprovalRequest) {
	if hub == nil {
		return
	}

	payload, err := json.Marshal(map[string]interface{}{
		"type": "ExecApprovalRequested",
		"data": approval,
	})
	if err != nil {
		return
	}

	hub.Broadcast(orgID, payload)
}

func parseExecApprovalWebhookPayload(body []byte) (execApprovalWebhookFields, error) {
	fields := execApprovalWebhookFields{
		Request: json.RawMessage(body),
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return fields, errors.New("invalid JSON payload")
	}

	root := raw
	approval := asMap(root["approval"])
	request := asMap(root["request"])
	data := asMap(root["data"])
	exec := asMap(root["exec"])

	candidates := []map[string]interface{}{approval, request, data, exec, root}

	fields.ExternalID = nonEmptyPtr(findStringIn(candidates, "approval_id", "request_id", "id", "external_id"))
	fields.AgentID = nonEmptyPtr(findStringIn([]map[string]interface{}{root, request, data, exec, approval}, "agent_id", "agentId"))
	fields.TaskID = nonEmptyPtr(findStringIn([]map[string]interface{}{root, request, data, exec, approval}, "task_id", "taskId"))
	fields.CallbackURL = nonEmptyPtr(findStringIn(candidates, "callback_url", "callbackUrl", "response_url", "responseUrl", "reply_url", "replyUrl"))
	fields.Cwd = nonEmptyPtr(findStringIn(candidates, "cwd", "working_dir", "working_directory", "workdir"))
	fields.Shell = nonEmptyPtr(findStringIn(candidates, "shell"))
	fields.Message = nonEmptyPtr(findStringIn(candidates, "message", "reason", "explanation", "summary", "context"))

	commandValue, commandOK := findValueIn(candidates, "command", "cmd")
	if commandOK {
		fields.Command = normalizeCommand(commandValue)
	}

	argsValue, argsOK := findValueIn(candidates, "args", "argv")
	if argsOK {
		encoded, _ := json.Marshal(argsValue)
		fields.Args = json.RawMessage(encoded)
		if fields.Command == "" {
			fields.Command = normalizeCommand(argsValue)
		}
	}

	envValue, envOK := findValueIn(candidates, "env", "environment")
	if envOK {
		encoded, _ := json.Marshal(envValue)
		fields.Env = json.RawMessage(encoded)
	}

	if fields.Command == "" {
		fields.Command = strings.TrimSpace(findStringIn(candidates, "shell_command", "full_command"))
	}

	return fields, nil
}

func asMap(value interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	m, ok := value.(map[string]interface{})
	if !ok {
		return nil
	}
	return m
}

func findStringIn(candidates []map[string]interface{}, keys ...string) string {
	for _, c := range candidates {
		if c == nil {
			continue
		}
		for _, key := range keys {
			if v, ok := c[key]; ok {
				if s, ok := v.(string); ok {
					trimmed := strings.TrimSpace(s)
					if trimmed != "" {
						return trimmed
					}
				}
			}
		}
	}
	return ""
}

func findValueIn(candidates []map[string]interface{}, keys ...string) (interface{}, bool) {
	for _, c := range candidates {
		if c == nil {
			continue
		}
		for _, key := range keys {
			if v, ok := c[key]; ok && v != nil {
				return v, true
			}
		}
	}
	return nil, false
}

func normalizeCommand(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []interface{}:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func nonEmptyPtr(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
