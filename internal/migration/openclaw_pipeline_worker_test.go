package migration

import (
	"errors"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/ws"
	"github.com/stretchr/testify/require"
)

func TestIsOpenClawPipelineTransientError(t *testing.T) {
	require.True(t, isOpenClawPipelineTransientError(ws.ErrOpenClawNotConnected))
	require.True(t, isOpenClawPipelineTransientError(errors.New("openclaw bridge call failed: websocket close 1012")))
	require.True(t, isOpenClawPipelineTransientError(errors.New(`openclaw gateway agent call failed: exec: "openclaw": executable file not found in $PATH`)))
	require.True(t, isOpenClawPipelineTransientError(errors.New("Unexpected server response: 502")))

	require.False(t, isOpenClawPipelineTransientError(errors.New("pq: insert or update on table \"memories\" violates foreign key constraint")))
	require.False(t, isOpenClawPipelineTransientError(errors.New("invalid migration payload")))
}
