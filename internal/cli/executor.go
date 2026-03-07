package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	nativetools "github.com/samhotchkiss/otter-camp/internal/tools/native"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
)

const (
	defaultTimeoutSeconds = 300
	maxTimeoutSeconds     = 3600

	chunkSizeBytes  = 4 * 1024
	inlineLimit     = 50 * 1024
	maxOutputBytes  = 10 * 1024 * 1024
	killGracePeriod = 5 * time.Second
	safePathValue   = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
)

var (
	ErrPathTraversal    = nativetools.ErrPathTraversal
	ErrCommandDenied    = errors.New("command_denied")
	ErrOutputTooLarge   = errors.New("output_size_exceeded")
	ErrOrgIDRequired    = errors.New("organization_id is required")
	ErrInvalidInputType = errors.New("invalid cli executor input")
)

type ArtifactWriter interface {
	Create(ctx context.Context, artifact controlplane.RunArtifact) (controlplane.RunArtifact, error)
}

type RunEventWriter interface {
	Append(ctx context.Context, event controlplane.RunEvent) (controlplane.RunEvent, error)
}

type ProjectReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Project, error)
}

type SecretResolver interface {
	ResolveRef(ctx context.Context, orgID uuid.UUID, ref string) (string, error)
}

type commandBuilder func(ctx context.Context, name string, args ...string) *exec.Cmd

type streamChunk struct {
	stream string
	data   []byte
	err    error
}

type ExecutorOptions struct {
	Pool          *pgxpool.Pool
	Executions    *Repository
	Artifacts     ArtifactWriter
	Events        RunEventWriter
	Projects      ProjectReader
	SecretService SecretResolver
	Store         storage.Store
	DataDir       string
	WorkspaceRoot string
	Now           func() time.Time
	Risk          *RiskClassifier
	Command       commandBuilder
}

type Executor struct {
	executions *Repository
	artifacts  ArtifactWriter
	events     RunEventWriter
	projects   ProjectReader
	secrets    SecretResolver
	store      storage.Store
	dataDir    string
	root       string
	now        func() time.Time
	risk       *RiskClassifier
	command    commandBuilder
}

//revive:disable-next-line:exported // Tool contract names intentionally match the cli.execute schema.
type CLIExecuteInput struct {
	RunID            uuid.UUID
	RunStepID        uuid.UUID
	RunAttemptID     *uuid.UUID
	TaskID           uuid.UUID
	ProjectID        uuid.UUID
	AgentID          uuid.UUID
	OrganizationID   *uuid.UUID
	Command          string
	WorkingDirectory *string
	TimeoutSeconds   *int
	EnvOverrides     map[string]string
}

//revive:disable-next-line:exported // Tool contract names intentionally match the cli.execute schema.
type CLIExecuteOutput struct {
	ExitCode         int
	StdoutTruncated  bool
	StderrTruncated  bool
	StdoutInline     *string
	StderrInline     *string
	StdoutArtifactID *uuid.UUID
	StderrArtifactID *uuid.UUID
	DurationMS       int
}

type CommandDeniedError struct {
	Code    string
	Pattern string
}

func (e CommandDeniedError) Error() string {
	if e.Pattern == "" {
		return fmt.Sprintf("%s: %s", ErrCommandDenied, strings.TrimSpace(e.Code))
	}
	return fmt.Sprintf("%s: %s (%s)", ErrCommandDenied, strings.TrimSpace(e.Code), strings.TrimSpace(e.Pattern))
}

func NewExecutor(opts ExecutorOptions) *Executor {
	instance := &Executor{
		executions: opts.Executions,
		artifacts:  opts.Artifacts,
		events:     opts.Events,
		projects:   opts.Projects,
		secrets:    opts.SecretService,
		store:      opts.Store,
		dataDir:    workspace.ResolveDataDir(opts.DataDir),
		root:       strings.TrimSpace(opts.WorkspaceRoot),
		now:        opts.Now,
		risk:       opts.Risk,
		command:    opts.Command,
	}
	if instance.executions == nil && opts.Pool != nil {
		instance.executions = NewRepository(opts.Pool)
	}
	if instance.artifacts == nil && opts.Pool != nil {
		instance.artifacts = controlplane.NewRunArtifactRepository(opts.Pool)
	}
	if instance.events == nil && opts.Pool != nil {
		instance.events = controlplane.NewRunEventRepository(opts.Pool)
	}
	if instance.projects == nil && opts.Pool != nil {
		instance.projects = repo.NewProjectRepo(opts.Pool)
	}
	if instance.now == nil {
		instance.now = func() time.Time { return time.Now().UTC() }
	}
	if instance.risk == nil {
		instance.risk = NewRiskClassifier()
	}
	if instance.command == nil {
		instance.command = exec.CommandContext
	}
	return instance
}

func (e *Executor) Execute(ctx context.Context, input map[string]any) (map[string]any, error) {
	req, err := decodeMapInput(input)
	if err != nil {
		return nil, err
	}
	out, err := e.ExecuteCommand(ctx, req)
	if err != nil {
		return nil, err
	}
	response := map[string]any{
		"exit_code":        out.ExitCode,
		"stdout_truncated": out.StdoutTruncated,
		"stderr_truncated": out.StderrTruncated,
		"duration_ms":      out.DurationMS,
	}
	if out.StdoutInline != nil {
		response["stdout_inline"] = *out.StdoutInline
	}
	if out.StderrInline != nil {
		response["stderr_inline"] = *out.StderrInline
	}
	if out.StdoutArtifactID != nil {
		response["stdout_artifact_id"] = out.StdoutArtifactID.String()
	}
	if out.StderrArtifactID != nil {
		response["stderr_artifact_id"] = out.StderrArtifactID.String()
	}
	return response, nil
}

func (e *Executor) ExecuteCommand(ctx context.Context, input CLIExecuteInput) (CLIExecuteOutput, error) {
	if e == nil || e.executions == nil {
		return CLIExecuteOutput{}, fmt.Errorf("cli executor requires execution repository")
	}
	if err := validateInput(input); err != nil {
		return CLIExecuteOutput{}, err
	}

	orgID, err := resolveOrganizationID(ctx, input.OrganizationID)
	if err != nil {
		return CLIExecuteOutput{}, err
	}

	workingDirectory, err := e.resolveWorkingDirectory(ctx, orgID, input)
	if err != nil {
		return CLIExecuteOutput{}, err
	}
	envVars, envUsed, err := e.buildEnvironment(ctx, orgID, input)
	if err != nil {
		return CLIExecuteOutput{}, err
	}

	start := e.now()
	classification := e.risk.Evaluate(input.Command)
	if classification.RiskLevel == "" {
		classification.RiskLevel = RiskLow
	}

	envUsedRaw, err := json.Marshal(envUsed)
	if err != nil {
		return CLIExecuteOutput{}, fmt.Errorf("marshal env_vars_used: %w", err)
	}

	execution, err := e.executions.Create(ctx, Execution{
		RunID:            input.RunID,
		RunStepID:        input.RunStepID,
		TaskID:           input.TaskID,
		ProjectID:        input.ProjectID,
		AgentID:          input.AgentID,
		Command:          strings.TrimSpace(input.Command),
		WorkingDirectory: workingDirectory,
		RiskLevel:        classification.RiskLevel,
		PolicyDecision:   "allowed",
		EnvVarsUsed:      envUsedRaw,
		StartedAt:        &start,
		Metadata:         json.RawMessage(`{}`),
	})
	if err != nil {
		return CLIExecuteOutput{}, err
	}

	if classification.Denied {
		now := e.now()
		duration := int(now.Sub(start).Milliseconds())
		meta := map[string]any{"error": classification.ErrorCode}
		if classification.Pattern != "" {
			meta["pattern"] = classification.Pattern
		}
		metaRaw, marshalErr := json.Marshal(meta)
		if marshalErr != nil {
			return CLIExecuteOutput{}, marshalErr
		}
		if _, updateErr := e.executions.UpdateCompletion(ctx, execution.ID, CompletionUpdate{
			PolicyDecision: "denied",
			CompletedAt:    &now,
			DurationMS:     &duration,
			Metadata:       metaRaw,
		}); updateErr != nil {
			return CLIExecuteOutput{}, updateErr
		}
		if classification.ErrorCode == "" {
			classification.ErrorCode = "command_denied"
		}
		return CLIExecuteOutput{}, CommandDeniedError{Code: classification.ErrorCode, Pattern: classification.Pattern}
	}

	timeout := normalizeTimeout(input.TimeoutSeconds)
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	homeDir := buildHomeDir(input.RunID)
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		return CLIExecuteOutput{}, fmt.Errorf("create isolated home dir: %w", err)
	}
	envVars = append(envVars,
		"OTTERCAMP_TASK_ID="+input.TaskID.String(),
		"OTTERCAMP_PROJECT_ID="+input.ProjectID.String(),
		"OTTERCAMP_AGENT_ID="+input.AgentID.String(),
		"OTTERCAMP_ORG_ID="+orgID.String(),
		"OTTERCAMP_RUN_ID="+input.RunID.String(),
		"HOME="+homeDir,
		"PATH="+safePathValue,
	)

	cmd := e.command(execCtx, "/bin/sh", "-c", input.Command)
	cmd.Dir = workingDirectory
	cmd.Env = envVars
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return CLIExecuteOutput{}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return CLIExecuteOutput{}, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return CLIExecuteOutput{}, fmt.Errorf("start command: %w", err)
	}

	var (
		stdoutBuf bytes.Buffer
		stderrBuf bytes.Buffer
		mu        sync.Mutex
	)

	chunks := make(chan streamChunk, 32)
	var readers sync.WaitGroup
	readers.Add(2)
	go streamPipe(stdoutPipe, "stdout", chunks, &readers)
	go streamPipe(stderrPipe, "stderr", chunks, &readers)
	go func() {
		readers.Wait()
		close(chunks)
	}()

	var (
		waitErr           error
		waitDone          = make(chan struct{})
		eventErr          error
		outputExceeded    bool
		timeoutEventSent  bool
		totalOutput       int
		streamReadErr     error
		shutdownTriggered bool
	)

	go func() {
		waitErr = cmd.Wait()
		close(waitDone)
	}()

	go func() {
		<-execCtx.Done()
		if cmd.Process != nil {
			terminateProcessGroup(cmd.Process.Pid)
		}
	}()

	for chunks != nil || waitDone != nil {
		select {
		case ch, ok := <-chunks:
			if !ok {
				chunks = nil
				continue
			}
			if ch.err != nil && streamReadErr == nil {
				streamReadErr = ch.err
			}
			if len(ch.data) == 0 {
				continue
			}
			totalOutput += len(ch.data)
			if totalOutput > maxOutputBytes {
				outputExceeded = true
				if !shutdownTriggered {
					shutdownTriggered = true
					cancel()
				}
			}

			mu.Lock()
			if ch.stream == "stdout" {
				_, _ = stdoutBuf.Write(ch.data)
			} else {
				_, _ = stderrBuf.Write(ch.data)
			}
			mu.Unlock()

			if e.events != nil {
				payload := map[string]any{"stream": ch.stream, "delta": string(ch.data)}
				payloadRaw, marshalErr := json.Marshal(payload)
				if marshalErr != nil {
					if eventErr == nil {
						eventErr = marshalErr
					}
				} else {
					_, appendErr := e.events.Append(ctx, controlplane.RunEvent{
						RunID:        input.RunID,
						RunStepID:    &input.RunStepID,
						RunAttemptID: input.RunAttemptID,
						EventType:    "output_chunk",
						ActorType:    "system",
						Payload:      payloadRaw,
					})
					if appendErr != nil && eventErr == nil {
						eventErr = appendErr
					}
				}
			}
		case <-waitDone:
			waitDone = nil
		}
	}

	timedOut := errors.Is(execCtx.Err(), context.DeadlineExceeded)
	if timedOut && e.events != nil {
		timeoutEventSent = true
		payloadRaw, _ := json.Marshal(map[string]any{"type": "timeout"})
		if _, appendErr := e.events.Append(ctx, controlplane.RunEvent{
			RunID:        input.RunID,
			RunStepID:    &input.RunStepID,
			RunAttemptID: input.RunAttemptID,
			EventType:    "output_chunk",
			ActorType:    "system",
			Payload:      payloadRaw,
		}); appendErr != nil && eventErr == nil {
			eventErr = appendErr
		}
	}

	mu.Lock()
	stdoutBytes := append([]byte(nil), stdoutBuf.Bytes()...)
	stderrBytes := append([]byte(nil), stderrBuf.Bytes()...)
	mu.Unlock()

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	if timedOut || outputExceeded {
		exitCode = -1
	}

	completedAt := e.now()
	duration := int(completedAt.Sub(start).Milliseconds())

	stdoutInline, stdoutArtifactID, stdoutTruncated, err := e.persistStream(ctx, input, execution.ID, "stdout", stdoutBytes)
	if err != nil {
		return CLIExecuteOutput{}, err
	}
	stderrInline, stderrArtifactID, stderrTruncated, err := e.persistStream(ctx, input, execution.ID, "stderr", stderrBytes)
	if err != nil {
		return CLIExecuteOutput{}, err
	}

	metadata := map[string]any{
		"stdout_truncated": stdoutTruncated,
		"stderr_truncated": stderrTruncated,
	}
	if stdoutInline != nil {
		metadata["stdout_inline"] = *stdoutInline
	}
	if stderrInline != nil {
		metadata["stderr_inline"] = *stderrInline
	}
	if outputExceeded {
		metadata["error"] = "output_size_exceeded"
	}
	if timeoutEventSent {
		metadata["timeout"] = true
	}
	if waitErr != nil {
		metadata["wait_error"] = waitErr.Error()
	}
	if streamReadErr != nil {
		metadata["stream_error"] = streamReadErr.Error()
	}

	metaRaw, err := json.Marshal(metadata)
	if err != nil {
		return CLIExecuteOutput{}, fmt.Errorf("marshal completion metadata: %w", err)
	}

	if _, err := e.executions.UpdateCompletion(ctx, execution.ID, CompletionUpdate{
		PolicyDecision:   "allowed",
		ExitCode:         &exitCode,
		StdoutArtifactID: stdoutArtifactID,
		StderrArtifactID: stderrArtifactID,
		CompletedAt:      &completedAt,
		DurationMS:       &duration,
		Metadata:         metaRaw,
	}); err != nil {
		return CLIExecuteOutput{}, err
	}

	if outputExceeded {
		return CLIExecuteOutput{ExitCode: exitCode, StdoutTruncated: stdoutTruncated, StderrTruncated: stderrTruncated, StdoutInline: stdoutInline, StderrInline: stderrInline, StdoutArtifactID: stdoutArtifactID, StderrArtifactID: stderrArtifactID, DurationMS: duration}, ErrOutputTooLarge
	}
	if eventErr != nil {
		slog.Error("cli execution run_event append failed", "run_id", input.RunID, "run_step_id", input.RunStepID, "error", eventErr)
	}

	return CLIExecuteOutput{
		ExitCode:         exitCode,
		StdoutTruncated:  stdoutTruncated,
		StderrTruncated:  stderrTruncated,
		StdoutInline:     stdoutInline,
		StderrInline:     stderrInline,
		StdoutArtifactID: stdoutArtifactID,
		StderrArtifactID: stderrArtifactID,
		DurationMS:       duration,
	}, nil
}

func (e *Executor) resolveWorkingDirectory(ctx context.Context, orgID uuid.UUID, input CLIExecuteInput) (string, error) {
	root := strings.TrimSpace(e.root)
	if root == "" {
		var err error
		root, err = workspace.ProjectRootByID(ctx, e.projects, e.dataDir, orgID, input.ProjectID)
		if err != nil {
			return "", err
		}
	}
	workDir, err := nativetools.NewSessionWorkDir(root)
	if err != nil {
		return "", err
	}
	if input.WorkingDirectory == nil || strings.TrimSpace(*input.WorkingDirectory) == "" {
		return workDir.Root(), nil
	}
	resolved, err := workDir.ResolvePath(*input.WorkingDirectory)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func (e *Executor) buildEnvironment(ctx context.Context, orgID uuid.UUID, input CLIExecuteInput) ([]string, map[string]any, error) {
	result := map[string]string{}
	for _, pair := range os.Environ() {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		if isBlockedEnvKey(key, false) {
			continue
		}
		result[key] = value
	}

	used := map[string]any{}
	projectVars, err := e.loadProjectEnvironment(ctx, orgID, input.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	for key, value := range projectVars {
		if isBlockedEnvKey(key, true) {
			continue
		}
		result[key] = value
		used[key] = "project"
	}

	for key, value := range input.EnvOverrides {
		if isBlockedEnvKey(key, false) {
			continue
		}
		result[key] = value
		used[key] = "override"
	}

	keys := make([]string, 0, len(result))
	for key := range result {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+result[key])
	}
	return env, used, nil
}

func (e *Executor) loadProjectEnvironment(ctx context.Context, orgID, projectID uuid.UUID) (map[string]string, error) {
	if e.projects == nil {
		return map[string]string{}, nil
	}
	project, err := e.projects.GetByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	if project.OrganizationID != orgID {
		return nil, fmt.Errorf("project organization mismatch")
	}

	if len(project.Settings) == 0 {
		return map[string]string{}, nil
	}

	var raw map[string]any
	if err := json.Unmarshal(project.Settings, &raw); err != nil {
		return nil, fmt.Errorf("decode project settings env vars: %w", err)
	}

	vars := map[string]string{}
	envRaw, ok := raw["env_vars"].(map[string]any)
	if !ok {
		return vars, nil
	}

	for key, value := range envRaw {
		str, ok := value.(string)
		if !ok {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(str), "ref:") {
			if e.secrets == nil {
				return nil, fmt.Errorf("project env var %s references secret but secret service is unavailable", key)
			}
			resolved, err := e.secrets.ResolveRef(ctx, orgID, str)
			if err != nil {
				return nil, err
			}
			str = resolved
		}
		vars[key] = str
	}
	return vars, nil
}

func (e *Executor) persistStream(ctx context.Context, input CLIExecuteInput, executionID uuid.UUID, stream string, data []byte) (*string, *uuid.UUID, bool, error) {
	if len(data) <= inlineLimit {
		inline := string(data)
		return &inline, nil, false, nil
	}
	if e.store == nil || e.artifacts == nil {
		return nil, nil, true, fmt.Errorf("large %s output requires object storage and run artifact repository", stream)
	}

	key := fmt.Sprintf("runs/%s/steps/%s/cli/%s/%s.txt", input.RunID, input.RunStepID, executionID, stream)
	putCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := e.store.Put(putCtx, key, bytes.NewReader(data), storage.PutOptions{ContentType: "text/plain; charset=utf-8", ContentLength: int64(len(data))}); err != nil {
		return nil, nil, true, err
	}

	artifact, err := e.artifacts.Create(ctx, controlplane.RunArtifact{
		RunID:         input.RunID,
		RunStepID:     &input.RunStepID,
		RunAttemptID:  input.RunAttemptID,
		ArtifactType:  stream,
		StorageKey:    key,
		ContentType:   "text/plain; charset=utf-8",
		ByteSize:      len(data),
		InlineContent: nil,
		Filename:      nil,
		Metadata:      json.RawMessage(`{"source":"cli_execution"}`),
	})
	if err != nil {
		return nil, nil, true, err
	}
	return nil, &artifact.ID, true, nil
}

func streamPipe(reader io.Reader, stream string, output chan<- streamChunk, wg *sync.WaitGroup) {
	defer wg.Done()
	buffer := make([]byte, chunkSizeBytes)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			copied := make([]byte, n)
			copy(copied, buffer[:n])
			output <- streamChunk{stream: stream, data: copied}
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, io.EOF) {
			output <- streamChunk{stream: stream, err: err}
		}
		return
	}
}

func terminateProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	deadline := time.Now().Add(killGracePeriod)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pid, 0); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func validateInput(input CLIExecuteInput) error {
	switch {
	case input.RunID == uuid.Nil:
		return fmt.Errorf("run_id is required")
	case input.RunStepID == uuid.Nil:
		return fmt.Errorf("run_step_id is required")
	case input.TaskID == uuid.Nil:
		return fmt.Errorf("task_id is required")
	case input.ProjectID == uuid.Nil:
		return fmt.Errorf("project_id is required")
	case input.AgentID == uuid.Nil:
		return fmt.Errorf("agent_id is required")
	case strings.TrimSpace(input.Command) == "":
		return fmt.Errorf("command is required")
	default:
		return nil
	}
}

func resolveOrganizationID(ctx context.Context, explicit *uuid.UUID) (uuid.UUID, error) {
	if explicit != nil && *explicit != uuid.Nil {
		return *explicit, nil
	}
	fromContext := mcp.ExecutionContextFromContext(ctx).OrganizationID
	if fromContext != uuid.Nil {
		return fromContext, nil
	}
	return uuid.Nil, ErrOrgIDRequired
}

func normalizeTimeout(seconds *int) time.Duration {
	value := defaultTimeoutSeconds
	if seconds != nil {
		value = *seconds
	}
	if value <= 0 {
		value = defaultTimeoutSeconds
	}
	if value > maxTimeoutSeconds {
		value = maxTimeoutSeconds
	}
	return time.Duration(value) * time.Second
}

func buildHomeDir(runID uuid.UUID) string {
	return filepath.Join(os.TempDir(), "ottercamp-"+runID.String())
}

func isBlockedEnvKey(key string, allowSecretPattern bool) bool {
	normalized := strings.ToUpper(strings.TrimSpace(key))
	switch normalized {
	case "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OTTERCAMP_MASTER_KEY", "OTTERCAMP_DB_URL":
		return true
	}
	if allowSecretPattern {
		return false
	}
	return strings.HasSuffix(normalized, "_SECRET") || strings.HasSuffix(normalized, "_PASSWORD")
}

func decodeMapInput(input map[string]any) (CLIExecuteInput, error) {
	if input == nil {
		return CLIExecuteInput{}, ErrInvalidInputType
	}

	getUUID := func(keys ...string) (uuid.UUID, error) {
		for _, key := range keys {
			value, ok := input[key]
			if !ok {
				continue
			}
			switch typed := value.(type) {
			case uuid.UUID:
				return typed, nil
			case string:
				parsed, err := uuid.Parse(strings.TrimSpace(typed))
				if err != nil {
					return uuid.Nil, fmt.Errorf("invalid %s: %w", key, err)
				}
				return parsed, nil
			}
		}
		return uuid.Nil, nil
	}

	runID, err := getUUID("run_id")
	if err != nil {
		return CLIExecuteInput{}, err
	}
	runStepID, err := getUUID("run_step_id")
	if err != nil {
		return CLIExecuteInput{}, err
	}
	taskID, err := getUUID("task_id")
	if err != nil {
		return CLIExecuteInput{}, err
	}
	projectID, err := getUUID("project_id")
	if err != nil {
		return CLIExecuteInput{}, err
	}
	agentID, err := getUUID("agent_id")
	if err != nil {
		return CLIExecuteInput{}, err
	}
	runAttemptID, err := getUUID("run_attempt_id")
	if err != nil {
		return CLIExecuteInput{}, err
	}
	orgID, err := getUUID("org_id", "organization_id")
	if err != nil {
		return CLIExecuteInput{}, err
	}

	command, _ := input["command"].(string)

	var workingDirectory *string
	if value, ok := input["working_directory"].(string); ok && strings.TrimSpace(value) != "" {
		trimmed := strings.TrimSpace(value)
		workingDirectory = &trimmed
	}

	var timeoutSeconds *int
	if rawTimeout, ok := input["timeout_seconds"]; ok {
		switch typed := rawTimeout.(type) {
		case int:
			timeoutSeconds = &typed
		case int32:
			value := int(typed)
			timeoutSeconds = &value
		case int64:
			value := int(typed)
			timeoutSeconds = &value
		case float64:
			value := int(typed)
			timeoutSeconds = &value
		case string:
			parsed, parseErr := strconv.Atoi(strings.TrimSpace(typed))
			if parseErr != nil {
				return CLIExecuteInput{}, fmt.Errorf("invalid timeout_seconds: %w", parseErr)
			}
			timeoutSeconds = &parsed
		}
	}

	envOverrides := map[string]string{}
	if rawOverrides, ok := input["env_overrides"]; ok {
		typedMap, ok := rawOverrides.(map[string]any)
		if !ok {
			return CLIExecuteInput{}, fmt.Errorf("env_overrides must be an object")
		}
		for key, value := range typedMap {
			switch str := value.(type) {
			case string:
				envOverrides[key] = str
			case fmt.Stringer:
				envOverrides[key] = str.String()
			default:
				envOverrides[key] = fmt.Sprint(value)
			}
		}
	}

	request := CLIExecuteInput{
		RunID:            runID,
		RunStepID:        runStepID,
		TaskID:           taskID,
		ProjectID:        projectID,
		AgentID:          agentID,
		Command:          command,
		WorkingDirectory: workingDirectory,
		TimeoutSeconds:   timeoutSeconds,
		EnvOverrides:     envOverrides,
	}
	if runAttemptID != uuid.Nil {
		request.RunAttemptID = &runAttemptID
	}
	if orgID != uuid.Nil {
		request.OrganizationID = &orgID
	}
	return request, nil
}
