package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

type CommandShortcutModifiers struct {
	Cmd   bool `json:"cmd,omitempty"`
	Shift bool `json:"shift,omitempty"`
	Alt   bool `json:"alt,omitempty"`
}

type CommandShortcut struct {
	Key       string                    `json:"key"`
	Modifiers *CommandShortcutModifiers `json:"modifiers,omitempty"`
}

type CommandExecuteRequest struct {
	Type       string           `json:"type,omitempty"`
	Shortcut   *CommandShortcut `json:"shortcut,omitempty"`
	Parameters json.RawMessage  `json:"parameters,omitempty"`
}

type CommandExecuteResponse struct {
	OK          bool        `json:"ok"`
	Type        string      `json:"type"`
	RedirectURL string      `json:"redirect_url,omitempty"`
	Result      interface{} `json:"result,omitempty"`
}

type commandExecuteError struct {
	status int
	msg    string
}

func (e commandExecuteError) Error() string {
	return e.msg
}

// CommandExecuteHandler handles POST /api/commands/execute.
//
// It accepts either:
// - { "type": "navigate"|"create"|"search"|"action", "parameters": { ... } }
// - { "shortcut": { "key": "...", "modifiers": { ... } }, "parameters": { ... } }
func CommandExecuteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	req, err := decodeCommandExecuteRequest(r)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	commandType := strings.ToLower(strings.TrimSpace(req.Type))
	params := normalizeJSONParams(req.Parameters)

	if commandType == "" && req.Shortcut != nil {
		resolvedType, defaults, ok := resolveCommandFromShortcut(*req.Shortcut)
		if !ok {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "unsupported shortcut"})
			return
		}

		commandType = resolvedType
		merged, mergeErr := mergeJSONDefaults(defaults, params)
		if mergeErr != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid parameters"})
			return
		}
		params = merged
	}

	if commandType == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing command type"})
		return
	}

	var resp CommandExecuteResponse
	var execErr *commandExecuteError

	switch commandType {
	case "navigate":
		resp, execErr = executeNavigateCommand(params)
	case "create":
		resp, execErr = executeCreateCommand(r, params)
	case "search":
		resp, execErr = executeSearchCommand(r, params)
	case "action":
		resp, execErr = executeActionCommand(params)
	default:
		execErr = &commandExecuteError{status: http.StatusBadRequest, msg: "unsupported command type"}
	}

	if execErr != nil {
		sendJSON(w, execErr.status, errorResponse{Error: execErr.msg})
		return
	}

	sendJSON(w, http.StatusOK, resp)
}

func decodeCommandExecuteRequest(r *http.Request) (CommandExecuteRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return CommandExecuteRequest{}, errors.New("invalid request body")
	}

	typ := ""
	if rawType, ok := raw["type"]; ok && len(rawType) > 0 && string(rawType) != "null" {
		if err := json.Unmarshal(rawType, &typ); err != nil {
			return CommandExecuteRequest{}, errors.New("invalid type")
		}
	}

	params := json.RawMessage(nil)
	if rawParams, ok := raw["parameters"]; ok {
		params = rawParams
	} else if rawParams, ok := raw["params"]; ok {
		params = rawParams
	}

	var shortcut *CommandShortcut
	if rawShortcut, ok := raw["shortcut"]; ok {
		if len(rawShortcut) == 0 || string(rawShortcut) == "null" {
			shortcut = nil
		} else {
			var parsed CommandShortcut
			if err := json.Unmarshal(rawShortcut, &parsed); err != nil {
				return CommandExecuteRequest{}, errors.New("invalid shortcut")
			}
			parsed.Key = strings.TrimSpace(parsed.Key)
			if parsed.Modifiers == nil {
				parsed.Modifiers = &CommandShortcutModifiers{}
			}
			if parsed.Key != "" {
				shortcut = &parsed
			}
		}
	}

	return CommandExecuteRequest{
		Type:       typ,
		Shortcut:   shortcut,
		Parameters: params,
	}, nil
}

func normalizeJSONParams(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}

func mergeJSONDefaults(defaults json.RawMessage, provided json.RawMessage) (json.RawMessage, error) {
	if len(defaults) == 0 || string(defaults) == "null" {
		return provided, nil
	}

	var defaultsMap map[string]interface{}
	if err := json.Unmarshal(defaults, &defaultsMap); err != nil {
		return nil, err
	}
	if defaultsMap == nil {
		defaultsMap = map[string]interface{}{}
	}

	var providedMap map[string]interface{}
	if len(provided) == 0 || string(provided) == "null" {
		providedMap = map[string]interface{}{}
	} else if err := json.Unmarshal(provided, &providedMap); err != nil {
		return nil, err
	} else if providedMap == nil {
		providedMap = map[string]interface{}{}
	}

	for key, value := range defaultsMap {
		if _, ok := providedMap[key]; !ok {
			providedMap[key] = value
		}
	}

	merged, err := json.Marshal(providedMap)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(merged), nil
}

func resolveCommandFromShortcut(shortcut CommandShortcut) (commandType string, defaultParams json.RawMessage, ok bool) {
	key := strings.ToLower(strings.TrimSpace(shortcut.Key))
	mod := shortcut.Modifiers
	if mod == nil {
		mod = &CommandShortcutModifiers{}
	}

	switch {
	case mod.Cmd && !mod.Shift && !mod.Alt && key == "k":
		return "action", json.RawMessage(`{"name":"open_command_palette"}`), true
	case !mod.Cmd && !mod.Shift && !mod.Alt && key == "/":
		return "action", json.RawMessage(`{"name":"open_command_palette"}`), true
	case mod.Cmd && !mod.Shift && !mod.Alt && key == "/":
		return "action", json.RawMessage(`{"name":"show_shortcuts_help"}`), true
	case !mod.Cmd && !mod.Shift && !mod.Alt && key == "escape":
		return "action", json.RawMessage(`{"name":"close_modal"}`), true
	case mod.Cmd && !mod.Shift && !mod.Alt && key == "n":
		return "create", json.RawMessage(`{"resource":"task"}`), true
	default:
		return "", nil, false
	}
}

func executeNavigateCommand(params json.RawMessage) (CommandExecuteResponse, *commandExecuteError) {
	urlValue, err := parseRequiredStringFieldAny(params, "url", "path")
	if err != nil {
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: err.Error()}
	}

	urlValue = strings.TrimSpace(urlValue)
	if urlValue == "" {
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: "missing url"}
	}

	if !strings.HasPrefix(urlValue, "/") || strings.HasPrefix(urlValue, "//") {
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid url"}
	}
	if parsed, parseErr := url.Parse(urlValue); parseErr != nil || parsed.Scheme != "" || parsed.Host != "" {
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid url"}
	}

	return CommandExecuteResponse{
		OK:          true,
		Type:        "navigate",
		RedirectURL: urlValue,
	}, nil
}

func executeCreateCommand(r *http.Request, params json.RawMessage) (CommandExecuteResponse, *commandExecuteError) {
	resource, err := parseRequiredStringFieldAny(params, "resource", "entity", "kind")
	if err != nil {
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: err.Error()}
	}

	resource = strings.ToLower(strings.TrimSpace(resource))
	switch resource {
	case "task":
		task, createErr := executeCreateTaskCommand(r, params)
		if createErr != nil {
			return CommandExecuteResponse{}, createErr
		}
		return CommandExecuteResponse{
			OK:          true,
			Type:        "create",
			RedirectURL: fmt.Sprintf("/tasks/%d", task.Number),
			Result:      task,
		}, nil
	default:
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: "unsupported resource"}
	}
}

func executeCreateTaskCommand(r *http.Request, params json.RawMessage) (Task, *commandExecuteError) {
	rawParams, err := parseParamsObject(params)
	if err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid parameters"}
	}

	orgID, err := parseRequiredTrimmedStringFieldAny(rawParams, "org_id", "orgId")
	if err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: err.Error()}
	}
	if !uuidRegex.MatchString(orgID) {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid org_id"}
	}

	title, err := parseRequiredTrimmedStringFieldAny(rawParams, "title")
	if err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: err.Error()}
	}

	description, _, err := parseOptionalStringFieldAny(rawParams, "description")
	if err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid description"}
	}

	statusValue, _, err := parseOptionalStringFieldAny(rawParams, "status")
	if err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid status"}
	}
	status := ""
	if statusValue != nil {
		status = *statusValue
	}
	status = normalizeStatus(status)
	if status == "" {
		status = "queued"
	}
	if !isValidStatus(status) {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid status"}
	}

	priorityValue, _, err := parseOptionalStringFieldAny(rawParams, "priority")
	if err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid priority"}
	}
	priority := ""
	if priorityValue != nil {
		priority = *priorityValue
	}
	priority = normalizePriority(priority)
	if priority == "" {
		priority = "P2"
	}
	if !isValidPriority(priority) {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid priority"}
	}

	projectID, _, err := parseOptionalStringFieldAny(rawParams, "project_id", "projectId")
	if err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid project_id"}
	}
	if err := validateOptionalUUID(projectID, "project_id"); err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: err.Error()}
	}

	assignedAgentID, _, err := parseOptionalStringFieldAny(rawParams, "assigned_agent_id", "assignedAgentId")
	if err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid assigned_agent_id"}
	}
	if err := validateOptionalUUID(assignedAgentID, "assigned_agent_id"); err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: err.Error()}
	}

	parentTaskID, _, err := parseOptionalStringFieldAny(rawParams, "parent_task_id", "parentTaskId")
	if err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid parent_task_id"}
	}
	if err := validateOptionalUUID(parentTaskID, "parent_task_id"); err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: err.Error()}
	}

	context, _, err := parseOptionalRawField(rawParams, "context")
	if err != nil {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid context"}
	}
	contextBytes := normalizeContext(context)
	if !json.Valid(contextBytes) {
		return Task{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid context"}
	}

	db, dbErr := getTasksDB()
	if dbErr != nil {
		return Task{}, &commandExecuteError{status: http.StatusServiceUnavailable, msg: dbErr.Error()}
	}

	args := []interface{}{
		orgID,
		nullableString(projectID),
		title,
		nullableString(description),
		status,
		priority,
		contextBytes,
		nullableString(assignedAgentID),
		nullableString(parentTaskID),
	}

	task, scanErr := scanTask(db.QueryRowContext(r.Context(), createTaskSQL, args...))
	if scanErr != nil {
		return Task{}, &commandExecuteError{status: http.StatusInternalServerError, msg: "failed to create task"}
	}

	return task, nil
}

func executeSearchCommand(r *http.Request, params json.RawMessage) (CommandExecuteResponse, *commandExecuteError) {
	rawParams, err := parseParamsObject(params)
	if err != nil {
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid parameters"}
	}

	query, err := parseRequiredTrimmedStringFieldAny(rawParams, "q", "query")
	if err != nil {
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: err.Error()}
	}

	orgID, err := parseRequiredTrimmedStringFieldAny(rawParams, "org_id", "orgId")
	if err != nil {
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: err.Error()}
	}

	modePtr, _, err := parseOptionalStringFieldAny(rawParams, "mode", "scope")
	if err != nil {
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid mode"}
	}
	mode := "commands"
	if modePtr != nil && strings.TrimSpace(*modePtr) != "" {
		mode = strings.ToLower(strings.TrimSpace(*modePtr))
	}

	var handler http.HandlerFunc
	endpoint := ""

	switch mode {
	case "commands":
		handler = CommandSearchHandler
		endpoint = "/api/commands/search"
	case "global":
		handler = SearchHandler
		endpoint = "/api/search"
	default:
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid mode"}
	}

	queryValues := url.Values{}
	queryValues.Set("q", query)
	queryValues.Set("org_id", orgID)
	if mode == "global" {
		if rawLimit, ok := rawParams["limit"]; ok && len(rawLimit) > 0 && string(rawLimit) != "null" {
			limitValue := ""
			var parsedInt int
			if err := json.Unmarshal(rawLimit, &parsedInt); err == nil {
				limitValue = fmt.Sprintf("%d", parsedInt)
			} else {
				var parsedString string
				if err := json.Unmarshal(rawLimit, &parsedString); err == nil {
					limitValue = strings.TrimSpace(parsedString)
				}
			}
			if limitValue != "" {
				queryValues.Set("limit", limitValue)
			}
		}
	}

	target := endpoint + "?" + queryValues.Encode()
	req := httptest.NewRequest(http.MethodGet, target, nil).WithContext(r.Context())
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		msg := extractErrorMessage(rec.Body.Bytes())
		if msg == "" {
			msg = "search failed"
		}
		status := rec.Code
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return CommandExecuteResponse{}, &commandExecuteError{status: status, msg: msg}
	}

	return CommandExecuteResponse{
		OK:     true,
		Type:   "search",
		Result: json.RawMessage(rec.Body.Bytes()),
	}, nil
}

func executeActionCommand(params json.RawMessage) (CommandExecuteResponse, *commandExecuteError) {
	rawParams, err := parseParamsObject(params)
	if err != nil {
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: "invalid parameters"}
	}

	actionName, err := parseRequiredTrimmedStringFieldAny(rawParams, "name", "action")
	if err != nil {
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: err.Error()}
	}
	actionName = strings.ToLower(strings.TrimSpace(actionName))

	switch actionName {
	case "ping":
		return CommandExecuteResponse{
			OK:   true,
			Type: "action",
			Result: map[string]interface{}{
				"pong": true,
			},
		}, nil
	case "open_command_palette", "show_shortcuts_help", "close_modal":
		return CommandExecuteResponse{
			OK:   true,
			Type: "action",
			Result: map[string]interface{}{
				"client_action": actionName,
			},
		}, nil
	default:
		return CommandExecuteResponse{}, &commandExecuteError{status: http.StatusBadRequest, msg: "unsupported action"}
	}
}

func parseParamsObject(params json.RawMessage) (map[string]json.RawMessage, error) {
	params = normalizeJSONParams(params)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(params, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	return raw, nil
}

func parseRequiredStringFieldAny(params json.RawMessage, keys ...string) (string, error) {
	raw, err := parseParamsObject(params)
	if err != nil {
		return "", errors.New("invalid parameters")
	}
	return parseRequiredTrimmedStringFieldAny(raw, keys...)
}

func parseRequiredTrimmedStringFieldAny(raw map[string]json.RawMessage, keys ...string) (string, error) {
	valuePtr, set, err := parseOptionalStringFieldAny(raw, keys...)
	if err != nil {
		return "", errors.New("invalid " + keys[0])
	}
	if !set || valuePtr == nil {
		return "", errors.New("missing " + keys[0])
	}
	trimmed := strings.TrimSpace(*valuePtr)
	if trimmed == "" {
		return "", errors.New("missing " + keys[0])
	}
	return trimmed, nil
}

func extractErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		return strings.TrimSpace(payload.Error)
	}
	return ""
}
