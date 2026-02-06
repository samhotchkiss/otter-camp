package githubsync

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeRepoBindingPollStore struct {
	bindings []store.ProjectRepoBinding
	err      error
	calls    int
	calledCh chan struct{}
}

func (f *fakeRepoBindingPollStore) ListBindingsForPolling(context.Context) ([]store.ProjectRepoBinding, error) {
	f.calls++
	if f.calledCh != nil {
		select {
		case f.calledCh <- struct{}{}:
		default:
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	out := make([]store.ProjectRepoBinding, len(f.bindings))
	copy(out, f.bindings)
	return out, nil
}

type fakeRepoSyncJobEnqueuer struct {
	inputs   []store.EnqueueGitHubSyncJobInput
	contexts []context.Context
	err      error
}

func (f *fakeRepoSyncJobEnqueuer) Enqueue(ctx context.Context, input store.EnqueueGitHubSyncJobInput) (*store.GitHubSyncJob, error) {
	f.contexts = append(f.contexts, ctx)
	f.inputs = append(f.inputs, input)
	if f.err != nil {
		return nil, f.err
	}
	return &store.GitHubSyncJob{
		ID:      fmt.Sprintf("job-%d", len(f.inputs)),
		JobType: input.JobType,
	}, nil
}

type fakeRepoBranchHeadClient struct {
	heads map[string]string
	errs  map[string]error
	calls []string
}

func (f *fakeRepoBranchHeadClient) GetBranchHeadSHA(
	_ context.Context,
	repositoryFullName,
	branch string,
) (string, error) {
	key := repositoryFullName + "@" + branch
	f.calls = append(f.calls, key)
	if f.errs != nil {
		if err, ok := f.errs[key]; ok {
			return "", err
		}
	}
	if f.heads == nil {
		return "", nil
	}
	return f.heads[key], nil
}

type fakeTicker struct {
	events chan time.Time
}

func (t *fakeTicker) C() <-chan time.Time { return t.events }
func (t *fakeTicker) Stop()               {}

func TestRepoDriftPollerSchedulerRunsAtConfiguredInterval(t *testing.T) {
	ResetRepoDriftPollSnapshotForTests()

	bindingStore := &fakeRepoBindingPollStore{calledCh: make(chan struct{}, 1)}
	queue := &fakeRepoSyncJobEnqueuer{}
	heads := &fakeRepoBranchHeadClient{}
	poller := NewRepoDriftPoller(bindingStore, queue, heads, 90*time.Minute)

	ticker := &fakeTicker{events: make(chan time.Time, 1)}
	var capturedInterval time.Duration
	poller.newTicker = func(interval time.Duration) intervalTicker {
		capturedInterval = interval
		return ticker
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		poller.Start(ctx)
		close(done)
	}()

	ticker.events <- time.Now()
	select {
	case <-bindingStore.calledCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected poller to run after ticker event")
	}

	cancel()
	<-done

	require.Equal(t, 90*time.Minute, capturedInterval)
	require.Equal(t, 1, bindingStore.calls)
}

func TestRepoDriftPollerSkipsProjectsWithoutIntegration(t *testing.T) {
	ResetRepoDriftPollSnapshotForTests()

	orgID := "550e8400-e29b-41d4-a716-446655440111"
	bindingStore := &fakeRepoBindingPollStore{
		bindings: []store.ProjectRepoBinding{
			{
				OrgID:              orgID,
				ProjectID:          "550e8400-e29b-41d4-a716-446655440001",
				RepositoryFullName: "samhotchkiss/disabled-project",
				DefaultBranch:      "main",
				Enabled:            false,
			},
			{
				OrgID:              orgID,
				ProjectID:          "550e8400-e29b-41d4-a716-446655440002",
				RepositoryFullName: "samhotchkiss/enabled-project",
				DefaultBranch:      "main",
				Enabled:            true,
				LastSyncedSHA:      pollerStringPtr("sha-1"),
			},
		},
	}
	queue := &fakeRepoSyncJobEnqueuer{}
	heads := &fakeRepoBranchHeadClient{
		heads: map[string]string{
			"samhotchkiss/enabled-project@main": "sha-1",
		},
	}

	poller := NewRepoDriftPoller(bindingStore, queue, heads, time.Hour)
	result, err := poller.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, result.ProjectsScanned)
	require.Equal(t, 1, result.ProjectsChecked)
	require.Equal(t, 0, result.DriftDetected)
	require.Equal(t, 0, result.JobsEnqueued)
	require.Equal(t, []string{"samhotchkiss/enabled-project@main"}, heads.calls)
}

func TestRepoDriftPollerDetectsDriftAndEnqueuesSync(t *testing.T) {
	ResetRepoDriftPollSnapshotForTests()

	orgID := "550e8400-e29b-41d4-a716-446655440222"
	projectID := "550e8400-e29b-41d4-a716-446655440003"
	bindingStore := &fakeRepoBindingPollStore{
		bindings: []store.ProjectRepoBinding{
			{
				OrgID:              orgID,
				ProjectID:          projectID,
				RepositoryFullName: "samhotchkiss/otter-camp",
				DefaultBranch:      "main",
				Enabled:            true,
				LastSyncedSHA:      pollerStringPtr("sha-old"),
			},
		},
	}
	queue := &fakeRepoSyncJobEnqueuer{}
	heads := &fakeRepoBranchHeadClient{
		heads: map[string]string{
			"samhotchkiss/otter-camp@main": "sha-new",
		},
	}

	poller := NewRepoDriftPoller(bindingStore, queue, heads, time.Hour)
	result, err := poller.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.ProjectsChecked)
	require.Equal(t, 1, result.DriftDetected)
	require.Equal(t, 1, result.JobsEnqueued)

	require.Len(t, queue.inputs, 1)
	enqueued := queue.inputs[0]
	require.Equal(t, store.GitHubSyncJobTypeRepoSync, enqueued.JobType)
	require.NotNil(t, enqueued.ProjectID)
	require.Equal(t, projectID, *enqueued.ProjectID)
	require.NotNil(t, enqueued.SourceEventID)
	require.Contains(t, *enqueued.SourceEventID, "poll:"+projectID+":main:sha-new")
	require.Equal(t, orgID, middleware.WorkspaceFromContext(queue.contexts[0]))

	var payload map[string]any
	require.NoError(t, json.Unmarshal(enqueued.Payload, &payload))
	require.Equal(t, "poll_reconciler", payload["reason"])
	require.Equal(t, "sha-new", payload["detected_head_sha"])
	require.Equal(t, "sha-old", payload["last_synced_sha"])

	snapshot := CurrentRepoDriftPollSnapshot()
	require.NotNil(t, snapshot.LastRunAt)
	require.NotNil(t, snapshot.LastCompletedAt)
	require.Equal(t, 1, snapshot.DriftDetected)
	require.Equal(t, 1, snapshot.JobsEnqueued)
}

func TestRepoDriftPollerNoDriftDoesNotEnqueue(t *testing.T) {
	ResetRepoDriftPollSnapshotForTests()

	bindingStore := &fakeRepoBindingPollStore{
		bindings: []store.ProjectRepoBinding{
			{
				OrgID:              "550e8400-e29b-41d4-a716-446655440333",
				ProjectID:          "550e8400-e29b-41d4-a716-446655440004",
				RepositoryFullName: "samhotchkiss/otter-camp",
				DefaultBranch:      "main",
				Enabled:            true,
				LastSyncedSHA:      pollerStringPtr("sha-same"),
			},
		},
	}
	queue := &fakeRepoSyncJobEnqueuer{}
	heads := &fakeRepoBranchHeadClient{
		heads: map[string]string{
			"samhotchkiss/otter-camp@main": "sha-same",
		},
	}

	poller := NewRepoDriftPoller(bindingStore, queue, heads, time.Hour)
	result, err := poller.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.ProjectsChecked)
	require.Equal(t, 0, result.DriftDetected)
	require.Equal(t, 0, result.JobsEnqueued)
	require.Empty(t, queue.inputs)
}

func pollerStringPtr(value string) *string {
	return &value
}
