package importer

type OpenClawMigrationSummaryReport struct {
	AgentImportProcessed      int
	HistoryEventsProcessed    int
	HistoryMessagesInserted   int
	MemoryExtractionProcessed int
	ProjectDiscoveryProcessed int
	FailedItems               int
	Warnings                  []string
}

func BuildOpenClawMigrationSummaryReport(result RunOpenClawMigrationResult) OpenClawMigrationSummaryReport {
	report := OpenClawMigrationSummaryReport{}

	if result.AgentImport != nil {
		report.AgentImportProcessed = result.AgentImport.ImportedAgents
	}
	if result.HistoryBackfill != nil {
		report.HistoryEventsProcessed = result.HistoryBackfill.EventsProcessed
		report.HistoryMessagesInserted = result.HistoryBackfill.MessagesInserted
	}
	if result.EllieBackfill != nil {
		report.MemoryExtractionProcessed = result.EllieBackfill.ProcessedMessages
	}
	if result.ProjectDiscovery != nil {
		report.ProjectDiscoveryProcessed = result.ProjectDiscovery.ProcessedItems
	}
	if result.Paused {
		report.Warnings = append(report.Warnings, "migration paused before all phases completed")
	}
	return report
}
