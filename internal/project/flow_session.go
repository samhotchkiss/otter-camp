package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type FlowRun struct {
	ID         uuid.UUID
	TaskID     *uuid.UUID
	FlowNodeID *uuid.UUID
	Summary    string
}

type FlowSessionBridge interface {
	EnsureNodeSession(ctx context.Context, execution repo.FlowNodeExecution) (repo.ChatSession, error)
	RecordCommitSHA(ctx context.Context, flowNodeExecutionID uuid.UUID, commitSHA string) error
	RouteRunToSession(ctx context.Context, run FlowRun) error
}

type flowSessionExecutionRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNodeExecution, error)
	GetActive(ctx context.Context, taskID, flowNodeID uuid.UUID) (repo.FlowNodeExecution, error)
	SetSessionID(ctx context.Context, id, sessionID uuid.UUID) (repo.FlowNodeExecution, error)
	RecordCommitSHA(ctx context.Context, id uuid.UUID, commitSHA string) (repo.FlowNodeExecution, error)
}

type flowSessionTaskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
}

type flowSessionChatService interface {
	CreateSession(ctx context.Context, input chat.CreateSessionInput) (*repo.ChatSession, error)
	GetSession(ctx context.Context, id uuid.UUID) (*repo.ChatSession, error)
	AppendMessage(ctx context.Context, input chat.AppendMessageInput) (*repo.ChatMessage, error)
}

type FlowSessionBridgeOptions struct {
	Pool       *pgxpool.Pool
	Executions flowSessionExecutionRepository
	Tasks      flowSessionTaskRepository
	Chats      flowSessionChatService
}

type flowSessionBridge struct {
	pool       *pgxpool.Pool
	executions flowSessionExecutionRepository
	tasks      flowSessionTaskRepository
	chats      flowSessionChatService
}

func NewFlowSessionBridge(opts FlowSessionBridgeOptions) (FlowSessionBridge, error) {
	requiresPool := opts.Executions == nil || opts.Tasks == nil
	if opts.Pool == nil && requiresPool {
		return nil, fmt.Errorf("flow session bridge requires a database pool")
	}
	if opts.Chats == nil {
		return nil, fmt.Errorf("flow session bridge requires a chat service")
	}

	bridge := &flowSessionBridge{pool: opts.Pool, chats: opts.Chats}
	if opts.Executions != nil {
		bridge.executions = opts.Executions
	} else {
		bridge.executions = repo.NewFlowNodeExecutionRepo(opts.Pool)
	}
	if opts.Tasks != nil {
		bridge.tasks = opts.Tasks
	} else {
		bridge.tasks = repo.NewProjectTaskRepo(opts.Pool)
	}
	return bridge, nil
}

func (b *flowSessionBridge) EnsureNodeSession(ctx context.Context, execution repo.FlowNodeExecution) (repo.ChatSession, error) {
	if execution.ID == uuid.Nil {
		return repo.ChatSession{}, fmt.Errorf("flow_node_execution id is required")
	}
	if execution.TaskID == uuid.Nil {
		return repo.ChatSession{}, fmt.Errorf("flow_node_execution task_id is required")
	}

	if execution.SessionID != nil && *execution.SessionID != uuid.Nil {
		session, err := b.chats.GetSession(ctx, *execution.SessionID)
		if err != nil {
			return repo.ChatSession{}, err
		}
		if session == nil {
			return repo.ChatSession{}, repo.ErrNotFound
		}
		return *session, nil
	}

	if b.pool != nil {
		return b.ensureNodeSessionTx(ctx, execution)
	}

	taskRecord, err := b.tasks.GetByID(ctx, execution.TaskID)
	if err != nil {
		return repo.ChatSession{}, err
	}

	metadata, err := json.Marshal(map[string]any{
		"flow_node_execution_id": execution.ID.String(),
		"flow_node_id":           execution.FlowNodeID.String(),
	})
	if err != nil {
		return repo.ChatSession{}, err
	}

	session, err := b.chats.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: taskRecord.OrganizationID,
		ScopeType:      "project_task",
		ScopeID:        execution.TaskID,
		Mode:           "async",
		Metadata:       metadata,
	})
	if err != nil {
		return repo.ChatSession{}, err
	}

	_, err = b.executions.SetSessionID(ctx, execution.ID, session.ID)
	if err != nil {
		return repo.ChatSession{}, err
	}
	return *session, nil
}

func (b *flowSessionBridge) ensureNodeSessionTx(ctx context.Context, execution repo.FlowNodeExecution) (repo.ChatSession, error) {
	taskRecord, err := b.tasks.GetByID(ctx, execution.TaskID)
	if err != nil {
		return repo.ChatSession{}, err
	}

	tx, err := b.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return repo.ChatSession{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, execution.ID.String()); err != nil {
		return repo.ChatSession{}, err
	}

	var existingID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM chat_session
		WHERE mode = 'async'
		  AND status = 'active'
		  AND metadata->>'flow_node_execution_id' = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, execution.ID.String()).Scan(&existingID)
	switch {
	case err == nil:
		if _, err := tx.Exec(ctx, `
			UPDATE flow_node_execution
			SET session_id = $2
			WHERE id = $1
			  AND (session_id IS NULL OR session_id = $2)
		`, execution.ID, existingID); err != nil {
			return repo.ChatSession{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return repo.ChatSession{}, err
		}
		session, err := b.chats.GetSession(ctx, existingID)
		if err != nil {
			return repo.ChatSession{}, err
		}
		if session == nil {
			return repo.ChatSession{}, repo.ErrNotFound
		}
		return *session, nil
	case errors.Is(err, pgx.ErrNoRows):
		// continue
	default:
		return repo.ChatSession{}, err
	}

	metadata, err := json.Marshal(map[string]any{
		"flow_node_execution_id": execution.ID.String(),
		"flow_node_id":           execution.FlowNodeID.String(),
	})
	if err != nil {
		return repo.ChatSession{}, err
	}

	var createdID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO chat_session (
			organization_id,
			scope_type,
			scope_id,
			mode,
			status,
			created_by_type,
			created_by_id,
			metadata
		)
		VALUES ($1, 'project_task', $2, 'async', 'active', 'system', $3, $4::jsonb)
		RETURNING id
	`, taskRecord.OrganizationID, execution.TaskID, uuid.Nil, metadata).Scan(&createdID); err != nil {
		return repo.ChatSession{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE flow_node_execution
		SET session_id = $2
		WHERE id = $1
	`, execution.ID, createdID); err != nil {
		return repo.ChatSession{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return repo.ChatSession{}, err
	}

	session, err := b.chats.GetSession(ctx, createdID)
	if err != nil {
		return repo.ChatSession{}, err
	}
	if session == nil {
		return repo.ChatSession{}, repo.ErrNotFound
	}
	return *session, nil
}

func (b *flowSessionBridge) RecordCommitSHA(ctx context.Context, flowNodeExecutionID uuid.UUID, commitSHA string) error {
	if flowNodeExecutionID == uuid.Nil {
		return fmt.Errorf("flow_node_execution id is required")
	}
	_, err := b.executions.RecordCommitSHA(ctx, flowNodeExecutionID, strings.TrimSpace(commitSHA))
	return err
}

func (b *flowSessionBridge) RouteRunToSession(ctx context.Context, run FlowRun) error {
	if run.FlowNodeID == nil || *run.FlowNodeID == uuid.Nil {
		return nil
	}
	if run.TaskID == nil || *run.TaskID == uuid.Nil {
		return nil
	}

	execution, err := b.executions.GetActive(ctx, *run.TaskID, *run.FlowNodeID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	session, err := b.EnsureNodeSession(ctx, execution)
	if err != nil {
		return err
	}

	summary := strings.TrimSpace(run.Summary)
	if summary == "" {
		summary = "run"
	}
	content, err := json.Marshal(map[string]any{
		"type":    "run_log_link",
		"run_id":  run.ID.String(),
		"summary": summary,
	})
	if err != nil {
		return err
	}

	_, err = b.chats.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:     session.ID,
		Role:          "tool_result",
		Content:       string(content),
		ContentFormat: "tool_json",
		Metadata:      json.RawMessage(`{}`),
	})
	return err
}
