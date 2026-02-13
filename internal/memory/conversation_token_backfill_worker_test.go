package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeConversationTokenBackfillQueue struct {
	batches []int
	err     error
	calls   []int
}

func (f *fakeConversationTokenBackfillQueue) BackfillMissingTokenCounts(_ context.Context, limit int) (int, error) {
	f.calls = append(f.calls, limit)
	if f.err != nil {
		return 0, f.err
	}
	if len(f.batches) == 0 {
		return 0, nil
	}
	processed := f.batches[0]
	f.batches = f.batches[1:]
	return processed, nil
}

func TestConversationTokenBackfillWorker(t *testing.T) {
	queue := &fakeConversationTokenBackfillQueue{
		batches: []int{3, 0},
	}

	worker := NewConversationTokenBackfillWorker(queue, ConversationTokenBackfillWorkerConfig{
		BatchSize:    25,
		PollInterval: 10 * time.Millisecond,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, processed)
	require.Equal(t, []int{25}, queue.calls)

	processed, err = worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, processed)
	require.Equal(t, []int{25, 25}, queue.calls)
}

func TestConversationTokenBackfillWorkerReportsQueueErrors(t *testing.T) {
	queue := &fakeConversationTokenBackfillQueue{
		err: errors.New("boom"),
	}
	worker := NewConversationTokenBackfillWorker(queue, ConversationTokenBackfillWorkerConfig{
		BatchSize:    10,
		PollInterval: time.Millisecond,
	})
	worker.Logf = nil

	_, err := worker.RunOnce(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "backfill conversation tokens")
}
