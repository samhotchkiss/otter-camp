package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var openClawImportUUIDRegex = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

var openClawImportActiveAgentSlugs = map[string]struct{}{
	"main":     {},
	"frank":    {},
	"elephant": {},
	"ellie":    {},
	"lori":     {},
}

var openClawImportActiveAgentNames = map[string]struct{}{
	"frank": {},
	"ellie": {},
	"lori":  {},
}

type OpenClawAgentImportOptions struct {
	OrgID         string
	Installation  *OpenClawInstallation
	SummaryWriter io.Writer
}

type OpenClawAgentImportResult struct {
	ImportedAgents int
	ActiveAgents   int
	InactiveAgents int
}

type OpenClawAgentPayloadImportOptions struct {
	OrgID         string
	Identities    []ImportedAgentIdentity
	SummaryWriter io.Writer
}

type OpenClawAgentPayloadImportResult struct {
	Processed      int
	Inserted       int
	Updated        int
	Skipped        int
	ActiveAgents   int
	InactiveAgents int
	Warnings       []string
}

func ImportOpenClawAgents(
	ctx context.Context,
	db *sql.DB,
	opts OpenClawAgentImportOptions,
) (OpenClawAgentImportResult, error) {
	if db == nil {
		return OpenClawAgentImportResult{}, fmt.Errorf("database is required")
	}

	orgID := strings.TrimSpace(opts.OrgID)
	if !openClawImportUUIDRegex.MatchString(orgID) {
		return OpenClawAgentImportResult{}, fmt.Errorf("invalid org_id")
	}
	if opts.Installation == nil {
		return OpenClawAgentImportResult{}, fmt.Errorf("installation is required")
	}
	identities, err := ImportOpenClawAgentIdentities(opts.Installation)
	if err != nil {
		return OpenClawAgentImportResult{}, err
	}

	result, err := ImportOpenClawAgentsFromPayload(ctx, db, OpenClawAgentPayloadImportOptions{
		OrgID:      orgID,
		Identities: identities,
	})
	if err != nil {
		return OpenClawAgentImportResult{}, err
	}

	legacyResult := OpenClawAgentImportResult{
		ImportedAgents: result.Processed,
		ActiveAgents:   result.ActiveAgents,
		InactiveAgents: result.InactiveAgents,
	}

	if opts.SummaryWriter != nil {
		_, _ = fmt.Fprintf(
			opts.SummaryWriter,
			"OpenClaw agent import: imported %d agents (%d active, %d inactive)\n",
			legacyResult.ImportedAgents,
			legacyResult.ActiveAgents,
			legacyResult.InactiveAgents,
		)
	}

	return legacyResult, nil
}

func ImportOpenClawAgentsFromPayload(
	ctx context.Context,
	db *sql.DB,
	opts OpenClawAgentPayloadImportOptions,
) (OpenClawAgentPayloadImportResult, error) {
	if db == nil {
		return OpenClawAgentPayloadImportResult{}, fmt.Errorf("database is required")
	}

	orgID := strings.TrimSpace(opts.OrgID)
	if !openClawImportUUIDRegex.MatchString(orgID) {
		return OpenClawAgentPayloadImportResult{}, fmt.Errorf("invalid org_id")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return OpenClawAgentPayloadImportResult{}, fmt.Errorf("failed to start openclaw agent import transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result, err := importOpenClawAgentPayload(ctx, tx, orgID, opts.Identities)
	if err != nil {
		return OpenClawAgentPayloadImportResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return OpenClawAgentPayloadImportResult{}, fmt.Errorf("failed to commit openclaw agent import transaction: %w", err)
	}
	committed = true

	if opts.SummaryWriter != nil {
		_, _ = fmt.Fprintf(
			opts.SummaryWriter,
			"OpenClaw agent payload import: processed=%d inserted=%d updated=%d skipped=%d active=%d inactive=%d\n",
			result.Processed,
			result.Inserted,
			result.Updated,
			result.Skipped,
			result.ActiveAgents,
			result.InactiveAgents,
		)
	}

	return result, nil
}

func importOpenClawAgentPayload(
	ctx context.Context,
	tx *sql.Tx,
	orgID string,
	identities []ImportedAgentIdentity,
) (OpenClawAgentPayloadImportResult, error) {
	result := OpenClawAgentPayloadImportResult{}
	seenSlugs := make(map[string]struct{}, len(identities))
	for idx, identity := range identities {
		rawID := strings.TrimSpace(identity.ID)
		if rawID == "" {
			result.Skipped++
			result.Warnings = append(result.Warnings, fmt.Sprintf("identity[%d] skipped: missing identity id", idx))
			continue
		}

		slug := normalizeOpenClawImportAgentSlug(rawID)
		if slug == "" {
			result.Skipped++
			result.Warnings = append(result.Warnings, fmt.Sprintf("identity[%d] skipped: invalid identity id %q", idx, rawID))
			continue
		}
		if _, seen := seenSlugs[slug]; seen {
			result.Skipped++
			result.Warnings = append(result.Warnings, fmt.Sprintf("identity[%d] skipped: duplicate identity slug %q", idx, slug))
			continue
		}
		seenSlugs[slug] = struct{}{}

		displayName := strings.TrimSpace(identity.Name)
		if displayName == "" {
			displayName = slug
		}

		status := deriveOpenClawImportAgentStatus(slug, displayName)
		role := extractOpenClawImportAgentRole(identity.Soul)
		emoji := extractOpenClawImportAgentEmoji(displayName)

		exists, err := openClawImportAgentExists(ctx, tx, orgID, slug)
		if err != nil {
			return OpenClawAgentPayloadImportResult{}, err
		}

		var agentID string
		if err := tx.QueryRowContext(
			ctx,
			`INSERT INTO agents (
				org_id,
				slug,
				display_name,
				status,
				role,
				emoji,
				soul_md,
				identity_md,
				instructions_md,
				is_ephemeral
			) VALUES (
				$1,
				$2,
				$3,
				$4,
				$5,
				$6,
				$7,
				$8,
				$9,
				false
			)
			ON CONFLICT (org_id, slug) DO UPDATE SET
				display_name = EXCLUDED.display_name,
				status = EXCLUDED.status,
				role = COALESCE(EXCLUDED.role, agents.role),
				emoji = COALESCE(EXCLUDED.emoji, agents.emoji),
				soul_md = COALESCE(EXCLUDED.soul_md, agents.soul_md),
				identity_md = COALESCE(EXCLUDED.identity_md, agents.identity_md),
				instructions_md = COALESCE(EXCLUDED.instructions_md, agents.instructions_md),
				is_ephemeral = false,
				updated_at = NOW()
			RETURNING id::text`,
			orgID,
			slug,
			displayName,
			status,
			openClawImportNullableText(role),
			openClawImportNullableText(emoji),
			openClawImportNullableText(identity.Soul),
			openClawImportNullableText(identity.Identity),
			openClawImportNullableText(identity.Tools),
		).Scan(&agentID); err != nil {
			return OpenClawAgentPayloadImportResult{}, fmt.Errorf("failed to upsert openclaw agent %q: %w", slug, err)
		}

		// Index agent MEMORY.md as first-class memories. This is curated, high-signal context.
		// We keep ingestion deterministic here (split into bullet/paragraph chunks) and rely on
		// embedding + dedup downstream to normalize.
		if err := upsertOpenClawAgentMemoryMarkdown(ctx, tx, orgID, agentID, slug, identity); err != nil {
			// Never fail the whole agent import because one agent memory file is malformed.
			result.Warnings = append(result.Warnings, fmt.Sprintf("agent %q MEMORY.md ingest failed: %v", slug, err))
		}

		result.Processed++
		if exists {
			result.Updated++
		} else {
			result.Inserted++
		}
		if status == "active" {
			result.ActiveAgents++
		} else {
			result.InactiveAgents++
		}
	}

	return result, nil
}

func upsertOpenClawAgentMemoryMarkdown(
	ctx context.Context,
	tx *sql.Tx,
	orgID string,
	agentID string,
	agentSlug string,
	identity ImportedAgentIdentity,
) error {
	if tx == nil {
		return fmt.Errorf("transaction is required")
	}
	orgID = strings.TrimSpace(orgID)
	agentID = strings.TrimSpace(agentID)
	agentSlug = strings.TrimSpace(agentSlug)
	if orgID == "" || agentID == "" || agentSlug == "" {
		return nil
	}

	raw := strings.TrimSpace(identity.Memory)
	if raw == "" {
		return nil
	}

	sourcePath := ""
	if identity.SourceFiles != nil {
		sourcePath = strings.TrimSpace(identity.SourceFiles["MEMORY.md"])
	}

	chunks := splitAgentMemoryMarkdown(raw)
	if len(chunks) == 0 {
		return nil
	}

	now := time.Now().UTC()
	for _, chunk := range chunks {
		content := strings.TrimSpace(chunk)
		if content == "" {
			continue
		}
		kind, title := deriveAgentMemoryKindAndTitle(agentSlug, content)

		metadataRaw, _ := json.Marshal(map[string]any{
			"source_table": "agent_memory_md",
			"agent_id":     agentID,
			"agent_slug":   agentSlug,
			"file_name":    "MEMORY.md",
			"file_path":    sourcePath,
		})

		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO memories (
				org_id, kind, title, content, metadata, importance, confidence, status,
				source_conversation_id, source_project_id, occurred_at, sensitivity
			) VALUES (
				$1, $2, $3, $4, $5::jsonb, $6, $7, 'active',
				NULL, NULL, $8, 'internal'
			)
			ON CONFLICT (org_id, content_hash) WHERE status = 'active' DO NOTHING`,
			orgID,
			kind,
			title,
			content,
			metadataRaw,
			5,
			0.95,
			now,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func splitAgentMemoryMarkdown(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	var para []string

	flush := func() {
		if len(para) == 0 {
			return
		}
		text := strings.TrimSpace(strings.Join(para, " "))
		para = para[:0]
		if text != "" {
			out = append(out, text)
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			flush()
			continue
		}

		// Prefer bullets as atomic memories.
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			flush()
			item := strings.TrimSpace(trimmed[2:])
			if item != "" {
				out = append(out, item)
			}
			continue
		}

		para = append(para, trimmed)
	}
	flush()

	// Cap size; memory rows should be small and standalone.
	capped := make([]string, 0, len(out))
	for _, item := range out {
		runes := []rune(strings.TrimSpace(item))
		if len(runes) == 0 {
			continue
		}
		if len(runes) > 400 {
			item = string(runes[:400])
		}
		capped = append(capped, item)
	}
	return capped
}

func deriveAgentMemoryKindAndTitle(agentSlug string, content string) (string, string) {
	lower := strings.ToLower(content)
	kind := "fact"
	title := "Agent memory"
	switch {
	case strings.Contains(lower, "prefer") || strings.Contains(lower, "preference"):
		kind = "preference"
		title = "Agent preference"
	case strings.Contains(lower, "avoid") || strings.Contains(lower, "don't") || strings.Contains(lower, "do not"):
		kind = "anti_pattern"
		title = "Agent anti-pattern"
	case strings.Contains(lower, "decide") || strings.Contains(lower, "decision:"):
		kind = "technical_decision"
		title = "Agent decision"
	}
	if strings.TrimSpace(agentSlug) != "" {
		title = fmt.Sprintf("%s (%s)", title, strings.TrimSpace(agentSlug))
	}
	return kind, title
}

func openClawImportAgentExists(ctx context.Context, tx *sql.Tx, orgID, slug string) (bool, error) {
	var existing string
	err := tx.QueryRowContext(
		ctx,
		`SELECT slug
		   FROM agents
		  WHERE org_id = $1
		    AND slug = $2
		  LIMIT 1`,
		orgID,
		slug,
	).Scan(&existing)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, fmt.Errorf("failed to check existing openclaw agent %q: %w", slug, err)
}

func normalizeOpenClawImportAgentSlug(raw string) string {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	if normalized == "" {
		return ""
	}
	replacer := strings.NewReplacer(" ", "-", "_", "-", ".", "-")
	normalized = replacer.Replace(normalized)

	var b strings.Builder
	lastDash := false
	for _, ch := range normalized {
		isAlnum := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
		if isAlnum {
			b.WriteRune(ch)
			lastDash = false
			continue
		}
		if ch == '-' && !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}

	return strings.Trim(b.String(), "-")
}

func deriveOpenClawImportAgentStatus(slug, displayName string) string {
	if _, ok := openClawImportActiveAgentSlugs[strings.ToLower(strings.TrimSpace(slug))]; ok {
		return "active"
	}
	if _, ok := openClawImportActiveAgentNames[strings.ToLower(strings.TrimSpace(displayName))]; ok {
		return "active"
	}
	return "inactive"
}

func extractOpenClawImportAgentRole(soul string) string {
	for _, line := range strings.Split(soul, "\n") {
		candidate := strings.TrimSpace(strings.TrimLeft(line, "#*- "))
		if candidate == "" {
			continue
		}
		return candidate
	}
	return ""
}

func extractOpenClawImportAgentEmoji(displayName string) string {
	trimmed := strings.TrimSpace(displayName)
	if trimmed == "" {
		return ""
	}
	first, _ := utf8.DecodeRuneInString(trimmed)
	if first == utf8.RuneError {
		return ""
	}
	if !unicode.Is(unicode.So, first) {
		return ""
	}
	return string(first)
}

func openClawImportNullableText(value string) interface{} {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}
