package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAlreadyAttached = errors.New("repo: already attached")

type FlowNodeMCPTool struct {
	ConnectionID uuid.UUID `json:"connection_id"`
	ToolName     string    `json:"tool_name"`
}

type FlowNode struct {
	ID                  uuid.UUID
	FlowTemplateID      uuid.UUID
	DisplayName         string
	NodeType            string
	Position            int
	ActorType           *string
	ActorID             *uuid.UUID
	NextNodeID          *uuid.UUID
	RejectNodeID        *uuid.UUID
	MCPTools            []FlowNodeMCPTool
	ToolDomains         []string
	RequiresHumanReview bool
	MaxVisits           int
	Metadata            json.RawMessage
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type FlowNodeSkill struct {
	ID         uuid.UUID
	FlowNodeID uuid.UUID
	SkillID    uuid.UUID
	Position   int
	CreatedAt  time.Time
}

type FlowNodeRepo struct {
	pool *pgxpool.Pool
}

func NewFlowNodeRepo(pool *pgxpool.Pool) *FlowNodeRepo {
	return &FlowNodeRepo{pool: pool}
}

func (r *FlowNodeRepo) Create(ctx context.Context, node FlowNode) (FlowNode, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO flow_node (
			flow_template_id,
			display_name,
			node_type,
			position,
			actor_type,
			actor_id,
			next_node_id,
			reject_node_id,
			mcp_tools,
			tool_domains,
			requires_human_review,
			max_visits,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::jsonb, $11, $12, $13::jsonb)
		RETURNING
			id,
			flow_template_id,
			display_name,
			node_type,
			position,
			actor_type,
			actor_id,
			next_node_id,
			reject_node_id,
			mcp_tools,
			tool_domains,
			requires_human_review,
			max_visits,
			metadata,
			created_at,
			updated_at
	`,
		node.FlowTemplateID,
		strings.TrimSpace(node.DisplayName),
		strings.TrimSpace(node.NodeType),
		node.Position,
		trimStringPointer(node.ActorType),
		node.ActorID,
		node.NextNodeID,
		node.RejectNodeID,
		marshalFlowNodeMCPTools(node.MCPTools),
		marshalFlowNodeToolDomains(node.ToolDomains),
		node.RequiresHumanReview,
		defaultFlowNodeMaxVisits(node.MaxVisits),
		normalizeFlowNodeMetadata(node.Metadata),
	)

	created, err := scanFlowNode(row)
	if err != nil {
		return FlowNode{}, mapDBError(err)
	}
	return created, nil
}

func (r *FlowNodeRepo) GetByID(ctx context.Context, id uuid.UUID) (FlowNode, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			flow_template_id,
			display_name,
			node_type,
			position,
			actor_type,
			actor_id,
			next_node_id,
			reject_node_id,
			mcp_tools,
			tool_domains,
			requires_human_review,
			max_visits,
			metadata,
			created_at,
			updated_at
		FROM flow_node
		WHERE id = $1
	`, id)

	node, err := scanFlowNode(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowNode{}, ErrNotFound
	}
	if err != nil {
		return FlowNode{}, mapDBError(err)
	}
	return node, nil
}

func (r *FlowNodeRepo) ListByTemplate(ctx context.Context, flowTemplateID uuid.UUID) ([]FlowNode, error) {
	return r.listByTemplate(ctx, flowTemplateID, false)
}

func (r *FlowNodeRepo) GetByTemplateOrdered(ctx context.Context, flowTemplateID uuid.UUID) ([]FlowNode, error) {
	return r.listByTemplate(ctx, flowTemplateID, true)
}

func (r *FlowNodeRepo) listByTemplate(ctx context.Context, flowTemplateID uuid.UUID, ordered bool) ([]FlowNode, error) {
	query := `
		SELECT
			id,
			flow_template_id,
			display_name,
			node_type,
			position,
			actor_type,
			actor_id,
			next_node_id,
			reject_node_id,
			mcp_tools,
			tool_domains,
			requires_human_review,
			max_visits,
			metadata,
			created_at,
			updated_at
		FROM flow_node
		WHERE flow_template_id = $1
	`
	if ordered {
		query += " ORDER BY position ASC, id ASC"
	} else {
		query += " ORDER BY created_at ASC, id ASC"
	}

	rows, err := r.pool.Query(ctx, query, flowTemplateID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	nodes := make([]FlowNode, 0)
	for rows.Next() {
		node, scanErr := scanFlowNode(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		nodes = append(nodes, node)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return nodes, nil
}

func (r *FlowNodeRepo) Update(ctx context.Context, node FlowNode) (FlowNode, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE flow_node
		SET
			display_name = $2,
			node_type = $3,
			position = $4,
			actor_type = $5,
			actor_id = $6,
			next_node_id = $7,
			reject_node_id = $8,
			mcp_tools = $9::jsonb,
			tool_domains = $10::jsonb,
			requires_human_review = $11,
			max_visits = $12,
			metadata = $13::jsonb
		WHERE id = $1
		RETURNING
			id,
			flow_template_id,
			display_name,
			node_type,
			position,
			actor_type,
			actor_id,
			next_node_id,
			reject_node_id,
			mcp_tools,
			tool_domains,
			requires_human_review,
			max_visits,
			metadata,
			created_at,
			updated_at
	`,
		node.ID,
		strings.TrimSpace(node.DisplayName),
		strings.TrimSpace(node.NodeType),
		node.Position,
		trimStringPointer(node.ActorType),
		node.ActorID,
		node.NextNodeID,
		node.RejectNodeID,
		marshalFlowNodeMCPTools(node.MCPTools),
		marshalFlowNodeToolDomains(node.ToolDomains),
		node.RequiresHumanReview,
		defaultFlowNodeMaxVisits(node.MaxVisits),
		normalizeFlowNodeMetadata(node.Metadata),
	)

	updated, err := scanFlowNode(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowNode{}, ErrNotFound
	}
	if err != nil {
		return FlowNode{}, mapDBError(err)
	}
	return updated, nil
}

func (r *FlowNodeRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		DELETE FROM flow_node
		WHERE id = $1
	`, id)
	if err != nil {
		return mapDBError(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type FlowNodeSkillRepo struct {
	pool *pgxpool.Pool
}

func NewFlowNodeSkillRepo(pool *pgxpool.Pool) *FlowNodeSkillRepo {
	return &FlowNodeSkillRepo{pool: pool}
}

func (r *FlowNodeSkillRepo) Attach(ctx context.Context, flowNodeID, skillID uuid.UUID, position int) (FlowNodeSkill, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO flow_node_skill (
			flow_node_id,
			skill_id,
			position
		)
		VALUES ($1, $2, $3)
		RETURNING id, flow_node_id, skill_id, position, created_at
	`, flowNodeID, skillID, position)

	attached, err := scanFlowNodeSkill(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return FlowNodeSkill{}, ErrAlreadyAttached
		}
		return FlowNodeSkill{}, mapDBError(err)
	}
	return attached, nil
}

func (r *FlowNodeSkillRepo) Detach(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		DELETE FROM flow_node_skill
		WHERE id = $1
	`, id)
	if err != nil {
		return mapDBError(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *FlowNodeSkillRepo) ListByNode(ctx context.Context, flowNodeID uuid.UUID) ([]FlowNodeSkill, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, flow_node_id, skill_id, position, created_at
		FROM flow_node_skill
		WHERE flow_node_id = $1
		ORDER BY position ASC, id ASC
	`, flowNodeID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	attachments := make([]FlowNodeSkill, 0)
	for rows.Next() {
		attachment, scanErr := scanFlowNodeSkill(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		attachments = append(attachments, attachment)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return attachments, nil
}

func (r *FlowNodeSkillRepo) SetPosition(ctx context.Context, id uuid.UUID, position int) (FlowNodeSkill, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE flow_node_skill
		SET position = $2
		WHERE id = $1
		RETURNING id, flow_node_id, skill_id, position, created_at
	`, id, position)

	updated, err := scanFlowNodeSkill(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowNodeSkill{}, ErrNotFound
	}
	if err != nil {
		return FlowNodeSkill{}, mapDBError(err)
	}
	return updated, nil
}

func scanFlowNode(row pgx.Row) (FlowNode, error) {
	var (
		node            FlowNode
		mcpToolsJSON    json.RawMessage
		toolDomainsJSON json.RawMessage
		metadataJSON    json.RawMessage
	)

	if err := row.Scan(
		&node.ID,
		&node.FlowTemplateID,
		&node.DisplayName,
		&node.NodeType,
		&node.Position,
		&node.ActorType,
		&node.ActorID,
		&node.NextNodeID,
		&node.RejectNodeID,
		&mcpToolsJSON,
		&toolDomainsJSON,
		&node.RequiresHumanReview,
		&node.MaxVisits,
		&metadataJSON,
		&node.CreatedAt,
		&node.UpdatedAt,
	); err != nil {
		return FlowNode{}, err
	}

	if len(mcpToolsJSON) == 0 || string(mcpToolsJSON) == "null" {
		mcpToolsJSON = json.RawMessage(`[]`)
	}
	if err := json.Unmarshal(mcpToolsJSON, &node.MCPTools); err != nil {
		return FlowNode{}, fmt.Errorf("unmarshal flow_node.mcp_tools: %w", err)
	}

	if len(toolDomainsJSON) == 0 || string(toolDomainsJSON) == "null" {
		toolDomainsJSON = json.RawMessage(`[]`)
	}
	if err := json.Unmarshal(toolDomainsJSON, &node.ToolDomains); err != nil {
		return FlowNode{}, fmt.Errorf("unmarshal flow_node.tool_domains: %w", err)
	}

	if len(metadataJSON) == 0 || string(metadataJSON) == "null" {
		metadataJSON = json.RawMessage(`{}`)
	}
	node.Metadata = metadataJSON

	if node.MCPTools == nil {
		node.MCPTools = make([]FlowNodeMCPTool, 0)
	}
	if node.ToolDomains == nil {
		node.ToolDomains = make([]string, 0)
	}

	return node, nil
}

func scanFlowNodeSkill(row pgx.Row) (FlowNodeSkill, error) {
	var attachment FlowNodeSkill
	if err := row.Scan(
		&attachment.ID,
		&attachment.FlowNodeID,
		&attachment.SkillID,
		&attachment.Position,
		&attachment.CreatedAt,
	); err != nil {
		return FlowNodeSkill{}, err
	}
	return attachment, nil
}

func marshalFlowNodeMCPTools(tools []FlowNodeMCPTool) json.RawMessage {
	if tools == nil {
		return json.RawMessage(`[]`)
	}
	data, err := json.Marshal(tools)
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return data
}

func marshalFlowNodeToolDomains(domains []string) json.RawMessage {
	if domains == nil {
		return json.RawMessage(`[]`)
	}
	data, err := json.Marshal(domains)
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return data
}

func normalizeFlowNodeMetadata(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func defaultFlowNodeMaxVisits(maxVisits int) int {
	if maxVisits <= 0 {
		return 10
	}
	return maxVisits
}
