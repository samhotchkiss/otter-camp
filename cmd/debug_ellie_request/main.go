package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type captureRunner struct {
	args []string
}

func (r *captureRunner) Run(ctx context.Context, args []string) ([]byte, error) {
	r.args = append([]string{}, args...)

	// Minimal valid OpenClaw gateway response with an empty candidates payload.
	resp := map[string]any{
		"runId":  "debug-run",
		"status": "ok",
		"result": map[string]any{
			"payloads": []map[string]any{{
				"text": "```json\n{\"candidates\": []}\n```",
			}},
			"meta": map[string]any{
				"agentMeta": map[string]any{
					"model": "claude-haiku-4-5",
				},
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b, nil
}

func mustOpenDB(dbURL string) *sql.DB {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}
	return db
}

type requestDump struct {
	Args       []string
	ParamsJSON map[string]any
	Prompt     string
}

var (
	// Conservative redactions for common secret/token formats we might see in chat history.
	reOpsToken       = regexp.MustCompile(`\bops_[A-Za-z0-9_-]{20,}\b`)
	reOcToken        = regexp.MustCompile(`\boc_[A-Za-z0-9_-]{20,}\b`)
	reOpenAIKey      = regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`)
	reGithubPat      = regexp.MustCompile(`\b(ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`)
	reSlackToken     = regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)
	reBearerToken    = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._-]{20,}\b`)
	reLoginTokenQS   = regexp.MustCompile(`(?i)(loginToken=)[A-Za-z0-9._-]{10,}`)
	reOPServiceToken = regexp.MustCompile(`(?i)(\bOP_SERVICE_ACCOUNT_TOKEN\b\s*[=:]\s*)("?)[^"\s]{10,}("?)`)
	// Matches cases like: "**Password:** `secret`" or "Password: `secret`"
	rePasswordBacktick = regexp.MustCompile("(?i)(\\bpassword\\b[^`\\n]{0,40}`)([^`]{6,})(`)")
	reLongBase64Like   = regexp.MustCompile(`\b[A-Za-z0-9+/_-]{48,}={0,2}\b`)
)

func redactSensitive(s string) string {
	if s == "" {
		return s
	}
	// Specific formats first.
	s = reBearerToken.ReplaceAllString(s, "Bearer <REDACTED>")
	s = reOPServiceToken.ReplaceAllString(s, "${1}${2}<REDACTED>${3}")
	s = reOpsToken.ReplaceAllString(s, "ops_<REDACTED>")
	s = reOcToken.ReplaceAllString(s, "oc_<REDACTED>")
	s = reOpenAIKey.ReplaceAllString(s, "sk-<REDACTED>")
	s = reGithubPat.ReplaceAllString(s, "<REDACTED_GITHUB_TOKEN>")
	s = reSlackToken.ReplaceAllString(s, "<REDACTED_SLACK_TOKEN>")
	s = reLoginTokenQS.ReplaceAllString(s, "${1}<REDACTED>")
	s = rePasswordBacktick.ReplaceAllString(s, "${1}<REDACTED>${3}")

	// Last-resort catch for giant opaque tokens that are likely secrets.
	// This will also redact some non-secrets, but keeps the prompt structure.
	s = reLongBase64Like.ReplaceAllStringFunc(s, func(m string) string {
		// Don't redact small-ish IDs that are actually UUID-ish or normal words.
		// This regex doesn't match UUIDs, but it can match long paths/filenames;
		// keep those by requiring at least one of +/= which are more token-ish.
		if strings.ContainsAny(m, "+/=") {
			return "<REDACTED_BLOB>"
		}
		return "<REDACTED_TOKEN>"
	})
	return s
}

func maybeRedactDump(d *requestDump, redact bool) {
	if d == nil || !redact {
		return
	}
	d.Prompt = redactSensitive(d.Prompt)
	if d.ParamsJSON != nil {
		if msg, ok := d.ParamsJSON["message"].(string); ok {
			d.ParamsJSON["message"] = redactSensitive(msg)
		}
	}
	// Redact the --params arg in Args (it duplicates the prompt).
	for i := 0; i < len(d.Args); i++ {
		if d.Args[i] != "--params" || i+1 >= len(d.Args) {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(d.Args[i+1]), &obj); err != nil {
			// If we can't parse, fall back to a blunt replacement.
			d.Args[i+1] = "<REDACTED_PARAMS_JSON>"
			break
		}
		if msg, ok := obj["message"].(string); ok {
			obj["message"] = redactSensitive(msg)
		}
		b, _ := json.Marshal(obj)
		d.Args[i+1] = string(b)
		break
	}
}

func buildRequestDump(
	ctx context.Context,
	fixedNow time.Time,
	orgID, roomID string,
	msgs []store.EllieIngestionMessage,
	maxPromptChars int,
	maxMessageChars int,
) (*requestDump, error) {
	runner := &captureRunner{}
	extractor, err := memory.NewEllieIngestionOpenClawExtractor(memory.EllieIngestionOpenClawExtractorConfig{
		Runner:           runner,
		OpenClawBinary:   "openclaw",
		GatewayURL:       "ws://127.0.0.1:18791",
		GatewayToken:     "",
		AgentID:          "elephant",
		SessionNamespace: "ellie-ingestion",
		Now: func() time.Time {
			return fixedNow
		},
		// Defaults used in production unless overridden by env.
		MaxPromptChars:  maxPromptChars,
		MaxMessageChars: maxMessageChars,
	})
	if err != nil {
		return nil, err
	}

	_, err = extractor.Extract(ctx, memory.EllieIngestionLLMExtractionInput{
		OrgID:    orgID,
		RoomID:   roomID,
		Messages: msgs,
	})
	if err != nil {
		return nil, err
	}

	dump := &requestDump{Args: runner.args}
	for i := 0; i < len(runner.args); i++ {
		if runner.args[i] != "--params" || i+1 >= len(runner.args) {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(runner.args[i+1]), &obj); err != nil {
			return nil, fmt.Errorf("failed to decode --params JSON: %w", err)
		}
		dump.ParamsJSON = obj
		if msg, ok := obj["message"].(string); ok {
			dump.Prompt = msg
		}
		break
	}
	return dump, nil
}

func countPromptMessages(prompt string) int {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return 0
	}
	// Prompt format uses lines that begin with "- id=".
	n := strings.Count(prompt, "\n- id=")
	if strings.HasPrefix(prompt, "- id=") {
		n++
	}
	return n
}

func main() {
	var (
		mode           = flag.String("mode", "room-day", "one of: room-day, room-oldest, org-oldest, db-oldest")
		dbURL          = flag.String("db", "", "database URL (env DATABASE_PUBLIC_URL or DATABASE_URL, or pass via -db)")
		orgID          = flag.String("org", "", "org/workspace UUID (required for room-day, room-oldest, org-oldest)")
		roomID         = flag.String("room", "", "room UUID")
		day0           = flag.String("day0", "", "day0 UTC date (YYYY-MM-DD); if empty, uses date_trunc(day, min(chat_messages.created_at))")
		nowStr         = flag.String("now", "2026-01-28T00:00:00Z", "fixed now() used only for idempotencyKey")
		limit          = flag.Int("limit", 30, "number of messages selected from the DB")
		redact         = flag.Bool("redact", true, "redact likely secrets/tokens in printed prompts/params")
		split          = flag.Bool("split", true, "split selected messages into multiple requests so every selected message is represented in some prompt")
		maxPromptChars = flag.Int("max-prompt-chars", 18000, "max prompt chars (matches ellie ingestion OpenClaw transport budget)")
		maxMessageChars = flag.Int("max-message-chars", 1200, "max message body chars per entry (matches ellie ingestion formatting)")
	)
	flag.Parse()

	if *dbURL == "" {
		*dbURL = strings.TrimSpace(os.Getenv("DATABASE_PUBLIC_URL"))
	}
	if *dbURL == "" {
		*dbURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if *orgID == "" {
		*orgID = strings.TrimSpace(os.Getenv("ORG_ID"))
	}
	if *roomID == "" {
		*roomID = strings.TrimSpace(os.Getenv("ROOM_ID"))
	}

	fixedNow, err := time.Parse(time.RFC3339, strings.TrimSpace(*nowStr))
	if err != nil {
		log.Fatalf("invalid -now: %v", err)
	}

	modeVal := strings.ToLower(strings.TrimSpace(*mode))
	if *dbURL == "" {
		log.Fatal("missing required -db (or env DATABASE_PUBLIC_URL/DATABASE_URL)")
	}
	if modeVal != "db-oldest" && strings.TrimSpace(*orgID) == "" {
		log.Fatal("missing required -org (or env ORG_ID)")
	}

	db := mustOpenDB(*dbURL)
	defer db.Close()

	ctx := context.Background()

	switch modeVal {
	case "db-oldest":
		type row struct {
			msg store.EllieIngestionMessage
			seq int
		}

		rows, err := db.QueryContext(ctx,
			`select id::text, org_id::text, room_id::text, body, created_at, conversation_id::text
			   from chat_messages
			  order by created_at asc, id asc
			  limit $1`,
			*limit,
		)
		if err != nil {
			log.Fatal(err)
		}
		defer rows.Close()

		// Group by (org_id, room_id). Extraction is per-room within an org.
		type groupKey struct {
			orgID  string
			roomID string
		}
		byGroup := map[groupKey][]row{}
		orgSet := map[string]struct{}{}
		seq := 0
		for rows.Next() {
			var (
				m              store.EllieIngestionMessage
				conversationID sql.NullString
			)
			if err := rows.Scan(&m.ID, &m.OrgID, &m.RoomID, &m.Body, &m.CreatedAt, &conversationID); err != nil {
				log.Fatal(err)
			}
			if conversationID.Valid {
				val := strings.TrimSpace(conversationID.String)
				if val != "" {
					m.ConversationID = &val
				}
			}
			k := groupKey{orgID: m.OrgID, roomID: m.RoomID}
			byGroup[k] = append(byGroup[k], row{msg: m, seq: seq})
			orgSet[m.OrgID] = struct{}{}
			seq++
		}
		if err := rows.Err(); err != nil {
			log.Fatal(err)
		}
		if len(byGroup) == 0 {
			log.Fatal("no messages found")
		}

		keys := make([]groupKey, 0, len(byGroup))
		for k := range byGroup {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].orgID != keys[j].orgID {
				return keys[i].orgID < keys[j].orgID
			}
			return keys[i].roomID < keys[j].roomID
		})

		fmt.Printf("mode=db-oldest\n")
		fmt.Printf("messages_selected_total=%d\n", seq)
		fmt.Printf("orgs_in_selection=%d\n", len(orgSet))
		fmt.Printf("groups_in_selection=%d\n\n", len(keys))

		for _, k := range keys {
			rows := byGroup[k]
			sort.Slice(rows, func(i, j int) bool { return rows[i].seq < rows[j].seq })
			msgs := make([]store.EllieIngestionMessage, 0, len(rows))
			for _, r := range rows {
				msgs = append(msgs, r.msg)
			}

			chunks := [][]store.EllieIngestionMessage{msgs}
			if *split {
				chunks = memory.SplitEllieIngestionWindowForPromptBudget(k.orgID, k.roomID, msgs, *maxPromptChars, *maxMessageChars)
			}

			totalInPrompts := 0
			for idx, chunk := range chunks {
				dump, err := buildRequestDump(ctx, fixedNow, k.orgID, k.roomID, chunk, *maxPromptChars, *maxMessageChars)
				if err != nil {
					log.Fatal(err)
				}
				fullDump, err := buildRequestDump(ctx, fixedNow, k.orgID, k.roomID, chunk, 0, *maxMessageChars)
				if err != nil {
					log.Fatal(err)
				}
				maybeRedactDump(dump, *redact)
				pretty, _ := json.MarshalIndent(dump.ParamsJSON, "", "  ")
				fmt.Printf(
					"=== org_id=%s room_id=%s chunk=%d/%d messages_selected=%d first_created_at=%s ===\n",
					k.orgID,
					k.roomID,
					idx+1,
					len(chunks),
					len(chunk),
					chunk[0].CreatedAt.UTC().Format(time.RFC3339),
				)
				fmt.Printf("prompt_chars=%d messages_in_prompt=%d\n", len(dump.Prompt), countPromptMessages(dump.Prompt))
				fmt.Printf("full_prompt_chars=%d full_messages_in_prompt=%d budget_prompt_chars=%d\n", len(fullDump.Prompt), countPromptMessages(fullDump.Prompt), *maxPromptChars)
				fmt.Printf("openclaw_args=\n")
				for i, a := range dump.Args {
					fmt.Printf("  [%02d] %s\n", i, a)
				}
				fmt.Printf("\nparams_json=\n%s\n\n", string(pretty))
				totalInPrompts += countPromptMessages(dump.Prompt)
			}
			if *split {
				fmt.Printf(
					"=== org_id=%s room_id=%s summary: messages_selected_total=%d messages_in_prompts_total=%d ===\n\n",
					k.orgID,
					k.roomID,
					len(msgs),
					totalInPrompts,
				)
			}
		}
		return

	case "org-oldest":
		type row struct {
			msg store.EllieIngestionMessage
			seq int
		}
		rows, err := db.QueryContext(ctx,
			`select id::text, org_id::text, room_id::text, body, created_at, conversation_id::text
			   from chat_messages
			  where org_id=$1
			  order by created_at asc, id asc
			  limit $2`,
			*orgID,
			*limit,
		)
		if err != nil {
			log.Fatal(err)
		}
		defer rows.Close()

		byRoom := map[string][]row{}
		seq := 0
		for rows.Next() {
			var (
				m              store.EllieIngestionMessage
				conversationID sql.NullString
			)
			if err := rows.Scan(&m.ID, &m.OrgID, &m.RoomID, &m.Body, &m.CreatedAt, &conversationID); err != nil {
				log.Fatal(err)
			}
			if conversationID.Valid {
				val := strings.TrimSpace(conversationID.String)
				if val != "" {
					m.ConversationID = &val
				}
			}
			byRoom[m.RoomID] = append(byRoom[m.RoomID], row{msg: m, seq: seq})
			seq++
		}
		if err := rows.Err(); err != nil {
			log.Fatal(err)
		}
		if len(byRoom) == 0 {
			log.Fatalf("no messages found for org=%s", *orgID)
		}

		roomIDs := make([]string, 0, len(byRoom))
		for id := range byRoom {
			roomIDs = append(roomIDs, id)
		}
		sort.Strings(roomIDs)

		fmt.Printf("mode=org-oldest\n")
		fmt.Printf("org_id=%s\n", *orgID)
		fmt.Printf("messages_selected_total=%d\n", seq)
		fmt.Printf("rooms_in_selection=%d\n\n", len(roomIDs))

		for _, rid := range roomIDs {
			rows := byRoom[rid]
			sort.Slice(rows, func(i, j int) bool { return rows[i].seq < rows[j].seq })
			msgs := make([]store.EllieIngestionMessage, 0, len(rows))
			for _, r := range rows {
				msgs = append(msgs, r.msg)
			}

			chunks := [][]store.EllieIngestionMessage{msgs}
			if *split {
				chunks = memory.SplitEllieIngestionWindowForPromptBudget(*orgID, rid, msgs, *maxPromptChars, *maxMessageChars)
			}

			totalInPrompts := 0
			for idx, chunk := range chunks {
				dump, err := buildRequestDump(ctx, fixedNow, *orgID, rid, chunk, *maxPromptChars, *maxMessageChars)
				if err != nil {
					log.Fatal(err)
				}
				fullDump, err := buildRequestDump(ctx, fixedNow, *orgID, rid, chunk, 0, *maxMessageChars)
				if err != nil {
					log.Fatal(err)
				}
				maybeRedactDump(dump, *redact)
				pretty, _ := json.MarshalIndent(dump.ParamsJSON, "", "  ")
				fmt.Printf(
					"=== room_id=%s chunk=%d/%d messages_selected=%d first_created_at=%s ===\n",
					rid,
					idx+1,
					len(chunks),
					len(chunk),
					chunk[0].CreatedAt.UTC().Format(time.RFC3339),
				)
				fmt.Printf("prompt_chars=%d messages_in_prompt=%d\n", len(dump.Prompt), countPromptMessages(dump.Prompt))
				fmt.Printf("full_prompt_chars=%d full_messages_in_prompt=%d budget_prompt_chars=%d\n", len(fullDump.Prompt), countPromptMessages(fullDump.Prompt), *maxPromptChars)
				fmt.Printf("openclaw_args=\n")
				for i, a := range dump.Args {
					fmt.Printf("  [%02d] %s\n", i, a)
				}
				fmt.Printf("\nparams_json=\n%s\n\n", string(pretty))
				totalInPrompts += countPromptMessages(dump.Prompt)
			}
			if *split {
				fmt.Printf(
					"=== room_id=%s summary: messages_selected_total=%d messages_in_prompts_total=%d ===\n\n",
					rid,
					len(msgs),
					totalInPrompts,
				)
			}
		}
		return

	case "room-oldest":
		if strings.TrimSpace(*roomID) == "" {
			log.Fatal("mode=room-oldest requires -room")
		}
		rows, err := db.QueryContext(ctx,
			`select id::text, org_id::text, room_id::text, body, created_at, conversation_id::text
			   from chat_messages
			  where org_id=$1
			    and room_id=$2
			  order by created_at asc, id asc
			  limit $3`,
			*orgID,
			*roomID,
			*limit,
		)
		if err != nil {
			log.Fatal(err)
		}
		defer rows.Close()

		msgs := make([]store.EllieIngestionMessage, 0, *limit)
		for rows.Next() {
			var (
				m              store.EllieIngestionMessage
				conversationID sql.NullString
			)
			if err := rows.Scan(&m.ID, &m.OrgID, &m.RoomID, &m.Body, &m.CreatedAt, &conversationID); err != nil {
				log.Fatal(err)
			}
			if conversationID.Valid {
				val := strings.TrimSpace(conversationID.String)
				if val != "" {
					m.ConversationID = &val
				}
			}
			msgs = append(msgs, m)
		}
		if err := rows.Err(); err != nil {
			log.Fatal(err)
		}
		if len(msgs) == 0 {
			log.Fatalf("no messages found for org=%s room=%s", *orgID, *roomID)
		}

		chunks := [][]store.EllieIngestionMessage{msgs}
		if *split {
			chunks = memory.SplitEllieIngestionWindowForPromptBudget(*orgID, *roomID, msgs, *maxPromptChars, *maxMessageChars)
		}

		fmt.Printf("mode=room-oldest\n")
		fmt.Printf("org_id=%s\n", *orgID)
		fmt.Printf("room_id=%s\n", *roomID)
		fmt.Printf("messages_selected_total=%d\n\n", len(msgs))

		totalInPrompts := 0
		for idx, chunk := range chunks {
			dump, err := buildRequestDump(ctx, fixedNow, *orgID, *roomID, chunk, *maxPromptChars, *maxMessageChars)
			if err != nil {
				log.Fatal(err)
			}
			fullDump, err := buildRequestDump(ctx, fixedNow, *orgID, *roomID, chunk, 0, *maxMessageChars)
			if err != nil {
				log.Fatal(err)
			}
			maybeRedactDump(dump, *redact)
			pretty, _ := json.MarshalIndent(dump.ParamsJSON, "", "  ")
			fmt.Printf("=== chunk=%d/%d messages_selected=%d first_created_at=%s ===\n", idx+1, len(chunks), len(chunk), chunk[0].CreatedAt.UTC().Format(time.RFC3339))
			fmt.Printf("prompt_chars=%d messages_in_prompt=%d\n", len(dump.Prompt), countPromptMessages(dump.Prompt))
			fmt.Printf("full_prompt_chars=%d full_messages_in_prompt=%d budget_prompt_chars=%d\n", len(fullDump.Prompt), countPromptMessages(fullDump.Prompt), *maxPromptChars)
			fmt.Printf("openclaw_args=\n")
			for i, a := range dump.Args {
				fmt.Printf("  [%02d] %s\n", i, a)
			}
			fmt.Printf("\nparams_json=\n%s\n\n", string(pretty))
			totalInPrompts += countPromptMessages(dump.Prompt)
		}
		if *split {
			fmt.Printf("summary: messages_selected_total=%d messages_in_prompts_total=%d\n", len(msgs), totalInPrompts)
		}
		return

	default: // room-day
		if strings.TrimSpace(*roomID) == "" {
			log.Fatal("mode=room-day requires -room")
		}

		var day0Start time.Time
		if strings.TrimSpace(*day0) != "" {
			day0Start, err = time.Parse("2006-01-02", strings.TrimSpace(*day0))
			if err != nil {
				log.Fatalf("invalid -day0: %v", err)
			}
			day0Start = day0Start.UTC()
		} else {
			if err := db.QueryRowContext(ctx,
				"select date_trunc('day', min(created_at)) from chat_messages where org_id=$1",
				*orgID,
			).Scan(&day0Start); err != nil {
				log.Fatal(err)
			}
			day0Start = day0Start.UTC()
		}
		day0End := day0Start.Add(24 * time.Hour)

		rows, err := db.QueryContext(ctx,
			`select id::text, org_id::text, room_id::text, body, created_at, conversation_id::text
			   from chat_messages
			  where org_id=$1
			    and room_id=$2
			    and created_at >= $3
			    and created_at < $4
			  order by created_at asc, id asc
			  limit $5`,
			*orgID,
			*roomID,
			day0Start,
			day0End,
			*limit,
		)
		if err != nil {
			log.Fatal(err)
		}
		defer rows.Close()

		msgs := make([]store.EllieIngestionMessage, 0, *limit)
		for rows.Next() {
			var (
				m              store.EllieIngestionMessage
				conversationID sql.NullString
			)
			if err := rows.Scan(&m.ID, &m.OrgID, &m.RoomID, &m.Body, &m.CreatedAt, &conversationID); err != nil {
				log.Fatal(err)
			}
			if conversationID.Valid {
				val := strings.TrimSpace(conversationID.String)
				if val != "" {
					m.ConversationID = &val
				}
			}
			msgs = append(msgs, m)
		}
		if err := rows.Err(); err != nil {
			log.Fatal(err)
		}
		if len(msgs) == 0 {
			log.Fatalf("no messages found for org=%s room=%s in [%s, %s)", *orgID, *roomID, day0Start.Format(time.RFC3339), day0End.Format(time.RFC3339))
		}

		fmt.Printf("mode=room-day\n")
		fmt.Printf("day0_start_utc=%s\n", day0Start.Format(time.RFC3339))
		fmt.Printf("org_id=%s\n", *orgID)
		fmt.Printf("room_id=%s\n", *roomID)
		fmt.Printf("messages_selected_total=%d\n\n", len(msgs))

		chunks := [][]store.EllieIngestionMessage{msgs}
		if *split {
			chunks = memory.SplitEllieIngestionWindowForPromptBudget(*orgID, *roomID, msgs, *maxPromptChars, *maxMessageChars)
		}
		totalInPrompts := 0
		for idx, chunk := range chunks {
			dump, err := buildRequestDump(ctx, fixedNow, *orgID, *roomID, chunk, *maxPromptChars, *maxMessageChars)
			if err != nil {
				log.Fatal(err)
			}
			fullDump, err := buildRequestDump(ctx, fixedNow, *orgID, *roomID, chunk, 0, *maxMessageChars)
			if err != nil {
				log.Fatal(err)
			}
			maybeRedactDump(dump, *redact)
			pretty, _ := json.MarshalIndent(dump.ParamsJSON, "", "  ")
			fmt.Printf("=== chunk=%d/%d messages_selected=%d first_created_at=%s ===\n", idx+1, len(chunks), len(chunk), chunk[0].CreatedAt.UTC().Format(time.RFC3339))
			fmt.Printf("prompt_chars=%d messages_in_prompt=%d\n", len(dump.Prompt), countPromptMessages(dump.Prompt))
			fmt.Printf("full_prompt_chars=%d full_messages_in_prompt=%d budget_prompt_chars=%d\n", len(fullDump.Prompt), countPromptMessages(fullDump.Prompt), *maxPromptChars)
			fmt.Printf("openclaw_args=\n")
			for i, a := range dump.Args {
				fmt.Printf("  [%02d] %s\n", i, a)
			}
			fmt.Printf("\nparams_json=\n%s\n\n", string(pretty))
			totalInPrompts += countPromptMessages(dump.Prompt)
		}
		if *split {
			fmt.Printf("summary: messages_selected_total=%d messages_in_prompts_total=%d\n", len(msgs), totalInPrompts)
		}
		return
	}
}
