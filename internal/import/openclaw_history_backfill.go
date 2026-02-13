package importer

import (
	"context"
	"crypto/md5"
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

type OpenClawHistoryBackfillOptions struct {
	OrgID         string
	UserID        string
	ParsedEvents  []OpenClawSessionEvent
	SummaryWriter io.Writer
}

type OpenClawHistoryBackfillResult struct {
	RoomsCreated      int
	ParticipantsAdded int
	MessagesInserted  int
	EventsProcessed   int
}

type openClawBackfillAgent struct {
	ID          string
	Slug        string
	DisplayName string
}

func BackfillOpenClawHistory(
	ctx context.Context,
	db *sql.DB,
	opts OpenClawHistoryBackfillOptions,
) (OpenClawHistoryBackfillResult, error) {
	if db == nil {
		return OpenClawHistoryBackfillResult{}, fmt.Errorf("database is required")
	}

	orgID := strings.TrimSpace(opts.OrgID)
	if !openClawImportUUIDRegex.MatchString(orgID) {
		return OpenClawHistoryBackfillResult{}, fmt.Errorf("invalid org_id")
	}
	userID := strings.TrimSpace(opts.UserID)
	if !openClawImportUUIDRegex.MatchString(userID) {
		return OpenClawHistoryBackfillResult{}, fmt.Errorf("invalid user_id")
	}
	if len(opts.ParsedEvents) == 0 {
		return OpenClawHistoryBackfillResult{}, nil
	}

	events := make([]OpenClawSessionEvent, 0, len(opts.ParsedEvents))
	for _, event := range opts.ParsedEvents {
		if strings.TrimSpace(event.AgentSlug) == "" {
			continue
		}
		if strings.TrimSpace(event.Body) == "" {
			continue
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		return OpenClawHistoryBackfillResult{}, nil
	}
	sortOpenClawBackfillEvents(events)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return OpenClawHistoryBackfillResult{}, fmt.Errorf("failed to begin openclaw history backfill transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	agentsBySlug, err := loadOpenClawBackfillAgents(ctx, tx, orgID, uniqueOpenClawEventSlugs(events))
	if err != nil {
		return OpenClawHistoryBackfillResult{}, err
	}
	userDisplayName, err := loadOpenClawBackfillUserDisplayName(ctx, tx, orgID, userID)
	if err != nil {
		return OpenClawHistoryBackfillResult{}, err
	}

	result := OpenClawHistoryBackfillResult{}
	roomByAgentSlug := make(map[string]string, len(agentsBySlug))

	agentSlugs := make([]string, 0, len(agentsBySlug))
	for slug := range agentsBySlug {
		agentSlugs = append(agentSlugs, slug)
	}
	sort.Strings(agentSlugs)
	for _, slug := range agentSlugs {
		agent := agentsBySlug[slug]
		roomID, roomCreated, participantsAdded, err := ensureOpenClawAgentHistoryRoom(
			ctx,
			tx,
			orgID,
			userID,
			userDisplayName,
			agent,
		)
		if err != nil {
			return OpenClawHistoryBackfillResult{}, err
		}
		roomByAgentSlug[slug] = roomID
		if roomCreated {
			result.RoomsCreated++
		}
		result.ParticipantsAdded += participantsAdded
	}

	for _, event := range events {
		agent, ok := agentsBySlug[event.AgentSlug]
		if !ok {
			return OpenClawHistoryBackfillResult{}, fmt.Errorf("no imported agent found for slug %q", event.AgentSlug)
		}
		roomID, ok := roomByAgentSlug[event.AgentSlug]
		if !ok || strings.TrimSpace(roomID) == "" {
			return OpenClawHistoryBackfillResult{}, fmt.Errorf("no room found for agent slug %q", event.AgentSlug)
		}

		senderID, senderType, messageType := mapOpenClawEventToMessageFields(event, userID, agent.ID)
		messageID := stableOpenClawBackfillMessageID(orgID, event)

		insertResult, err := tx.ExecContext(
			ctx,
			`INSERT INTO chat_messages (
				id,
				org_id,
				room_id,
				sender_id,
				sender_type,
				body,
				type,
				attachments,
				created_at
			) VALUES (
				$1,
				$2,
				$3,
				$4,
				$5,
				$6,
				$7,
				NULL,
				$8
			)
			ON CONFLICT (id) DO NOTHING`,
			messageID,
			orgID,
			roomID,
			senderID,
			senderType,
			event.Body,
			messageType,
			event.CreatedAt.UTC(),
		)
		if err != nil {
			return OpenClawHistoryBackfillResult{}, fmt.Errorf("failed to insert chat message for agent %q: %w", event.AgentSlug, err)
		}

		rowsAffected, err := insertResult.RowsAffected()
		if err == nil && rowsAffected > 0 {
			result.MessagesInserted++
		}
		result.EventsProcessed++
	}

	if err := tx.Commit(); err != nil {
		return OpenClawHistoryBackfillResult{}, fmt.Errorf("failed to commit openclaw history backfill transaction: %w", err)
	}
	committed = true

	if opts.SummaryWriter != nil {
		_, _ = fmt.Fprintf(
			opts.SummaryWriter,
			"OpenClaw history backfill: rooms_created=%d participants_added=%d messages_inserted=%d events_processed=%d\n",
			result.RoomsCreated,
			result.ParticipantsAdded,
			result.MessagesInserted,
			result.EventsProcessed,
		)
	}

	return result, nil
}

func loadOpenClawBackfillAgents(
	ctx context.Context,
	tx *sql.Tx,
	orgID string,
	slugs []string,
) (map[string]openClawBackfillAgent, error) {
	if len(slugs) == 0 {
		return map[string]openClawBackfillAgent{}, nil
	}

	rows, err := tx.QueryContext(
		ctx,
		`SELECT id::text, slug, display_name
		   FROM agents
		  WHERE org_id = $1
		    AND slug = ANY($2)`,
		orgID,
		pq.Array(slugs),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load imported agents for history backfill: %w", err)
	}
	defer rows.Close()

	agentsBySlug := make(map[string]openClawBackfillAgent, len(slugs))
	for rows.Next() {
		var agent openClawBackfillAgent
		if err := rows.Scan(&agent.ID, &agent.Slug, &agent.DisplayName); err != nil {
			return nil, fmt.Errorf("failed to scan imported agent row: %w", err)
		}
		agent.Slug = strings.TrimSpace(agent.Slug)
		agentsBySlug[agent.Slug] = agent
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading imported agent rows: %w", err)
	}

	missing := make([]string, 0)
	for _, slug := range slugs {
		if _, ok := agentsBySlug[slug]; ok {
			continue
		}
		missing = append(missing, slug)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing imported agent(s) for slug(s): %s", strings.Join(missing, ", "))
	}

	return agentsBySlug, nil
}

func ensureOpenClawAgentHistoryRoom(
	ctx context.Context,
	tx *sql.Tx,
	orgID, userID, userDisplayName string,
	agent openClawBackfillAgent,
) (roomID string, roomCreated bool, participantsAdded int, err error) {
	err = tx.QueryRowContext(
		ctx,
		`SELECT id::text
		   FROM rooms
		  WHERE org_id = $1
		    AND type = 'ad_hoc'
		    AND context_id = $2
		  ORDER BY created_at ASC, id ASC
		  LIMIT 1`,
		orgID,
		agent.ID,
	).Scan(&roomID)
	if err != nil {
		if err != sql.ErrNoRows {
			return "", false, 0, fmt.Errorf("failed to lookup existing room for agent %q: %w", agent.Slug, err)
		}
		expectedName := firstNonEmpty(strings.TrimSpace(userDisplayName), "User") + " & " + firstNonEmpty(strings.TrimSpace(agent.DisplayName), agent.Slug)
		err = tx.QueryRowContext(
			ctx,
			`INSERT INTO rooms (org_id, name, type, context_id)
			 VALUES ($1, $2, 'ad_hoc', $3)
			 RETURNING id::text`,
			orgID,
			expectedName,
			agent.ID,
		).Scan(&roomID)
		if err != nil {
			return "", false, 0, fmt.Errorf("failed to create ad_hoc room for agent %q: %w", agent.Slug, err)
		}
		roomCreated = true
	}

	addUser, err := tx.ExecContext(
		ctx,
		`INSERT INTO room_participants (org_id, room_id, participant_id, participant_type)
		 VALUES ($1, $2, $3, 'user')
		 ON CONFLICT (room_id, participant_id) DO NOTHING`,
		orgID,
		roomID,
		userID,
	)
	if err != nil {
		return "", roomCreated, 0, fmt.Errorf("failed adding user participant to room %q: %w", roomID, err)
	}

	addAgent, err := tx.ExecContext(
		ctx,
		`INSERT INTO room_participants (org_id, room_id, participant_id, participant_type)
		 VALUES ($1, $2, $3, 'agent')
		 ON CONFLICT (room_id, participant_id) DO NOTHING`,
		orgID,
		roomID,
		agent.ID,
	)
	if err != nil {
		return "", roomCreated, 0, fmt.Errorf("failed adding agent participant to room %q: %w", roomID, err)
	}

	addedCount := 0
	if rows, rowErr := addUser.RowsAffected(); rowErr == nil {
		addedCount += int(rows)
	}
	if rows, rowErr := addAgent.RowsAffected(); rowErr == nil {
		addedCount += int(rows)
	}

	return roomID, roomCreated, addedCount, nil
}

func loadOpenClawBackfillUserDisplayName(
	ctx context.Context,
	tx *sql.Tx,
	orgID, userID string,
) (string, error) {
	var displayName sql.NullString
	err := tx.QueryRowContext(
		ctx,
		`SELECT display_name
		   FROM users
		  WHERE org_id = $1
		    AND id = $2`,
		orgID,
		userID,
	).Scan(&displayName)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("user %q not found in org %q", userID, orgID)
		}
		return "", fmt.Errorf("failed to load user display name for history backfill: %w", err)
	}
	return firstNonEmpty(strings.TrimSpace(displayName.String), "User"), nil
}

func mapOpenClawEventToMessageFields(event OpenClawSessionEvent, userID, agentID string) (senderID, senderType, messageType string) {
	switch event.Role {
	case OpenClawSessionEventRoleAssistant:
		return agentID, "agent", "message"
	case OpenClawSessionEventRoleToolResult:
		return agentID, "system", "system"
	default:
		return userID, "user", "message"
	}
}

func uniqueOpenClawEventSlugs(events []OpenClawSessionEvent) []string {
	seen := map[string]struct{}{}
	slugs := make([]string, 0)
	for _, event := range events {
		slug := strings.TrimSpace(event.AgentSlug)
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}

func sortOpenClawBackfillEvents(events []OpenClawSessionEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].CreatedAt.Before(events[j].CreatedAt)
		}
		if events[i].AgentSlug != events[j].AgentSlug {
			return events[i].AgentSlug < events[j].AgentSlug
		}
		if events[i].SessionPath != events[j].SessionPath {
			return events[i].SessionPath < events[j].SessionPath
		}
		return events[i].Line < events[j].Line
	})
}

func stableOpenClawBackfillMessageID(orgID string, event OpenClawSessionEvent) string {
	eventID := strings.TrimSpace(event.EventID)
	if eventID == "" {
		eventID = "line-" + strconv.Itoa(event.Line)
	}
	seed := strings.Join([]string{
		orgID,
		strings.TrimSpace(event.AgentSlug),
		strings.TrimSpace(event.SessionID),
		eventID,
		event.Role,
		event.CreatedAt.UTC().Format(time.RFC3339Nano),
		strings.TrimSpace(event.Body),
	}, "|")

	hash := md5.Sum([]byte(seed))
	hex := fmt.Sprintf("%x", hash)
	return fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		hex[0:8],
		hex[8:12],
		hex[12:16],
		hex[16:20],
		hex[20:32],
	)
}
