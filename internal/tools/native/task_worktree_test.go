package native

import (
	"errors"
	"testing"
)

func TestIsRecoverableWorktreeRemoveError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "not a working tree",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: '/tmp/task-10' is not a working tree"),
			want: true,
		},
		{
			name: "legacy git file corruption",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: validation failed, cannot remove working tree: '/tmp/task-10/.git' is not a .git file, error code 7"),
			want: true,
		},
		{
			name: "legacy not a git repository",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: validation failed, cannot remove working tree: '/tmp/task-10/.git' not a git repository"),
			want: true,
		},
		{
			name: "unrelated git error",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: branch is currently checked out"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isRecoverableWorktreeRemoveError(tc.err); got != tc.want {
				t.Fatalf("isRecoverableWorktreeRemoveError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
