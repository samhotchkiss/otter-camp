package ottercli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	Slug        string `json:"slug"`
	Description string `json:"description"`
	RepoURL     string `json:"repo_url"`
	Status      string `json:"status"`
}

type projectListResponse struct {
	Projects []Project `json:"projects"`
	Total    int       `json:"total"`
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

func (c *Client) FindProject(query string) (Project, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Project{}, errors.New("project name or id is required")
	}
	projects, err := c.ListProjects()
	if err != nil {
		return Project{}, err
	}
	var matches []Project
	for _, p := range projects {
		if strings.EqualFold(p.ID, query) || strings.EqualFold(p.Name, query) {
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
	path := "/api/auth/validate?" + q.Encode()
	req, err := c.newRequest(http.MethodGet, path, nil)
	if err != nil {
		return whoamiResponse{}, err
	}
	var resp whoamiResponse
	if err := c.do(req, &resp); err != nil {
		return whoamiResponse{}, err
	}
	return resp, nil
}
