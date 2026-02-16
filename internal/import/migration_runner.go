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
	EntitySynthesisRunner  OpenClawEntitySynthesisRunner
	TaxonomyRunner         OpenClawTaxonomyClassificationRunner
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

type OpenClawEntitySynthesisInput struct {
	OrgID string
}

type OpenClawEntitySynthesisResult struct {
	ProcessedEntities int
	SynthesizedItems  int
}

type OpenClawEntitySynthesisRunner interface {
	RunSynthesis(ctx context.Context, input OpenClawEntitySynthesisInput) (OpenClawEntitySynthesisResult, error)
}

type noopOpenClawEntitySynthesisRunner struct{}

func (noopOpenClawEntitySynthesisRunner) RunSynthesis(
	_ context.Context,
	_ OpenClawEntitySynthesisInput,
) (OpenClawEntitySynthesisResult, error) {
	return OpenClawEntitySynthesisResult{}, nil
}

type OpenClawTaxonomyClassificationInput struct {
	OrgID string
}

type OpenClawTaxonomyClassificationResult struct {
	ProcessedMemories  int
	ClassifiedMemories int
	InvalidOutputs     int
}

type OpenClawTaxonomyClassificationRunner interface {
	RunClassification(ctx context.Context, input OpenClawTaxonomyClassificationInput) (OpenClawTaxonomyClassificationResult, error)
}

type noopOpenClawTaxonomyClassificationRunner struct{}

func (noopOpenClawTaxonomyClassificationRunner) RunClassification(
	_ context.Context,
	_ OpenClawTaxonomyClassificationInput,
) (OpenClawTaxonomyClassificationResult, error) {
	return OpenClawTaxonomyClassificationResult{}, nil
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
	EntitySynthesis  *OpenClawEntitySynthesisResult
	TaxonomyPhase    *OpenClawTaxonomyClassificationResult
	ProjectDiscovery *OpenClawProjectDiscoveryResult
	Summary          OpenClawMigrationSummaryReport
	Paused           bool
}

func NewOpenClawMigrationRunner(db *sql.DB) *OpenClawMigrationRunner {
	return &OpenClawMigrationRunner{
		DB:                     db,
		ProgressStore:          store.NewMigrationProgressStore(db),
		EllieBackfillRunner:    noopOpenClawEllieBackfillRunner{},
		EntitySynthesisRunner:  noopOpenClawEntitySynthesisRunner{},
		TaxonomyRunner:         noopOpenClawTaxonomyClassificationRunner{},
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
	sourceGuard, err := NewOpenClawSourceGuard(input.Installation.RootDir)
	if err != nil {
		return RunOpenClawMigrationResult{}, err
	}
	sourceSnapshot, err := sourceGuard.CaptureSnapshot()
	if err != nil {
		return RunOpenClawMigrationResult{}, err
	}

	result := RunOpenClawMigrationResult{}

	fmt.Printf("\n🦦 OpenClaw → Otter Camp Migration\n")
	fmt.Printf("   Source: %s\n", input.Installation.RootDir)
	fmt.Printf("   Agents: %d\n", len(input.Installation.Agents))
	fmt.Printf("   Events: %d\n\n", len(input.ParsedEvents))

	if !input.HistoryOnly {
		fmt.Printf("📦 Phase 1: Agent Import\n")
		agentResult, err := r.runAgentImportPhase(ctx, input)
		if err != nil {
			return RunOpenClawMigrationResult{}, err
		}
		result.AgentImport = agentResult
		if agentResult != nil {
			fmt.Printf("   ✅ %d agents imported (%d active, %d inactive)\n\n", agentResult.ImportedAgents, agentResult.ActiveAgents, agentResult.InactiveAgents)
		} else {
			fmt.Printf("   ⏭️  Already completed\n\n")
		}
	}

	if !input.AgentsOnly {
		fmt.Printf("💬 Phase 2: History Backfill (%d events)\n", len(input.ParsedEvents))
		historyResult, paused, err := r.runHistoryBackfillPhase(ctx, input)
		if err != nil {
			return RunOpenClawMigrationResult{}, err
		}
		result.HistoryBackfill = historyResult
		result.Paused = paused
		if historyResult != nil {
			fmt.Printf("   ✅ %d messages inserted, %d rooms created\n\n", historyResult.MessagesInserted, historyResult.RoomsCreated)
		} else if paused {
			fmt.Printf("   ⏸️  Paused\n\n")
		} else {
			fmt.Printf("   ⏭️  Already completed\n\n")
		}
	}
	if !input.AgentsOnly && !input.HistoryOnly && !result.Paused {
		fmt.Printf("🧠 Phase 3: Memory Extraction\n")
		ellieResult, err := r.runEllieBackfillPhase(ctx, input)
		if err != nil {
			return RunOpenClawMigrationResult{}, err
		}
		result.EllieBackfill = ellieResult
		if ellieResult != nil {
			fmt.Printf("   ✅ %d messages processed, %d memories extracted\n\n", ellieResult.ProcessedMessages, ellieResult.ExtractedMemories)
		} else {
			fmt.Printf("   ⏭️  Already completed\n\n")
		}

		fmt.Printf("🧩 Phase 4: Entity Synthesis\n")
		entityResult, entityErr := r.runEntitySynthesisPhase(ctx, input)
		if entityErr != nil {
			return RunOpenClawMigrationResult{}, entityErr
		}
		result.EntitySynthesis = entityResult
		if entityResult != nil {
			fmt.Printf("   ✅ %d entities processed, %d syntheses written\n\n", entityResult.ProcessedEntities, entityResult.SynthesizedItems)
		} else {
			fmt.Printf("   ⏭️  Already completed\n\n")
		}

		fmt.Printf("🏷️  Phase 5: Taxonomy Classification\n")
		taxonomyResult, taxonomyErr := r.runTaxonomyClassificationPhase(ctx, input)
		if taxonomyErr != nil {
			return RunOpenClawMigrationResult{}, taxonomyErr
		}
		result.TaxonomyPhase = taxonomyResult
		if taxonomyResult != nil {
			fmt.Printf(
				"   ✅ %d memories evaluated, %d classified (%d invalid output)\n\n",
				taxonomyResult.ProcessedMemories,
				taxonomyResult.ClassifiedMemories,
				taxonomyResult.InvalidOutputs,
			)
		} else {
			fmt.Printf("   ⏭️  Already completed\n\n")
		}

		fmt.Printf("🔍 Phase 6: Project Discovery\n")
		discoveryResult, discoveryErr := r.runProjectDiscoveryPhase(ctx, input)
		if discoveryErr != nil {
			return RunOpenClawMigrationResult{}, discoveryErr
		}
		result.ProjectDiscovery = discoveryResult
		if discoveryResult != nil {
			fmt.Printf("   ✅ %d projects created, %d issues created\n\n", discoveryResult.ProjectsCreated, discoveryResult.IssuesCreated)
		} else {
			fmt.Printf("   ⏭️  Already completed\n\n")
		}
	}

	result.Summary = BuildOpenClawMigrationSummaryReport(result)

	fmt.Printf("🔒 Verifying source integrity...\n")
	if err := sourceGuard.VerifyUnchanged(sourceSnapshot); err != nil {
		// Runtime file changes are expected when OpenClaw is running.
		// Warn but don't fail — the data is already committed.
		fmt.Printf("   ⚠️  %s\n", err.Error())
		fmt.Printf("   (Expected when OpenClaw is running during migration)\n\n")
		result.Summary.Warnings = append(result.Summary.Warnings, "source files changed during migration (expected if OpenClaw was running)")
	} else {
		fmt.Printf("   ✅ Source unchanged\n\n")
	}

	fmt.Printf("✨ Migration complete!\n")
	fmt.Printf("   Agents imported:  %d\n", result.Summary.AgentImportProcessed)
	fmt.Printf("   Messages created: %d\n", result.Summary.HistoryMessagesInserted)
	fmt.Printf("   Events processed: %d\n", result.Summary.HistoryEventsProcessed)
	fmt.Printf("   Entities processed: %d\n", result.Summary.EntitySynthesisProcessed)
	fmt.Printf("   Taxonomy memories: %d\n", result.Summary.TaxonomyClassificationProcessed)
	fmt.Printf("   Projects found:   %d\n", result.Summary.ProjectDiscoveryProcessed)
	if len(result.Summary.Warnings) > 0 {
		fmt.Printf("   Warnings:         %d\n", len(result.Summary.Warnings))
		for _, w := range result.Summary.Warnings {
			fmt.Printf("     ⚠️  %s\n", w)
		}
	}
	fmt.Println()

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
		if progress.Status == store.MigrationProgressStatusCompleted {
			return nil, nil
		}
		if progress.Status == store.MigrationProgressStatusFailed {
			return nil, fmt.Errorf("migration phase %q is failed; reset status before rerun", phaseType)
		}
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
		if progress.Status == store.MigrationProgressStatusCompleted {
			return &OpenClawHistoryBackfillResult{}, false, nil
		}
		if progress.Status == store.MigrationProgressStatusFailed {
			return nil, false, fmt.Errorf("migration phase %q is failed; reset status before rerun", phaseType)
		}
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
		if progress.Status == store.MigrationProgressStatusCompleted {
			return nil, nil
		}
		if progress.Status == store.MigrationProgressStatusFailed {
			return nil, fmt.Errorf("migration phase %q is failed; reset status before rerun", phaseType)
		}
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
		if progress.Status == store.MigrationProgressStatusCompleted {
			return nil, nil
		}
		if progress.Status == store.MigrationProgressStatusFailed {
			return nil, fmt.Errorf("migration phase %q is failed; reset status before rerun", phaseType)
		}
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

func (r *OpenClawMigrationRunner) runEntitySynthesisPhase(
	ctx context.Context,
	input RunOpenClawMigrationInput,
) (*OpenClawEntitySynthesisResult, error) {
	if r.EntitySynthesisRunner == nil {
		return nil, nil
	}

	phaseType := "entity_synthesis"
	progress, err := r.ProgressStore.GetByType(ctx, input.OrgID, phaseType)
	if err != nil {
		return nil, err
	}
	if progress == nil {
		started, startErr := r.ProgressStore.StartPhase(ctx, store.StartMigrationProgressInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			CurrentLabel:  migrationRunnerStringPtr("starting entity synthesis"),
		})
		if startErr != nil {
			return nil, startErr
		}
		progress = started
	} else {
		if progress.Status == store.MigrationProgressStatusCompleted {
			return nil, nil
		}
		if progress.Status == store.MigrationProgressStatusFailed {
			return nil, fmt.Errorf("migration phase %q is failed; reset status before rerun", phaseType)
		}
		if _, setErr := r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			Status:        store.MigrationProgressStatusRunning,
			CurrentLabel:  migrationRunnerStringPtr("resuming entity synthesis"),
		}); setErr != nil {
			return nil, setErr
		}
	}

	synthesisResult, synthesisErr := r.EntitySynthesisRunner.RunSynthesis(ctx, OpenClawEntitySynthesisInput{
		OrgID: input.OrgID,
	})
	if synthesisErr != nil {
		_, _ = r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			Status:        store.MigrationProgressStatusFailed,
			Error:         migrationRunnerStringPtr(synthesisErr.Error()),
		})
		return nil, synthesisErr
	}

	if synthesisResult.ProcessedEntities > 0 {
		if _, advanceErr := r.ProgressStore.Advance(ctx, store.AdvanceMigrationProgressInput{
			OrgID:          input.OrgID,
			MigrationType:  phaseType,
			ProcessedDelta: synthesisResult.ProcessedEntities,
			CurrentLabel: migrationRunnerStringPtr(
				fmt.Sprintf("processed %d entities", synthesisResult.ProcessedEntities),
			),
		}); advanceErr != nil {
			return nil, advanceErr
		}
	}

	if _, err := r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
		OrgID:         input.OrgID,
		MigrationType: phaseType,
		Status:        store.MigrationProgressStatusCompleted,
		CurrentLabel:  migrationRunnerStringPtr("entity synthesis complete"),
	}); err != nil {
		return nil, err
	}

	return &synthesisResult, nil
}

func (r *OpenClawMigrationRunner) runTaxonomyClassificationPhase(
	ctx context.Context,
	input RunOpenClawMigrationInput,
) (*OpenClawTaxonomyClassificationResult, error) {
	if r.TaxonomyRunner == nil {
		return nil, nil
	}

	phaseType := "taxonomy_classification"
	progress, err := r.ProgressStore.GetByType(ctx, input.OrgID, phaseType)
	if err != nil {
		return nil, err
	}
	if progress == nil {
		started, startErr := r.ProgressStore.StartPhase(ctx, store.StartMigrationProgressInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			CurrentLabel:  migrationRunnerStringPtr("starting taxonomy classification"),
		})
		if startErr != nil {
			return nil, startErr
		}
		progress = started
	} else {
		if progress.Status == store.MigrationProgressStatusCompleted {
			return nil, nil
		}
		if progress.Status == store.MigrationProgressStatusFailed {
			return nil, fmt.Errorf("migration phase %q is failed; reset status before rerun", phaseType)
		}
		if _, setErr := r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			Status:        store.MigrationProgressStatusRunning,
			CurrentLabel:  migrationRunnerStringPtr("resuming taxonomy classification"),
		}); setErr != nil {
			return nil, setErr
		}
	}

	classificationResult, classifyErr := r.TaxonomyRunner.RunClassification(ctx, OpenClawTaxonomyClassificationInput{
		OrgID: input.OrgID,
	})
	if classifyErr != nil {
		_, _ = r.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         input.OrgID,
			MigrationType: phaseType,
			Status:        store.MigrationProgressStatusFailed,
			Error:         migrationRunnerStringPtr(classifyErr.Error()),
		})
		return nil, classifyErr
	}

	if classificationResult.ProcessedMemories > 0 {
		if _, advanceErr := r.ProgressStore.Advance(ctx, store.AdvanceMigrationProgressInput{
			OrgID:          input.OrgID,
			MigrationType:  phaseType,
			ProcessedDelta: classificationResult.ProcessedMemories,
			CurrentLabel: migrationRunnerStringPtr(
				fmt.Sprintf(
					"classified %d of %d memories",
					classificationResult.ClassifiedMemories,
					classificationResult.ProcessedMemories,
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
		CurrentLabel:  migrationRunnerStringPtr("taxonomy classification complete"),
	}); err != nil {
		return nil, err
	}

	return &classificationResult, nil
}

func migrationRunnerStringPtr(value string) *string {
	return &value
}
