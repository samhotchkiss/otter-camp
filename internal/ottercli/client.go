package ottercli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	OrgID   string
	HTTP    *http.Client
}

const maxClientResponseBodyBytes = 1 << 20

type RequestError struct {
	StatusCode int
	Detail     string
}

func (e *RequestError) Error() string {
	if e == nil {
		return "request failed"
	}
	if strings.TrimSpace(e.Detail) == "" {
		return fmt.Sprintf("request failed (%d)", e.StatusCode)
	}
	return fmt.Sprintf("request failed (%d): %s", e.StatusCode, e.Detail)
}

func (e *RequestError) HTTPStatusCode() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

type ResponseDecodeError struct {
	StatusCode int
	Detail     string
}

func (e *ResponseDecodeError) Error() string {
	if e == nil {
		return "invalid response"
	}
	if strings.TrimSpace(e.Detail) == "" {
		return fmt.Sprintf("invalid response (%d)", e.StatusCode)
	}
	return fmt.Sprintf("invalid response (%d): %s", e.StatusCode, e.Detail)
}

func (e *ResponseDecodeError) HTTPStatusCode() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

// HTTPStatusCode returns the HTTP status carried by typed client errors.
func HTTPStatusCode(err error) (int, bool) {
	var statusErr interface {
		HTTPStatusCode() int
	}
	if !errors.As(err, &statusErr) {
		return 0, false
	}
	status := statusErr.HTTPStatusCode()
	if status <= 0 {
		return 0, false
	}
	return status, true
}

func NewClient(cfg Config, orgOverride string) (*Client, error) {
	org := strings.TrimSpace(orgOverride)
	if org == "" {
		org = strings.TrimSpace(cfg.DefaultOrg)
	}
	return &Client{
		BaseURL: normalizeAPIBaseURL(cfg.APIBaseURL),
		Token:   strings.TrimSpace(cfg.Token),
		OrgID:   org,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

func (c *Client) requireAuth() error {
	if strings.TrimSpace(c.Token) == "" {
		return errors.New("missing auth token; run `otter auth login --token <token> --org <org-id>`")
	}
	if strings.TrimSpace(c.OrgID) == "" {
		return errors.New("missing org id; pass --org or set defaultOrg in config")
	}
	return nil
}

func (c *Client) newRequest(method, path string, body io.Reader) (*http.Request, error) {
	baseURL := normalizeAPIBaseURL(c.BaseURL)
	if baseURL == "" {
		return nil, errors.New("missing API base URL")
	}
	endpoint := baseURL + path
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.Token))
		req.Header.Set("X-Session-Token", strings.TrimSpace(c.Token))
	}
	if strings.TrimSpace(c.OrgID) != "" {
		req.Header.Set("X-Org-ID", strings.TrimSpace(c.OrgID))
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) do(req *http.Request, out interface{}) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, maxClientResponseBodyBytes))
	if readErr != nil {
		return readErr
	}

	if resp.StatusCode >= 400 {
		return &RequestError{
			StatusCode: resp.StatusCode,
			Detail:     summarizeResponseBody(resp.Header.Get("Content-Type"), payload),
		}
	}

	if out == nil {
		return nil
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return io.EOF
	}
	if err := json.Unmarshal(payload, out); err != nil {
		detail := classifyDecodeErrorDetail(resp.Header.Get("Content-Type"), payload)
		if detail == "" {
			detail = fmt.Sprintf("invalid JSON response: %v", err)
		}
		return &ResponseDecodeError{
			StatusCode: resp.StatusCode,
			Detail:     detail,
		}
	}
	return nil
}

func summarizeResponseBody(contentType string, payload []byte) string {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return ""
	}
	if isLikelyHTMLResponse(contentType, trimmed) {
		return "html response body omitted"
	}
	if msg, ok := extractJSONErrorSummary(payload, contentType); ok {
		return msg
	}
	return truncateResponseText(trimmed, 200)
}

func classifyDecodeErrorDetail(contentType string, payload []byte) string {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" {
		return "empty response body"
	}
	if isLikelyHTMLResponse(contentType, trimmed) {
		return "expected JSON response but received HTML"
	}
	if !looksLikeJSONContent(contentType, trimmed) {
		return "expected JSON response but received non-JSON body"
	}
	return ""
}

func extractJSONErrorSummary(payload []byte, contentType string) (string, bool) {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" || !looksLikeJSONContent(contentType, trimmed) {
		return "", false
	}

	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return "", false
	}

	for _, key := range []string{"error", "message", "detail"} {
		raw, ok := body[key]
		if !ok {
			continue
		}
		switch value := raw.(type) {
		case string:
			value = strings.TrimSpace(value)
			if value != "" {
				return truncateResponseText(value, 200), true
			}
		case map[string]any:
			if nested, ok := value["message"].(string); ok && strings.TrimSpace(nested) != "" {
				return truncateResponseText(strings.TrimSpace(nested), 200), true
			}
		}
	}

	return "", false
}

func looksLikeJSONContent(contentType, body string) bool {
	if isJSONContentType(contentType) {
		return true
	}
	if body == "" {
		return false
	}
	return strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[")
}

func isJSONContentType(contentType string) bool {
	value := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if value == "" {
		return false
	}
	return value == "application/json" || value == "text/json" || strings.HasSuffix(value, "+json")
}

func isLikelyHTMLResponse(contentType, body string) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml+xml") {
		return true
	}
	lowerBody := strings.ToLower(strings.TrimSpace(body))
	return strings.HasPrefix(lowerBody, "<!doctype html") || strings.HasPrefix(lowerBody, "<html")
}

func truncateResponseText(value string, max int) string {
	collapsed := strings.Join(strings.Fields(value), " ")
	if len(collapsed) <= max {
		return collapsed
	}
	if max <= 3 {
		return collapsed[:max]
	}
	return collapsed[:max-3] + "..."
}

type Project struct {
	ID                 string         `json:"id"`
	OrgID              string         `json:"org_id"`
	Name               string         `json:"name"`
	URLSlug            string         `json:"slug"`
	Description        string         `json:"description"`
	RepoURL            string         `json:"repo_url"`
	Status             string         `json:"status"`
	RequireHumanReview bool           `json:"require_human_review"`
	WorkflowEnabled    bool           `json:"workflow_enabled"`
	WorkflowSchedule   map[string]any `json:"workflow_schedule,omitempty"`
	WorkflowTemplate   map[string]any `json:"workflow_template,omitempty"`
	WorkflowAgentID    *string        `json:"workflow_agent_id,omitempty"`
	WorkflowRunCount   int            `json:"workflow_run_count"`
}

type projectListResponse struct {
	Projects []Project `json:"projects"`
	Total    int       `json:"total"`
}

type Agent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Role  string `json:"role,omitempty"`
	Emoji string `json:"emoji,omitempty"`
}

type agentListResponse struct {
	Agents []Agent `json:"agents"`
}

type Issue struct {
	ID            string  `json:"id"`
	ProjectID     string  `json:"project_id"`
	IssueNumber   int64   `json:"issue_number"`
	Title         string  `json:"title"`
	Body          *string `json:"body,omitempty"`
	State         string  `json:"state"`
	Origin        string  `json:"origin"`
	ApprovalState string  `json:"approval_state"`
	OwnerAgentID  *string `json:"owner_agent_id,omitempty"`
	WorkStatus    string  `json:"work_status"`
	Priority      string  `json:"priority"`
	DueAt         *string `json:"due_at,omitempty"`
	NextStep      *string `json:"next_step,omitempty"`
	NextStepDueAt *string `json:"next_step_due_at,omitempty"`
}

type QuestionnaireQuestion struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Options     []string `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Default     any      `json:"default,omitempty"`
}

type Questionnaire struct {
	ID          string                  `json:"id"`
	ContextType string                  `json:"context_type"`
	ContextID   string                  `json:"context_id"`
	Author      string                  `json:"author"`
	Title       *string                 `json:"title,omitempty"`
	Questions   []QuestionnaireQuestion `json:"questions"`
	Responses   map[string]any          `json:"responses,omitempty"`
	RespondedBy *string                 `json:"responded_by,omitempty"`
	RespondedAt *string                 `json:"responded_at,omitempty"`
	CreatedAt   string                  `json:"created_at"`
}

type CreateIssueQuestionnaireInput struct {
	Author    string                  `json:"author"`
	Title     *string                 `json:"title,omitempty"`
	Questions []QuestionnaireQuestion `json:"questions"`
}

type RespondIssueQuestionnaireInput struct {
	RespondedBy string         `json:"responded_by"`
	Responses   map[string]any `json:"responses"`
}

type issueListResponse struct {
	Items []Issue `json:"items"`
	Total int     `json:"total"`
}

type issueDetailResponse struct {
	Issue Issue `json:"issue"`
}

type AgentJob struct {
	ID                  string  `json:"id"`
	OrgID               string  `json:"org_id"`
	AgentID             string  `json:"agent_id"`
	Name                string  `json:"name"`
	Description         *string `json:"description,omitempty"`
	ScheduleKind        string  `json:"schedule_kind"`
	CronExpr            *string `json:"cron_expr,omitempty"`
	IntervalMS          *int64  `json:"interval_ms,omitempty"`
	RunAt               *string `json:"run_at,omitempty"`
	Timezone            string  `json:"timezone"`
	PayloadKind         string  `json:"payload_kind"`
	PayloadText         string  `json:"payload_text"`
	RoomID              *string `json:"room_id,omitempty"`
	Enabled             bool    `json:"enabled"`
	Status              string  `json:"status"`
	LastRunAt           *string `json:"last_run_at,omitempty"`
	LastRunStatus       *string `json:"last_run_status,omitempty"`
	LastRunError        *string `json:"last_run_error,omitempty"`
	NextRunAt           *string `json:"next_run_at,omitempty"`
	RunCount            int     `json:"run_count"`
	ErrorCount          int     `json:"error_count"`
	MaxFailures         int     `json:"max_failures"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	CreatedBy           *string `json:"created_by,omitempty"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
}

type AgentJobRun struct {
	ID          string  `json:"id"`
	JobID       string  `json:"job_id"`
	OrgID       string  `json:"org_id"`
	Status      string  `json:"status"`
	StartedAt   string  `json:"started_at"`
	CompletedAt *string `json:"completed_at,omitempty"`
	DurationMS  *int    `json:"duration_ms,omitempty"`
	Error       *string `json:"error,omitempty"`
	PayloadText string  `json:"payload_text"`
	MessageID   *string `json:"message_id,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

type AgentJobListResponse struct {
	Items []AgentJob `json:"items"`
	Total int        `json:"total"`
}

type AgentJobRunsResponse struct {
	Items []AgentJobRun `json:"items"`
	Total int           `json:"total"`
}

type OpenClawCronJobImportResult struct {
	Total    int      `json:"total"`
	Imported int      `json:"imported"`
	Updated  int      `json:"updated"`
	Skipped  int      `json:"skipped"`
	Warnings []string `json:"warnings,omitempty"`
}

type PipelineRoleAssignment struct {
	AgentID *string `json:"agentId"`
}

type PipelineRoles struct {
	Planner  PipelineRoleAssignment `json:"planner"`
	Worker   PipelineRoleAssignment `json:"worker"`
	Reviewer PipelineRoleAssignment `json:"reviewer"`
}

type DeployConfig struct {
	DeployMethod  string  `json:"deployMethod"`
	GitHubRepoURL *string `json:"githubRepoUrl,omitempty"`
	GitHubBranch  string  `json:"githubBranch"`
	CLICommand    *string `json:"cliCommand,omitempty"`
}

type RoomTokenSenderStats struct {
	SenderID    string `json:"sender_id"`
	SenderType  string `json:"sender_type"`
	TotalTokens int64  `json:"total_tokens"`
}

type RoomTokenStats struct {
	RoomID                   string                 `json:"room_id"`
	RoomName                 string                 `json:"room_name"`
	TotalTokens              int64                  `json:"total_tokens"`
	ConversationCount        int                    `json:"conversation_count"`
	AvgTokensPerConversation int64                  `json:"avg_tokens_per_conversation"`
	Last7DaysTokens          int64                  `json:"last_7_days_tokens"`
	TokensBySender           []RoomTokenSenderStats `json:"tokens_by_sender"`
}

func (c *Client) ListProjects() ([]Project, error) {
	return c.ListProjectsWithWorkflow(false)
}

func (c *Client) ListProjectsWithWorkflow(workflowOnly bool) ([]Project, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	path := "/api/projects"
	if workflowOnly {
		path = "/api/projects?workflow=true"
	}
	req, err := c.newRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var resp projectListResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
}

type ProjectRun struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"project_id"`
	IssueNumber int64   `json:"issue_number"`
	Title       string  `json:"title"`
	State       string  `json:"state"`
	WorkStatus  string  `json:"work_status"`
	Priority    string  `json:"priority"`
	CreatedAt   string  `json:"created_at"`
	ClosedAt    *string `json:"closed_at,omitempty"`
}

type projectRunListResponse struct {
	Runs []ProjectRun `json:"runs"`
}

type projectRunTriggerResponse struct {
	Run       ProjectRun `json:"run"`
	RunNumber int        `json:"run_number"`
}

func (c *Client) TriggerProjectRun(projectID string) (projectRunTriggerResponse, error) {
	if err := c.requireAuth(); err != nil {
		return projectRunTriggerResponse{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return projectRunTriggerResponse{}, errors.New("project id is required")
	}
	req, err := c.newRequest(http.MethodPost, "/api/projects/"+url.PathEscape(projectID)+"/runs/trigger", nil)
	if err != nil {
		return projectRunTriggerResponse{}, err
	}
	var response projectRunTriggerResponse
	if err := c.do(req, &response); err != nil {
		return projectRunTriggerResponse{}, err
	}
	return response, nil
}

func (c *Client) ListProjectRuns(projectID string, limit int) ([]ProjectRun, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("project id is required")
	}
	if limit <= 0 {
		limit = 20
	}
	path := fmt.Sprintf("/api/projects/%s/runs?limit=%d", url.PathEscape(projectID), limit)
	req, err := c.newRequest(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var response projectRunListResponse
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response.Runs, nil
}

func (c *Client) CreateProject(input map[string]interface{}) (Project, error) {
	if err := c.requireAuth(); err != nil {
		return Project{}, err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return Project{}, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/projects", bytes.NewReader(payload))
	if err != nil {
		return Project{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var project Project
	if err := c.do(req, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (c *Client) GetRoomStats(roomID string) (RoomTokenStats, error) {
	if err := c.requireAuth(); err != nil {
		return RoomTokenStats{}, err
	}
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return RoomTokenStats{}, errors.New("room id is required")
	}

	req, err := c.newRequest(http.MethodGet, "/api/v1/rooms/"+url.PathEscape(roomID)+"/stats", nil)
	if err != nil {
		return RoomTokenStats{}, err
	}

	var stats RoomTokenStats
	if err := c.do(req, &stats); err != nil {
		return RoomTokenStats{}, err
	}
	return stats, nil
}

func (c *Client) GetProject(projectID string) (Project, error) {
	if err := c.requireAuth(); err != nil {
		return Project{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return Project{}, errors.New("project id is required")
	}
	req, err := c.newRequest(http.MethodGet, "/api/projects/"+url.PathEscape(projectID), nil)
	if err != nil {
		return Project{}, err
	}
	var project Project
	if err := c.do(req, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (c *Client) PatchProject(projectID string, input map[string]interface{}) (Project, error) {
	if err := c.requireAuth(); err != nil {
		return Project{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return Project{}, errors.New("project id is required")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return Project{}, err
	}
	req, err := c.newRequest(http.MethodPatch, "/api/projects/"+url.PathEscape(projectID), bytes.NewReader(payload))
	if err != nil {
		return Project{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var project Project
	if err := c.do(req, &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (c *Client) SetProjectRequireHumanReview(projectID string, requireHumanReview bool) (Project, error) {
	return c.PatchProject(projectID, map[string]interface{}{
		"requireHumanReview": requireHumanReview,
	})
}

func (c *Client) GetPipelineRoles(projectID string) (PipelineRoles, error) {
	if err := c.requireAuth(); err != nil {
		return PipelineRoles{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return PipelineRoles{}, errors.New("project id is required")
	}

	req, err := c.newRequest(http.MethodGet, "/api/projects/"+url.PathEscape(projectID)+"/pipeline-roles", nil)
	if err != nil {
		return PipelineRoles{}, err
	}

	var roles PipelineRoles
	if err := c.do(req, &roles); err != nil {
		return PipelineRoles{}, err
	}
	return roles, nil
}

func (c *Client) SetPipelineRoles(projectID string, roles PipelineRoles) (PipelineRoles, error) {
	if err := c.requireAuth(); err != nil {
		return PipelineRoles{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return PipelineRoles{}, errors.New("project id is required")
	}

	payload, err := json.Marshal(roles)
	if err != nil {
		return PipelineRoles{}, err
	}
	req, err := c.newRequest(http.MethodPut, "/api/projects/"+url.PathEscape(projectID)+"/pipeline-roles", bytes.NewReader(payload))
	if err != nil {
		return PipelineRoles{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	var updated PipelineRoles
	if err := c.do(req, &updated); err != nil {
		return PipelineRoles{}, err
	}
	return updated, nil
}

func (c *Client) GetDeployConfig(projectID string) (DeployConfig, error) {
	if err := c.requireAuth(); err != nil {
		return DeployConfig{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return DeployConfig{}, errors.New("project id is required")
	}

	req, err := c.newRequest(http.MethodGet, "/api/projects/"+url.PathEscape(projectID)+"/deploy-config", nil)
	if err != nil {
		return DeployConfig{}, err
	}
	var cfg DeployConfig
	if err := c.do(req, &cfg); err != nil {
		return DeployConfig{}, err
	}
	return cfg, nil
}

func (c *Client) SetDeployConfig(projectID string, cfg DeployConfig) (DeployConfig, error) {
	if err := c.requireAuth(); err != nil {
		return DeployConfig{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return DeployConfig{}, errors.New("project id is required")
	}

	payload, err := json.Marshal(cfg)
	if err != nil {
		return DeployConfig{}, err
	}
	req, err := c.newRequest(http.MethodPut, "/api/projects/"+url.PathEscape(projectID)+"/deploy-config", bytes.NewReader(payload))
	if err != nil {
		return DeployConfig{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	var updated DeployConfig
	if err := c.do(req, &updated); err != nil {
		return DeployConfig{}, err
	}
	return updated, nil
}

func (c *Client) DeleteProject(projectID string) error {
	if err := c.requireAuth(); err != nil {
		return err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return errors.New("project id is required")
	}
	req, err := c.newRequest(http.MethodDelete, "/api/projects/"+url.PathEscape(projectID), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// Slug returns a filesystem-safe slug derived from the project name.
func (p Project) Slug() string {
	if slug := strings.TrimSpace(p.URLSlug); slug != "" {
		return slug
	}
	return slugify(p.Name)
}

// slugify converts a name to a lowercase, hyphen-separated, filesystem-safe string.
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = regexp.MustCompile(`[^a-z0-9\-]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "project"
	}
	return s
}

func (c *Client) FindProject(query string) (Project, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Project{}, errors.New("project name or id is required")
	}
	projects, err := c.ListProjects()
	if err != nil {
		return Project{}, err
	}
	querySlug := slugify(query)
	var matches []Project
	for _, p := range projects {
		if strings.EqualFold(p.ID, query) || strings.EqualFold(p.Name, query) || p.Slug() == querySlug {
			matches = append(matches, p)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return Project{}, fmt.Errorf("project not found: %s", query)
	}
	return Project{}, fmt.Errorf("multiple projects matched %q; use project ID", query)
}

type whoamiResponse struct {
	Valid   bool   `json:"valid"`
	OrgID   string `json:"org_id"`
	OrgSlug string `json:"org_slug"`
	User    struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"user"`
}

type OnboardingBootstrapRequest struct {
	Name             string `json:"name"`
	Email            string `json:"email"`
	OrganizationName string `json:"organization_name"`
}

type OnboardingAgent struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

type OnboardingBootstrapResponse struct {
	OrgID       string            `json:"org_id"`
	OrgSlug     string            `json:"org_slug"`
	UserID      string            `json:"user_id"`
	Token       string            `json:"token"`
	ExpiresAt   time.Time         `json:"expires_at"`
	ProjectID   string            `json:"project_id"`
	ProjectName string            `json:"project_name"`
	IssueID     string            `json:"issue_id"`
	IssueNumber int64             `json:"issue_number"`
	IssueTitle  string            `json:"issue_title"`
	Agents      []OnboardingAgent `json:"agents"`
}

func (c *Client) WhoAmI() (whoamiResponse, error) {
	if strings.TrimSpace(c.Token) == "" {
		return whoamiResponse{}, errors.New("missing auth token")
	}
	baseURL := normalizeAPIBaseURL(c.BaseURL)
	if baseURL == "" {
		return whoamiResponse{}, errors.New("missing API base URL")
	}
	q := url.Values{}
	q.Set("token", strings.TrimSpace(c.Token))
	endpoint := baseURL + "/api/auth/validate?" + q.Encode()
	// Build request without Bearer header — validate endpoint reads token from query param only.
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return whoamiResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	var resp whoamiResponse
	if err := c.do(req, &resp); err != nil {
		return whoamiResponse{}, err
	}
	return resp, nil
}

func normalizeAPIBaseURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		trimmed := strings.TrimRight(value, "/")
		return strings.TrimSuffix(trimmed, "/api")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(parsed.Path, "/api") {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/api")
	}
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return strings.TrimRight(parsed.String(), "/")
}

func (c *Client) OnboardingBootstrap(input OnboardingBootstrapRequest) (OnboardingBootstrapResponse, error) {
	if strings.TrimSpace(input.Name) == "" {
		return OnboardingBootstrapResponse{}, errors.New("name is required")
	}
	if strings.TrimSpace(input.Email) == "" {
		return OnboardingBootstrapResponse{}, errors.New("email is required")
	}
	if strings.TrimSpace(input.OrganizationName) == "" {
		return OnboardingBootstrapResponse{}, errors.New("organization_name is required")
	}

	payload, err := json.Marshal(input)
	if err != nil {
		return OnboardingBootstrapResponse{}, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/onboarding/bootstrap", bytes.NewReader(payload))
	if err != nil {
		return OnboardingBootstrapResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	var response OnboardingBootstrapResponse
	if err := c.do(req, &response); err != nil {
		return OnboardingBootstrapResponse{}, err
	}
	return response, nil
}

func (c *Client) ListAgents() ([]Agent, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	req, err := c.newRequest(http.MethodGet, "/api/agents", nil)
	if err != nil {
		return nil, err
	}
	var resp agentListResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}
	return resp.Agents, nil
}

func (c *Client) AgentWhoAmI(agentID, sessionKey, profile string) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	agentID = strings.TrimSpace(agentID)
	sessionKey = strings.TrimSpace(sessionKey)
	profile = strings.TrimSpace(strings.ToLower(profile))
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}
	if sessionKey == "" {
		return nil, errors.New("session key is required")
	}
	if profile == "" {
		profile = "compact"
	}

	q := url.Values{}
	q.Set("session_key", sessionKey)
	q.Set("profile", profile)
	req, err := c.newRequest(http.MethodGet, "/api/agents/"+url.PathEscape(agentID)+"/whoami?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := c.do(req, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (c *Client) CreateAgent(input map[string]any) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/admin/agents", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) UpdateAgent(agentID string, input map[string]any) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(http.MethodPatch, "/api/agents/"+url.PathEscape(agentID), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) ArchiveAgent(agentID string) error {
	if err := c.requireAuth(); err != nil {
		return err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return errors.New("agent id is required")
	}
	req, err := c.newRequest(http.MethodPost, "/api/admin/agents/"+url.PathEscape(agentID)+"/retire", nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) ArchiveProjectEphemeralAgents(projectID string) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("project id is required")
	}
	req, err := c.newRequest(http.MethodPost, "/api/admin/agents/retire/project/"+url.PathEscape(projectID), nil)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) WriteAgentMemory(agentID string, input map[string]any) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/agents/"+url.PathEscape(agentID)+"/memory", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) ReadAgentMemory(agentID string, days int, includeLongTerm bool) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}
	if days <= 0 {
		days = 2
	}
	q := url.Values{}
	q.Set("days", fmt.Sprintf("%d", days))
	if includeLongTerm {
		q.Set("include_long_term", "true")
	}
	req, err := c.newRequest(http.MethodGet, "/api/agents/"+url.PathEscape(agentID)+"/memory?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) SearchAgentMemory(agentID, query string, limit int) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	agentID = strings.TrimSpace(agentID)
	query = strings.TrimSpace(query)
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}
	if query == "" {
		return nil, errors.New("query is required")
	}
	if limit <= 0 {
		limit = 20
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", fmt.Sprintf("%d", limit))
	req, err := c.newRequest(http.MethodGet, "/api/agents/"+url.PathEscape(agentID)+"/memory/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) CreateMemoryEntry(input map[string]any) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/memory/entries", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) ListMemoryEntries(agentID, kind string, limit, offset int) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	q := url.Values{}
	q.Set("agent_id", agentID)
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("offset", fmt.Sprintf("%d", offset))
	if trimmedKind := strings.TrimSpace(kind); trimmedKind != "" {
		q.Set("kind", trimmedKind)
	}

	req, err := c.newRequest(http.MethodGet, "/api/memory/entries?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) SearchMemoryEntries(agentID, query string, limit int) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	agentID = strings.TrimSpace(agentID)
	query = strings.TrimSpace(query)
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}
	if query == "" {
		return nil, errors.New("query is required")
	}
	if limit <= 0 {
		limit = 20
	}

	q := url.Values{}
	q.Set("agent_id", agentID)
	q.Set("q", query)
	q.Set("limit", fmt.Sprintf("%d", limit))

	req, err := c.newRequest(http.MethodGet, "/api/memory/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) RecallMemory(agentID, query string, maxResults int) (map[string]any, error) {
	return c.RecallMemoryWithQuality(agentID, query, maxResults, 0, 2000)
}

func (c *Client) RecallMemoryWithQuality(
	agentID, query string,
	maxResults int,
	minRelevance float64,
	maxChars int,
) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	agentID = strings.TrimSpace(agentID)
	query = strings.TrimSpace(query)
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}
	if query == "" {
		return nil, errors.New("query is required")
	}
	if maxResults <= 0 {
		maxResults = 5
	}
	if minRelevance < 0 || minRelevance > 1 {
		return nil, errors.New("min relevance must be between 0 and 1")
	}
	if maxChars <= 0 {
		maxChars = 2000
	}

	q := url.Values{}
	q.Set("agent_id", agentID)
	q.Set("q", query)
	q.Set("max_results", fmt.Sprintf("%d", maxResults))
	q.Set("min_relevance", strconv.FormatFloat(minRelevance, 'f', -1, 64))
	q.Set("max_chars", fmt.Sprintf("%d", maxChars))

	req, err := c.newRequest(http.MethodGet, "/api/memory/recall?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) DeleteMemoryEntry(id string) error {
	if err := c.requireAuth(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("memory id is required")
	}

	req, err := c.newRequest(http.MethodDelete, "/api/memory/entries/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) GetLatestMemoryEvaluation() (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	req, err := c.newRequest(http.MethodGet, "/api/memory/evaluations/latest", nil)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) ListMemoryEvaluations(limit int) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	q := url.Values{}
	q.Set("limit", fmt.Sprintf("%d", limit))
	req, err := c.newRequest(http.MethodGet, "/api/memory/evaluations/runs?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) RunMemoryEvaluation(fixturePath string) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if trimmedFixture := strings.TrimSpace(fixturePath); trimmedFixture != "" {
		body["fixture_path"] = trimmedFixture
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/memory/evaluations/run", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) TuneMemoryEvaluation(apply bool) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"apply": apply,
	})
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/memory/evaluations/tune", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) ListMemoryEvents(limit int, since string, types []string) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	q := url.Values{}
	q.Set("limit", fmt.Sprintf("%d", limit))
	if trimmedSince := strings.TrimSpace(since); trimmedSince != "" {
		q.Set("since", trimmedSince)
	}
	if len(types) > 0 {
		q.Set("types", strings.Join(types, ","))
	}

	req, err := c.newRequest(http.MethodGet, "/api/memory/events?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) ListKnowledge(limit int) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	q := url.Values{}
	q.Set("limit", fmt.Sprintf("%d", limit))

	req, err := c.newRequest(http.MethodGet, "/api/knowledge?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) ImportKnowledge(entries []map[string]any) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"entries": entries,
	})
	if err != nil {
		return nil, err
	}

	req, err := c.newRequest(http.MethodPost, "/api/knowledge/import", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) ListSharedKnowledge(agentID string, limit int) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}
	if limit <= 0 {
		limit = 50
	}
	q := url.Values{}
	q.Set("agent_id", agentID)
	q.Set("limit", fmt.Sprintf("%d", limit))

	req, err := c.newRequest(http.MethodGet, "/api/shared-knowledge?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) SearchSharedKnowledge(query string, limit int, minQuality float64, kinds, statuses []string) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	if limit <= 0 {
		limit = 20
	}
	if minQuality < 0 || minQuality > 1 {
		return nil, errors.New("min_quality must be between 0 and 1")
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", fmt.Sprintf("%d", limit))
	if minQuality > 0 {
		q.Set("min_quality", strconv.FormatFloat(minQuality, 'f', -1, 64))
	}
	if len(kinds) > 0 {
		q.Set("kinds", strings.Join(kinds, ","))
	}
	if len(statuses) > 0 {
		q.Set("statuses", strings.Join(statuses, ","))
	}

	req, err := c.newRequest(http.MethodGet, "/api/shared-knowledge/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) CreateSharedKnowledge(input map[string]any) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/shared-knowledge", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) ConfirmSharedKnowledge(id string) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("knowledge id is required")
	}
	req, err := c.newRequest(http.MethodPost, "/api/shared-knowledge/"+url.PathEscape(id)+"/confirm", nil)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) ContradictSharedKnowledge(id string) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("knowledge id is required")
	}
	req, err := c.newRequest(http.MethodPost, "/api/shared-knowledge/"+url.PathEscape(id)+"/contradict", nil)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) RunReleaseGate() (map[string]any, int, error) {
	if err := c.requireAuth(); err != nil {
		return nil, 0, err
	}

	requestBody, err := json.Marshal(map[string]bool{
		"confirm": true,
	})
	if err != nil {
		return nil, 0, err
	}

	req, err := c.newRequest(
		http.MethodPost,
		"/api/admin/config/release-gate",
		bytes.NewReader(requestBody),
	)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	payload := map[string]any{}
	bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxClientResponseBodyBytes))
	if readErr != nil {
		return nil, resp.StatusCode, readErr
	}
	trimmedBody := strings.TrimSpace(string(bodyBytes))
	if trimmedBody != "" {
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			return nil, resp.StatusCode, fmt.Errorf("invalid response (%d): %s", resp.StatusCode, trimmedBody)
		}
	}

	if resp.StatusCode >= 400 {
		return payload, resp.StatusCode, fmt.Errorf("request failed (%d)", resp.StatusCode)
	}
	return payload, resp.StatusCode, nil
}

func (c *Client) ResolveAgent(query string) (Agent, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Agent{}, errors.New("agent is required")
	}
	agents, err := c.ListAgents()
	if err != nil {
		return Agent{}, err
	}
	lower := strings.ToLower(query)
	var matches []Agent
	for _, agent := range agents {
		if strings.EqualFold(agent.ID, query) ||
			strings.EqualFold(agent.Name, query) ||
			strings.EqualFold(agent.Slug, query) ||
			strings.EqualFold(strings.ToLower(agent.Name), lower) {
			matches = append(matches, agent)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return Agent{}, fmt.Errorf("agent not found: %s", query)
	}
	return Agent{}, fmt.Errorf("multiple agents matched %q; use agent id", query)
}

func (c *Client) CreateIssue(projectID string, input map[string]interface{}) (Issue, error) {
	if err := c.requireAuth(); err != nil {
		return Issue{}, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return Issue{}, errors.New("project id is required")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return Issue{}, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/projects/"+url.PathEscape(projectID)+"/issues", bytes.NewReader(payload))
	if err != nil {
		return Issue{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var issue Issue
	if err := c.do(req, &issue); err != nil {
		return Issue{}, err
	}
	return issue, nil
}

func (c *Client) ListIssues(projectID string, filters map[string]string) ([]Issue, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("project id is required")
	}
	q := url.Values{}
	q.Set("project_id", projectID)
	for key, value := range filters {
		if strings.TrimSpace(value) == "" {
			continue
		}
		q.Set(key, strings.TrimSpace(value))
	}
	req, err := c.newRequest(http.MethodGet, "/api/issues?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var resp issueListResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) ListJobs(filters map[string]string) (AgentJobListResponse, error) {
	if err := c.requireAuth(); err != nil {
		return AgentJobListResponse{}, err
	}

	path := "/api/v1/jobs"
	q := url.Values{}
	for key, value := range filters {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" {
			continue
		}
		q.Set(strings.TrimSpace(key), trimmedValue)
	}
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	req, err := c.newRequest(http.MethodGet, path, nil)
	if err != nil {
		return AgentJobListResponse{}, err
	}
	var response AgentJobListResponse
	if err := c.do(req, &response); err != nil {
		return AgentJobListResponse{}, err
	}
	return response, nil
}

func (c *Client) CreateJob(input map[string]any) (AgentJob, error) {
	if err := c.requireAuth(); err != nil {
		return AgentJob{}, err
	}
	if len(input) == 0 {
		return AgentJob{}, errors.New("job payload is required")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return AgentJob{}, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader(payload))
	if err != nil {
		return AgentJob{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var response AgentJob
	if err := c.do(req, &response); err != nil {
		return AgentJob{}, err
	}
	return response, nil
}

func (c *Client) PauseJob(jobID string) (AgentJob, error) {
	return c.runJobAction(http.MethodPost, jobID, "/pause")
}

func (c *Client) ResumeJob(jobID string) (AgentJob, error) {
	return c.runJobAction(http.MethodPost, jobID, "/resume")
}

func (c *Client) RunJobNow(jobID string) (AgentJob, error) {
	return c.runJobAction(http.MethodPost, jobID, "/run")
}

func (c *Client) ListJobRuns(jobID string, limit int) (AgentJobRunsResponse, error) {
	if err := c.requireAuth(); err != nil {
		return AgentJobRunsResponse{}, err
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return AgentJobRunsResponse{}, errors.New("job id is required")
	}
	if limit <= 0 {
		limit = 50
	}
	path := fmt.Sprintf("/api/v1/jobs/%s/runs?limit=%d", url.PathEscape(jobID), limit)
	req, err := c.newRequest(http.MethodGet, path, nil)
	if err != nil {
		return AgentJobRunsResponse{}, err
	}
	var response AgentJobRunsResponse
	if err := c.do(req, &response); err != nil {
		return AgentJobRunsResponse{}, err
	}
	return response, nil
}

func (c *Client) DeleteJob(jobID string) (map[string]any, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, errors.New("job id is required")
	}
	req, err := c.newRequest(http.MethodDelete, "/api/v1/jobs/"+url.PathEscape(jobID), nil)
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := c.do(req, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (c *Client) ImportOpenClawCronJobs() (OpenClawCronJobImportResult, error) {
	if err := c.requireAuth(); err != nil {
		return OpenClawCronJobImportResult{}, err
	}
	req, err := c.newRequest(http.MethodPost, "/api/v1/jobs/import/openclaw-cron", nil)
	if err != nil {
		return OpenClawCronJobImportResult{}, err
	}
	var response OpenClawCronJobImportResult
	if err := c.do(req, &response); err != nil {
		return OpenClawCronJobImportResult{}, err
	}
	return response, nil
}

func (c *Client) runJobAction(method string, jobID string, suffix string) (AgentJob, error) {
	if err := c.requireAuth(); err != nil {
		return AgentJob{}, err
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return AgentJob{}, errors.New("job id is required")
	}
	req, err := c.newRequest(method, "/api/v1/jobs/"+url.PathEscape(jobID)+suffix, nil)
	if err != nil {
		return AgentJob{}, err
	}
	var response AgentJob
	if err := c.do(req, &response); err != nil {
		return AgentJob{}, err
	}
	return response, nil
}

func (c *Client) GetIssue(issueID string) (Issue, error) {
	if err := c.requireAuth(); err != nil {
		return Issue{}, err
	}
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return Issue{}, errors.New("issue id is required")
	}
	req, err := c.newRequest(http.MethodGet, "/api/issues/"+url.PathEscape(issueID), nil)
	if err != nil {
		return Issue{}, err
	}
	var resp issueDetailResponse
	if err := c.do(req, &resp); err != nil {
		return Issue{}, err
	}
	return resp.Issue, nil
}

func (c *Client) PatchIssue(issueID string, input map[string]interface{}) (Issue, error) {
	if err := c.requireAuth(); err != nil {
		return Issue{}, err
	}
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return Issue{}, errors.New("issue id is required")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return Issue{}, err
	}
	req, err := c.newRequest(http.MethodPatch, "/api/issues/"+url.PathEscape(issueID), bytes.NewReader(payload))
	if err != nil {
		return Issue{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var issue Issue
	if err := c.do(req, &issue); err != nil {
		return Issue{}, err
	}
	return issue, nil
}

func (c *Client) CommentIssue(issueID, authorAgentID, body string) error {
	if err := c.requireAuth(); err != nil {
		return err
	}
	issueID = strings.TrimSpace(issueID)
	authorAgentID = strings.TrimSpace(authorAgentID)
	body = strings.TrimSpace(body)
	if issueID == "" {
		return errors.New("issue id is required")
	}
	if authorAgentID == "" {
		return errors.New("author agent id is required")
	}
	if body == "" {
		return errors.New("comment body is required")
	}
	payload, err := json.Marshal(map[string]string{
		"author_agent_id": authorAgentID,
		"body":            body,
		"sender_type":     "agent",
	})
	if err != nil {
		return err
	}
	req, err := c.newRequest(http.MethodPost, "/api/issues/"+url.PathEscape(issueID)+"/comments", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, nil)
}

func (c *Client) AskIssueQuestionnaire(issueID string, input CreateIssueQuestionnaireInput) (Questionnaire, error) {
	if err := c.requireAuth(); err != nil {
		return Questionnaire{}, err
	}
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return Questionnaire{}, errors.New("issue id is required")
	}
	input.Author = strings.TrimSpace(input.Author)
	if input.Author == "" {
		return Questionnaire{}, errors.New("author is required")
	}
	if len(input.Questions) == 0 {
		return Questionnaire{}, errors.New("at least one question is required")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return Questionnaire{}, err
	}
	req, err := c.newRequest(
		http.MethodPost,
		"/api/issues/"+url.PathEscape(issueID)+"/questionnaire",
		bytes.NewReader(payload),
	)
	if err != nil {
		return Questionnaire{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var questionnaire Questionnaire
	if err := c.do(req, &questionnaire); err != nil {
		return Questionnaire{}, err
	}
	return questionnaire, nil
}

func (c *Client) RespondIssueQuestionnaire(questionnaireID string, input RespondIssueQuestionnaireInput) (Questionnaire, error) {
	if err := c.requireAuth(); err != nil {
		return Questionnaire{}, err
	}
	questionnaireID = strings.TrimSpace(questionnaireID)
	if questionnaireID == "" {
		return Questionnaire{}, errors.New("questionnaire id is required")
	}
	input.RespondedBy = strings.TrimSpace(input.RespondedBy)
	if input.RespondedBy == "" {
		return Questionnaire{}, errors.New("responded_by is required")
	}
	if len(input.Responses) == 0 {
		return Questionnaire{}, errors.New("at least one response is required")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return Questionnaire{}, err
	}
	req, err := c.newRequest(
		http.MethodPost,
		"/api/questionnaires/"+url.PathEscape(questionnaireID)+"/response",
		bytes.NewReader(payload),
	)
	if err != nil {
		return Questionnaire{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var questionnaire Questionnaire
	if err := c.do(req, &questionnaire); err != nil {
		return Questionnaire{}, err
	}
	return questionnaire, nil
}
