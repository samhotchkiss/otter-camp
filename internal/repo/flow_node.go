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

const flowNodeSkillUniqueConstraint = "flow_node_skill_node_skill_unique"

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
	mcpTools, err := normalizeFlowNodeMCPTools(node.MCPTools)
	if err != nil {
		return FlowNode{}, err
	}
	toolDomains, err := normalizeFlowNodeToolDomains(node.ToolDomains)
	if err != nil {
		return FlowNode{}, err
	}

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
		RETURNING id, flow_template_id, display_name, node_type, position, actor_type, actor_id, next_node_id, reject_node_id, mcp_tools, tool_domains, requires_human_review, max_visits, metadata, created_at, updated_at
	`,
		node.FlowTemplateID,
		node.DisplayName,
		node.NodeType,
		node.Position,
		normalizeFlowNodeActorType(node.ActorType),
		node.ActorID,
		node.NextNodeID,
		node.RejectNodeID,
		mcpTools,
		toolDomains,
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
		SELECT id, flow_template_id, display_name, node_type, position, actor_type, actor_id, next_node_id, reject_node_id, mcp_tools, tool_domains, requires_human_review, max_visits, metadata, created_at, updated_at
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
	rows, err := r.pool.Query(ctx, `
		SELECT id, flow_template_id, display_name, node_type, position, actor_type, actor_id, next_node_id, reject_node_id, mcp_tools, tool_domains, requires_human_review, max_visits, metadata, created_at, updated_at
		FROM flow_node
		WHERE flow_template_id = $1
		ORDER BY created_at ASC, id ASC
	`, flowTemplateID)
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

func (r *FlowNodeRepo) GetByTemplateOrdered(ctx context.Context, flowTemplateID uuid.UUID) ([]FlowNode, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, flow_template_id, display_name, node_type, position, actor_type, actor_id, next_node_id, reject_node_id, mcp_tools, tool_domains, requires_human_review, max_visits, metadata, created_at, updated_at
		FROM flow_node
		WHERE flow_template_id = $1
		ORDER BY position ASC, id ASC
	`, flowTemplateID)
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
	mcpTools, err := normalizeFlowNodeMCPTools(node.MCPTools)
	if err != nil {
		return FlowNode{}, err
	}
	toolDomains, err := normalizeFlowNodeToolDomains(node.ToolDomains)
	if err != nil {
		return FlowNode{}, err
	}

	row := r.pool.QueryRow(ctx, `
		UPDATE flow_node
		SET
			flow_template_id = $2,
			display_name = $3,
			node_type = $4,
			position = $5,
			actor_type = $6,
			actor_id = $7,
			next_node_id = $8,
			reject_node_id = $9,
			mcp_tools = $10::jsonb,
			tool_domains = $11::jsonb,
			requires_human_review = $12,
			max_visits = $13,
			metadata = $14::jsonb
		WHERE id = $1
		RETURNING id, flow_template_id, display_name, node_type, position, actor_type, actor_id, next_node_id, reject_node_id, mcp_tools, tool_domains, requires_human_review, max_visits, metadata, created_at, updated_at
	`,
		node.ID,
		node.FlowTemplateID,
		node.DisplayName,
		node.NodeType,
		node.Position,
		normalizeFlowNodeActorType(node.ActorType),
		node.ActorID,
		node.NextNodeID,
		node.RejectNodeID,
		mcpTools,
		toolDomains,
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

func (r *FlowNodeSkillRepo) Attach(ctx context.Context, nodeSkill FlowNodeSkill) (FlowNodeSkill, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO flow_node_skill (
			flow_node_id,
			skill_id,
			position
		)
		VALUES ($1, $2, $3)
		RETURNING id, flow_node_id, skill_id, position, created_at
	`, nodeSkill.FlowNodeID, nodeSkill.SkillID, nodeSkill.Position)

	attached, err := scanFlowNodeSkill(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == flowNodeSkillUniqueConstraint {
			return FlowNodeSkill{}, ErrAlreadyAttached
		}
		return FlowNodeSkill{}, mapDBError(err)
	}
	return attached, nil
}

func (r *FlowNodeSkillRepo) Detach(ctx context.Context, flowNodeID, skillID uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		DELETE FROM flow_node_skill
		WHERE flow_node_id = $1
		  AND skill_id = $2
	`, flowNodeID, skillID)
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

	skills := make([]FlowNodeSkill, 0)
	for rows.Next() {
		nodeSkill, scanErr := scanFlowNodeSkill(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		skills = append(skills, nodeSkill)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return skills, nil
}

func (r *FlowNodeSkillRepo) SetPosition(ctx context.Context, flowNodeID, skillID uuid.UUID, position int) (FlowNodeSkill, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE flow_node_skill
		SET position = $3
		WHERE flow_node_id = $1
		  AND skill_id = $2
		RETURNING id, flow_node_id, skill_id, position, created_at
	`, flowNodeID, skillID, position)

	nodeSkill, err := scanFlowNodeSkill(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowNodeSkill{}, ErrNotFound
	}
	if err != nil {
		return FlowNodeSkill{}, mapDBError(err)
	}
	return nodeSkill, nil
}

func normalizeFlowNodeActorType(actorType *string) *string {
	if actorType == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*actorType)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func normalizeFlowNodeMCPTools(tools []FlowNodeMCPTool) (json.RawMessage, error) {
	if len(tools) == 0 {
		return json.RawMessage(`[]`), nil
	}
	raw, err := json.Marshal(tools)
	if err != nil {
		return nil, fmt.Errorf("marshal flow node mcp tools: %w", err)
	}
	return raw, nil
}

func normalizeFlowNodeToolDomains(domains []string) (json.RawMessage, error) {
	if len(domains) == 0 {
		return json.RawMessage(`[]`), nil
	}
	raw, err := json.Marshal(domains)
	if err != nil {
		return nil, fmt.Errorf("marshal flow node tool domains: %w", err)
	}
	return raw, nil
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

func decodeFlowNodeMCPTools(raw []byte) ([]FlowNodeMCPTool, error) {
	if len(raw) == 0 {
		return []FlowNodeMCPTool{}, nil
	}
	var tools []FlowNodeMCPTool
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("unmarshal flow node mcp tools: %w", err)
	}
	if tools == nil {
		return []FlowNodeMCPTool{}, nil
	}
	return tools, nil
}

func decodeFlowNodeToolDomains(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var domains []string
	if err := json.Unmarshal(raw, &domains); err != nil {
		return nil, fmt.Errorf("unmarshal flow node tool domains: %w", err)
	}
	if domains == nil {
		return []string{}, nil
	}
	return domains, nil
}

func scanFlowNode(row pgx.Row) (FlowNode, error) {
	var (
		node           FlowNode
		mcpToolsRaw    []byte
		toolDomainsRaw []byte
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
		&mcpToolsRaw,
		&toolDomainsRaw,
		&node.RequiresHumanReview,
		&node.MaxVisits,
		&node.Metadata,
		&node.CreatedAt,
		&node.UpdatedAt,
	); err != nil {
		return FlowNode{}, err
	}

	mcpTools, err := decodeFlowNodeMCPTools(mcpToolsRaw)
	if err != nil {
		return FlowNode{}, err
	}
	node.MCPTools = mcpTools

	toolDomains, err := decodeFlowNodeToolDomains(toolDomainsRaw)
	if err != nil {
		return FlowNode{}, err
	}
	node.ToolDomains = toolDomains

	if len(node.Metadata) == 0 {
		node.Metadata = json.RawMessage(`{}`)
	}

	return node, nil
}

func scanFlowNodeSkill(row pgx.Row) (FlowNodeSkill, error) {
	var nodeSkill FlowNodeSkill
	if err := row.Scan(
		&nodeSkill.ID,
		&nodeSkill.FlowNodeID,
		&nodeSkill.SkillID,
		&nodeSkill.Position,
		&nodeSkill.CreatedAt,
	); err != nil {
		return FlowNodeSkill{}, err
	}
	return nodeSkill, nil
}
