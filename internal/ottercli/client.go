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
	ID          string `json:"id"`
	OrgID       string `json:"org_id"`
	Name        string `json:"name"`
	URLSlug     string `json:"slug"`
	Description string `json:"description"`
	RepoURL     string `json:"repo_url"`
	Status      string `json:"status"`
}

type projectListResponse struct {
	Projects []Project `json:"projects"`
	Total    int       `json:"total"`
}

type Agent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
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

type issueListResponse struct {
	Items []Issue `json:"items"`
	Total int     `json:"total"`
}

type issueDetailResponse struct {
	Issue Issue `json:"issue"`
}

func (c *Client) ListProjects() ([]Project, error) {
	if err := c.requireAuth(); err != nil {
		return nil, err
	}
	req, err := c.newRequest(http.MethodGet, "/api/projects", nil)
	if err != nil {
		return nil, err
	}
	var resp projectListResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
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

// Slug returns a filesystem-safe slug derived from the project name.
func (p Project) Slug() string {
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
