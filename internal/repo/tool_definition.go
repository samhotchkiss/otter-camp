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
	"github.com/jackc/pgx/v5/pgxpool"
)

type ToolDefinition struct {
	ID                 uuid.UUID
	Name               string
	DisplayName        string
	Description        string
	ToolTier           string
	ToolDomain         string
	RequiredCapability *string
	InputSchema        json.RawMessage
	IsEnabled          bool
	CreatedAt          time.Time
}

type ToolDefinitionRepo struct {
	pool *pgxpool.Pool
}

func NewToolDefinitionRepo(pool *pgxpool.Pool) *ToolDefinitionRepo {
	return &ToolDefinitionRepo{pool: pool}
}

func (r *ToolDefinitionRepo) Create(ctx context.Context, tool ToolDefinition) (ToolDefinition, error) {
	inputSchema := tool.InputSchema
	if len(inputSchema) == 0 {
		inputSchema = json.RawMessage(`{}`)
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO tool_definition (
			name,
			display_name,
			description,
			tool_tier,
			tool_domain,
			required_capability,
			input_schema,
			is_enabled
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8)
		RETURNING id, name, display_name, description, tool_tier, tool_domain, required_capability, input_schema, is_enabled, created_at
	`, tool.Name, tool.DisplayName, tool.Description, tool.ToolTier, tool.ToolDomain, tool.RequiredCapability, inputSchema, tool.IsEnabled)

	created, err := scanToolDefinition(row)
	if err != nil {
		return ToolDefinition{}, mapDBError(err)
	}
	return created, nil
}

func (r *ToolDefinitionRepo) GetByName(ctx context.Context, name string) (ToolDefinition, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, name, display_name, description, tool_tier, tool_domain, required_capability, input_schema, is_enabled, created_at
		FROM tool_definition
		WHERE name = $1
	`, name)

	tool, err := scanToolDefinition(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ToolDefinition{}, ErrNotFound
	}
	if err != nil {
		return ToolDefinition{}, mapDBError(err)
	}
	return tool, nil
}

func (r *ToolDefinitionRepo) ListByDomain(ctx context.Context, domain string) ([]ToolDefinition, error) {
	return r.list(ctx, `
		SELECT id, name, display_name, description, tool_tier, tool_domain, required_capability, input_schema, is_enabled, created_at
		FROM tool_definition
		WHERE tool_domain = $1
		ORDER BY name
	`, domain)
}

func (r *ToolDefinitionRepo) ListByTier(ctx context.Context, tier string) ([]ToolDefinition, error) {
	return r.list(ctx, `
		SELECT id, name, display_name, description, tool_tier, tool_domain, required_capability, input_schema, is_enabled, created_at
		FROM tool_definition
		WHERE tool_tier = $1
		ORDER BY name
	`, tier)
}

func (r *ToolDefinitionRepo) ListEnabled(ctx context.Context) ([]ToolDefinition, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, display_name, description, tool_tier, tool_domain, required_capability, input_schema, is_enabled, created_at
		FROM tool_definition
		WHERE is_enabled = true
		ORDER BY name
	`)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	tools := make([]ToolDefinition, 0)
	for rows.Next() {
		tool, scanErr := scanToolDefinition(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		tools = append(tools, tool)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return tools, nil
}

func (r *ToolDefinitionRepo) SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) (ToolDefinition, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE tool_definition
		SET is_enabled = $2
		WHERE id = $1
		RETURNING id, name, display_name, description, tool_tier, tool_domain, required_capability, input_schema, is_enabled, created_at
	`, id, enabled)

	tool, err := scanToolDefinition(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ToolDefinition{}, ErrNotFound
	}
	if err != nil {
		return ToolDefinition{}, mapDBError(err)
	}
	return tool, nil
}

func (r *ToolDefinitionRepo) BulkUpsert(ctx context.Context, tools []ToolDefinition) ([]ToolDefinition, error) {
	if len(tools) == 0 {
		return nil, nil
	}

	var sb strings.Builder
	sb.WriteString(`
		INSERT INTO tool_definition (
			name,
			display_name,
			description,
			tool_tier,
			tool_domain,
			required_capability,
			input_schema,
			is_enabled
		) VALUES
	`)

	args := make([]any, 0, len(tools)*8)
	for i, tool := range tools {
		if i > 0 {
			sb.WriteString(",")
		}

		placeholder := i*8 + 1
		sb.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d::jsonb,$%d)",
			placeholder,
			placeholder+1,
			placeholder+2,
			placeholder+3,
			placeholder+4,
			placeholder+5,
			placeholder+6,
			placeholder+7,
		))

		inputSchema := tool.InputSchema
		if len(inputSchema) == 0 {
			inputSchema = json.RawMessage(`{}`)
		}

		args = append(args,
			tool.Name,
			tool.DisplayName,
			tool.Description,
			tool.ToolTier,
			tool.ToolDomain,
			tool.RequiredCapability,
			inputSchema,
			tool.IsEnabled,
		)
	}

	sb.WriteString(`
		ON CONFLICT (name)
		DO UPDATE SET
			display_name = EXCLUDED.display_name,
			description = EXCLUDED.description,
			tool_tier = EXCLUDED.tool_tier,
			tool_domain = EXCLUDED.tool_domain,
			required_capability = EXCLUDED.required_capability,
			input_schema = EXCLUDED.input_schema,
			is_enabled = EXCLUDED.is_enabled
		RETURNING id, name, display_name, description, tool_tier, tool_domain, required_capability, input_schema, is_enabled, created_at
	`)

	rows, err := r.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	upserted := make([]ToolDefinition, 0, len(tools))
	for rows.Next() {
		tool, scanErr := scanToolDefinition(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		upserted = append(upserted, tool)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return upserted, nil
}

func (r *ToolDefinitionRepo) list(ctx context.Context, query string, arg any) ([]ToolDefinition, error) {
	rows, err := r.pool.Query(ctx, query, arg)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	tools := make([]ToolDefinition, 0)
	for rows.Next() {
		tool, scanErr := scanToolDefinition(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		tools = append(tools, tool)
	}

	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}

	return tools, nil
}

func scanToolDefinition(row pgx.Row) (ToolDefinition, error) {
	var tool ToolDefinition
	if err := row.Scan(
		&tool.ID,
		&tool.Name,
		&tool.DisplayName,
		&tool.Description,
		&tool.ToolTier,
		&tool.ToolDomain,
		&tool.RequiredCapability,
		&tool.InputSchema,
		&tool.IsEnabled,
		&tool.CreatedAt,
	); err != nil {
		return ToolDefinition{}, err
	}

	if len(tool.InputSchema) == 0 {
		tool.InputSchema = json.RawMessage(`{}`)
	}
	if !json.Valid(tool.InputSchema) {
		return ToolDefinition{}, fmt.Errorf("invalid input_schema json")
	}

	return tool, nil
}
