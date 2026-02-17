package importer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeOpenClawEmbeddingRunOnceWorker struct {
	results []int
	index   int
}

func (f *fakeOpenClawEmbeddingRunOnceWorker) RunOnce(_ context.Context) (int, error) {
	if len(f.results) == 0 {
		return 0, nil
	}
	if f.index >= len(f.results) {
		return f.results[len(f.results)-1], nil
	}
	value := f.results[f.index]
	f.index += 1
	return value, nil
}

type fakeOpenClawEmbeddingPendingCounter struct {
	results []int
	index   int
}

func (f *fakeOpenClawEmbeddingPendingCounter) CountPendingEmbeddings(_ context.Context, _ string) (int, error) {
	if len(f.results) == 0 {
		return 0, nil
	}
	if f.index >= len(f.results) {
		return f.results[len(f.results)-1], nil
	}
	value := f.results[f.index]
	f.index += 1
	return value, nil
}

func TestOpenClawEmbeddingPhaseDrainRunnerCompletesWhenBacklogDrains(t *testing.T) {
	now := time.Date(2026, 2, 17, 12, 0, 0, 0, time.UTC)
	runner := &OpenClawEmbeddingPhaseDrainRunner{
		Worker: &fakeOpenClawEmbeddingRunOnceWorker{
			results: []int{1, 2},
		},
		PendingCounter: &fakeOpenClawEmbeddingPendingCounter{
			results: []int{3, 2, 0},
		},
		Timeout:      10 * time.Second,
		PollInterval: time.Second,
		now: func() time.Time {
			return now
		},
		sleep: func(_ context.Context, duration time.Duration) error {
			now = now.Add(duration)
			return nil
		},
	}

	result, err := runner.RunEmbeddingPhase(context.Background(), OpenClawEmbeddingPhaseInput{
		OrgID: "00000000-0000-0000-0000-000000000111",
	})
	require.NoError(t, err)
	require.False(t, result.TimedOut)
	require.Equal(t, 3, result.ProcessedEmbeddings)
	require.Zero(t, result.RemainingEmbeddings)
}

func TestOpenClawEmbeddingPhaseDrainRunnerTimesOutWithRemainingBacklog(t *testing.T) {
	now := time.Date(2026, 2, 17, 12, 0, 0, 0, time.UTC)
	runner := &OpenClawEmbeddingPhaseDrainRunner{
		Worker: &fakeOpenClawEmbeddingRunOnceWorker{
			results: []int{0, 0, 0},
		},
		PendingCounter: &fakeOpenClawEmbeddingPendingCounter{
			results: []int{2, 2, 2, 2, 2},
		},
		Timeout:      3 * time.Second,
		PollInterval: time.Second,
		now: func() time.Time {
			return now
		},
		sleep: func(_ context.Context, duration time.Duration) error {
			now = now.Add(duration)
			return nil
		},
	}

	result, err := runner.RunEmbeddingPhase(context.Background(), OpenClawEmbeddingPhaseInput{
		OrgID: "00000000-0000-0000-0000-000000000222",
	})
	require.NoError(t, err)
	require.True(t, result.TimedOut)
	require.Equal(t, 2, result.RemainingEmbeddings)
	require.Zero(t, result.ProcessedEmbeddings)
	require.Equal(t, 3*time.Second, result.Duration)
}
