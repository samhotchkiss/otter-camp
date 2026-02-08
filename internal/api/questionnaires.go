package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type QuestionnaireHandler struct {
	QuestionnaireStore *store.QuestionnaireStore
}

type createQuestionnaireRequest struct {
	Author    string                  `json:"author"`
	Title     *string                 `json:"title,omitempty"`
	Questions []questionnaireQuestion `json:"questions"`
}

type questionnaireRespondRequest struct {
	RespondedBy string         `json:"responded_by"`
	Responses   map[string]any `json:"responses"`
}

type questionnaireQuestion struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Default     any      `json:"default,omitempty"`
}

type questionnairePayload struct {
	ID          string                  `json:"id"`
	ContextType string                  `json:"context_type"`
	ContextID   string                  `json:"context_id"`
	Author      string                  `json:"author"`
	Title       *string                 `json:"title,omitempty"`
	Questions   []questionnaireQuestion `json:"questions"`
	Responses   map[string]any          `json:"responses,omitempty"`
	RespondedBy *string                 `json:"responded_by,omitempty"`
	RespondedAt *string                 `json:"responded_at,omitempty"`
	CreatedAt   string                  `json:"created_at"`
}

const (
	maxQuestionnaireQuestionCount = 100
	maxQuestionnaireOptionCount   = 200
)

func (h *QuestionnaireHandler) CreateIssueQuestionnaire(w http.ResponseWriter, r *http.Request) {
	h.createQuestionnaireForContext(w, r, store.QuestionnaireContextIssue)
}

func (h *QuestionnaireHandler) CreateProjectChatQuestionnaire(w http.ResponseWriter, r *http.Request) {
	h.createQuestionnaireForContext(w, r, store.QuestionnaireContextProjectChat)
}

func (h *QuestionnaireHandler) createQuestionnaireForContext(
	w http.ResponseWriter,
	r *http.Request,
	contextType string,
) {
	if h.QuestionnaireStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	contextID := strings.TrimSpace(chi.URLParam(r, "id"))
	if contextID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "context id is required"})
		return
	}

	var req createQuestionnaireRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}
	if strings.TrimSpace(req.Author) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "author is required"})
		return
	}

	questions, err := normalizeQuestionnaireQuestions(req.Questions)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	questionsJSON, err := json.Marshal(questions)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to encode questions"})
		return
	}

	record, err := h.QuestionnaireStore.Create(r.Context(), store.CreateQuestionnaireInput{
		ContextType: contextType,
		ContextID:   contextID,
		Author:      req.Author,
		Title:       req.Title,
		Questions:   questionsJSON,
	})
	if err != nil {
		handleQuestionnaireStoreError(w, err)
		return
	}

	payload, err := toQuestionnairePayload(*record)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to encode questionnaire"})
		return
	}
	sendJSON(w, http.StatusCreated, payload)
}

func (h *QuestionnaireHandler) Respond(w http.ResponseWriter, r *http.Request) {
	if h.QuestionnaireStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	questionnaireID := strings.TrimSpace(chi.URLParam(r, "id"))
	if questionnaireID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "questionnaire id is required"})
		return
	}

	var req questionnaireRespondRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}
	if req.Responses == nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "responses is required"})
		return
	}
	if strings.TrimSpace(req.RespondedBy) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "responded_by is required"})
		return
	}

	current, err := h.QuestionnaireStore.GetByID(r.Context(), questionnaireID)
	if err != nil {
		handleQuestionnaireStoreError(w, err)
		return
	}

	questions, err := decodeQuestionnaireQuestions(current.Questions)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "questionnaire definition is invalid"})
		return
	}

	normalizedResponses, err := normalizeQuestionnaireResponses(questions, req.Responses)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	responsesJSON, err := json.Marshal(normalizedResponses)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to encode responses"})
		return
	}

	updated, err := h.QuestionnaireStore.Respond(r.Context(), store.RespondQuestionnaireInput{
		QuestionnaireID: questionnaireID,
		RespondedBy:     req.RespondedBy,
		Responses:       responsesJSON,
	})
	if err != nil {
		handleQuestionnaireStoreError(w, err)
		return
	}

	payload, err := toQuestionnairePayload(*updated)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to encode questionnaire"})
		return
	}
	sendJSON(w, http.StatusOK, payload)
}

func toQuestionnairePayload(record store.Questionnaire) (questionnairePayload, error) {
	questions, err := decodeQuestionnaireQuestions(record.Questions)
	if err != nil {
		return questionnairePayload{}, err
	}

	var responses map[string]any
	if len(record.Responses) > 0 {
		if err := json.Unmarshal(record.Responses, &responses); err != nil {
			return questionnairePayload{}, err
		}
	}

	var respondedAt *string
	if record.RespondedAt != nil {
		value := record.RespondedAt.UTC().Format(time.RFC3339)
		respondedAt = &value
	}

	return questionnairePayload{
		ID:          record.ID,
		ContextType: record.ContextType,
		ContextID:   record.ContextID,
		Author:      record.Author,
		Title:       record.Title,
		Questions:   questions,
		Responses:   responses,
		RespondedBy: record.RespondedBy,
		RespondedAt: respondedAt,
		CreatedAt:   record.CreatedAt.UTC().Format(time.RFC3339),
	}, nil
}

func mapQuestionnairePayloads(records []store.Questionnaire) ([]questionnairePayload, error) {
	out := make([]questionnairePayload, 0, len(records))
	for _, record := range records {
		payload, err := toQuestionnairePayload(record)
		if err != nil {
			return nil, err
		}
		out = append(out, payload)
	}
	return out, nil
}

func decodeQuestionnaireQuestions(raw json.RawMessage) ([]questionnaireQuestion, error) {
	var questions []questionnaireQuestion
	if err := json.Unmarshal(raw, &questions); err != nil {
		return nil, err
	}
	return questions, nil
}

func normalizeQuestionnaireQuestions(raw []questionnaireQuestion) ([]questionnaireQuestion, error) {
	if len(raw) == 0 {
		return nil, errors.New("questions is required")
	}
	if len(raw) > maxQuestionnaireQuestionCount {
		return nil, fmt.Errorf("questions exceeds max count of %d", maxQuestionnaireQuestionCount)
	}

	normalized := make([]questionnaireQuestion, 0, len(raw))
	ids := make(map[string]struct{}, len(raw))
	for _, question := range raw {
		id := strings.TrimSpace(question.ID)
		if id == "" {
			return nil, errors.New("question id is required")
		}
		if _, exists := ids[id]; exists {
			return nil, fmt.Errorf("duplicate question id: %s", id)
		}
		ids[id] = struct{}{}

		text := strings.TrimSpace(question.Text)
		if text == "" {
			return nil, fmt.Errorf("question %s text is required", id)
		}

		qType := strings.TrimSpace(strings.ToLower(question.Type))
		if !isSupportedQuestionType(qType) {
			return nil, fmt.Errorf("question %s has unsupported type", id)
		}

		options := normalizeQuestionnaireOptions(question.Options)
		if len(options) > maxQuestionnaireOptionCount {
			return nil, fmt.Errorf(
				"question %s options exceeds max count of %d",
				id,
				maxQuestionnaireOptionCount,
			)
		}
		if (qType == "select" || qType == "multiselect") && len(options) == 0 {
			return nil, fmt.Errorf("question %s requires options", id)
		}
		if qType != "select" && qType != "multiselect" {
			options = nil
		}

		placeholder := ""
		if qType == "text" || qType == "textarea" {
			placeholder = strings.TrimSpace(question.Placeholder)
		}

		normalizedQuestion := questionnaireQuestion{
			ID:          id,
			Text:        text,
			Type:        qType,
			Required:    question.Required,
			Options:     options,
			Placeholder: placeholder,
		}

		if question.Default != nil {
			value, provided, err := normalizeQuestionnaireValue(normalizedQuestion, question.Default)
			if err != nil {
				return nil, fmt.Errorf("question %s default is invalid: %w", id, err)
			}
			if provided {
				normalizedQuestion.Default = value
			}
		}

		normalized = append(normalized, normalizedQuestion)
	}
	return normalized, nil
}

func normalizeQuestionnaireResponses(
	questions []questionnaireQuestion,
	raw map[string]any,
) (map[string]any, error) {
	questionByID := make(map[string]questionnaireQuestion, len(questions))
	for _, question := range questions {
		questionByID[question.ID] = question
	}

	for questionID := range raw {
		if _, ok := questionByID[questionID]; !ok {
			return nil, fmt.Errorf("unknown response key: %s", questionID)
		}
	}

	normalized := make(map[string]any)
	for _, question := range questions {
		rawValue, provided := raw[question.ID]
		if !provided || rawValue == nil {
			if question.Required {
				return nil, fmt.Errorf("question %s is required", question.ID)
			}
			continue
		}

		value, hasValue, err := normalizeQuestionnaireValue(question, rawValue)
		if err != nil {
			return nil, fmt.Errorf("question %s has invalid response: %w", question.ID, err)
		}
		if !hasValue {
			if question.Required {
				return nil, fmt.Errorf("question %s is required", question.ID)
			}
			continue
		}

		normalized[question.ID] = value
	}
	return normalized, nil
}

func normalizeQuestionnaireValue(question questionnaireQuestion, rawValue any) (any, bool, error) {
	switch question.Type {
	case "text", "textarea":
		value, ok := rawValue.(string)
		if !ok {
			return nil, false, errors.New("must be a string")
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, false, nil
		}
		return trimmed, true, nil
	case "boolean":
		value, ok := rawValue.(bool)
		if !ok {
			return nil, false, errors.New("must be a boolean")
		}
		return value, true, nil
	case "select":
		value, ok := rawValue.(string)
		if !ok {
			return nil, false, errors.New("must be a string")
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, false, nil
		}
		if !containsQuestionnaireOption(question.Options, trimmed) {
			return nil, false, errors.New("must be one of the defined options")
		}
		return trimmed, true, nil
	case "multiselect":
		values, ok := rawValue.([]any)
		if !ok {
			return nil, false, errors.New("must be an array")
		}
		out := make([]string, 0, len(values))
		seen := make(map[string]struct{}, len(values))
		for _, entry := range values {
			text, ok := entry.(string)
			if !ok {
				return nil, false, errors.New("entries must be strings")
			}
			trimmed := strings.TrimSpace(text)
			if trimmed == "" {
				continue
			}
			if !containsQuestionnaireOption(question.Options, trimmed) {
				return nil, false, errors.New("entry must be one of the defined options")
			}
			if _, exists := seen[trimmed]; exists {
				continue
			}
			seen[trimmed] = struct{}{}
			out = append(out, trimmed)
		}
		if len(out) == 0 {
			return nil, false, nil
		}
		return out, true, nil
	case "number":
		value, ok := rawValue.(float64)
		if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, false, errors.New("must be a finite number")
		}
		return value, true, nil
	case "date":
		value, ok := rawValue.(string)
		if !ok {
			return nil, false, errors.New("must be a string")
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, false, nil
		}
		if _, err := time.Parse("2006-01-02", trimmed); err != nil {
			return nil, false, errors.New("must be an ISO date (YYYY-MM-DD)")
		}
		return trimmed, true, nil
	default:
		return nil, false, errors.New("unsupported question type")
	}
}

func isSupportedQuestionType(questionType string) bool {
	switch questionType {
	case "text", "textarea", "boolean", "select", "multiselect", "number", "date":
		return true
	default:
		return false
	}
}

func normalizeQuestionnaireOptions(options []string) []string {
	if len(options) == 0 {
		return nil
	}
	out := make([]string, 0, len(options))
	seen := make(map[string]struct{}, len(options))
	for _, option := range options {
		trimmed := strings.TrimSpace(option)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func containsQuestionnaireOption(options []string, candidate string) bool {
	for _, option := range options {
		if option == candidate {
			return true
		}
	}
	return false
}

func handleQuestionnaireStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace context"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	case errors.Is(err, store.ErrQuestionnaireAlreadyResponded):
		sendJSON(w, http.StatusConflict, errorResponse{Error: "questionnaire already responded"})
	default:
		log.Printf("questionnaire store error: %v", err)
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal error"})
	}
}
