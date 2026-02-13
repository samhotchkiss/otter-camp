package importer

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type OpenClawMigrationRunner struct {
	DB                     *sql.DB
	ProgressStore          *store.MigrationProgressStore
	EllieBackfillRunner    OpenClawEllieBackfillRunner
	ProjectDiscoveryRunner OpenClawProjectDiscoveryRunner
	HistoryCheckpoint      int
	OnHistoryCheckpoint    func(processed, total int)
}

type OpenClawEllieBackfillInput struct {
	OrgID string
}

type OpenClawEllieBackfillResult struct {
	ProcessedMessages int
	ExtractedMemories int
}

type OpenClawEllieBackfillRunner interface {
	RunBackfill(ctx context.Context, input OpenClawEllieBackfillInput) (OpenClawEllieBackfillResult, error)
}

type noopOpenClawEllieBackfillRunner struct{}

func (noopOpenClawEllieBackfillRunner) RunBackfill(
	_ context.Context,
	_ OpenClawEllieBackfillInput,
) (OpenClawEllieBackfillResult, error) {
	return OpenClawEllieBackfillResult{}, nil
}

type RunOpenClawMigrationInput struct {
	OrgID        string
	UserID       string
	Installation *OpenClawInstallation
	ParsedEvents []OpenClawSessionEvent
	AgentsOnly   bool
	HistoryOnly  bool
}

type RunOpenClawMigrationResult struct {
	AgentImport      *OpenClawAgentImportResult
	HistoryBackfill  *OpenClawHistoryBackfillResult
	EllieBackfill    *OpenClawEllieBackfillResult
	ProjectDiscovery *OpenClawProjectDiscoveryResult
	Paused           bool
}

func NewOpenClawMigrationRunner(db *sql.DB) *OpenClawMigrationRunner {
	return &OpenClawMigrationRunner{
		DB:                     db,
		ProgressStore:          store.NewMigrationProgressStore(db),
		EllieBackfillRunner:    noopOpenClawEllieBackfillRunner{},
		ProjectDiscoveryRunner: newOpenClawProjectDiscoveryRunner(db),
		HistoryCheckpoint:      100,
	}
}

func (r *OpenClawMigrationRunner) Run(ctx context.Context, input RunOpenClawMigrationInput) (RunOpenClawMigrationResult, error) {
	if r == nil || r.DB == nil || r.ProgressStore == nil {
		return RunOpenClawMigrationResult{}, fmt.Errorf("openclaw migration runner is not configured")
	}
	if input.Installation == nil {
		return RunOpenClawMigrationResult{}, fmt.Errorf("installation is required")
	}

	result := RunOpenClawMigrationResult{}

	if !input.HistoryOnly {
		agentResult, err := r.runAgentImportPhase(ctx, input)
		if err != nil {
			return RunOpenClawMigrationResult{}, err
		}
		result.AgentImport = agentResult
	}

	if !input.AgentsOnly {
		historyResult, paused, err := r.runHistoryBackfillPhase(ctx, input)
		if err != nil {
			return RunOpenClawMigrationResult{}, err
		}
		result.HistoryBackfill = historyResult
		result.Paused = paused
	}
	if !input.AgentsOnly && !result.Paused {
		ellieResult, err := r.runEllieBackfillPhase(ctx, input)
		if err != nil {
			return RunOpenClawMigrationResult{}, err
		}
		result.EllieBackfill = ellieResult

		discoveryResult, discoveryErr := r.runProjectDiscoveryPhase(ctx, input)
		if discoveryErr != nil {
			return RunOpenClawMigrationResult{}, discoveryErr
		}
		result.ProjectDiscovery = discoveryResult
	}

	return result, nil
}

func (r *OpenClawMigrationRunner) runAgentImportPhase(ctx context.Context, input RunOpenClawMigrationInput) (*OpenClawAgentImportResult, error) {
	identities, err := ImportOpenClawAgentIdentities(input.Installation)
	if err != nil {
		return nil, err
	}
	totalItems := len(identities)
	phaseType := "agent_import"

	progress, err := r.ProgressStore.GetByType(ctx, input.OrgID, phaseType)
	if err != nil {
		return nil, err
	}
	if progress == nil {
		started, startErr := r.ProgressStore.StartPhase(ctx, store.StartMigrationProgressInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			TotalItems:    &totalItems,
			CurrentLabel:  migrationRunnerStringPtr("starting agent import"),
		})
		if startErr != nil {
			return nil, startErr
		}
		progress = started
	} else {
		if _, setErr := r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			Status:        store.MigrationProgressStatusRunning,
			CurrentLabel:  migrationRunnerStringPtr("running agent import"),
		}); setErr != nil {
			return nil, setErr
		}
	}

	imported, err := ImportOpenClawAgents(ctx, r.DB, OpenClawAgentImportOptions{
		OrgID:        input.OrgID,
		Installation: input.Installation,
	})
	if err != nil {
		_, _ = r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			Status:        store.MigrationProgressStatusFailed,
			Error:         migrationRunnerStringPtr(err.Error()),
		})
		return nil, err
	}

	processedDelta := imported.ImportedAgents
	if progress != nil && progress.ProcessedItems > 0 {
		processedDelta = max(totalItems-progress.ProcessedItems, 0)
	}

	if processedDelta > 0 {
		if _, advanceErr := r.ProgressStore.Advance(ctx, store.AdvanceMigrationProgressInput{
			OrgID:          input.OrgID,
			MigrationType:  phaseType,
			ProcessedDelta: processedDelta,
			CurrentLabel:   migrationRunnerStringPtr(fmt.Sprintf("processed %d/%d agents", totalItems, totalItems)),
		}); advanceErr != nil {
			return nil, advanceErr
		}
	}

	if _, err := r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
		OrgID:         input.OrgID,
		MigrationType: phaseType,
		Status:        store.MigrationProgressStatusCompleted,
		CurrentLabel:  migrationRunnerStringPtr("agent import complete"),
	}); err != nil {
		return nil, err
	}

	return &imported, nil
}

func (r *OpenClawMigrationRunner) runHistoryBackfillPhase(
	ctx context.Context,
	input RunOpenClawMigrationInput,
) (*OpenClawHistoryBackfillResult, bool, error) {
	events := input.ParsedEvents
	if len(events) == 0 {
		parsed, err := ParseOpenClawSessionEvents(input.Installation)
		if err != nil {
			return nil, false, err
		}
		events = parsed
	}
	totalItems := len(events)
	phaseType := "history_backfill"

	progress, err := r.ProgressStore.GetByType(ctx, input.OrgID, phaseType)
	if err != nil {
		return nil, false, err
	}
	if progress == nil {
		started, startErr := r.ProgressStore.StartPhase(ctx, store.StartMigrationProgressInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			TotalItems:    &totalItems,
			CurrentLabel:  migrationRunnerStringPtr("starting history backfill"),
		})
		if startErr != nil {
			return nil, false, startErr
		}
		progress = started
	} else {
		if _, setErr := r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			Status:        store.MigrationProgressStatusRunning,
			CurrentLabel:  migrationRunnerStringPtr("resuming history backfill"),
		}); setErr != nil {
			return nil, false, setErr
		}
	}

	checkpoint := r.HistoryCheckpoint
	if checkpoint <= 0 {
		checkpoint = 100
	}

	startIndex := 0
	if progress != nil && progress.ProcessedItems > 0 {
		startIndex = progress.ProcessedItems
	}
	if startIndex < 0 {
		startIndex = 0
	}
	if startIndex > totalItems {
		startIndex = totalItems
	}

	aggregated := OpenClawHistoryBackfillResult{}
	for offset := startIndex; offset < totalItems; offset += checkpoint {
		end := offset + checkpoint
		if end > totalItems {
			end = totalItems
		}

		batch := events[offset:end]
		batchResult, batchErr := BackfillOpenClawHistory(ctx, r.DB, OpenClawHistoryBackfillOptions{
			OrgID:        input.OrgID,
			UserID:       input.UserID,
			ParsedEvents: batch,
		})
		if batchErr != nil {
			_, _ = r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
				OrgID:         input.OrgID,
				MigrationType: phaseType,
				Status:        store.MigrationProgressStatusFailed,
				Error:         migrationRunnerStringPtr(batchErr.Error()),
			})
			return nil, false, batchErr
		}

		aggregated.RoomsCreated += batchResult.RoomsCreated
		aggregated.ParticipantsAdded += batchResult.ParticipantsAdded
		aggregated.MessagesInserted += batchResult.MessagesInserted
		aggregated.EventsProcessed += batchResult.EventsProcessed

		if _, advanceErr := r.ProgressStore.Advance(ctx, store.AdvanceMigrationProgressInput{
			OrgID:          input.OrgID,
			MigrationType:  phaseType,
			ProcessedDelta: len(batch),
			CurrentLabel:   migrationRunnerStringPtr(fmt.Sprintf("processed %d/%d events", end, totalItems)),
		}); advanceErr != nil {
			return nil, false, advanceErr
		}

		if r.OnHistoryCheckpoint != nil {
			r.OnHistoryCheckpoint(end, totalItems)
		}

		latest, latestErr := r.ProgressStore.GetByType(ctx, input.OrgID, phaseType)
		if latestErr != nil {
			return nil, false, latestErr
		}
		if latest != nil && latest.Status == store.MigrationProgressStatusPaused {
			return &aggregated, true, nil
		}
	}

	if _, err := r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
		OrgID:         input.OrgID,
		MigrationType: phaseType,
		Status:        store.MigrationProgressStatusCompleted,
		CurrentLabel:  migrationRunnerStringPtr("history backfill complete"),
	}); err != nil {
		return nil, false, err
	}

	return &aggregated, false, nil
}

func (r *OpenClawMigrationRunner) runEllieBackfillPhase(
	ctx context.Context,
	input RunOpenClawMigrationInput,
) (*OpenClawEllieBackfillResult, error) {
	if r.EllieBackfillRunner == nil {
		return nil, nil
	}

	phaseType := "memory_extraction"
	progress, err := r.ProgressStore.GetByType(ctx, input.OrgID, phaseType)
	if err != nil {
		return nil, err
	}
	if progress == nil {
		started, startErr := r.ProgressStore.StartPhase(ctx, store.StartMigrationProgressInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			CurrentLabel:  migrationRunnerStringPtr("starting memory extraction"),
		})
		if startErr != nil {
			return nil, startErr
		}
		progress = started
	} else {
		if _, setErr := r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			Status:        store.MigrationProgressStatusRunning,
			CurrentLabel:  migrationRunnerStringPtr("resuming memory extraction"),
		}); setErr != nil {
			return nil, setErr
		}
	}

	backfillResult, backfillErr := r.EllieBackfillRunner.RunBackfill(ctx, OpenClawEllieBackfillInput{
		OrgID: input.OrgID,
	})
	if backfillErr != nil {
		_, _ = r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			Status:        store.MigrationProgressStatusFailed,
			Error:         migrationRunnerStringPtr(backfillErr.Error()),
		})
		return nil, backfillErr
	}

	if backfillResult.ProcessedMessages > 0 {
		if _, advanceErr := r.ProgressStore.Advance(ctx, store.AdvanceMigrationProgressInput{
			OrgID:          input.OrgID,
			MigrationType:  phaseType,
			ProcessedDelta: backfillResult.ProcessedMessages,
			CurrentLabel: migrationRunnerStringPtr(
				fmt.Sprintf("processed %d messages", backfillResult.ProcessedMessages),
			),
		}); advanceErr != nil {
			return nil, advanceErr
		}
	}

	if _, err := r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
		OrgID:         input.OrgID,
		MigrationType: phaseType,
		Status:        store.MigrationProgressStatusCompleted,
		CurrentLabel:  migrationRunnerStringPtr("memory extraction complete"),
	}); err != nil {
		return nil, err
	}

	return &backfillResult, nil
}

func (r *OpenClawMigrationRunner) runProjectDiscoveryPhase(
	ctx context.Context,
	input RunOpenClawMigrationInput,
) (*OpenClawProjectDiscoveryResult, error) {
	if r.ProjectDiscoveryRunner == nil {
		return nil, nil
	}

	events := input.ParsedEvents
	if len(events) == 0 {
		parsed, err := ParseOpenClawSessionEvents(input.Installation)
		if err != nil {
			return nil, err
		}
		events = parsed
	}

	totalItems := len(events)
	phaseType := "project_discovery"
	progress, err := r.ProgressStore.GetByType(ctx, input.OrgID, phaseType)
	if err != nil {
		return nil, err
	}
	if progress == nil {
		started, startErr := r.ProgressStore.StartPhase(ctx, store.StartMigrationProgressInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			TotalItems:    &totalItems,
			CurrentLabel:  migrationRunnerStringPtr("starting project discovery"),
		})
		if startErr != nil {
			return nil, startErr
		}
		progress = started
	} else {
		if _, setErr := r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			Status:        store.MigrationProgressStatusRunning,
			CurrentLabel:  migrationRunnerStringPtr("resuming project discovery"),
		}); setErr != nil {
			return nil, setErr
		}
	}

	discoveryResult, discoveryErr := r.ProjectDiscoveryRunner.Discover(ctx, OpenClawProjectDiscoveryInput{
		OrgID:        input.OrgID,
		ParsedEvents: events,
	})
	if discoveryErr != nil {
		_, _ = r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			Status:        store.MigrationProgressStatusFailed,
			Error:         migrationRunnerStringPtr(discoveryErr.Error()),
		})
		return nil, discoveryErr
	}

	processedDelta := discoveryResult.ProcessedItems
	if processedDelta <= 0 {
		processedDelta = totalItems
	}
	if processedDelta > 0 {
		if _, advanceErr := r.ProgressStore.Advance(ctx, store.AdvanceMigrationProgressInput{
			OrgID:          input.OrgID,
			MigrationType:  phaseType,
			ProcessedDelta: processedDelta,
			CurrentLabel: migrationRunnerStringPtr(
				fmt.Sprintf(
					"projects + issues upserted: %d + %d",
					discoveryResult.ProjectsCreated+discoveryResult.ProjectsUpdated,
					discoveryResult.IssuesCreated+discoveryResult.IssuesUpdated,
				),
			),
		}); advanceErr != nil {
			return nil, advanceErr
		}
	}

	if _, err := r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
		OrgID:         input.OrgID,
		MigrationType: phaseType,
		Status:        store.MigrationProgressStatusCompleted,
		CurrentLabel:  migrationRunnerStringPtr("project discovery complete"),
	}); err != nil {
		return nil, err
	}

	return &discoveryResult, nil
}

func migrationRunnerStringPtr(value string) *string {
	return &value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
