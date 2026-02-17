package importer

import "time"

type OpenClawMigrationSummaryReport struct {
	AgentImportProcessed             int
	HistoryEventsProcessed           int
	HistoryEventsSkipped             int
	HistoryMessagesInserted          int
	HistorySkippedUnknownAgentCounts map[string]int
	EmbeddingPhaseProcessed          int
	EmbeddingPhaseRemaining          int
	EmbeddingPhaseDuration           time.Duration
	MemoryExtractionProcessed        int
	EntitySynthesisProcessed         int
	MemoryDedupProcessed             int
	TaxonomyClassificationProcessed  int
	ProjectDiscoveryProcessed        int
	ProjectDocsScanningProcessed     int
	FailedItems                      int
	Warnings                         []string
}

func BuildOpenClawMigrationSummaryReport(result RunOpenClawMigrationResult) OpenClawMigrationSummaryReport {
	report := OpenClawMigrationSummaryReport{}

	if result.AgentImport != nil {
		report.AgentImportProcessed = result.AgentImport.ImportedAgents
	}
	if result.HistoryBackfill != nil {
		report.HistoryEventsProcessed = result.HistoryBackfill.EventsProcessed
		report.HistoryEventsSkipped = result.HistoryBackfill.EventsSkippedUnknownAgent
		report.HistoryMessagesInserted = result.HistoryBackfill.MessagesInserted
		report.FailedItems += result.HistoryBackfill.FailedItems
		if len(result.HistoryBackfill.SkippedUnknownAgentCounts) > 0 {
			report.HistorySkippedUnknownAgentCounts = make(map[string]int, len(result.HistoryBackfill.SkippedUnknownAgentCounts))
			for slug, count := range result.HistoryBackfill.SkippedUnknownAgentCounts {
				report.HistorySkippedUnknownAgentCounts[slug] = count
			}
		}
		if result.HistoryBackfill.FailedItems > 0 {
			report.Warnings = append(
				report.Warnings,
				"history backfill completed with failed rows recorded in failure ledger",
			)
		}
	}
	if result.EmbeddingPhase != nil {
		report.EmbeddingPhaseProcessed = result.EmbeddingPhase.ProcessedEmbeddings
		report.EmbeddingPhaseRemaining = result.EmbeddingPhase.RemainingEmbeddings
		report.EmbeddingPhaseDuration = result.EmbeddingPhase.Duration
		if result.EmbeddingPhase.TimedOut {
			report.Warnings = append(
				report.Warnings,
				"history embedding phase timed out with remaining backlog",
			)
		}
	}
	if result.EllieBackfill != nil {
		report.MemoryExtractionProcessed = result.EllieBackfill.ProcessedMessages
	}
	if result.EntitySynthesis != nil {
		report.EntitySynthesisProcessed = result.EntitySynthesis.ProcessedEntities
	}
	if result.Dedup != nil {
		report.MemoryDedupProcessed = result.Dedup.ProcessedClusters
	}
	if result.TaxonomyPhase != nil {
		report.TaxonomyClassificationProcessed = result.TaxonomyPhase.ProcessedMemories
	}
	if result.ProjectDiscovery != nil {
		report.ProjectDiscoveryProcessed = result.ProjectDiscovery.ProcessedItems
	}
	if result.ProjectDocsScanning != nil {
		report.ProjectDocsScanningProcessed = result.ProjectDocsScanning.ProcessedDocs
	}
	if result.Paused {
		report.Warnings = append(report.Warnings, "migration paused before all phases completed")
	}
	return report
}
