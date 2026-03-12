package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/projectfailure"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const projectBootstrapRestartBundleSettingsKey = "bootstrap_restart_bundle"

type projectBootstrapRestartBundle struct {
	OperatorBrief       string                               `json:"operator_brief,omitempty"`
	OperatorConstraints []string                             `json:"operator_constraints,omitempty"`
	Environments        []projectBootstrapEnvironmentBinding `json:"environment_bindings,omitempty"`
	Remotes             []projectBootstrapRemoteBinding      `json:"remote_bindings,omitempty"`
	SourceSessionID     string                               `json:"source_session_id,omitempty"`
	SourceMessageID     string                               `json:"source_message_id,omitempty"`
	SourceProjectID     string                               `json:"source_project_id,omitempty"`
	RestartProjectID    string                               `json:"restart_project_id,omitempty"`
	RetryBudget         int                                  `json:"retry_budget,omitempty"`
	RetryAttemptCount   int                                  `json:"retry_attempt_count,omitempty"`
	FailureHistory      []projectfailure.FailureHistoryEntry `json:"failure_history,omitempty"`
	CapturedAt          *time.Time                           `json:"captured_at,omitempty"`
}

type projectBootstrapEnvironmentBinding struct {
	Name          string `json:"name,omitempty"`
	DeliveryMode  string `json:"delivery_mode,omitempty"`
	RemoteName    string `json:"remote_name,omitempty"`
	RepoURL       string `json:"repo_url,omitempty"`
	RepoPath      string `json:"repo_path,omitempty"`
	TargetBranch  string `json:"target_branch,omitempty"`
	IsActive      bool   `json:"is_active,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
}

type projectBootstrapRemoteBinding struct {
	Name          string `json:"name,omitempty"`
	URL           string `json:"url,omitempty"`
	Transport     string `json:"transport,omitempty"`
	IsDefault     bool   `json:"is_default,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
}

func projectBootstrapRestartBundleFromSettings(settings json.RawMessage) projectBootstrapRestartBundle {
	if len(settings) == 0 || !json.Valid(settings) {
		return normalizeProjectBootstrapRestartBundle(projectBootstrapRestartBundle{})
	}
	var payload map[string]any
	if err := json.Unmarshal(settings, &payload); err != nil {
		return normalizeProjectBootstrapRestartBundle(projectBootstrapRestartBundle{})
	}
	raw, ok := payload[projectBootstrapRestartBundleSettingsKey]
	if !ok || raw == nil {
		return normalizeProjectBootstrapRestartBundle(projectBootstrapRestartBundle{})
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return normalizeProjectBootstrapRestartBundle(projectBootstrapRestartBundle{})
	}
	var bundle projectBootstrapRestartBundle
	if err := json.Unmarshal(encoded, &bundle); err != nil {
		return normalizeProjectBootstrapRestartBundle(projectBootstrapRestartBundle{})
	}
	return normalizeProjectBootstrapRestartBundle(bundle)
}

func normalizeProjectBootstrapRestartBundle(bundle projectBootstrapRestartBundle) projectBootstrapRestartBundle {
	bundle.OperatorBrief = strings.TrimSpace(bundle.OperatorBrief)
	bundle.SourceSessionID = strings.TrimSpace(bundle.SourceSessionID)
	bundle.SourceMessageID = strings.TrimSpace(bundle.SourceMessageID)
	bundle.SourceProjectID = strings.TrimSpace(bundle.SourceProjectID)
	bundle.RestartProjectID = strings.TrimSpace(bundle.RestartProjectID)
	if bundle.RetryBudget <= 0 {
		bundle.RetryBudget = defaultProjectBootstrapRestartRetryBudget
	}
	if bundle.RetryAttemptCount < 0 {
		bundle.RetryAttemptCount = 0
	}
	bundle.FailureHistory = normalizeProjectBootstrapFailureHistory(bundle.FailureHistory)
	bundle.OperatorConstraints = normalizeStringList(bundle.OperatorConstraints)
	for i := range bundle.Environments {
		bundle.Environments[i].Name = strings.TrimSpace(bundle.Environments[i].Name)
		bundle.Environments[i].DeliveryMode = strings.TrimSpace(bundle.Environments[i].DeliveryMode)
		bundle.Environments[i].RemoteName = strings.TrimSpace(bundle.Environments[i].RemoteName)
		bundle.Environments[i].RepoURL = strings.TrimSpace(bundle.Environments[i].RepoURL)
		bundle.Environments[i].RepoPath = strings.TrimSpace(bundle.Environments[i].RepoPath)
		bundle.Environments[i].TargetBranch = strings.TrimSpace(bundle.Environments[i].TargetBranch)
		bundle.Environments[i].CredentialRef = strings.TrimSpace(bundle.Environments[i].CredentialRef)
	}
	for i := range bundle.Remotes {
		bundle.Remotes[i].Name = strings.TrimSpace(bundle.Remotes[i].Name)
		bundle.Remotes[i].URL = strings.TrimSpace(bundle.Remotes[i].URL)
		bundle.Remotes[i].Transport = strings.TrimSpace(bundle.Remotes[i].Transport)
		bundle.Remotes[i].CredentialRef = strings.TrimSpace(bundle.Remotes[i].CredentialRef)
	}
	return bundle
}

func applyProjectBootstrapRestartBundle(settings json.RawMessage, bundle projectBootstrapRestartBundle) (json.RawMessage, error) {
	payload := map[string]any{}
	if len(settings) > 0 && json.Valid(settings) {
		if err := json.Unmarshal(settings, &payload); err != nil {
			return nil, err
		}
	}
	payload[projectBootstrapRestartBundleSettingsKey] = bundle
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func clearProjectBootstrapRuntimeSettings(settings json.RawMessage) json.RawMessage {
	payload := map[string]any{}
	if len(settings) > 0 && json.Valid(settings) {
		if err := json.Unmarshal(settings, &payload); err != nil {
			return json.RawMessage(`{}`)
		}
	}
	delete(payload, "automatic_failure")
	delete(payload, projectBootstrapMetadataKey)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func normalizeStringList(items []string) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func parseBootstrapConstraints(metadata json.RawMessage) []string {
	values := messageMetadataMap(metadata)
	candidates := []string{"operator_constraints", "bootstrap_constraints", "constraints"}
	for _, key := range candidates {
		raw, ok := values[key]
		if !ok || raw == nil {
			continue
		}
		switch typed := raw.(type) {
		case []any:
			items := make([]string, 0, len(typed))
			for _, item := range typed {
				if value := stringValue(item); value != "" {
					items = append(items, value)
				}
			}
			return normalizeStringList(items)
		case string:
			return normalizeStringList([]string{typed})
		}
	}
	return nil
}

func (e *TurnEngine) captureProjectBootstrapRestartBundle(ctx context.Context, projectRecord repo.Project) (repo.Project, projectBootstrapRestartBundle, error) {
	bundle := projectBootstrapRestartBundleFromSettings(projectRecord.Settings)
	if e == nil || e.pool == nil || projectRecord.ID == uuid.Nil {
		return projectRecord, bundle, nil
	}

	session, err := e.latestProjectBootstrapSession(ctx, projectRecord.OrganizationID, projectRecord.ID)
	if err != nil {
		return projectRecord, bundle, err
	}
	if strings.TrimSpace(bundle.SourceProjectID) == "" {
		bundle.SourceProjectID = projectRecord.ID.String()
	}
	if session != nil {
		bundle.SourceSessionID = session.ID.String()
		state := projectBootstrapStateFromMetadata(session.Metadata)
		messageID, ok := parseUUIDAny(state.InitialMessageID)
		if !ok || messageID == uuid.Nil {
			messageID = earliestProjectBootstrapUserMessageID(ctx, e.messages, session.ID)
		}
		if messageID != uuid.Nil && e.messages != nil {
			message, getErr := e.messages.GetByID(ctx, messageID)
			if getErr == nil {
				if bundle.OperatorBrief == "" {
					bundle.OperatorBrief = strings.TrimSpace(message.Content)
				}
				if len(bundle.OperatorConstraints) == 0 {
					bundle.OperatorConstraints = parseBootstrapConstraints(message.Metadata)
				}
				bundle.SourceMessageID = message.ID.String()
			}
		}
	}

	remoteRepo := repo.NewProjectRemoteRepo(e.pool)
	environmentRepo := repo.NewProjectEnvironmentRepo(e.pool)
	remotes, err := remoteRepo.ListByProject(ctx, projectRecord.ID)
	if err != nil {
		return projectRecord, bundle, err
	}
	environments, err := environmentRepo.ListByProject(ctx, projectRecord.ID)
	if err != nil {
		return projectRecord, bundle, err
	}
	if len(bundle.Remotes) == 0 {
		bundle.Remotes = snapshotProjectBootstrapRemotes(remotes)
	}
	if len(bundle.Environments) == 0 {
		bundle.Environments = snapshotProjectBootstrapEnvironments(environments, remotes)
	}
	if bundle.CapturedAt == nil {
		now := e.now().UTC()
		bundle.CapturedAt = &now
	}

	updatedSettings, err := applyProjectBootstrapRestartBundle(projectRecord.Settings, bundle)
	if err != nil {
		return projectRecord, bundle, err
	}
	if string(updatedSettings) == string(projectRecord.Settings) {
		return projectRecord, bundle, nil
	}
	projectRecord.Settings = updatedSettings
	updatedProject, err := repo.NewProjectRepo(e.pool).Update(ctx, projectRecord)
	if err != nil {
		return projectRecord, bundle, err
	}
	return updatedProject, bundle, nil
}

func (e *TurnEngine) latestProjectBootstrapSession(ctx context.Context, organizationID, projectID uuid.UUID) (*repo.ChatSession, error) {
	if e == nil || e.pool == nil || organizationID == uuid.Nil || projectID == uuid.Nil {
		return nil, nil
	}
	sessions, err := repo.NewChatSessionRepo(e.pool).ListByOrg(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	var matches []repo.ChatSession
	for _, item := range sessions {
		if item.ScopeID != projectID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.ScopeType), "project") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.Mode), "async") {
			continue
		}
		matches = append(matches, item)
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.Slice(matches, func(i, j int) bool {
		left := matches[i]
		right := matches[j]
		switch {
		case left.LastMessageAt != nil && right.LastMessageAt != nil && !left.LastMessageAt.Equal(*right.LastMessageAt):
			return left.LastMessageAt.After(*right.LastMessageAt)
		case left.LastMessageAt != nil && right.LastMessageAt == nil:
			return true
		case left.LastMessageAt == nil && right.LastMessageAt != nil:
			return false
		case !left.CreatedAt.Equal(right.CreatedAt):
			return left.CreatedAt.After(right.CreatedAt)
		default:
			return left.ID.String() > right.ID.String()
		}
	})
	session := matches[0]
	return &session, nil
}

func earliestProjectBootstrapUserMessageID(ctx context.Context, messages messageRepository, sessionID uuid.UUID) uuid.UUID {
	if messages == nil || sessionID == uuid.Nil {
		return uuid.Nil
	}
	items, err := messages.ListBySession(ctx, sessionID)
	if err != nil {
		return uuid.Nil
	}
	for _, item := range items {
		if !strings.EqualFold(strings.TrimSpace(item.Role), "user") {
			continue
		}
		return item.ID
	}
	return uuid.Nil
}

func snapshotProjectBootstrapRemotes(remotes []repo.ProjectRemote) []projectBootstrapRemoteBinding {
	out := make([]projectBootstrapRemoteBinding, 0, len(remotes))
	for _, item := range remotes {
		out = append(out, projectBootstrapRemoteBinding{
			Name:          strings.TrimSpace(item.Name),
			URL:           strings.TrimSpace(item.URL),
			Transport:     strings.TrimSpace(item.Transport),
			IsDefault:     item.IsDefault,
			CredentialRef: strings.TrimSpace(pointerString(item.CredentialRef)),
		})
	}
	return out
}

func snapshotProjectBootstrapEnvironments(environments []repo.ProjectEnvironment, remotes []repo.ProjectRemote) []projectBootstrapEnvironmentBinding {
	remoteByID := make(map[uuid.UUID]repo.ProjectRemote, len(remotes))
	for _, item := range remotes {
		remoteByID[item.ID] = item
	}
	sort.Slice(environments, func(i, j int) bool {
		return environments[i].CreatedAt.Before(environments[j].CreatedAt)
	})
	out := make([]projectBootstrapEnvironmentBinding, 0, len(environments))
	for _, item := range environments {
		binding := projectBootstrapEnvironmentBinding{
			Name:         strings.TrimSpace(item.Name),
			DeliveryMode: strings.TrimSpace(item.DeliveryMode),
			RepoURL:      strings.TrimSpace(pointerString(item.RepoURL)),
			RepoPath:     strings.TrimSpace(pointerString(item.RepoPath)),
			TargetBranch: strings.TrimSpace(item.TargetBranch),
			IsActive:     item.IsActive,
		}
		if item.RemoteID != nil {
			if remoteRecord, ok := remoteByID[*item.RemoteID]; ok {
				binding.RemoteName = strings.TrimSpace(remoteRecord.Name)
				binding.CredentialRef = strings.TrimSpace(pointerString(remoteRecord.CredentialRef))
				if binding.RepoURL == "" {
					binding.RepoURL = strings.TrimSpace(remoteRecord.URL)
				}
			}
		}
		out = append(out, binding)
	}
	return out
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func normalizeProjectBootstrapFailureHistory(entries []projectfailure.FailureHistoryEntry) []projectfailure.FailureHistoryEntry {
	out := make([]projectfailure.FailureHistoryEntry, 0, len(entries))
	for _, entry := range entries {
		normalized := projectfailure.FailureHistoryEntry{
			ProjectID:                strings.TrimSpace(entry.ProjectID),
			RetryAttemptCount:        entry.RetryAttemptCount,
			FailureCategory:          strings.TrimSpace(entry.FailureCategory),
			FailureClass:             strings.TrimSpace(entry.FailureClass),
			FailurePhase:             strings.TrimSpace(entry.FailurePhase),
			LastCheckpoint:           strings.TrimSpace(entry.LastCheckpoint),
			LastSuccessfulCheckpoint: strings.TrimSpace(entry.LastSuccessfulCheckpoint),
			FailureReason:            strings.TrimSpace(entry.FailureReason),
			SetupPersisted:           entry.SetupPersisted,
			RecordedAt:               cloneTimePointer(entry.RecordedAt),
		}
		if normalized.ProjectID == "" &&
			normalized.FailureCategory == "" &&
			normalized.FailureClass == "" &&
			normalized.FailurePhase == "" &&
			normalized.LastCheckpoint == "" &&
			normalized.LastSuccessfulCheckpoint == "" &&
			normalized.FailureReason == "" &&
			normalized.RetryAttemptCount == 0 &&
			!normalized.SetupPersisted &&
			normalized.RecordedAt == nil {
			continue
		}
		out = append(out, normalized)
	}
	return out
}

func appendProjectBootstrapFailureHistory(bundle projectBootstrapRestartBundle, projectRecord repo.Project, record projectAutomaticFailureRecord) projectBootstrapRestartBundle {
	entry := projectfailure.FailureHistoryEntry{
		ProjectID:                projectRecord.ID.String(),
		RetryAttemptCount:        bundle.RetryAttemptCount,
		FailureCategory:          strings.TrimSpace(record.FailureCategory),
		FailureClass:             strings.TrimSpace(record.FailureClass),
		FailurePhase:             strings.TrimSpace(record.FailurePhase),
		LastCheckpoint:           strings.TrimSpace(record.LastCheckpoint),
		LastSuccessfulCheckpoint: strings.TrimSpace(record.LastSuccessfulCheckpoint),
		FailureReason:            strings.TrimSpace(record.FailureReason),
		SetupPersisted:           record.SetupPersisted,
		RecordedAt:               cloneTimePointer(&record.RecordedAt),
	}
	history := normalizeProjectBootstrapFailureHistory(bundle.FailureHistory)
	if len(history) != 0 {
		latest := history[len(history)-1]
		if latest.ProjectID == entry.ProjectID &&
			latest.RetryAttemptCount == entry.RetryAttemptCount &&
			latest.FailureClass == entry.FailureClass &&
			latest.FailureReason == entry.FailureReason {
			bundle.FailureHistory = history
			return bundle
		}
	}
	history = append(history, entry)
	bundle.FailureHistory = history
	return bundle
}

func applyProjectBootstrapRetryFailureState(settings json.RawMessage, bundle projectBootstrapRestartBundle, record projectAutomaticFailureRecord) (json.RawMessage, error) {
	state := projectfailure.Parse(settings)
	state.LastSuccessfulCheckpoint = strings.TrimSpace(record.LastSuccessfulCheckpoint)
	state.SetupPersisted = record.SetupPersisted
	state.RetryBudget = bundle.RetryBudget
	state.RetryAttemptCount = bundle.RetryAttemptCount
	state.FailureHistory = normalizeProjectBootstrapFailureHistory(bundle.FailureHistory)
	return projectfailure.Apply(settings, state)
}

func (e *TurnEngine) maybeRestartArchivedBootstrapProject(ctx context.Context, archivedProject repo.Project, record projectAutomaticFailureRecord) error {
	if e == nil || e.pool == nil || e.events == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(record.Action), projectFailureActionArchive) {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(record.Source), projectBootstrapSource) {
		return nil
	}

	projectRepo := repo.NewProjectRepo(e.pool)
	currentArchived, err := projectRepo.GetByID(ctx, archivedProject.ID)
	if err != nil {
		return err
	}

	updatedProject, bundle, err := e.captureProjectBootstrapRestartBundle(ctx, currentArchived)
	if err != nil {
		return err
	}
	if strings.TrimSpace(bundle.OperatorBrief) == "" {
		return nil
	}
	if restartID, ok := parseUUIDAny(bundle.RestartProjectID); ok && restartID != uuid.Nil {
		if restarted, getErr := projectRepo.GetByID(ctx, restartID); getErr == nil && strings.EqualFold(strings.TrimSpace(restarted.Status), "active") {
			return nil
		}
	}
	if existingRestart, ok, err := e.findAnyActiveBootstrapRestartProject(ctx, updatedProject.OrganizationID, updatedProject.ID); err != nil {
		return err
	} else if ok {
		bundle.RestartProjectID = existingRestart.ID.String()
		updatedProject.Settings, err = applyProjectBootstrapRestartBundle(updatedProject.Settings, bundle)
		if err != nil {
			return err
		}
		if _, err := projectRepo.Update(ctx, updatedProject); err != nil {
			return err
		}
		return nil
	}
	plannedRetryAttempt := bundle.RetryAttemptCount + 1
	if existingRestart, ok, err := e.findExistingBootstrapRestartProject(ctx, updatedProject.OrganizationID, updatedProject.ID, plannedRetryAttempt); err != nil {
		return err
	} else if ok {
		bundle.RestartProjectID = existingRestart.ID.String()
		updatedProject.Settings, err = applyProjectBootstrapRestartBundle(updatedProject.Settings, bundle)
		if err != nil {
			return err
		}
		if _, err := projectRepo.Update(ctx, updatedProject); err != nil {
			return err
		}
		return nil
	}
	bundle = appendProjectBootstrapFailureHistory(bundle, updatedProject, record)
	updatedProject.Settings, err = applyProjectBootstrapRestartBundle(updatedProject.Settings, bundle)
	if err != nil {
		return err
	}
	updatedProject.Settings, err = applyProjectBootstrapRetryFailureState(updatedProject.Settings, bundle, record)
	if err != nil {
		return err
	}
	if _, err := projectRepo.Update(ctx, updatedProject); err != nil {
		return err
	}
	if bundle.RetryAttemptCount >= bundle.RetryBudget {
		return nil
	}

	projectService, err := projectsvc.NewService(projectsvc.Options{
		Pool:    e.pool,
		DataDir: e.dataDir,
		Events:  e.events,
	})
	if err != nil {
		return err
	}

	bundle.RetryAttemptCount++
	restartSettings, err := applyProjectBootstrapRestartBundle(clearProjectBootstrapRuntimeSettings(updatedProject.Settings), bundle)
	if err != nil {
		return err
	}
	restartSlug, err := e.nextBootstrapRestartSlug(ctx, updatedProject.OrganizationID, updatedProject.Slug)
	if err != nil {
		return err
	}
	created, err := projectService.Create(ctx, projectsvc.CreateProjectRequest{
		OrganizationID: updatedProject.OrganizationID,
		Slug:           restartSlug,
		DisplayName:    updatedProject.DisplayName,
		Description:    updatedProject.Description,
		DeliveryMode:   updatedProject.DeliveryMode,
		Settings:       restartSettings,
		CreatedByType:  "system",
	})
	if err != nil {
		return err
	}
	if err := e.applyBootstrapRestartBindings(ctx, bundle, created.ID); err != nil {
		return err
	}

	sessionMetadata, err := json.Marshal(map[string]any{
		"bootstrap_restart": map[string]any{
			"source_project_id": updatedProject.ID.String(),
		},
	})
	if err != nil {
		return err
	}
	restartSession, err := e.chat.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: created.OrganizationID,
		ScopeType:      "project",
		ScopeID:        created.ID,
		Mode:           "async",
		Metadata:       sessionMetadata,
	})
	if err != nil {
		return err
	}
	if frankID, frankErr := e.resolveFrankStarterID(ctx, created.OrganizationID); frankErr == nil && frankID != uuid.Nil {
		_ = e.ensureAgentParticipant(ctx, restartSession.ID, frankID)
	}
	if loriID, loriErr := e.resolveLoriStarterID(ctx, created.OrganizationID); loriErr == nil && loriID != uuid.Nil {
		_ = e.ensureAgentParticipant(ctx, restartSession.ID, loriID)
	}

	authorType := "agent"
	var authorID *uuid.UUID
	if frankID, frankErr := e.resolveFrankStarterID(ctx, created.OrganizationID); frankErr == nil && frankID != uuid.Nil {
		authorID = &frankID
	} else if loriID, loriErr := e.resolveLoriStarterID(ctx, created.OrganizationID); loriErr == nil && loriID != uuid.Nil {
		authorID = &loriID
	}
	restartMessage, err := e.chat.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  restartSession.ID,
		AuthorType: &authorType,
		AuthorID:   authorID,
		Role:       "user",
		Content:    buildProjectBootstrapRestartPrompt(bundle, updatedProject, *created),
		Metadata: mustJSONRaw(map[string]any{
			"source":                      projectBootstrapSource,
			"bootstrap_restart":           true,
			"bootstrap_source_project_id": updatedProject.ID.String(),
			"bootstrap_source_session_id": bundle.SourceSessionID,
			"bootstrap_source_message_id": bundle.SourceMessageID,
		}),
	})
	if err != nil {
		return err
	}
	var targetAgentID *uuid.UUID
	if loriID, loriErr := e.resolveLoriStarterID(ctx, created.OrganizationID); loriErr == nil && loriID != uuid.Nil {
		targetAgentID = &loriID
	} else if frankID, frankErr := e.resolveFrankStarterID(ctx, created.OrganizationID); frankErr == nil && frankID != uuid.Nil {
		targetAgentID = &frankID
	}
	payload := AgentTurnPayload{
		SessionID: restartSession.ID,
		MessageID: restartMessage.ID,
		AgentID:   targetAgentID,
	}
	if _, err := e.enqueueAgentTurnIfActive(ctx, restartSession, payload, nil); err != nil {
		return err
	}

	bundle.RestartProjectID = created.ID.String()
	updatedProject.Settings, err = applyProjectBootstrapRestartBundle(updatedProject.Settings, bundle)
	if err != nil {
		return err
	}
	if _, err := projectRepo.Update(ctx, updatedProject); err != nil {
		return err
	}
	return repo.NewChatSessionRepo(e.pool).CloseProjectScoped(ctx, updatedProject.ID)
}

func (e *TurnEngine) findExistingBootstrapRestartProject(ctx context.Context, organizationID, sourceProjectID uuid.UUID, retryAttemptCount int) (repo.Project, bool, error) {
	if e == nil || e.pool == nil || organizationID == uuid.Nil || sourceProjectID == uuid.Nil || retryAttemptCount <= 0 {
		return repo.Project{}, false, nil
	}
	projects, err := repo.NewProjectRepo(e.pool).ListAll(ctx, organizationID)
	if err != nil {
		return repo.Project{}, false, err
	}
	for _, item := range projects {
		bundle := projectBootstrapRestartBundleFromSettings(item.Settings)
		if !strings.EqualFold(strings.TrimSpace(bundle.SourceProjectID), sourceProjectID.String()) {
			continue
		}
		if bundle.RetryAttemptCount != retryAttemptCount {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.Status), "active") {
			continue
		}
		return item, true, nil
	}
	return repo.Project{}, false, nil
}

func (e *TurnEngine) findAnyActiveBootstrapRestartProject(ctx context.Context, organizationID, sourceProjectID uuid.UUID) (repo.Project, bool, error) {
	if e == nil || e.pool == nil || organizationID == uuid.Nil || sourceProjectID == uuid.Nil {
		return repo.Project{}, false, nil
	}
	projects, err := repo.NewProjectRepo(e.pool).ListAll(ctx, organizationID)
	if err != nil {
		return repo.Project{}, false, err
	}
	for _, item := range projects {
		if !strings.EqualFold(strings.TrimSpace(item.Status), "active") {
			continue
		}
		bundle := projectBootstrapRestartBundleFromSettings(item.Settings)
		if !strings.EqualFold(strings.TrimSpace(bundle.SourceProjectID), sourceProjectID.String()) {
			continue
		}
		return item, true, nil
	}
	return repo.Project{}, false, nil
}

func mustJSONRaw(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(encoded)
}

func buildProjectBootstrapRestartPrompt(bundle projectBootstrapRestartBundle, archivedProject repo.Project, restartedProject repo.Project) string {
	lines := []string{
		fmt.Sprintf("Start a fresh bootstrap restart for project slug=%s project_id=%s.", strings.TrimSpace(restartedProject.Slug), restartedProject.ID),
		fmt.Sprintf("The previous bootstrap project slug=%s project_id=%s was archived for audit. Do not reuse its project-session chatter, partial task tree, or stale runtime ownership as live context.", strings.TrimSpace(archivedProject.Slug), archivedProject.ID),
		"Use only this canonical bootstrap bundle as the live restart input:",
		fmt.Sprintf("Operator brief: %s", strings.TrimSpace(bundle.OperatorBrief)),
	}
	if len(bundle.OperatorConstraints) > 0 {
		lines = append(lines, "Explicit operator constraints:")
		for _, item := range bundle.OperatorConstraints {
			lines = append(lines, "- "+item)
		}
	}
	if len(bundle.Environments) > 0 {
		lines = append(lines, "Environment and repo bindings:")
		for _, item := range bundle.Environments {
			line := fmt.Sprintf("- %s delivery_mode=%s repo_path=%s target_branch=%s", strings.TrimSpace(item.Name), strings.TrimSpace(item.DeliveryMode), strings.TrimSpace(item.RepoPath), strings.TrimSpace(item.TargetBranch))
			if strings.TrimSpace(item.RepoURL) != "" {
				line += fmt.Sprintf(" repo_url=%s", strings.TrimSpace(item.RepoURL))
			}
			if strings.TrimSpace(item.CredentialRef) != "" {
				line += fmt.Sprintf(" credential_ref=%s", strings.TrimSpace(item.CredentialRef))
			}
			lines = append(lines, line)
		}
	}
	if len(bundle.Remotes) > 0 {
		lines = append(lines, "Remote and credential bindings:")
		for _, item := range bundle.Remotes {
			line := fmt.Sprintf("- %s %s transport=%s", strings.TrimSpace(item.Name), strings.TrimSpace(item.URL), strings.TrimSpace(item.Transport))
			if strings.TrimSpace(item.CredentialRef) != "" {
				line += fmt.Sprintf(" credential_ref=%s", strings.TrimSpace(item.CredentialRef))
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, "Create a fresh bootstrap handoff and setup plan from this canonical input only.")
	return strings.Join(lines, "\n")
}

func (e *TurnEngine) applyBootstrapRestartBindings(ctx context.Context, bundle projectBootstrapRestartBundle, projectID uuid.UUID) error {
	if e == nil || e.pool == nil || projectID == uuid.Nil {
		return nil
	}
	remoteRepo := repo.NewProjectRemoteRepo(e.pool)
	environmentRepo := repo.NewProjectEnvironmentRepo(e.pool)

	remoteIDs := make(map[string]uuid.UUID, len(bundle.Remotes))
	for _, item := range bundle.Remotes {
		created, err := remoteRepo.Create(ctx, repo.ProjectRemote{
			ProjectID:     projectID,
			Name:          item.Name,
			URL:           item.URL,
			IsDefault:     item.IsDefault,
			Transport:     item.Transport,
			CredentialRef: stringPtr(item.CredentialRef),
		})
		if err != nil {
			return err
		}
		remoteIDs[strings.ToLower(strings.TrimSpace(item.Name))] = created.ID
	}

	existing, err := environmentRepo.ListByProject(ctx, projectID)
	if err != nil {
		return err
	}
	if len(bundle.Environments) == 0 {
		return nil
	}
	sort.Slice(existing, func(i, j int) bool {
		return existing[i].CreatedAt.Before(existing[j].CreatedAt)
	})
	for index, item := range bundle.Environments {
		env := repo.ProjectEnvironment{
			ProjectID:    projectID,
			Name:         item.Name,
			DeliveryMode: item.DeliveryMode,
			RepoURL:      stringPtr(item.RepoURL),
			RepoPath:     stringPtr(item.RepoPath),
			TargetBranch: item.TargetBranch,
			IsActive:     item.IsActive,
		}
		if remoteID, ok := remoteIDs[strings.ToLower(strings.TrimSpace(item.RemoteName))]; ok && remoteID != uuid.Nil {
			env.RemoteID = &remoteID
		}
		if index < len(existing) {
			env.ID = existing[index].ID
			env.DeployTaskID = existing[index].DeployTaskID
			env.ScheduleID = existing[index].ScheduleID
			if currentPath := strings.TrimSpace(pointerString(existing[index].RepoPath)); currentPath != "" {
				env.RepoPath = stringPtr(currentPath)
			}
			if _, err := environmentRepo.Update(ctx, env); err != nil {
				return err
			}
			continue
		}
		if _, err := environmentRepo.Create(ctx, env); err != nil {
			return err
		}
	}
	return nil
}

func (e *TurnEngine) nextBootstrapRestartSlug(ctx context.Context, organizationID uuid.UUID, baseSlug string) (string, error) {
	projectRepo := repo.NewProjectRepo(e.pool)
	projects, err := projectRepo.ListAll(ctx, organizationID)
	if err != nil {
		return "", err
	}
	used := make(map[string]struct{}, len(projects))
	for _, item := range projects {
		used[strings.ToLower(strings.TrimSpace(item.Slug))] = struct{}{}
	}
	trimmedBase := strings.TrimSpace(baseSlug)
	if trimmedBase == "" {
		trimmedBase = "project"
	}
	candidate := trimmedBase + "-restart"
	if _, ok := used[strings.ToLower(candidate)]; !ok {
		return candidate, nil
	}
	for attempt := 2; ; attempt++ {
		candidate = fmt.Sprintf("%s-restart-%d", trimmedBase, attempt)
		if _, ok := used[strings.ToLower(candidate)]; !ok {
			return candidate, nil
		}
	}
}
