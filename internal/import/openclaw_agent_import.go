package importer

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"regexp"
	"strings"
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

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return OpenClawAgentImportResult{}, fmt.Errorf("failed to start openclaw agent import transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result := OpenClawAgentImportResult{}
	seenSlugs := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		slug := normalizeOpenClawImportAgentSlug(identity.ID)
		if slug == "" {
			continue
		}
		if _, seen := seenSlugs[slug]; seen {
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

		if _, err := tx.ExecContext(
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
				updated_at = NOW()`,
			orgID,
			slug,
			displayName,
			status,
			openClawImportNullableText(role),
			openClawImportNullableText(emoji),
			openClawImportNullableText(identity.Soul),
			openClawImportNullableText(identity.Identity),
			openClawImportNullableText(identity.Tools),
		); err != nil {
			return OpenClawAgentImportResult{}, fmt.Errorf("failed to upsert openclaw agent %q: %w", slug, err)
		}

		result.ImportedAgents++
		if status == "active" {
			result.ActiveAgents++
		} else {
			result.InactiveAgents++
		}
	}

	if err := tx.Commit(); err != nil {
		return OpenClawAgentImportResult{}, fmt.Errorf("failed to commit openclaw agent import transaction: %w", err)
	}
	committed = true

	if opts.SummaryWriter != nil {
		_, _ = fmt.Fprintf(
			opts.SummaryWriter,
			"OpenClaw agent import: imported %d agents (%d active, %d inactive)\n",
			result.ImportedAgents,
			result.ActiveAgents,
			result.InactiveAgents,
		)
	}

	return result, nil
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
