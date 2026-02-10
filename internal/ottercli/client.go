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
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	OrgID   string
	HTTP    *http.Client
}

func NewClient(cfg Config, orgOverride string) (*Client, error) {
	org := strings.TrimSpace(orgOverride)
	if org == "" {
		org = strings.TrimSpace(cfg.DefaultOrg)
	}
	return &Client{
		BaseURL: strings.TrimRight(cfg.APIBaseURL, "/"),
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
	if c.BaseURL == "" {
		return nil, errors.New("missing API base URL")
	}
	endpoint := c.BaseURL + path
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

	if resp.StatusCode >= 400 {
		payload, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type Project struct {
	ID               string         `json:"id"`
	OrgID            string         `json:"org_id"`
	Name             string         `json:"name"`
	URLSlug          string         `json:"slug"`
	Description      string         `json:"description"`
	RepoURL          string         `json:"repo_url"`
	Status           string         `json:"status"`
	RequireHumanReview bool         `json:"require_human_review"`
	WorkflowEnabled  bool           `json:"workflow_enabled"`
	WorkflowSchedule map[string]any `json:"workflow_schedule,omitempty"`
	WorkflowTemplate map[string]any `json:"workflow_template,omitempty"`
	WorkflowAgentID  *string        `json:"workflow_agent_id,omitempty"`
	WorkflowRunCount int            `json:"workflow_run_count"`
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
	Valid bool `json:"valid"`
	User  struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"user"`
}

func (c *Client) WhoAmI() (whoamiResponse, error) {
	if strings.TrimSpace(c.Token) == "" {
		return whoamiResponse{}, errors.New("missing auth token")
	}
	q := url.Values{}
	q.Set("token", strings.TrimSpace(c.Token))
	endpoint := c.BaseURL + "/api/auth/validate?" + q.Encode()
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

	q := url.Values{}
	q.Set("agent_id", agentID)
	q.Set("q", query)
	q.Set("max_results", fmt.Sprintf("%d", maxResults))

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
	bodyBytes, readErr := io.ReadAll(resp.Body)
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
