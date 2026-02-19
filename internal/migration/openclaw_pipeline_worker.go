package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	importer "github.com/samhotchkiss/otter-camp/internal/import"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

var openClawPipelinePhaseOrder = []string{
	"history_embedding_1536",
	"memory_extraction",
	"entity_synthesis",
	"memory_dedup",
	"taxonomy_classification",
	"project_discovery",
	"project_docs_scanning",
}

type OpenClawPipelineWorker struct {
	DB *sql.DB

	ProgressStore *store.MigrationProgressStore

	EmbeddingStore  *store.ConversationEmbeddingStore
	IngestionStore  *store.EllieIngestionStore
	IngestionWorker *memory.EllieIngestionWorker

	EntityWorker   *memory.EllieEntitySynthesisWorker
	DedupWorker    *memory.EllieDedupWorker
	TaxonomyWorker *memory.EllieTaxonomyClassifierWorker

	ProjectDocsScanner *memory.EllieProjectDocsScanner
	ProjectDocsStore   memory.EllieProjectDocsStore

	PollInterval time.Duration
	Logf         func(format string, args ...any)
}

func (w *OpenClawPipelineWorker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	interval := w.PollInterval
	if interval <= 0 {
		interval = 3 * time.Second
	}
	logf := w.Logf
	if logf == nil {
		logf = log.Printf
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return
		}
		w.runOnceBestEffort(ctx, logf)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *OpenClawPipelineWorker) runOnceBestEffort(ctx context.Context, logf func(string, ...any)) {
	if w == nil || w.DB == nil {
		return
	}
	if w.ProgressStore == nil {
		w.ProgressStore = store.NewMigrationProgressStore(w.DB)
	}
	orgIDs, err := w.listActiveOrgIDs(ctx)
	if err != nil {
		logf("openclaw pipeline: list active orgs failed: %v", err)
		return
	}
	for _, orgID := range orgIDs {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := w.reconcileOrg(ctx, orgID); err != nil {
			logf("openclaw pipeline: org %s reconcile failed: %v", orgID, err)
		}
	}
}

func (w *OpenClawPipelineWorker) listActiveOrgIDs(ctx context.Context) ([]string, error) {
	phases := openClawPipelinePhaseOrder
	if len(phases) == 0 {
		return []string{}, nil
	}

	placeholders := make([]string, 0, len(phases))
	args := make([]any, 0, len(phases)+1)
	args = append(args, store.MigrationProgressStatusRunning)
	for i, phase := range phases {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+2))
		args = append(args, phase)
	}

	query := fmt.Sprintf(
		`SELECT DISTINCT org_id::text
		   FROM migration_progress
		  WHERE status = $1
		    AND migration_type IN (%s)
		  ORDER BY org_id ASC
		  LIMIT 50`,
		strings.Join(placeholders, ","),
	)

	rows, err := w.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orgIDs := make([]string, 0)
	for rows.Next() {
		var orgID string
		if err := rows.Scan(&orgID); err != nil {
			return nil, err
		}
		orgID = strings.TrimSpace(orgID)
		if orgID != "" {
			orgIDs = append(orgIDs, orgID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orgIDs, nil
}

func (w *OpenClawPipelineWorker) reconcileOrg(ctx context.Context, orgID string) error {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return nil
	}
	for _, phase := range openClawPipelinePhaseOrder {
		progress, err := w.ProgressStore.GetByType(ctx, orgID, phase)
		if err != nil {
			return err
		}
		if progress == nil {
			continue
		}
		if progress.Status != store.MigrationProgressStatusRunning {
			continue
		}
		switch phase {
		case "history_embedding_1536":
			return w.reconcileEmbeddings(ctx, orgID)
		case "memory_extraction":
			return w.reconcileMemoryExtraction(ctx, orgID)
		case "entity_synthesis":
			return w.reconcileEntitySynthesis(ctx, orgID)
		case "memory_dedup":
			return w.reconcileMemoryDedup(ctx, orgID)
		case "taxonomy_classification":
			return w.reconcileTaxonomy(ctx, orgID)
		case "project_discovery":
			return w.reconcileProjectDiscovery(ctx, orgID)
		case "project_docs_scanning":
			return w.reconcileProjectDocs(ctx, orgID)
		default:
			continue
		}
	}
	return nil
}

func (w *OpenClawPipelineWorker) reconcileEmbeddings(ctx context.Context, orgID string) error {
	if w.EmbeddingStore == nil {
		w.EmbeddingStore = store.NewConversationEmbeddingStoreWithDimension(w.DB, 1536)
	}
	pending, err := w.EmbeddingStore.CountPendingEmbeddings(ctx, orgID)
	if err != nil {
		return err
	}
	if pending == 0 {
		_, err := w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         orgID,
			MigrationType: "history_embedding_1536",
			Status:        store.MigrationProgressStatusCompleted,
			CurrentLabel:  stringPtr("embeddings complete"),
		})
		return err
	}
	_, err = w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "history_embedding_1536",
		Status:        store.MigrationProgressStatusRunning,
		CurrentLabel:  stringPtr(fmt.Sprintf("pending embeddings: %d", pending)),
	})
	return err
}

func (w *OpenClawPipelineWorker) reconcileMemoryExtraction(ctx context.Context, orgID string) error {
	if w.IngestionStore == nil {
		w.IngestionStore = store.NewEllieIngestionStore(w.DB)
	}
	if w.IngestionWorker == nil {
		// This should be injected from runtime so it can use the OpenClaw bridge runner.
		return w.failPhase(ctx, orgID, "memory_extraction", fmt.Errorf("ellie ingestion worker not configured"))
	}

	agentMemoryReplay, err := importer.ReplayOpenClawAgentMemorySnapshots(ctx, w.DB, orgID)
	if err != nil {
		return w.failPhase(ctx, orgID, "memory_extraction", fmt.Errorf("replay openclaw agent memory snapshots: %w", err))
	}

	pendingRooms, err := w.IngestionStore.CountRoomsForIngestion(ctx, orgID)
	if err != nil {
		return err
	}
	if pendingRooms == 0 {
		label := "memory extraction complete"
		if agentMemoryReplay.ChunksInserted > 0 {
			label = fmt.Sprintf(
				"memory extraction complete (agent_memory_md inserted=%d agents=%d)",
				agentMemoryReplay.ChunksInserted,
				agentMemoryReplay.AgentsProcessed,
			)
		}
		_, err := w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         orgID,
			MigrationType: "memory_extraction",
			Status:        store.MigrationProgressStatusCompleted,
			CurrentLabel:  stringPtr(label),
		})
		return err
	}

	// During migration backfill, we want count-based windows (sliding windows) rather than
	// large time-gap windows so we don't collapse long rooms into a handful of memories.
	w.IngestionWorker.SetOrgID(orgID)
	w.IngestionWorker.SetMode(memory.EllieIngestionModeBackfill)
	runResult, runErr := w.IngestionWorker.RunOnce(ctx)
	if runErr != nil {
		return w.handlePhaseError(ctx, orgID, "memory_extraction", runErr)
	}

	// Refresh pending room count after ingest work.
	pendingRoomsAfter, err := w.IngestionStore.CountRoomsForIngestion(ctx, orgID)
	if err != nil {
		return err
	}
	if pendingRoomsAfter == 0 {
		_, err := w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         orgID,
			MigrationType: "memory_extraction",
			Status:        store.MigrationProgressStatusCompleted,
			CurrentLabel:  stringPtr("memory extraction complete"),
		})
		return err
	}

	if runResult.ProcessedMessages > 0 || runResult.InsertedMemories > 0 || agentMemoryReplay.ChunksInserted > 0 {
		_, _ = w.ProgressStore.Advance(ctx, store.AdvanceMigrationProgressInput{
			OrgID:          orgID,
			MigrationType:  "memory_extraction",
			ProcessedDelta: max(runResult.ProcessedMessages, 0) + max(agentMemoryReplay.ChunksInserted, 0),
			CurrentLabel: stringPtr(fmt.Sprintf(
				"rooms_pending=%d processed_msgs=%d windows=%d inserted=%d (llm=%d heuristic=%d) + agent_memory_md_inserted=%d",
				pendingRoomsAfter,
				runResult.ProcessedMessages,
				runResult.WindowsProcessed,
				runResult.InsertedMemories,
				runResult.InsertedLLMMemories,
				runResult.InsertedHeuristicMemories,
				agentMemoryReplay.ChunksInserted,
			)),
		})
	}

	_, err = w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "memory_extraction",
		Status:        store.MigrationProgressStatusRunning,
		CurrentLabel:  stringPtr(fmt.Sprintf("rooms pending ingestion: %d", pendingRoomsAfter)),
	})
	return err
}

func (w *OpenClawPipelineWorker) reconcileEntitySynthesis(ctx context.Context, orgID string) error {
	if w.EntityWorker == nil {
		return w.failPhase(ctx, orgID, "entity_synthesis", fmt.Errorf("entity synthesis worker not configured"))
	}
	result, err := w.EntityWorker.RunOnce(ctx, orgID)
	if err != nil {
		return w.handlePhaseError(ctx, orgID, "entity_synthesis", err)
	}
	if result.CandidatesConsidered == 0 {
		_, err := w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         orgID,
			MigrationType: "entity_synthesis",
			Status:        store.MigrationProgressStatusCompleted,
			CurrentLabel:  stringPtr("entity synthesis complete"),
		})
		return err
	}
	processed := result.CreatedCount + result.UpdatedCount + result.SkippedExistingCount
	if processed < 0 {
		processed = 0
	}
	_, _ = w.ProgressStore.Advance(ctx, store.AdvanceMigrationProgressInput{
		OrgID:          orgID,
		MigrationType:  "entity_synthesis",
		ProcessedDelta: processed,
		CurrentLabel: stringPtr(fmt.Sprintf(
			"created=%d updated=%d skipped=%d",
			result.CreatedCount,
			result.UpdatedCount,
			result.SkippedExistingCount,
		)),
	})
	return nil
}

func (w *OpenClawPipelineWorker) reconcileMemoryDedup(ctx context.Context, orgID string) error {
	if w.DedupWorker == nil {
		return w.failPhase(ctx, orgID, "memory_dedup", fmt.Errorf("dedup worker not configured"))
	}
	result, err := w.DedupWorker.RunOnce(ctx, orgID)
	if err != nil {
		return w.handlePhaseError(ctx, orgID, "memory_dedup", err)
	}
	if result.PairsDiscovered == 0 && result.ClustersReviewed == 0 {
		_, err := w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         orgID,
			MigrationType: "memory_dedup",
			Status:        store.MigrationProgressStatusCompleted,
			CurrentLabel:  stringPtr("memory dedup complete"),
		})
		return err
	}
	processed := result.ClustersReviewed
	if processed < 0 {
		processed = 0
	}
	_, _ = w.ProgressStore.Advance(ctx, store.AdvanceMigrationProgressInput{
		OrgID:          orgID,
		MigrationType:  "memory_dedup",
		ProcessedDelta: processed,
		CurrentLabel: stringPtr(fmt.Sprintf(
			"pairs=%d clusters=%d deprecated=%d merges=%d",
			result.PairsDiscovered,
			result.ClustersReviewed,
			result.MemoriesDeprecated,
			result.MergesCreated,
		)),
	})
	return nil
}

func (w *OpenClawPipelineWorker) reconcileTaxonomy(ctx context.Context, orgID string) error {
	if w.TaxonomyWorker == nil {
		return w.failPhase(ctx, orgID, "taxonomy_classification", fmt.Errorf("taxonomy worker not configured"))
	}
	// Ensure a baseline taxonomy exists before classification.
	if err := memory.SeedDefaultEllieTaxonomy(ctx, store.NewEllieTaxonomyStore(w.DB), orgID); err != nil {
		return w.failPhase(ctx, orgID, "taxonomy_classification", err)
	}
	result, err := w.TaxonomyWorker.RunOnce(ctx, orgID)
	if err != nil {
		return w.handlePhaseError(ctx, orgID, "taxonomy_classification", err)
	}
	if result.PendingMemories == 0 {
		_, err := w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         orgID,
			MigrationType: "taxonomy_classification",
			Status:        store.MigrationProgressStatusCompleted,
			CurrentLabel:  stringPtr("taxonomy classification complete"),
		})
		return err
	}
	_, _ = w.ProgressStore.Advance(ctx, store.AdvanceMigrationProgressInput{
		OrgID:          orgID,
		MigrationType:  "taxonomy_classification",
		ProcessedDelta: result.ClassifiedMemories,
		FailedDelta:    result.InvalidOutputs,
		CurrentLabel: stringPtr(fmt.Sprintf(
			"pending=%d classified=%d invalid=%d",
			result.PendingMemories,
			result.ClassifiedMemories,
			result.InvalidOutputs,
		)),
	})
	return nil
}

func (w *OpenClawPipelineWorker) reconcileProjectDiscovery(ctx context.Context, orgID string) error {
	result, err := discoverProjectsFromChatMessages(ctx, w.DB, orgID)
	if err != nil {
		return w.failPhase(ctx, orgID, "project_discovery", err)
	}
	_, _ = w.ProgressStore.Advance(ctx, store.AdvanceMigrationProgressInput{
		OrgID:          orgID,
		MigrationType:  "project_discovery",
		ProcessedDelta: result.ProcessedItems,
		CurrentLabel: stringPtr(fmt.Sprintf(
			"projects + issues upserted: %d + %d",
			result.ProjectsCreated+result.ProjectsUpdated,
			result.IssuesCreated+result.IssuesUpdated,
		)),
	})
	_, err = w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "project_discovery",
		Status:        store.MigrationProgressStatusCompleted,
		CurrentLabel:  stringPtr("project discovery complete"),
	})
	return err
}

func (w *OpenClawPipelineWorker) reconcileProjectDocs(ctx context.Context, orgID string) error {
	if w.ProjectDocsScanner == nil || w.ProjectDocsStore == nil {
		// If docs scanning isn't configured (e.g. hosted without local clones), complete the phase.
		_, err := w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
			OrgID:         orgID,
			MigrationType: "project_docs_scanning",
			Status:        store.MigrationProgressStatusCompleted,
			CurrentLabel:  stringPtr("project docs scanning skipped"),
		})
		return err
	}

	type binding struct {
		ProjectID string
		Path      string
	}
	rows, err := w.DB.QueryContext(
		ctx,
		`SELECT project_id::text, local_repo_path
		   FROM project_repo_bindings
		  WHERE org_id = $1
		    AND enabled = TRUE
		    AND local_repo_path IS NOT NULL
		    AND TRIM(local_repo_path) <> ''
		  ORDER BY updated_at ASC`,
		orgID,
	)
	if err != nil {
		return w.failPhase(ctx, orgID, "project_docs_scanning", err)
	}
	defer rows.Close()

	bindings := make([]binding, 0)
	for rows.Next() {
		var b binding
		if err := rows.Scan(&b.ProjectID, &b.Path); err != nil {
			return w.failPhase(ctx, orgID, "project_docs_scanning", err)
		}
		b.ProjectID = strings.TrimSpace(b.ProjectID)
		b.Path = strings.TrimSpace(b.Path)
		if b.ProjectID != "" && b.Path != "" {
			bindings = append(bindings, b)
		}
	}
	if err := rows.Err(); err != nil {
		return w.failPhase(ctx, orgID, "project_docs_scanning", err)
	}

	processedDocs := 0
	updatedDocs := 0
	for _, b := range bindings {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := w.ProjectDocsScanner.ScanAndPersist(ctx, memory.EllieProjectDocsScanAndPersistInput{
			OrgID:       orgID,
			ProjectID:   b.ProjectID,
			ProjectRoot: b.Path,
			Store:       w.ProjectDocsStore,
		})
		if err != nil {
			return w.handlePhaseError(ctx, orgID, "project_docs_scanning", err)
		}
		processedDocs += res.ProcessedDocs
		updatedDocs += res.UpdatedDocs
	}

	_, _ = w.ProgressStore.Advance(ctx, store.AdvanceMigrationProgressInput{
		OrgID:          orgID,
		MigrationType:  "project_docs_scanning",
		ProcessedDelta: processedDocs,
		CurrentLabel:   stringPtr(fmt.Sprintf("docs processed=%d updated=%d", processedDocs, updatedDocs)),
	})

	_, err = w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "project_docs_scanning",
		Status:        store.MigrationProgressStatusCompleted,
		CurrentLabel:  stringPtr("project docs scanning complete"),
	})
	return err
}

func (w *OpenClawPipelineWorker) failPhase(ctx context.Context, orgID, phase string, err error) error {
	if err == nil {
		return nil
	}
	_, _ = w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: phase,
		Status:        store.MigrationProgressStatusFailed,
		Error:         stringPtr(err.Error()),
		CurrentLabel:  stringPtr("failed"),
	})
	return err
}

func (w *OpenClawPipelineWorker) handlePhaseError(ctx context.Context, orgID, phase string, err error) error {
	if err == nil {
		return nil
	}
	if !isOpenClawPipelineTransientError(err) {
		return w.failPhase(ctx, orgID, phase, err)
	}

	label := "waiting for openclaw bridge retry"
	if trimmed := strings.TrimSpace(err.Error()); trimmed != "" {
		if len(trimmed) > 220 {
			trimmed = trimmed[:220] + "..."
		}
		label = fmt.Sprintf("retrying after transient error: %s", trimmed)
	}
	_, setErr := w.ProgressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: phase,
		Status:        store.MigrationProgressStatusRunning,
		CurrentLabel:  stringPtr(label),
		Error:         nil,
	})
	if setErr != nil {
		return setErr
	}
	return nil
}

func isOpenClawPipelineTransientError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ws.ErrOpenClawNotConnected) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "openclaw bridge call failed") ||
		strings.Contains(msg, "openclaw bridge not connected") ||
		strings.Contains(msg, "websocket") ||
		strings.Contains(msg, "econnrefused") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "bad gateway") ||
		strings.Contains(msg, "unexpected server response: 502") ||
		strings.Contains(msg, "executable file not found in $path") {
		return true
	}
	return false
}

func stringPtr(value string) *string {
	v := strings.TrimSpace(value)
	return &v
}

func discoverProjectsFromChatMessages(ctx context.Context, db *sql.DB, orgID string) (importer.OpenClawProjectDiscoveryResult, error) {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return importer.OpenClawProjectDiscoveryResult{}, fmt.Errorf("org_id is required")
	}
	if db == nil {
		return importer.OpenClawProjectDiscoveryResult{}, fmt.Errorf("db is required")
	}

	// Only pull messages that look like they might carry inline hints.
	rows, err := db.QueryContext(
		ctx,
		`SELECT sender_id::text, body, created_at
		   FROM chat_messages
		  WHERE org_id = $1
		    AND (
		      body ILIKE '%project:%'
		      OR body ILIKE '%repo:%'
		      OR body ILIKE '%issue:%'
		      OR body ILIKE '%task:%'
		      OR body ILIKE '%todo:%'
		    )
		  ORDER BY created_at ASC, id ASC
		  LIMIT 200000`,
		orgID,
	)
	if err != nil {
		return importer.OpenClawProjectDiscoveryResult{}, fmt.Errorf("list chat messages for project discovery: %w", err)
	}
	defer rows.Close()

	signals := make([]importer.OpenClawSessionSignal, 0)
	for rows.Next() {
		var (
			senderID string
			body     string
			created  time.Time
		)
		if err := rows.Scan(&senderID, &body, &created); err != nil {
			return importer.OpenClawProjectDiscoveryResult{}, fmt.Errorf("scan project discovery chat message: %w", err)
		}
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		signals = append(signals, importer.OpenClawSessionSignal{
			AgentID:    strings.TrimSpace(senderID),
			Summary:    body,
			OccurredAt: created.UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return importer.OpenClawProjectDiscoveryResult{}, fmt.Errorf("read project discovery chat messages: %w", err)
	}

	reference := time.Now().UTC()
	if raw := strings.TrimSpace(os.Getenv("OTTER_PIPELINE_REFERENCE_TIME_UTC")); raw != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, raw); parseErr == nil && !parsed.IsZero() {
			reference = parsed.UTC()
		}
	}

	return importer.DiscoverOpenClawProjects(ctx, db, importer.OpenClawProjectDiscoveryPersistInput{
		OrgID: orgID,
		ImportInput: importer.OpenClawProjectImportInput{
			Sessions: signals,
		},
		ReferenceTime: reference,
	})
}
