package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Agent struct {
	ID                    uuid.UUID
	OrganizationID        uuid.UUID
	DisplayName           string
	AgentClass            string
	LifecycleStatus       string
	SystemPrompt          string
	OperatorInstructions  string
	AgentType             string
	IsStarterTrio         bool
	PrivateMemory         bool
	MemoryReadScopes      []string
	ToolAllowList         []string
	ToolDenyList          []string
	DefaultModelProfileID *string
	BudgetCapTokens       *int64
	BudgetPeriod          *string
	TempProjectID         *uuid.UUID
	TempTTLSeconds        *int32
	TempExpiresAt         *time.Time
	PromotedToAgentID     *uuid.UUID
	CreatedByType         string
	CreatedByID           uuid.UUID
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type AgentRepo struct {
	pool *pgxpool.Pool
}

func NewAgentRepo(pool *pgxpool.Pool) *AgentRepo {
	return &AgentRepo{pool: pool}
}

func (r *AgentRepo) Create(ctx context.Context, agent Agent) (Agent, error) {
	memoryReadScopes := normalizeStringSlice(agent.MemoryReadScopes)
	toolAllowList := normalizeStringSlice(agent.ToolAllowList)
	toolDenyList := normalizeStringSlice(agent.ToolDenyList)

	row := r.pool.QueryRow(ctx, `
		INSERT INTO agent (
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
		RETURNING
			id,
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
	`,
		agent.OrganizationID,
		agent.DisplayName,
		agent.AgentClass,
		agent.LifecycleStatus,
		agent.SystemPrompt,
		agent.OperatorInstructions,
		agent.AgentType,
		agent.IsStarterTrio,
		agent.PrivateMemory,
		memoryReadScopes,
		toolAllowList,
		toolDenyList,
		agent.DefaultModelProfileID,
		agent.BudgetCapTokens,
		agent.BudgetPeriod,
		agent.TempProjectID,
		agent.TempTTLSeconds,
		agent.TempExpiresAt,
		agent.PromotedToAgentID,
		agent.CreatedByType,
		agent.CreatedByID,
	)

	created, err := scanAgent(row)
	if err != nil {
		return Agent{}, mapDBError(err)
	}
	return created, nil
}

func (r *AgentRepo) GetByID(ctx context.Context, id uuid.UUID) (Agent, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM agent
		WHERE id = $1
	`, id)

	agent, err := scanAgent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, mapDBError(err)
	}
	return agent, nil
}

func (r *AgentRepo) List(ctx context.Context, organizationID uuid.UUID) ([]Agent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM agent
		WHERE organization_id = $1
		ORDER BY created_at ASC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	agents := make([]Agent, 0)
	for rows.Next() {
		agent, scanErr := scanAgent(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		agents = append(agents, agent)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return agents, nil
}

func (r *AgentRepo) Update(ctx context.Context, agent Agent) (Agent, error) {
	memoryReadScopes := normalizeStringSlice(agent.MemoryReadScopes)
	toolAllowList := normalizeStringSlice(agent.ToolAllowList)
	toolDenyList := normalizeStringSlice(agent.ToolDenyList)

	row := r.pool.QueryRow(ctx, `
		UPDATE agent
		SET
			display_name = $2,
			agent_class = $3,
			lifecycle_status = $4,
			system_prompt = $5,
			operator_instructions = $6,
			agent_type = $7,
			is_starter_trio = $8,
			private_memory = $9,
			memory_read_scopes = $10,
			tool_allow_list = $11,
			tool_deny_list = $12,
			default_model_profile_id = $13,
			budget_cap_tokens = $14,
			budget_period = $15,
			temp_project_id = $16,
			temp_ttl_seconds = $17,
			temp_expires_at = $18,
			promoted_to_agent_id = $19
		WHERE id = $1
		RETURNING
			id,
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
	`,
		agent.ID,
		agent.DisplayName,
		agent.AgentClass,
		agent.LifecycleStatus,
		agent.SystemPrompt,
		agent.OperatorInstructions,
		agent.AgentType,
		agent.IsStarterTrio,
		agent.PrivateMemory,
		memoryReadScopes,
		toolAllowList,
		toolDenyList,
		agent.DefaultModelProfileID,
		agent.BudgetCapTokens,
		agent.BudgetPeriod,
		agent.TempProjectID,
		agent.TempTTLSeconds,
		agent.TempExpiresAt,
		agent.PromotedToAgentID,
	)

	updated, err := scanAgent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, mapDBError(err)
	}
	return updated, nil
}

func (r *AgentRepo) SetLifecycleStatus(ctx context.Context, id uuid.UUID, lifecycleStatus string) (Agent, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE agent
		SET lifecycle_status = $2
		WHERE id = $1
		RETURNING
			id,
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
	`, id, lifecycleStatus)

	updated, err := scanAgent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, mapDBError(err)
	}
	return updated, nil
}

func (r *AgentRepo) ListActiveTemps(ctx context.Context, organizationID uuid.UUID) ([]Agent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM agent
		WHERE organization_id = $1
		  AND agent_class = 'temp'
		  AND lifecycle_status = 'active'
		ORDER BY created_at ASC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	agents := make([]Agent, 0)
	for rows.Next() {
		agent, scanErr := scanAgent(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		agents = append(agents, agent)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return agents, nil
}

func (r *AgentRepo) ListExpiredTemps(ctx context.Context, organizationID uuid.UUID) ([]Agent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM agent
		WHERE organization_id = $1
		  AND agent_class = 'temp'
		  AND lifecycle_status = 'active'
		  AND temp_expires_at IS NOT NULL
		  AND temp_expires_at < now()
		ORDER BY temp_expires_at ASC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	agents := make([]Agent, 0)
	for rows.Next() {
		agent, scanErr := scanAgent(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		agents = append(agents, agent)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return agents, nil
}

func (r *AgentRepo) CountActiveTemps(ctx context.Context, organizationID uuid.UUID) (int, error) {
	var count int
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent
		WHERE organization_id = $1
		  AND agent_class = 'temp'
		  AND lifecycle_status = 'active'
	`, organizationID).Scan(&count); err != nil {
		return 0, mapDBError(err)
	}
	return count, nil
}

func (r *AgentRepo) GetStarterTrio(ctx context.Context, organizationID uuid.UUID) ([]Agent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			display_name,
			agent_class,
			lifecycle_status,
			system_prompt,
			operator_instructions,
			agent_type,
			is_starter_trio,
			private_memory,
			memory_read_scopes,
			tool_allow_list,
			tool_deny_list,
			default_model_profile_id,
			budget_cap_tokens,
			budget_period,
			temp_project_id,
			temp_ttl_seconds,
			temp_expires_at,
			promoted_to_agent_id,
			created_by_type,
			created_by_id,
			created_at,
			updated_at
		FROM agent
		WHERE organization_id = $1
		  AND is_starter_trio = true
		ORDER BY display_name ASC
	`, organizationID)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	agents := make([]Agent, 0)
	for rows.Next() {
		agent, scanErr := scanAgent(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		agents = append(agents, agent)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return agents, nil
}

func scanAgent(row pgx.Row) (Agent, error) {
	var agent Agent
	if err := row.Scan(
		&agent.ID,
		&agent.OrganizationID,
		&agent.DisplayName,
		&agent.AgentClass,
		&agent.LifecycleStatus,
		&agent.SystemPrompt,
		&agent.OperatorInstructions,
		&agent.AgentType,
		&agent.IsStarterTrio,
		&agent.PrivateMemory,
		&agent.MemoryReadScopes,
		&agent.ToolAllowList,
		&agent.ToolDenyList,
		&agent.DefaultModelProfileID,
		&agent.BudgetCapTokens,
		&agent.BudgetPeriod,
		&agent.TempProjectID,
		&agent.TempTTLSeconds,
		&agent.TempExpiresAt,
		&agent.PromotedToAgentID,
		&agent.CreatedByType,
		&agent.CreatedByID,
		&agent.CreatedAt,
		&agent.UpdatedAt,
	); err != nil {
		return Agent{}, err
	}
	return agent, nil
}

type AgentProfileTemplate struct {
	ID                    uuid.UUID
	OrganizationID        *uuid.UUID
	DisplayName           string
	Source                string
	SourceAgentID         *uuid.UUID
	SystemPrompt          string
	OperatorInstructions  string
	AgentType             string
	DefaultModelProfileID *string
	ToolAllowList         []string
	ToolDenyList          []string
	MemoryReadScopes      []string
	PrivateMemory         bool
	Metadata              json.RawMessage
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type AgentProfileTemplateRepo struct {
	pool *pgxpool.Pool
}

func NewAgentProfileTemplateRepo(pool *pgxpool.Pool) *AgentProfileTemplateRepo {
	return &AgentProfileTemplateRepo{pool: pool}
}

func (r *AgentProfileTemplateRepo) Create(ctx context.Context, template AgentProfileTemplate) (AgentProfileTemplate, error) {
	metadata := template.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	toolAllowList := normalizeStringSlice(template.ToolAllowList)
	toolDenyList := normalizeStringSlice(template.ToolDenyList)
	memoryReadScopes := normalizeStringSlice(template.MemoryReadScopes)

	row := r.pool.QueryRow(ctx, `
		INSERT INTO agent_profile_template (
			organization_id,
			display_name,
			source,
			source_agent_id,
			system_prompt,
			operator_instructions,
			agent_type,
			default_model_profile_id,
			tool_allow_list,
			tool_deny_list,
			memory_read_scopes,
			private_memory,
			metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb)
		RETURNING
			id,
			organization_id,
			display_name,
			source,
			source_agent_id,
			system_prompt,
			operator_instructions,
			agent_type,
			default_model_profile_id,
			tool_allow_list,
			tool_deny_list,
			memory_read_scopes,
			private_memory,
			metadata,
			created_at,
			updated_at
	`,
		template.OrganizationID,
		template.DisplayName,
		template.Source,
		template.SourceAgentID,
		template.SystemPrompt,
		template.OperatorInstructions,
		template.AgentType,
		template.DefaultModelProfileID,
		toolAllowList,
		toolDenyList,
		memoryReadScopes,
		template.PrivateMemory,
		metadata,
	)

	created, err := scanAgentProfileTemplate(row)
	if err != nil {
		return AgentProfileTemplate{}, mapDBError(err)
	}
	return created, nil
}

func (r *AgentProfileTemplateRepo) GetByID(ctx context.Context, id uuid.UUID) (AgentProfileTemplate, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT
			id,
			organization_id,
			display_name,
			source,
			source_agent_id,
			system_prompt,
			operator_instructions,
			agent_type,
			default_model_profile_id,
			tool_allow_list,
			tool_deny_list,
			memory_read_scopes,
			private_memory,
			metadata,
			created_at,
			updated_at
		FROM agent_profile_template
		WHERE id = $1
	`, id)

	template, err := scanAgentProfileTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentProfileTemplate{}, ErrNotFound
	}
	if err != nil {
		return AgentProfileTemplate{}, mapDBError(err)
	}
	return template, nil
}

func (r *AgentProfileTemplateRepo) List(ctx context.Context, organizationID uuid.UUID, includeSystem bool) ([]AgentProfileTemplate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT
			id,
			organization_id,
			display_name,
			source,
			source_agent_id,
			system_prompt,
			operator_instructions,
			agent_type,
			default_model_profile_id,
			tool_allow_list,
			tool_deny_list,
			memory_read_scopes,
			private_memory,
			metadata,
			created_at,
			updated_at
		FROM agent_profile_template
		WHERE organization_id = $1 OR ($2 AND organization_id IS NULL)
		ORDER BY created_at ASC
	`, organizationID, includeSystem)
	if err != nil {
		return nil, mapDBError(err)
	}
	defer rows.Close()

	templates := make([]AgentProfileTemplate, 0)
	for rows.Next() {
		template, scanErr := scanAgentProfileTemplate(rows)
		if scanErr != nil {
			return nil, mapDBError(scanErr)
		}
		templates = append(templates, template)
	}
	if rows.Err() != nil {
		return nil, mapDBError(rows.Err())
	}
	return templates, nil
}

func (r *AgentProfileTemplateRepo) Update(ctx context.Context, template AgentProfileTemplate) (AgentProfileTemplate, error) {
	metadata := template.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	toolAllowList := normalizeStringSlice(template.ToolAllowList)
	toolDenyList := normalizeStringSlice(template.ToolDenyList)
	memoryReadScopes := normalizeStringSlice(template.MemoryReadScopes)

	row := r.pool.QueryRow(ctx, `
		UPDATE agent_profile_template
		SET
			organization_id = $2,
			display_name = $3,
			source = $4,
			source_agent_id = $5,
			system_prompt = $6,
			operator_instructions = $7,
			agent_type = $8,
			default_model_profile_id = $9,
			tool_allow_list = $10,
			tool_deny_list = $11,
			memory_read_scopes = $12,
			private_memory = $13,
			metadata = $14::jsonb
		WHERE id = $1
		RETURNING
			id,
			organization_id,
			display_name,
			source,
			source_agent_id,
			system_prompt,
			operator_instructions,
			agent_type,
			default_model_profile_id,
			tool_allow_list,
			tool_deny_list,
			memory_read_scopes,
			private_memory,
			metadata,
			created_at,
			updated_at
	`,
		template.ID,
		template.OrganizationID,
		template.DisplayName,
		template.Source,
		template.SourceAgentID,
		template.SystemPrompt,
		template.OperatorInstructions,
		template.AgentType,
		template.DefaultModelProfileID,
		toolAllowList,
		toolDenyList,
		memoryReadScopes,
		template.PrivateMemory,
		metadata,
	)

	updated, err := scanAgentProfileTemplate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentProfileTemplate{}, ErrNotFound
	}
	if err != nil {
		return AgentProfileTemplate{}, mapDBError(err)
	}
	return updated, nil
}

func (r *AgentProfileTemplateRepo) Delete(ctx context.Context, id uuid.UUID) error {
	ct, err := r.pool.Exec(ctx, `
		DELETE FROM agent_profile_template
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

func scanAgentProfileTemplate(row pgx.Row) (AgentProfileTemplate, error) {
	var template AgentProfileTemplate
	if err := row.Scan(
		&template.ID,
		&template.OrganizationID,
		&template.DisplayName,
		&template.Source,
		&template.SourceAgentID,
		&template.SystemPrompt,
		&template.OperatorInstructions,
		&template.AgentType,
		&template.DefaultModelProfileID,
		&template.ToolAllowList,
		&template.ToolDenyList,
		&template.MemoryReadScopes,
		&template.PrivateMemory,
		&template.Metadata,
		&template.CreatedAt,
		&template.UpdatedAt,
	); err != nil {
		return AgentProfileTemplate{}, err
	}

	if len(template.Metadata) == 0 {
		template.Metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(template.Metadata) {
		return AgentProfileTemplate{}, fmt.Errorf("invalid metadata json")
	}

	return template, nil
}

func normalizeStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
