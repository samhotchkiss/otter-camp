package turn

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type BootstrapRelauncherOptions struct {
	Pool     *pgxpool.Pool
	DataDir  string
	Chat     ChatService
	Events   EventBus
	Enqueuer JobEnqueuer
	Now      func() time.Time
	Logger   *slog.Logger
}

// NewBootstrapRelauncher builds the minimal TurnEngine subset needed to create
// canonical bootstrap restart projects from archived bootstrap failures.
func NewBootstrapRelauncher(opts BootstrapRelauncherOptions) (*TurnEngine, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("bootstrap relauncher requires database pool")
	}
	if opts.Chat == nil {
		return nil, fmt.Errorf("bootstrap relauncher requires chat service")
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("bootstrap relauncher requires event bus")
	}
	if opts.Enqueuer == nil {
		return nil, fmt.Errorf("bootstrap relauncher requires job enqueuer")
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &TurnEngine{
		pool:         opts.Pool,
		dataDir:      opts.DataDir,
		chat:         opts.Chat,
		events:       opts.Events,
		enqueuer:     opts.Enqueuer,
		messages:     repo.NewChatMessageRepo(opts.Pool),
		sessions:     repo.NewChatSessionRepo(opts.Pool),
		agents:       repo.NewAgentRepo(opts.Pool),
		tasks:        repo.NewProjectTaskRepo(opts.Pool),
		projects:     repo.NewProjectRepo(opts.Pool),
		environments: repo.NewProjectEnvironmentRepo(opts.Pool),
		assignments:  repo.NewAgentProjectAssignmentRepo(opts.Pool),
		jobPriority:  defaultAgentTurnJobPriority,
		now:          opts.Now,
		logger:       opts.Logger,
	}, nil
}
