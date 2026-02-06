package githubsync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/github"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type RepoBindingPollStore interface {
	ListBindingsForPolling(ctx context.Context) ([]store.ProjectRepoBinding, error)
}

type RepoSyncJobEnqueuer interface {
	Enqueue(ctx context.Context, input store.EnqueueGitHubSyncJobInput) (*store.GitHubSyncJob, error)
}

type RepoBranchHeadClient interface {
	GetBranchHeadSHA(ctx context.Context, repositoryFullName, branch string) (string, error)
}

type RepoDriftPollResult struct {
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at"`
	ProjectsScanned int       `json:"projects_scanned"`
	ProjectsChecked int       `json:"projects_checked"`
	DriftDetected   int       `json:"drift_detected"`
	JobsEnqueued    int       `json:"jobs_enqueued"`
}

type RepoDriftPollSnapshot struct {
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	LastCompletedAt *time.Time `json:"last_completed_at,omitempty"`
	LastError       *string    `json:"last_error,omitempty"`
	ProjectsScanned int        `json:"projects_scanned"`
	ProjectsChecked int        `json:"projects_checked"`
	DriftDetected   int        `json:"drift_detected"`
	JobsEnqueued    int        `json:"jobs_enqueued"`
}

type RepoDriftPoller struct {
	Bindings    RepoBindingPollStore
	SyncJobs    RepoSyncJobEnqueuer
	BranchHeads RepoBranchHeadClient
	Interval    time.Duration
	now         func() time.Time
	newTicker   func(interval time.Duration) intervalTicker
}

type intervalTicker interface {
	C() <-chan time.Time
	Stop()
}

type stdIntervalTicker struct {
	ticker *time.Ticker
}

func (t stdIntervalTicker) C() <-chan time.Time { return t.ticker.C }
func (t stdIntervalTicker) Stop()               { t.ticker.Stop() }

func NewRepoDriftPoller(
	bindings RepoBindingPollStore,
	syncJobs RepoSyncJobEnqueuer,
	branchHeads RepoBranchHeadClient,
	interval time.Duration,
) *RepoDriftPoller {
	if interval <= 0 {
		interval = time.Hour
	}
	return &RepoDriftPoller{
		Bindings:    bindings,
		SyncJobs:    syncJobs,
		BranchHeads: branchHeads,
		Interval:    interval,
		now:         time.Now,
		newTicker: func(interval time.Duration) intervalTicker {
			return stdIntervalTicker{ticker: time.NewTicker(interval)}
		},
	}
}

func (p *RepoDriftPoller) Start(ctx context.Context) {
	if p == nil || p.Bindings == nil || p.SyncJobs == nil || p.BranchHeads == nil {
		return
	}

	ticker := p.newTicker(p.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			_, _ = p.RunOnce(ctx)
		}
	}
}

func (p *RepoDriftPoller) RunOnce(ctx context.Context) (*RepoDriftPollResult, error) {
	if p == nil {
		return nil, fmt.Errorf("poller is required")
	}
	if p.Bindings == nil || p.SyncJobs == nil || p.BranchHeads == nil {
		return nil, fmt.Errorf("poller dependencies are not configured")
	}
	if p.now == nil {
		p.now = time.Now
	}

	startedAt := p.now().UTC()
	result := RepoDriftPollResult{StartedAt: startedAt}

	bindings, err := p.Bindings.ListBindingsForPolling(ctx)
	if err != nil {
		completedAt := p.now().UTC()
		errText := err.Error()
		setRepoDriftPollSnapshot(RepoDriftPollSnapshot{
			LastRunAt:       &startedAt,
			LastCompletedAt: &completedAt,
			LastError:       &errText,
		})
		return nil, err
	}

	for _, binding := range bindings {
		result.ProjectsScanned++
		if !binding.Enabled {
			continue
		}

		repoFullName := strings.TrimSpace(binding.RepositoryFullName)
		if repoFullName == "" {
			continue
		}

		branch := strings.TrimSpace(binding.DefaultBranch)
		if branch == "" {
			branch = "main"
		}

		result.ProjectsChecked++
		headSHA, err := p.BranchHeads.GetBranchHeadSHA(ctx, repoFullName, branch)
		if err != nil {
			continue
		}
		headSHA = strings.TrimSpace(headSHA)
		if headSHA == "" {
			continue
		}

		lastSyncedSHA := strings.TrimSpace(derefString(binding.LastSyncedSHA))
		if lastSyncedSHA == headSHA {
			continue
		}

		result.DriftDetected++
		polledAt := p.now().UTC()
		payload, err := json.Marshal(map[string]any{
			"reason":               "poll_reconciler",
			"project_id":           binding.ProjectID,
			"repository_full_name": repoFullName,
			"branch":               branch,
			"last_synced_sha":      nullableStringOrNil(lastSyncedSHA),
			"detected_head_sha":    headSHA,
			"polled_at":            polledAt,
		})
		if err != nil {
			continue
		}

		sourceID := fmt.Sprintf("poll:%s:%s:%s", binding.ProjectID, branch, headSHA)
		workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, binding.OrgID)
		if _, err := p.SyncJobs.Enqueue(workspaceCtx, store.EnqueueGitHubSyncJobInput{
			ProjectID:     stringRef(binding.ProjectID),
			JobType:       store.GitHubSyncJobTypeRepoSync,
			Payload:       payload,
			SourceEventID: &sourceID,
			MaxAttempts:   5,
		}); err != nil {
			continue
		}
		result.JobsEnqueued++
	}

	completedAt := p.now().UTC()
	result.CompletedAt = completedAt
	setRepoDriftPollSnapshot(RepoDriftPollSnapshot{
		LastRunAt:       &startedAt,
		LastCompletedAt: &completedAt,
		ProjectsScanned: result.ProjectsScanned,
		ProjectsChecked: result.ProjectsChecked,
		DriftDetected:   result.DriftDetected,
		JobsEnqueued:    result.JobsEnqueued,
	})

	return &result, nil
}

type GitHubBranchHeadClient struct {
	Client *github.Client
}

func (c *GitHubBranchHeadClient) GetBranchHeadSHA(
	ctx context.Context,
	repositoryFullName,
	branch string,
) (string, error) {
	if c == nil || c.Client == nil {
		return "", fmt.Errorf("github client is required")
	}

	repositoryFullName = strings.TrimSpace(repositoryFullName)
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", fmt.Errorf("branch is required")
	}

	parts := strings.Split(repositoryFullName, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", fmt.Errorf("repository_full_name must be owner/repo")
	}
	owner := strings.TrimSpace(parts[0])
	repo := strings.TrimSpace(parts[1])
	endpoint := fmt.Sprintf(
		"/repos/%s/%s/branches/%s",
		url.PathEscape(owner),
		url.PathEscape(repo),
		url.PathEscape(branch),
	)

	request, err := c.Client.NewRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	response, err := c.Client.Do(ctx, github.JobTypeSync, request)
	if err != nil {
		return "", err
	}

	var payload struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		return "", fmt.Errorf("decode github branch response: %w", err)
	}

	sha := strings.TrimSpace(payload.Commit.SHA)
	if sha == "" {
		return "", fmt.Errorf("github branch response missing commit sha")
	}
	return sha, nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullableStringOrNil(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func stringRef(value string) *string {
	return &value
}

type repoDriftPollState struct {
	mu       sync.RWMutex
	snapshot RepoDriftPollSnapshot
}

var globalRepoDriftPollState = &repoDriftPollState{}

func CurrentRepoDriftPollSnapshot() RepoDriftPollSnapshot {
	globalRepoDriftPollState.mu.RLock()
	defer globalRepoDriftPollState.mu.RUnlock()
	return globalRepoDriftPollState.snapshot
}

func ResetRepoDriftPollSnapshotForTests() {
	globalRepoDriftPollState.mu.Lock()
	defer globalRepoDriftPollState.mu.Unlock()
	globalRepoDriftPollState.snapshot = RepoDriftPollSnapshot{}
}

func setRepoDriftPollSnapshot(snapshot RepoDriftPollSnapshot) {
	globalRepoDriftPollState.mu.Lock()
	defer globalRepoDriftPollState.mu.Unlock()
	globalRepoDriftPollState.snapshot = snapshot
}
