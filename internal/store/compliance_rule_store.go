package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ComplianceRuleCategoryCodeQuality = "code_quality"
	ComplianceRuleCategorySecurity    = "security"
	ComplianceRuleCategoryScope       = "scope"
	ComplianceRuleCategoryStyle       = "style"
	ComplianceRuleCategoryProcess     = "process"
	ComplianceRuleCategoryTechnical   = "technical"
)

const (
	ComplianceRuleSeverityRequired      = "required"
	ComplianceRuleSeverityRecommended   = "recommended"
	ComplianceRuleSeverityInformational = "informational"
)

var complianceRuleCategories = map[string]struct{}{
	ComplianceRuleCategoryCodeQuality: {},
	ComplianceRuleCategorySecurity:    {},
	ComplianceRuleCategoryScope:       {},
	ComplianceRuleCategoryStyle:       {},
	ComplianceRuleCategoryProcess:     {},
	ComplianceRuleCategoryTechnical:   {},
}

var complianceRuleSeverities = map[string]struct{}{
	ComplianceRuleSeverityRequired:      {},
	ComplianceRuleSeverityRecommended:   {},
	ComplianceRuleSeverityInformational: {},
}

type ComplianceRule struct {
	ID                   string
	OrgID                string
	ProjectID            *string
	Title                string
	Description          string
	CheckInstruction     string
	Category             string
	Severity             string
	Enabled              bool
	SourceConversationID *string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CreateComplianceRuleInput struct {
	OrgID                string
	ProjectID            *string
	Title                string
	Description          string
	CheckInstruction     string
	Category             string
	Severity             string
	SourceConversationID *string
}

type UpdateComplianceRuleInput struct {
	Title            *string
	Description      *string
	CheckInstruction *string
	Category         *string
	Severity         *string
}

type ComplianceRuleStore struct {
	db *sql.DB
}

func NewComplianceRuleStore(db *sql.DB) *ComplianceRuleStore {
	return &ComplianceRuleStore{db: db}
}

func (s *ComplianceRuleStore) Create(ctx context.Context, input CreateComplianceRuleInput) (*ComplianceRule, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("compliance rule store is not configured")
	}

	orgID := strings.TrimSpace(input.OrgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}

	projectID, err := normalizeComplianceOptionalUUID(input.ProjectID, "project_id")
	if err != nil {
		return nil, err
	}
	sourceConversationID, err := normalizeComplianceOptionalUUID(input.SourceConversationID, "source_conversation_id")
	if err != nil {
		return nil, err
	}
	if err := s.ensureProjectScope(ctx, orgID, projectID); err != nil {
		return nil, err
	}
	if err := s.ensureConversationScope(ctx, orgID, sourceConversationID); err != nil {
		return nil, err
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	description := strings.TrimSpace(input.Description)
	if description == "" {
		return nil, fmt.Errorf("description is required")
	}
	checkInstruction := strings.TrimSpace(input.CheckInstruction)
	if checkInstruction == "" {
		return nil, fmt.Errorf("check_instruction is required")
	}

	category := strings.TrimSpace(strings.ToLower(input.Category))
	if _, ok := complianceRuleCategories[category]; !ok {
		return nil, fmt.Errorf("invalid category")
	}

	severity := strings.TrimSpace(strings.ToLower(input.Severity))
	if severity == "" {
		severity = ComplianceRuleSeverityRequired
	}
	if _, ok := complianceRuleSeverities[severity]; !ok {
		return nil, fmt.Errorf("invalid severity")
	}

	row := s.db.QueryRowContext(
		ctx,
		`INSERT INTO compliance_rules (
			org_id,
			project_id,
			title,
			description,
			check_instruction,
			category,
			severity,
			enabled,
			source_conversation_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, true, $8
		)
		RETURNING
			id,
			org_id,
			project_id::text,
			title,
			description,
			check_instruction,
			category,
			severity,
			enabled,
			source_conversation_id::text,
			created_at,
			updated_at`,
		orgID,
		projectID,
		title,
		description,
		checkInstruction,
		category,
		severity,
		sourceConversationID,
	)

	rule, err := scanComplianceRule(row)
	if err != nil {
		return nil, fmt.Errorf("failed to create compliance rule: %w", err)
	}
	return &rule, nil
}

func (s *ComplianceRuleStore) SetEnabled(ctx context.Context, orgID, ruleID string, enabled bool) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("compliance rule store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	ruleID = strings.TrimSpace(ruleID)
	if !uuidRegex.MatchString(orgID) {
		return fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(ruleID) {
		return fmt.Errorf("invalid rule_id")
	}

	result, err := s.db.ExecContext(
		ctx,
		`UPDATE compliance_rules
		 SET enabled = $3,
		     updated_at = NOW()
		 WHERE org_id = $1
		   AND id = $2`,
		orgID,
		ruleID,
		enabled,
	)
	if err != nil {
		return fmt.Errorf("failed to update compliance rule enabled status: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read compliance rule update result: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *ComplianceRuleStore) GetByID(ctx context.Context, orgID, ruleID string) (*ComplianceRule, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("compliance rule store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	ruleID = strings.TrimSpace(ruleID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(ruleID) {
		return nil, fmt.Errorf("invalid rule_id")
	}

	rule, err := scanComplianceRule(s.db.QueryRowContext(
		ctx,
		`SELECT
			id,
			org_id,
			project_id::text,
			title,
			description,
			check_instruction,
			category,
			severity,
			enabled,
			source_conversation_id::text,
			created_at,
			updated_at
		 FROM compliance_rules
		 WHERE org_id = $1
		   AND id = $2`,
		orgID,
		ruleID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load compliance rule: %w", err)
	}

	return &rule, nil
}

func (s *ComplianceRuleStore) Update(
	ctx context.Context,
	orgID, ruleID string,
	input UpdateComplianceRuleInput,
) (*ComplianceRule, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("compliance rule store is not configured")
	}
	orgID = strings.TrimSpace(orgID)
	ruleID = strings.TrimSpace(ruleID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}
	if !uuidRegex.MatchString(ruleID) {
		return nil, fmt.Errorf("invalid rule_id")
	}

	setTitle := input.Title != nil
	setDescription := input.Description != nil
	setCheckInstruction := input.CheckInstruction != nil
	setCategory := input.Category != nil
	setSeverity := input.Severity != nil

	if !setTitle && !setDescription && !setCheckInstruction && !setCategory && !setSeverity {
		return nil, fmt.Errorf("at least one field is required")
	}

	title := ""
	if setTitle {
		title = strings.TrimSpace(*input.Title)
		if title == "" {
			return nil, fmt.Errorf("title is required")
		}
	}

	description := ""
	if setDescription {
		description = strings.TrimSpace(*input.Description)
		if description == "" {
			return nil, fmt.Errorf("description is required")
		}
	}

	checkInstruction := ""
	if setCheckInstruction {
		checkInstruction = strings.TrimSpace(*input.CheckInstruction)
		if checkInstruction == "" {
			return nil, fmt.Errorf("check_instruction is required")
		}
	}

	category := ""
	if setCategory {
		category = strings.TrimSpace(strings.ToLower(*input.Category))
		if _, ok := complianceRuleCategories[category]; !ok {
			return nil, fmt.Errorf("invalid category")
		}
	}

	severity := ""
	if setSeverity {
		severity = strings.TrimSpace(strings.ToLower(*input.Severity))
		if _, ok := complianceRuleSeverities[severity]; !ok {
			return nil, fmt.Errorf("invalid severity")
		}
	}

	updated, err := scanComplianceRule(s.db.QueryRowContext(
		ctx,
		`UPDATE compliance_rules
		 SET title = CASE WHEN $3 THEN $4 ELSE title END,
		     description = CASE WHEN $5 THEN $6 ELSE description END,
		     check_instruction = CASE WHEN $7 THEN $8 ELSE check_instruction END,
		     category = CASE WHEN $9 THEN $10 ELSE category END,
		     severity = CASE WHEN $11 THEN $12 ELSE severity END,
		     updated_at = NOW()
		 WHERE org_id = $1
		   AND id = $2
		 RETURNING
			id,
			org_id,
			project_id::text,
			title,
			description,
			check_instruction,
			category,
			severity,
			enabled,
			source_conversation_id::text,
			created_at,
			updated_at`,
		orgID,
		ruleID,
		setTitle,
		title,
		setDescription,
		description,
		setCheckInstruction,
		checkInstruction,
		setCategory,
		category,
		setSeverity,
		severity,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to update compliance rule: %w", err)
	}

	return &updated, nil
}

func (s *ComplianceRuleStore) ListApplicableRules(
	ctx context.Context,
	orgID string,
	projectID *string,
) ([]ComplianceRule, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("compliance rule store is not configured")
	}

	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return nil, fmt.Errorf("invalid org_id")
	}

	var rows *sql.Rows
	var err error
	if projectID == nil || strings.TrimSpace(*projectID) == "" {
		rows, err = s.db.QueryContext(
			ctx,
			`SELECT
				id,
				org_id,
				project_id::text,
				title,
				description,
				check_instruction,
				category,
				severity,
				enabled,
				source_conversation_id::text,
				created_at,
				updated_at
			 FROM compliance_rules
			 WHERE org_id = $1
			   AND enabled = true
			   AND project_id IS NULL
			 ORDER BY created_at ASC, id ASC`,
			orgID,
		)
	} else {
		normalizedProjectID := strings.TrimSpace(*projectID)
		if !uuidRegex.MatchString(normalizedProjectID) {
			return nil, fmt.Errorf("invalid project_id")
		}
		rows, err = s.db.QueryContext(
			ctx,
			`SELECT
				id,
				org_id,
				project_id::text,
				title,
				description,
				check_instruction,
				category,
				severity,
				enabled,
				source_conversation_id::text,
				created_at,
				updated_at
			 FROM compliance_rules
			 WHERE org_id = $1
			   AND enabled = true
			   AND (project_id IS NULL OR project_id = $2)
			 ORDER BY CASE WHEN project_id IS NULL THEN 0 ELSE 1 END ASC, created_at ASC, id ASC`,
			orgID,
			normalizedProjectID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list applicable compliance rules: %w", err)
	}
	defer rows.Close()

	rules := make([]ComplianceRule, 0)
	for rows.Next() {
		rule, scanErr := scanComplianceRule(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan compliance rule row: %w", scanErr)
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading compliance rule rows: %w", err)
	}

	return rules, nil
}

func (s *ComplianceRuleStore) ensureProjectScope(ctx context.Context, orgID string, projectID interface{}) error {
	if projectID == nil {
		return nil
	}
	projectIDValue, ok := projectID.(string)
	if !ok || strings.TrimSpace(projectIDValue) == "" {
		return nil
	}

	var exists bool
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT EXISTS(
			SELECT 1
			  FROM projects
			 WHERE id = $1
			   AND org_id = $2
		)`,
		projectIDValue,
		orgID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("failed to validate project scope: %w", err)
	}
	if !exists {
		return fmt.Errorf("project_id does not belong to org")
	}
	return nil
}

func (s *ComplianceRuleStore) ensureConversationScope(ctx context.Context, orgID string, conversationID interface{}) error {
	if conversationID == nil {
		return nil
	}
	conversationIDValue, ok := conversationID.(string)
	if !ok || strings.TrimSpace(conversationIDValue) == "" {
		return nil
	}

	var exists bool
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT EXISTS(
			SELECT 1
			  FROM conversations
			 WHERE id = $1
			   AND org_id = $2
		)`,
		conversationIDValue,
		orgID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("failed to validate source conversation scope: %w", err)
	}
	if !exists {
		return fmt.Errorf("source_conversation_id does not belong to org")
	}
	return nil
}

func normalizeComplianceOptionalUUID(value *string, field string) (interface{}, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if !uuidRegex.MatchString(trimmed) {
		return nil, fmt.Errorf("invalid %s", field)
	}
	return trimmed, nil
}

func scanComplianceRule(scanner interface{ Scan(...any) error }) (ComplianceRule, error) {
	var (
		rule                 ComplianceRule
		projectID            sql.NullString
		sourceConversationID sql.NullString
	)
	err := scanner.Scan(
		&rule.ID,
		&rule.OrgID,
		&projectID,
		&rule.Title,
		&rule.Description,
		&rule.CheckInstruction,
		&rule.Category,
		&rule.Severity,
		&rule.Enabled,
		&sourceConversationID,
		&rule.CreatedAt,
		&rule.UpdatedAt,
	)
	if err != nil {
		return ComplianceRule{}, err
	}
	if projectID.Valid {
		value := strings.TrimSpace(projectID.String)
		if value != "" {
			rule.ProjectID = &value
		}
	}
	if sourceConversationID.Valid {
		value := strings.TrimSpace(sourceConversationID.String)
		if value != "" {
			rule.SourceConversationID = &value
		}
	}
	return rule, nil
}
