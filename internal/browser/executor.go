package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
)

const (
	defaultIdleCheckInterval = 5 * time.Minute
	defaultIdleTimeout       = time.Hour
)

var autoScreenshotActions = map[string]struct{}{
	"navigate":            {},
	"click":               {},
	"type":                {},
	"select":              {},
	"hover":               {},
	"scroll":              {},
	"press_key":           {},
	"wait_for_navigation": {},
	"back":                {},
	"forward":             {},
	"refresh":             {},
	"evaluate":            {},
}

type sessionRepository interface {
	Create(ctx context.Context, session BrowserSession) (BrowserSession, error)
	Get(ctx context.Context, id uuid.UUID) (BrowserSession, error)
	GetActiveByTask(ctx context.Context, taskID uuid.UUID) (BrowserSession, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, revokedAt *time.Time) (BrowserSession, error)
	UpdateCurrentURL(ctx context.Context, id uuid.UUID, currentURL *string) (BrowserSession, error)
	UpdateIdleSince(ctx context.Context, id uuid.UUID, idleSince *time.Time) (BrowserSession, error)
}

type actionRepository interface {
	Create(ctx context.Context, action BrowserAction) (BrowserAction, error)
	UpdateCompletion(ctx context.Context, id uuid.UUID, update ActionCompletion) (BrowserAction, error)
}

type handoffRepository interface {
	Create(ctx context.Context, handoff BrowserHandoff) (BrowserHandoff, error)
	Get(ctx context.Context, id uuid.UUID) (BrowserHandoff, error)
	GetByInboxItem(ctx context.Context, inboxItemID uuid.UUID) (BrowserHandoff, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, update HandoffStatusUpdate) (BrowserHandoff, error)
}

type projectReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type runService interface {
	PauseRun(ctx context.Context, runID uuid.UUID) error
	ResumeRun(ctx context.Context, runID uuid.UUID) error
	FailRun(ctx context.Context, runID uuid.UUID, reason, failureClass string) error
}

type inboxRepository interface {
	Create(ctx context.Context, item repo.InboxItem) (repo.InboxItem, error)
	MarkActed(ctx context.Context, id, actedByID uuid.UUID) (repo.InboxItem, error)
}

type runArtifactRepository interface {
	Create(ctx context.Context, artifact controlplane.RunArtifact) (controlplane.RunArtifact, error)
}

type credentialResolver interface {
	ResolveForBrowserSession(ctx context.Context, agentID uuid.UUID) ([]BrowserCredential, error)
}

type noopCredentialResolver struct{}

func (noopCredentialResolver) ResolveForBrowserSession(context.Context, uuid.UUID) ([]BrowserCredential, error) {
	return nil, nil
}

type ExecutorOptions struct {
	Pool *pgxpool.Pool

	Sessions sessionRepository
	Actions  actionRepository
	Handoffs handoffRepository
	Projects projectReader
	Runs     runService
	Inbox    inboxRepository

	Artifacts runArtifactRepository
	Store     storage.Store

	PolicyChecker DomainPolicyChecker
	Secrets       credentialResolver
	DriverFactory DriverFactory

	IdleCheckInterval time.Duration
	IdleTimeout       time.Duration
	Now               func() time.Time
	Logger            *slog.Logger
}

type Executor struct {
	sessions sessionRepository
	actions  actionRepository
	handoffs handoffRepository
	projects projectReader
	runs     runService
	inbox    inboxRepository

	artifacts runArtifactRepository
	store     storage.Store

	policyChecker DomainPolicyChecker
	secrets       credentialResolver
	driverFactory DriverFactory

	idleCheckInterval time.Duration
	idleTimeout       time.Duration
	now               func() time.Time
	logger            *slog.Logger

	mu            sync.Mutex
	drivers       map[uuid.UUID]BrowserDriver
	monitorCancel map[uuid.UUID]context.CancelFunc
}

func NewExecutor(opts ExecutorOptions) (*Executor, error) {
	requiresPool := opts.Sessions == nil || opts.Actions == nil || opts.Handoffs == nil || opts.Projects == nil || opts.Inbox == nil
	if opts.Pool == nil && requiresPool {
		return nil, fmt.Errorf("browser executor requires a database pool")
	}
	if opts.Runs == nil {
		return nil, fmt.Errorf("browser executor requires run service")
	}
	if opts.Store == nil {
		return nil, fmt.Errorf("browser executor requires object storage")
	}
	if opts.Artifacts == nil {
		return nil, fmt.Errorf("browser executor requires run artifact repository")
	}

	exec := &Executor{
		runs:              opts.Runs,
		artifacts:         opts.Artifacts,
		store:             opts.Store,
		policyChecker:     opts.PolicyChecker,
		secrets:           opts.Secrets,
		driverFactory:     opts.DriverFactory,
		idleCheckInterval: opts.IdleCheckInterval,
		idleTimeout:       opts.IdleTimeout,
		now:               opts.Now,
		logger:            opts.Logger,
		drivers:           map[uuid.UUID]BrowserDriver{},
		monitorCancel:     map[uuid.UUID]context.CancelFunc{},
	}
	if exec.driverFactory == nil {
		exec.driverFactory = defaultDriverFactory
	}
	if exec.policyChecker == nil {
		if opts.Pool != nil {
			exec.policyChecker = NewDomainPolicyChecker(NewOrganizationPolicyRepository(opts.Pool))
		} else {
			exec.policyChecker = NewDomainPolicyChecker(nil)
		}
	}
	if exec.secrets == nil {
		exec.secrets = noopCredentialResolver{}
	}
	if exec.idleCheckInterval <= 0 {
		exec.idleCheckInterval = defaultIdleCheckInterval
	}
	if exec.idleTimeout <= 0 {
		exec.idleTimeout = defaultIdleTimeout
	}
	if exec.now == nil {
		exec.now = func() time.Time { return time.Now().UTC() }
	}
	if exec.logger == nil {
		exec.logger = slog.Default()
	}

	if opts.Sessions != nil {
		exec.sessions = opts.Sessions
	} else {
		exec.sessions = NewBrowserSessionRepository(opts.Pool)
	}
	if opts.Actions != nil {
		exec.actions = opts.Actions
	} else {
		exec.actions = NewBrowserActionRepository(opts.Pool)
	}
	if opts.Handoffs != nil {
		exec.handoffs = opts.Handoffs
	} else {
		exec.handoffs = NewBrowserHandoffRepository(opts.Pool)
	}
	if opts.Projects != nil {
		exec.projects = opts.Projects
	} else {
		exec.projects = repo.NewProjectRepo(opts.Pool)
	}
	if opts.Inbox != nil {
		exec.inbox = opts.Inbox
	} else {
		exec.inbox = repo.NewInboxItemRepo(opts.Pool)
	}

	return exec, nil
}

func (e *Executor) Execute(ctx context.Context, input map[string]any) (map[string]any, error) {
	actionType := strings.TrimSpace(actionTypeFromInput(input))
	if actionType == "" {
		return nil, fmt.Errorf("action_type is required")
	}

	runID, err := parseUUIDField(input, "run_id")
	if err != nil {
		return nil, err
	}
	runStepID, err := parseUUIDField(input, "run_step_id")
	if err != nil {
		return nil, err
	}

	sessionID, sessionErr := parseOptionalUUIDField(input, "session_id")
	if sessionErr != nil {
		return nil, sessionErr
	}
	if sessionID == uuid.Nil {
		taskID, taskErr := parseUUIDField(input, "task_id")
		if taskErr != nil {
			return nil, taskErr
		}
		agentID, agentErr := parseUUIDField(input, "agent_id")
		if agentErr != nil {
			return nil, agentErr
		}
		projectID, projectErr := parseUUIDField(input, "project_id")
		if projectErr != nil {
			return nil, projectErr
		}

		session, getErr := e.GetOrCreateSession(ctx, taskID, agentID, projectID)
		if getErr != nil {
			return nil, getErr
		}
		sessionID = session.ID
	}

	params := extractActionInput(input)
	output, err := e.ExecuteAction(ctx, sessionID, runID, runStepID, ExecuteActionInput{
		ActionType: actionType,
		Input:      params,
	})
	if err != nil {
		return nil, err
	}

	response := cloneMap(output.Output)
	response["session_id"] = sessionID.String()
	response["action_id"] = output.ActionID.String()
	if output.ScreenshotArtifactID != nil {
		response["screenshot_artifact_id"] = output.ScreenshotArtifactID.String()
	}
	if output.URLAtExecution != nil {
		response["url"] = *output.URLAtExecution
	}
	return response, nil
}

func (e *Executor) GetOrCreateSession(ctx context.Context, taskID, agentID, projectID uuid.UUID) (BrowserSession, error) {
	if existing, err := e.sessions.GetActiveByTask(ctx, taskID); err == nil {
		if _, ensureErr := e.ensureDriver(ctx, existing); ensureErr != nil {
			return BrowserSession{}, ensureErr
		}
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return BrowserSession{}, err
	}

	projectRecord, err := e.projects.GetByID(ctx, projectID)
	if err != nil {
		return BrowserSession{}, err
	}

	candidate := BrowserSession{
		TaskID:         taskID,
		ProjectID:      projectID,
		AgentID:        agentID,
		OrganizationID: projectRecord.OrganizationID,
		Status:         "active",
		BrowserType:    "chromium",
	}

	driver, err := e.driverFactory(ctx, candidate)
	if err != nil {
		return BrowserSession{}, err
	}

	credentials, err := e.secrets.ResolveForBrowserSession(ctx, agentID)
	if err != nil {
		_ = driver.Close()
		return BrowserSession{}, err
	}
	candidate.CredentialsInjected = collectCredentialSlugs(credentials)
	if injector, ok := driver.(CredentialAwareDriver); ok && len(credentials) > 0 {
		if injectErr := injector.InjectCredentials(ctx, credentials); injectErr != nil {
			_ = driver.Close()
			return BrowserSession{}, injectErr
		}
	}

	created, err := e.sessions.Create(ctx, candidate)
	if err != nil {
		_ = driver.Close()
		if errors.Is(err, ErrConflict) {
			existing, getErr := e.sessions.GetActiveByTask(ctx, taskID)
			if getErr != nil {
				return BrowserSession{}, getErr
			}
			if _, ensureErr := e.ensureDriver(ctx, existing); ensureErr != nil {
				return BrowserSession{}, ensureErr
			}
			return existing, nil
		}
		return BrowserSession{}, err
	}

	e.mu.Lock()
	e.drivers[created.ID] = driver
	e.mu.Unlock()
	e.startIdleMonitor(created.ID)
	return created, nil
}

func (e *Executor) ExecuteAction(ctx context.Context, sessionID, runID, runStepID uuid.UUID, action ExecuteActionInput) (ExecuteActionOutput, error) {
	session, err := e.sessions.Get(ctx, sessionID)
	if err != nil {
		return ExecuteActionOutput{}, err
	}
	if session.Status == "revoked" {
		return ExecuteActionOutput{}, ErrSessionRevoked
	}
	if session.Status != "active" {
		return ExecuteActionOutput{}, ErrSessionNotActive
	}

	inputJSON, marshalErr := json.Marshal(action.Input)
	if marshalErr != nil {
		return ExecuteActionOutput{}, fmt.Errorf("encode browser action input: %w", marshalErr)
	}
	now := e.now()
	actionType := strings.ToLower(strings.TrimSpace(action.ActionType))

	checkURL := policyURLForAction(session, actionType, action.Input)
	if checkURL != "" && !e.policyChecker.IsAllowed(ctx, session.OrganizationID, checkURL) {
		blocked, createErr := e.actions.Create(ctx, BrowserAction{
			BrowserSessionID: sessionID,
			RunID:            runID,
			RunStepID:        runStepID,
			ActionType:       actionType,
			Input:            inputJSON,
			Status:           StatusBlockedByDomainPolicy,
			URLAtExecution:   stringPtr(checkURL),
			StartedAt:        &now,
			CompletedAt:      &now,
			DurationMS:       intPtr(0),
		})
		if createErr != nil {
			return ExecuteActionOutput{}, createErr
		}
		return ExecuteActionOutput{ActionID: blocked.ID, URLAtExecution: blocked.URLAtExecution}, ErrDomainPolicyDenied
	}

	created, err := e.actions.Create(ctx, BrowserAction{
		BrowserSessionID: sessionID,
		RunID:            runID,
		RunStepID:        runStepID,
		ActionType:       actionType,
		Input:            inputJSON,
		Status:           StatusInProgress,
		StartedAt:        &now,
	})
	if err != nil {
		return ExecuteActionOutput{}, err
	}

	driver, err := e.ensureDriver(ctx, session)
	if err != nil {
		return ExecuteActionOutput{}, err
	}

	result, execErr := driver.Execute(ctx, DriverAction{Type: actionType, Input: cloneMap(action.Input)})
	finishedAt := e.now()
	duration := int(finishedAt.Sub(now).Milliseconds())
	if duration < 0 {
		duration = 0
	}

	urlAtExecution := resolveExecutionURL(session, actionType, action.Input, result)
	var screenshotArtifactID *uuid.UUID

	if execErr != nil {
		screenshotArtifactID, _ = e.captureActionScreenshot(ctx, driver, runID, runStepID, created.ID, "error")
		message := strings.TrimSpace(execErr.Error())
		_, _ = e.actions.UpdateCompletion(ctx, created.ID, ActionCompletion{
			Status:               StatusFailed,
			ErrorMessage:         stringPtr(message),
			ScreenshotArtifactID: screenshotArtifactID,
			URLAtExecution:       urlAtExecution,
			StartedAt:            &now,
			CompletedAt:          &finishedAt,
			DurationMS:           &duration,
		})
		_, _ = e.sessions.UpdateIdleSince(ctx, sessionID, &finishedAt)
		if urlAtExecution != nil {
			_, _ = e.sessions.UpdateCurrentURL(ctx, sessionID, urlAtExecution)
		}
		return ExecuteActionOutput{ActionID: created.ID, ScreenshotArtifactID: screenshotArtifactID, URLAtExecution: urlAtExecution}, execErr
	}

	if actionType == "screenshot" || shouldAutoCapture(actionType) {
		screenshotArtifactID, err = e.captureActionScreenshot(ctx, driver, runID, runStepID, created.ID, actionType)
		if err != nil {
			return ExecuteActionOutput{}, err
		}
	}

	encodedOutput, err := json.Marshal(result)
	if err != nil {
		return ExecuteActionOutput{}, fmt.Errorf("encode browser action output: %w", err)
	}

	if _, err := e.actions.UpdateCompletion(ctx, created.ID, ActionCompletion{
		Status:               StatusCompleted,
		Output:               encodedOutput,
		ScreenshotArtifactID: screenshotArtifactID,
		URLAtExecution:       urlAtExecution,
		StartedAt:            &now,
		CompletedAt:          &finishedAt,
		DurationMS:           &duration,
	}); err != nil {
		return ExecuteActionOutput{}, err
	}

	if _, err := e.sessions.UpdateIdleSince(ctx, sessionID, &finishedAt); err != nil {
		return ExecuteActionOutput{}, err
	}
	if _, err := e.sessions.UpdateCurrentURL(ctx, sessionID, urlAtExecution); err != nil {
		return ExecuteActionOutput{}, err
	}

	return ExecuteActionOutput{
		ActionID:             created.ID,
		Output:               result,
		ScreenshotArtifactID: screenshotArtifactID,
		URLAtExecution:       urlAtExecution,
	}, nil
}

func (e *Executor) CloseSession(ctx context.Context, sessionID uuid.UUID) error {
	driver := e.clearDriver(sessionID)
	if driver != nil {
		_ = driver.Close()
	}
	_, err := e.sessions.UpdateStatus(ctx, sessionID, "closed", nil)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

func (e *Executor) RevokeSession(ctx context.Context, sessionID uuid.UUID) error {
	revokedAt := e.now()
	if _, err := e.sessions.UpdateStatus(ctx, sessionID, "revoked", &revokedAt); err != nil {
		return err
	}
	e.cancelMonitor(sessionID)
	return nil
}

func (e *Executor) RequestHandoff(ctx context.Context, sessionID, runID, agentID, targetUserID uuid.UUID, reason string) (BrowserHandoff, error) {
	session, err := e.sessions.Get(ctx, sessionID)
	if err != nil {
		return BrowserHandoff{}, err
	}
	driver, err := e.ensureDriver(ctx, session)
	if err != nil {
		return BrowserHandoff{}, err
	}

	preScreenshotID, err := e.captureHandoffScreenshot(ctx, driver, runID, "pre")
	if err != nil {
		return BrowserHandoff{}, err
	}

	handoffID := uuid.New()
	expiresAt := e.now().Add(24 * time.Hour)
	payload, _ := json.Marshal(map[string]any{
		"browser_session_id": sessionID.String(),
		"browser_handoff_id": handoffID.String(),
		"pre_screenshot_url": fmt.Sprintf("/v1/control/run-artifacts/%s/download", preScreenshotID.String()),
	})

	body := strings.TrimSpace(reason)
	createdByID := agentID
	inboxItem, err := e.inbox.Create(ctx, repo.InboxItem{
		OrganizationID:  session.OrganizationID,
		TargetUserID:    &targetUserID,
		ItemType:        "browser_handoff",
		SourceProjectID: &session.ProjectID,
		SourceTaskID:    &session.TaskID,
		CreatedByType:   "agent",
		CreatedByID:     &createdByID,
		Title:           "Browser handoff requested",
		Body:            stringPtr(body),
		ActionPayload:   payload,
		ExpiresAt:       &expiresAt,
	})
	if err != nil {
		return BrowserHandoff{}, err
	}

	handoff, err := e.handoffs.Create(ctx, BrowserHandoff{
		ID:                             handoffID,
		BrowserSessionID:               sessionID,
		InboxItemID:                    inboxItem.ID,
		RunID:                          runID,
		AgentID:                        agentID,
		TargetUserID:                   targetUserID,
		Reason:                         body,
		PreHandoffScreenshotArtifactID: preScreenshotID,
		Status:                         "pending",
		ExpiresAt:                      expiresAt,
	})
	if err != nil {
		return BrowserHandoff{}, err
	}

	if err := e.runs.PauseRun(ctx, runID); err != nil {
		return BrowserHandoff{}, err
	}
	return handoff, nil
}

func (e *Executor) CompleteHandoff(ctx context.Context, handoffID uuid.UUID, actionDecision string) error {
	handoff, err := e.handoffs.Get(ctx, handoffID)
	if err != nil {
		return err
	}
	return e.completeHandoff(ctx, handoff, handoff.TargetUserID, actionDecision)
}

func (e *Executor) CompleteHandoffByInboxItem(ctx context.Context, inboxItemID, actedByUserID uuid.UUID, actionDecision string) error {
	handoff, err := e.handoffs.GetByInboxItem(ctx, inboxItemID)
	if err != nil {
		return err
	}
	return e.completeHandoff(ctx, handoff, actedByUserID, actionDecision)
}

func (e *Executor) Shutdown() {
	e.mu.Lock()
	cancelFns := make([]context.CancelFunc, 0, len(e.monitorCancel))
	for _, cancel := range e.monitorCancel {
		cancelFns = append(cancelFns, cancel)
	}
	drivers := make([]BrowserDriver, 0, len(e.drivers))
	for _, driver := range e.drivers {
		drivers = append(drivers, driver)
	}
	e.monitorCancel = map[uuid.UUID]context.CancelFunc{}
	e.drivers = map[uuid.UUID]BrowserDriver{}
	e.mu.Unlock()

	for _, cancel := range cancelFns {
		cancel()
	}
	for _, driver := range drivers {
		_ = driver.Close()
	}
}

func (e *Executor) completeHandoff(ctx context.Context, handoff BrowserHandoff, actedByUserID uuid.UUID, actionDecision string) error {
	decision := strings.ToLower(strings.TrimSpace(actionDecision))
	if decision != "completed" && decision != "cancelled" {
		return fmt.Errorf("invalid handoff decision %q", actionDecision)
	}
	if handoff.Status != "pending" {
		return nil
	}

	session, err := e.sessions.Get(ctx, handoff.BrowserSessionID)
	if err != nil {
		return err
	}
	driver, err := e.ensureDriver(ctx, session)
	if err != nil {
		return err
	}
	postScreenshotID, err := e.captureHandoffScreenshot(ctx, driver, handoff.RunID, "post")
	if err != nil {
		return err
	}

	completedAt := e.now()
	if _, err := e.handoffs.UpdateStatus(ctx, handoff.ID, HandoffStatusUpdate{
		Status:                          decision,
		PostHandoffScreenshotArtifactID: postScreenshotID,
		CompletedAt:                     &completedAt,
	}); err != nil {
		return err
	}

	if actedByUserID == uuid.Nil {
		actedByUserID = handoff.TargetUserID
	}
	if _, err := e.inbox.MarkActed(ctx, handoff.InboxItemID, actedByUserID); err != nil {
		return err
	}

	if decision == "completed" {
		return e.runs.ResumeRun(ctx, handoff.RunID)
	}
	return e.runs.FailRun(ctx, handoff.RunID, "handoff_cancelled", "permanent")
}

func (e *Executor) ensureDriver(ctx context.Context, session BrowserSession) (BrowserDriver, error) {
	e.mu.Lock()
	driver := e.drivers[session.ID]
	e.mu.Unlock()
	if driver != nil {
		e.startIdleMonitor(session.ID)
		return driver, nil
	}

	created, err := e.driverFactory(ctx, session)
	if err != nil {
		return nil, err
	}
	if injector, ok := created.(CredentialAwareDriver); ok && len(session.CredentialsInjected) > 0 {
		credentials := make([]BrowserCredential, 0, len(session.CredentialsInjected))
		for _, slug := range session.CredentialsInjected {
			credentials = append(credentials, BrowserCredential{Slug: slug})
		}
		if err := injector.InjectCredentials(ctx, credentials); err != nil {
			_ = created.Close()
			return nil, err
		}
	}

	e.mu.Lock()
	e.drivers[session.ID] = created
	e.mu.Unlock()
	e.startIdleMonitor(session.ID)
	return created, nil
}

func (e *Executor) startIdleMonitor(sessionID uuid.UUID) {
	e.mu.Lock()
	if _, ok := e.monitorCancel[sessionID]; ok {
		e.mu.Unlock()
		return
	}
	monitorCtx, cancel := context.WithCancel(context.Background())
	e.monitorCancel[sessionID] = cancel
	e.mu.Unlock()

	go func() {
		ticker := time.NewTicker(e.idleCheckInterval)
		defer ticker.Stop()
		defer e.cancelMonitor(sessionID)

		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
				session, err := e.sessions.Get(context.Background(), sessionID)
				if err != nil {
					if !errors.Is(err, ErrNotFound) {
						e.logger.Warn("browser idle monitor fetch failed", "session_id", sessionID.String(), "error", err)
					}
					return
				}
				if session.Status == "closed" || session.Status == "revoked" {
					return
				}
				if session.IdleSince == nil {
					continue
				}
				if e.now().Sub(session.IdleSince.UTC()) > e.idleTimeout {
					if err := e.CloseSession(context.Background(), sessionID); err != nil && !errors.Is(err, ErrNotFound) {
						e.logger.Warn("browser idle monitor close failed", "session_id", sessionID.String(), "error", err)
					}
					return
				}
			}
		}
	}()
}

func (e *Executor) cancelMonitor(sessionID uuid.UUID) {
	e.mu.Lock()
	cancel, ok := e.monitorCancel[sessionID]
	if ok {
		delete(e.monitorCancel, sessionID)
	}
	e.mu.Unlock()
	if ok {
		cancel()
	}
}

func (e *Executor) clearDriver(sessionID uuid.UUID) BrowserDriver {
	e.cancelMonitor(sessionID)
	e.mu.Lock()
	defer e.mu.Unlock()
	driver := e.drivers[sessionID]
	delete(e.drivers, sessionID)
	return driver
}

func (e *Executor) captureActionScreenshot(ctx context.Context, driver BrowserDriver, runID, runStepID, actionID uuid.UUID, source string) (*uuid.UUID, error) {
	image, err := driver.TakeScreenshot(ctx)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("runs/%s/steps/%s/browser/%s.png", runID.String(), runStepID.String(), actionID.String())
	artifact, err := e.storeScreenshotArtifact(ctx, image, key, runID, &runStepID, map[string]any{"source": source, "action_id": actionID.String()})
	if err != nil {
		return nil, err
	}
	id := artifact.ID
	return &id, nil
}

func (e *Executor) captureHandoffScreenshot(ctx context.Context, driver BrowserDriver, runID uuid.UUID, phase string) (*uuid.UUID, error) {
	image, err := driver.TakeScreenshot(ctx)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("runs/%s/browser_handoff/%s-%s.png", runID.String(), uuid.NewString(), strings.ToLower(strings.TrimSpace(phase)))
	artifact, err := e.storeScreenshotArtifact(ctx, image, key, runID, nil, map[string]any{"source": "handoff", "phase": phase})
	if err != nil {
		return nil, err
	}
	id := artifact.ID
	return &id, nil
}

func (e *Executor) storeScreenshotArtifact(ctx context.Context, image []byte, key string, runID uuid.UUID, runStepID *uuid.UUID, metadata map[string]any) (controlplane.RunArtifact, error) {
	if err := e.store.Put(ctx, key, bytes.NewReader(image), storage.PutOptions{
		ContentType:   "image/png",
		ContentLength: int64(len(image)),
	}); err != nil {
		return controlplane.RunArtifact{}, err
	}
	metadataJSON, _ := json.Marshal(metadata)
	artifact, err := e.artifacts.Create(ctx, controlplane.RunArtifact{
		RunID:         runID,
		RunStepID:     runStepID,
		ArtifactType:  "screenshot",
		StorageKey:    key,
		ContentType:   "image/png",
		ByteSize:      len(image),
		InlineContent: nil,
		Filename:      nil,
		Metadata:      metadataJSON,
	})
	if err != nil {
		return controlplane.RunArtifact{}, err
	}
	return artifact, nil
}

func shouldAutoCapture(actionType string) bool {
	_, ok := autoScreenshotActions[strings.ToLower(strings.TrimSpace(actionType))]
	return ok
}

func policyURLForAction(session BrowserSession, actionType string, input map[string]any) string {
	actionType = strings.ToLower(strings.TrimSpace(actionType))
	if actionType == "navigate" {
		if raw, ok := input["url"].(string); ok {
			return strings.TrimSpace(raw)
		}
	}
	if session.CurrentURL == nil {
		return ""
	}
	return strings.TrimSpace(*session.CurrentURL)
}

func resolveExecutionURL(session BrowserSession, actionType string, input map[string]any, output map[string]any) *string {
	if raw, ok := output["current_url"].(string); ok {
		value := strings.TrimSpace(raw)
		if value != "" {
			return &value
		}
	}
	if strings.EqualFold(strings.TrimSpace(actionType), "navigate") {
		if raw, ok := input["url"].(string); ok {
			value := strings.TrimSpace(raw)
			if value != "" {
				return &value
			}
		}
	}
	if session.CurrentURL != nil {
		value := strings.TrimSpace(*session.CurrentURL)
		if value != "" {
			return &value
		}
	}
	return nil
}

func collectCredentialSlugs(credentials []BrowserCredential) []string {
	out := make([]string, 0, len(credentials))
	seen := map[string]struct{}{}
	for _, credential := range credentials {
		slug := strings.TrimSpace(credential.Slug)
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		out = append(out, slug)
	}
	return out
}

func actionTypeFromInput(input map[string]any) string {
	if input == nil {
		return ""
	}
	if raw, ok := input["action_type"].(string); ok {
		trimmed := strings.ToLower(strings.TrimSpace(raw))
		if trimmed != "" {
			return trimmed
		}
	}
	if raw, ok := input["tool_name"].(string); ok {
		trimmed := strings.ToLower(strings.TrimSpace(raw))
		trimmed = strings.TrimPrefix(trimmed, "browser.")
		return strings.TrimSpace(trimmed)
	}
	return ""
}

func extractActionInput(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	if nested, ok := input["input"].(map[string]any); ok {
		return cloneMap(nested)
	}
	reserved := map[string]struct{}{
		"action_type":    {},
		"tool_name":      {},
		"session_id":     {},
		"run_id":         {},
		"run_step_id":    {},
		"run_attempt_id": {},
		"task_id":        {},
		"project_id":     {},
		"agent_id":       {},
	}
	out := map[string]any{}
	for key, value := range input {
		if _, ok := reserved[key]; ok {
			continue
		}
		out[key] = value
	}
	return out
}

func parseUUIDField(input map[string]any, key string) (uuid.UUID, error) {
	value, ok := input[key]
	if !ok || value == nil {
		return uuid.Nil, fmt.Errorf("%s is required", key)
	}
	switch v := value.(type) {
	case string:
		parsed, err := uuid.Parse(strings.TrimSpace(v))
		if err != nil {
			return uuid.Nil, fmt.Errorf("invalid %s", key)
		}
		return parsed, nil
	case uuid.UUID:
		if v == uuid.Nil {
			return uuid.Nil, fmt.Errorf("%s is required", key)
		}
		return v, nil
	default:
		return uuid.Nil, fmt.Errorf("invalid %s", key)
	}
}

func parseOptionalUUIDField(input map[string]any, key string) (uuid.UUID, error) {
	value, ok := input[key]
	if !ok || value == nil {
		return uuid.Nil, nil
	}
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return uuid.Nil, nil
		}
		parsed, err := uuid.Parse(trimmed)
		if err != nil {
			return uuid.Nil, fmt.Errorf("invalid %s", key)
		}
		return parsed, nil
	case uuid.UUID:
		return v, nil
	default:
		return uuid.Nil, fmt.Errorf("invalid %s", key)
	}
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func stringPtr(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func intPtr(value int) *int {
	return &value
}
