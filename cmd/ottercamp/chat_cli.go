package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	clitools "github.com/samhotchkiss/otter-camp/internal/cli"
)

const defaultChatWaitTimeout = 5 * time.Minute

var (
	newChatCommandClient = func() (chatCommandClient, error) {
		return newCLIAPIClient("", "")
	}
	chatNow = time.Now
)

type chatCommandClient interface {
	CreateChatSession(ctx context.Context, req chatCreateSessionRequest) (chatSessionEnvelope, error)
	SendChatMessage(ctx context.Context, sessionID uuid.UUID, content string) (chatMessageEnvelope, error)
	ListChatSessions(ctx context.Context, filter chatListSessionsFilter) (chatSessionListEnvelope, error)
	ListChatMessages(ctx context.Context, sessionID uuid.UUID, filter chatListMessagesFilter) (chatMessageListEnvelope, error)
	ListChatParticipants(ctx context.Context, sessionID uuid.UUID) ([]cliChatParticipant, error)
	CancelChatTurn(ctx context.Context, sessionID uuid.UUID) error
	OpenEventsStream(ctx context.Context, scopes string) (io.ReadCloser, error)
}

type chatCreateSessionRequest struct {
	ScopeType string  `json:"scope_type"`
	ScopeID   string  `json:"scope_id"`
	Mode      string  `json:"mode"`
	Title     *string `json:"title,omitempty"`
}

type chatListSessionsFilter struct {
	Status    string
	ScopeType string
	ScopeID   string
	Limit     int
}

type chatListMessagesFilter struct {
	Limit          int
	BeforeSequence *int64
}

type chatSessionEnvelope struct {
	Data cliChatSession `json:"data"`
	Meta any            `json:"meta"`
}

type chatSessionListEnvelope struct {
	Data []cliChatSession `json:"data"`
	Meta any              `json:"meta"`
}

type chatMessageEnvelope struct {
	Data cliChatMessage `json:"data"`
	Meta any            `json:"meta"`
}

type chatMessageListEnvelope struct {
	Data []cliChatMessage `json:"data"`
	Meta any              `json:"meta"`
}

type cliChatSession struct {
	ID            uuid.UUID  `json:"id"`
	ScopeType     string     `json:"scope_type"`
	ScopeID       uuid.UUID  `json:"scope_id"`
	Mode          string     `json:"mode"`
	Status        string     `json:"status"`
	Title         *string    `json:"title"`
	MessageCount  int        `json:"message_count"`
	LastMessageAt *time.Time `json:"last_message_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

type cliChatMessage struct {
	ID             uuid.UUID       `json:"id"`
	SessionID      uuid.UUID       `json:"session_id"`
	SequenceNumber int64           `json:"sequence_number"`
	AuthorType     *string         `json:"author_type"`
	AuthorID       *uuid.UUID      `json:"author_id"`
	Role           string          `json:"role"`
	Content        string          `json:"content"`
	Status         string          `json:"status"`
	IsRedacted     bool            `json:"is_redacted"`
	Metadata       json.RawMessage `json:"metadata"`
	CreatedAt      time.Time       `json:"created_at"`
}

type cliChatParticipant struct {
	ParticipantType string    `json:"participant_type"`
	ParticipantID   uuid.UUID `json:"participant_id"`
}

type realtimeEventEnvelope struct {
	Payload json.RawMessage `json:"payload"`
}

type cliAPIError struct {
	Method     string
	Path       string
	StatusCode int
	Code       string
	Message    string
	Body       string
}

func (e *cliAPIError) Error() string {
	if e == nil {
		return "api error"
	}
	if strings.TrimSpace(e.Code) != "" {
		return fmt.Sprintf("api %s %s returned %d (%s): %s", e.Method, e.Path, e.StatusCode, e.Code, strings.TrimSpace(e.Message))
	}
	if strings.TrimSpace(e.Message) != "" {
		return fmt.Sprintf("api %s %s returned %d: %s", e.Method, e.Path, e.StatusCode, strings.TrimSpace(e.Message))
	}
	return fmt.Sprintf("api %s %s returned %d: %s", e.Method, e.Path, e.StatusCode, strings.TrimSpace(e.Body))
}

func runChatCommandImpl(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ottercamp chat <start|send|list|history> [flags]")
		return 1
	}

	switch args[0] {
	case "start":
		return runChatStart(args[1:])
	case "send":
		return runChatSend(args[1:])
	case "list":
		return runChatList(args[1:])
	case "history":
		return runChatHistory(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown chat command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "usage: ottercamp chat <start|send|list|history> [flags]")
		return 1
	}
}

func runChatStart(args []string) int {
	flags := flag.NewFlagSet("chat start", flag.ContinueOnError)
	scopeType := flags.String("scope-type", "organization", "scope type: organization|project|project_task")
	scopeIDRaw := flags.String("scope-id", "", "scope id")
	mode := flags.String("mode", "sync", "session mode: sync|async")
	title := flags.String("title", "", "optional title")
	interactive := flags.Bool("interactive", false, "start interactive mode")
	flags.BoolVar(interactive, "i", false, "start interactive mode")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "chat start argument error: %v\n", err)
		return 1
	}

	normalizedScopeType := strings.ToLower(strings.TrimSpace(*scopeType))
	if normalizedScopeType == "" {
		normalizedScopeType = "organization"
	}
	if normalizedScopeType != "organization" && normalizedScopeType != "project" && normalizedScopeType != "project_task" {
		fmt.Fprintln(os.Stderr, "chat start requires --scope-type to be organization, project, or project_task")
		return 1
	}

	normalizedMode := strings.ToLower(strings.TrimSpace(*mode))
	if normalizedMode != "sync" && normalizedMode != "async" {
		fmt.Fprintln(os.Stderr, "chat start requires --mode to be sync or async")
		return 1
	}
	if *interactive && normalizedMode != "sync" {
		fmt.Fprintln(os.Stderr, "Interactive mode requires --mode=sync")
		return 1
	}

	formatter, err := clitools.NewOutputFormatter(defaultOutputMode, os.Stdout, defaultNoColor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat start output error: %v\n", err)
		return 1
	}
	if *interactive && formatter.Mode() != clitools.OutputModeTable {
		fmt.Fprintln(os.Stderr, "Interactive mode requires --output=table")
		return 1
	}

	scopeID, err := resolveChatScopeID(normalizedScopeType, strings.TrimSpace(*scopeIDRaw))
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat start scope error: %v\n", err)
		return 1
	}

	client, err := newChatCommandClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat start setup error: %v\n", err)
		return 1
	}

	var titlePtr *string
	if strings.TrimSpace(*title) != "" {
		value := strings.TrimSpace(*title)
		titlePtr = &value
	}

	result, err := client.CreateChatSession(context.Background(), chatCreateSessionRequest{
		ScopeType: normalizedScopeType,
		ScopeID:   scopeID.String(),
		Mode:      normalizedMode,
		Title:     titlePtr,
	})
	if err != nil {
		if apiErr, ok := asCLIAPIError(err); ok && apiErr.StatusCode == http.StatusConflict && apiErr.Code == "active_sync_session_exists" {
			fmt.Fprintln(os.Stderr, "Error: An active sync session already exists for this scope. Use 'ottercamp chat list' to find it, or use --mode=async.")
			return 1
		}
		fmt.Fprintf(os.Stderr, "chat start failed: %v\n", err)
		return 1
	}

	switch formatter.Mode() {
	case clitools.OutputModeJSON:
		if err := formatter.WriteJSON(result); err != nil {
			fmt.Fprintf(os.Stderr, "chat start output error: %v\n", err)
			return 1
		}
	case clitools.OutputModeQuiet:
		if err := formatter.WriteQuiet(result.Data.ID.String()); err != nil {
			fmt.Fprintf(os.Stderr, "chat start output error: %v\n", err)
			return 1
		}
	default:
		scopeDisplay := "org"
		if result.Data.ScopeType != "organization" {
			scopeDisplay = fmt.Sprintf("%s/%s", result.Data.ScopeType, result.Data.ScopeID)
		}
		titleDisplay := "(none)"
		if result.Data.Title != nil && strings.TrimSpace(*result.Data.Title) != "" {
			titleDisplay = strings.TrimSpace(*result.Data.Title)
		}
		fmt.Fprintln(os.Stdout, "Session created")
		fmt.Fprintf(os.Stdout, "ID:    %s\n", result.Data.ID)
		fmt.Fprintf(os.Stdout, "Scope: %s\n", scopeDisplay)
		fmt.Fprintf(os.Stdout, "Mode:  %s\n", result.Data.Mode)
		fmt.Fprintf(os.Stdout, "Title: %s\n", titleDisplay)
	}

	if !*interactive {
		return 0
	}

	return runChatInteractive(result.Data.ID, client)
}

func runChatSend(args []string) int {
	flags := flag.NewFlagSet("chat send", flag.ContinueOnError)
	wait := flags.Bool("wait", false, "wait for turn completion")
	timeout := flags.Duration("timeout", defaultChatWaitTimeout, "wait timeout")
	normalizedArgs, err := normalizeInterspersedChatArgs(args, map[string]struct{}{
		"--timeout": {},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat send argument error: %v\n", err)
		return 1
	}
	if err := flags.Parse(normalizedArgs); err != nil {
		fmt.Fprintf(os.Stderr, "chat send argument error: %v\n", err)
		return 1
	}
	if flags.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "chat send requires <session-id>")
		return 1
	}

	sessionID, err := uuid.Parse(strings.TrimSpace(flags.Arg(0)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat send session id error: %v\n", err)
		return 1
	}

	message, err := resolveChatMessage(flags.Args()[1:], os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat send message error: %v\n", err)
		return 1
	}

	formatter, err := clitools.NewOutputFormatter(defaultOutputMode, os.Stdout, defaultNoColor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat send output error: %v\n", err)
		return 1
	}

	client, err := newChatCommandClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat send setup error: %v\n", err)
		return 1
	}

	sent, err := client.SendChatMessage(context.Background(), sessionID, message)
	if err != nil {
		if apiErr, ok := asCLIAPIError(err); ok && apiErr.StatusCode == http.StatusNotFound {
			fmt.Fprintf(os.Stderr, "Error: Session %s not found.\n", sessionID)
			return 1
		}
		fmt.Fprintf(os.Stderr, "chat send failed: %v\n", err)
		return 1
	}

	if !*wait {
		switch formatter.Mode() {
		case clitools.OutputModeJSON:
			if err := formatter.WriteJSON(sent); err != nil {
				fmt.Fprintf(os.Stderr, "chat send output error: %v\n", err)
				return 1
			}
		case clitools.OutputModeQuiet:
			if err := formatter.WriteQuiet(sent.Data.ID.String()); err != nil {
				fmt.Fprintf(os.Stderr, "chat send output error: %v\n", err)
				return 1
			}
		default:
			fmt.Fprintln(os.Stdout, "Message sent")
			fmt.Fprintf(os.Stdout, "Message ID: %s\n", sent.Data.ID)
			fmt.Fprintf(os.Stdout, "Sequence:   %d\n", sent.Data.SequenceNumber)
			fmt.Fprintf(os.Stdout, "Status:     %s\n", sent.Data.Status)
		}
		return 0
	}

	started := chatNow()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if formatter.Mode() == clitools.OutputModeTable {
		fmt.Fprintln(os.Stdout, "Message sent. Waiting for response...")
		fmt.Fprintln(os.Stdout)
		fmt.Fprint(os.Stdout, "Agent: ")
	}

	responseText, err := waitForChatTurnCompletion(ctx, client, sessionID, func(delta string) {
		if formatter.Mode() == clitools.OutputModeTable {
			fmt.Fprint(os.Stdout, delta)
		}
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(os.Stderr, "Error: Timed out waiting for agent response after %s. The turn may still be in progress; use 'ottercamp chat history %s' to check.\n", timeout.String(), sessionID)
			return 1
		}
		fmt.Fprintf(os.Stderr, "chat send wait failed: %v\n", err)
		return 1
	}
	var responseMessage *cliChatMessage
	latest, latestErr := latestAssistantMessage(ctx, client, sessionID)
	if latestErr == nil {
		responseMessage = &latest
		if strings.TrimSpace(responseText) == "" {
			responseText = latest.Content
		}
	}

	switch formatter.Mode() {
	case clitools.OutputModeJSON:
		responsePayload := any(map[string]any{"content": strings.TrimSpace(responseText)})
		if responseMessage != nil {
			responsePayload = responseMessage
		}
		payload := map[string]any{
			"data": map[string]any{
				"sent_message":     sent.Data,
				"response_message": responsePayload,
			},
			"meta": sent.Meta,
		}
		if err := formatter.WriteJSON(payload); err != nil {
			fmt.Fprintf(os.Stderr, "chat send output error: %v\n", err)
			return 1
		}
	case clitools.OutputModeQuiet:
		if err := formatter.WriteQuiet(sent.Data.ID.String()); err != nil {
			fmt.Fprintf(os.Stderr, "chat send output error: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout)
		fmt.Fprintf(os.Stdout, "Turn completed in %.1fs\n", chatNow().Sub(started).Seconds())
	}

	return 0
}

func runChatList(args []string) int {
	flags := flag.NewFlagSet("chat list", flag.ContinueOnError)
	status := flags.String("status", "active", "status filter: active|closed|archived|all")
	scopeType := flags.String("scope-type", "", "scope type filter")
	scopeIDRaw := flags.String("scope-id", "", "scope id filter")
	limit := flags.Int("limit", 20, "max rows")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "chat list argument error: %v\n", err)
		return 1
	}

	filter := chatListSessionsFilter{
		Status:    strings.ToLower(strings.TrimSpace(*status)),
		ScopeType: strings.ToLower(strings.TrimSpace(*scopeType)),
		ScopeID:   strings.TrimSpace(*scopeIDRaw),
		Limit:     *limit,
	}
	if filter.Status == "" {
		filter.Status = "active"
	}
	if filter.Status != "active" && filter.Status != "closed" && filter.Status != "archived" && filter.Status != "all" {
		fmt.Fprintln(os.Stderr, "chat list requires --status to be active, closed, archived, or all")
		return 1
	}
	if filter.ScopeType != "" && filter.ScopeType != "organization" && filter.ScopeType != "project" && filter.ScopeType != "project_task" {
		fmt.Fprintln(os.Stderr, "chat list requires --scope-type to be organization, project, or project_task")
		return 1
	}
	if filter.ScopeID != "" {
		if _, err := uuid.Parse(filter.ScopeID); err != nil {
			fmt.Fprintf(os.Stderr, "chat list scope-id error: %v\n", err)
			return 1
		}
	}
	if filter.Limit <= 0 {
		filter.Limit = 20
	}

	client, err := newChatCommandClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat list setup error: %v\n", err)
		return 1
	}
	result, err := client.ListChatSessions(context.Background(), filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat list failed: %v\n", err)
		return 1
	}

	formatter, err := clitools.NewOutputFormatter(defaultOutputMode, os.Stdout, defaultNoColor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat list output error: %v\n", err)
		return 1
	}

	switch formatter.Mode() {
	case clitools.OutputModeJSON:
		if err := formatter.WriteJSON(result); err != nil {
			fmt.Fprintf(os.Stderr, "chat list output error: %v\n", err)
			return 1
		}
	case clitools.OutputModeQuiet:
		for _, item := range result.Data {
			if err := formatter.WriteQuiet(item.ID.String()); err != nil {
				fmt.Fprintf(os.Stderr, "chat list output error: %v\n", err)
				return 1
			}
		}
	default:
		rows := make([][]string, 0, len(result.Data))
		now := chatNow()
		for _, item := range result.Data {
			scope := "org"
			if item.ScopeType != "organization" {
				scope = fmt.Sprintf("%s/%s", item.ScopeType, item.ScopeID)
			}
			last := item.LastMessageAt
			if last == nil {
				last = &item.CreatedAt
			}
			rows = append(rows, []string{
				item.ID.String(),
				scope,
				item.Mode,
				item.Status,
				strconv.Itoa(item.MessageCount),
				relativeTime(*last, now),
			})
		}
		if err := formatter.WriteTable([]string{"id", "scope", "mode", "status", "messages", "last activity"}, rows); err != nil {
			fmt.Fprintf(os.Stderr, "chat list output error: %v\n", err)
			return 1
		}
	}

	return 0
}

func runChatHistory(args []string) int {
	flags := flag.NewFlagSet("chat history", flag.ContinueOnError)
	limit := flags.Int("limit", 50, "max messages")
	beforeSequence := flags.Int64("before-sequence", 0, "messages before sequence")
	format := flags.String("format", "compact", "compact|full")
	roles := flags.String("roles", "", "comma-separated role filter")
	normalizedArgs, err := normalizeInterspersedChatArgs(args, map[string]struct{}{
		"--limit":           {},
		"--before-sequence": {},
		"--format":          {},
		"--roles":           {},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat history argument error: %v\n", err)
		return 1
	}
	if err := flags.Parse(normalizedArgs); err != nil {
		fmt.Fprintf(os.Stderr, "chat history argument error: %v\n", err)
		return 1
	}
	if flags.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "chat history requires <session-id>")
		return 1
	}

	sessionID, err := uuid.Parse(strings.TrimSpace(flags.Arg(0)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat history session id error: %v\n", err)
		return 1
	}
	if *limit <= 0 {
		*limit = 50
	}
	displayFormat := strings.ToLower(strings.TrimSpace(*format))
	if displayFormat != "compact" && displayFormat != "full" {
		fmt.Fprintln(os.Stderr, "chat history requires --format to be compact or full")
		return 1
	}

	client, err := newChatCommandClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat history setup error: %v\n", err)
		return 1
	}

	filter := chatListMessagesFilter{Limit: *limit}
	if *beforeSequence > 0 {
		filter.BeforeSequence = beforeSequence
	}
	result, err := client.ListChatMessages(context.Background(), sessionID, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat history failed: %v\n", err)
		return 1
	}

	roleFilter := parseRoleFilter(*roles)
	items := make([]cliChatMessage, 0, len(result.Data))
	for _, item := range result.Data {
		if len(roleFilter) > 0 {
			if _, ok := roleFilter[strings.ToLower(strings.TrimSpace(item.Role))]; !ok {
				continue
			}
		}
		items = append(items, item)
	}

	participants, err := client.ListChatParticipants(context.Background(), sessionID)
	if err != nil {
		participants = nil
	}
	participantNames := participantNameMap(participants)

	formatter, err := clitools.NewOutputFormatter(defaultOutputMode, os.Stdout, defaultNoColor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chat history output error: %v\n", err)
		return 1
	}

	switch formatter.Mode() {
	case clitools.OutputModeJSON:
		payload := chatMessageListEnvelope{Data: items, Meta: result.Meta}
		if err := formatter.WriteJSON(payload); err != nil {
			fmt.Fprintf(os.Stderr, "chat history output error: %v\n", err)
			return 1
		}
	case clitools.OutputModeQuiet:
		for _, item := range items {
			if err := formatter.WriteQuiet(item.ID.String()); err != nil {
				fmt.Fprintf(os.Stderr, "chat history output error: %v\n", err)
				return 1
			}
		}
	default:
		if displayFormat == "full" {
			for _, item := range items {
				fmt.Fprintf(os.Stdout, "#%d %s %s\n", item.SequenceNumber, item.Role, authorLabel(item, participantNames))
				fmt.Fprintf(os.Stdout, "status: %s\n", item.Status)
				fmt.Fprintf(os.Stdout, "content: %s\n\n", historyContent(item))
			}
			return 0
		}

		rows := make([][]string, 0, len(items))
		for _, item := range items {
			rows = append(rows, []string{
				strconv.FormatInt(item.SequenceNumber, 10),
				authorLabel(item, participantNames),
				item.Role,
				truncateForHistory(historyContent(item), 120),
			})
		}
		if err := formatter.WriteTable([]string{"#", "author", "role", "content"}, rows); err != nil {
			fmt.Fprintf(os.Stderr, "chat history output error: %v\n", err)
			return 1
		}
	}

	return 0
}

func runChatInteractive(sessionID uuid.UUID, client chatCommandClient) int {
	lines := make(chan string)
	errs := make(chan error, 1)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)

	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for {
			fmt.Fprint(os.Stdout, "You: ")
			if !scanner.Scan() {
				if scanner.Err() != nil {
					errs <- scanner.Err()
				}
				close(lines)
				return
			}
			lines <- scanner.Text()
		}
	}()

	for {
		select {
		case <-signals:
			_ = client.CancelChatTurn(context.Background(), sessionID)
			fmt.Fprintln(os.Stdout)
			return 0
		case err := <-errs:
			fmt.Fprintf(os.Stderr, "chat interactive input error: %v\n", err)
			return 1
		case line, ok := <-lines:
			if !ok {
				return 0
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			if _, err := client.SendChatMessage(context.Background(), sessionID, line); err != nil {
				fmt.Fprintf(os.Stderr, "chat interactive send failed: %v\n", err)
				return 1
			}

			fmt.Fprint(os.Stdout, "Agent: ")
			ctx, cancel := context.WithTimeout(context.Background(), defaultChatWaitTimeout)
			_, err := waitForChatTurnCompletion(ctx, client, sessionID, func(delta string) {
				fmt.Fprint(os.Stdout, delta)
			})
			cancel()
			fmt.Fprintln(os.Stdout)
			if err != nil && !errors.Is(err, context.DeadlineExceeded) {
				fmt.Fprintf(os.Stderr, "chat interactive wait failed: %v\n", err)
				return 1
			}
		}
	}
}

func waitForChatTurnCompletion(ctx context.Context, client chatCommandClient, sessionID uuid.UUID, onChunk func(string)) (string, error) {
	stream, err := client.OpenEventsStream(ctx, "session:"+sessionID.String())
	if err != nil {
		return "", err
	}
	defer stream.Close()

	reader := bufio.NewReader(stream)
	builder := strings.Builder{}

	for {
		eventName, _, data, err := readSSEFrame(reader)
		if err != nil {
			if ctx.Err() != nil {
				return builder.String(), ctx.Err()
			}
			return builder.String(), err
		}

		eventName = strings.TrimSpace(eventName)
		if eventName == "" || strings.TrimSpace(data) == "" {
			continue
		}

		var envelope realtimeEventEnvelope
		if err := json.Unmarshal([]byte(data), &envelope); err != nil {
			continue
		}
		payload := map[string]any{}
		if len(envelope.Payload) > 0 {
			_ = json.Unmarshal(envelope.Payload, &payload)
		}
		payloadSessionID, _ := payload["session_id"].(string)
		if strings.TrimSpace(payloadSessionID) != "" && payloadSessionID != sessionID.String() {
			continue
		}

		switch eventName {
		case "chat.message.chunk":
			delta, _ := payload["delta"].(string)
			if delta != "" {
				builder.WriteString(delta)
				if onChunk != nil {
					onChunk(delta)
				}
			}
		case "chat.turn.completed":
			return builder.String(), nil
		}
	}
}

func resolveChatScopeID(scopeType, scopeIDRaw string) (uuid.UUID, error) {
	switch scopeType {
	case "organization":
		if strings.TrimSpace(scopeIDRaw) == "" {
			return parseOrgID("")
		}
	case "project", "project_task":
		if strings.TrimSpace(scopeIDRaw) == "" {
			return uuid.Nil, fmt.Errorf("scope-id is required when --scope-type=%s", scopeType)
		}
	default:
		return uuid.Nil, fmt.Errorf("invalid scope type %q", scopeType)
	}
	return uuid.Parse(strings.TrimSpace(scopeIDRaw))
}

func resolveChatMessage(positional []string, stdin *os.File) (string, error) {
	if len(positional) > 0 {
		joined := strings.TrimSpace(strings.Join(positional, " "))
		if joined != "" {
			return joined, nil
		}
	}
	if stdin == nil {
		return "", fmt.Errorf("message is required")
	}
	info, err := stdin.Stat()
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", fmt.Errorf("message is required")
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", fmt.Errorf("message is required")
	}
	return text, nil
}

func readSSEFrame(reader *bufio.Reader) (eventName string, id string, data string, err error) {
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			if errors.Is(readErr, io.EOF) && line == "" {
				return "", "", "", io.EOF
			}
			if line == "" {
				return "", "", "", readErr
			}
		}

		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, ":") {
			if readErr != nil {
				return eventName, id, data, readErr
			}
			continue
		}
		if line == "" {
			return eventName, id, data, nil
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "id:") {
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		} else if strings.HasPrefix(line, "data:") {
			chunk := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				data = chunk
			} else {
				data += "\n" + chunk
			}
		}
		if readErr != nil {
			return eventName, id, data, readErr
		}
	}
}

func normalizeInterspersedChatArgs(args []string, flagsWithValues map[string]struct{}) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}

	normalized := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, args[i])
			continue
		}

		flagName := arg
		if idx := strings.Index(flagName, "="); idx >= 0 {
			flagName = flagName[:idx]
		}
		if _, ok := flagsWithValues[flagName]; ok && !strings.Contains(arg, "=") {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag needs an argument: %s", flagName)
			}
			normalized = append(normalized, arg, args[i+1])
			i++
			continue
		}
		normalized = append(normalized, arg)
	}
	normalized = append(normalized, positionals...)
	return normalized, nil
}

func latestAssistantMessage(ctx context.Context, client chatCommandClient, sessionID uuid.UUID) (cliChatMessage, error) {
	result, err := client.ListChatMessages(ctx, sessionID, chatListMessagesFilter{Limit: 100})
	if err != nil {
		return cliChatMessage{}, err
	}
	sort.SliceStable(result.Data, func(i, j int) bool {
		return result.Data[i].SequenceNumber < result.Data[j].SequenceNumber
	})
	for i := len(result.Data) - 1; i >= 0; i-- {
		item := result.Data[i]
		if strings.EqualFold(item.Role, "assistant") {
			return item, nil
		}
	}
	return cliChatMessage{}, fmt.Errorf("assistant response not found")
}

func parseRoleFilter(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.ToLower(strings.TrimSpace(part))
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	return out
}

func historyContent(message cliChatMessage) string {
	if message.IsRedacted || strings.EqualFold(strings.TrimSpace(message.Status), "redacted") {
		return "[redacted]"
	}
	return message.Content
}

func truncateForHistory(value string, max int) string {
	if max <= 0 {
		return value
	}
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func participantNameMap(participants []cliChatParticipant) map[uuid.UUID]string {
	out := make(map[uuid.UUID]string, len(participants))
	for _, item := range participants {
		label := item.ParticipantType + "/" + item.ParticipantID.String()
		out[item.ParticipantID] = label
	}
	return out
}

func authorLabel(message cliChatMessage, participants map[uuid.UUID]string) string {
	if message.AuthorID == nil || message.AuthorType == nil {
		return "(system)"
	}
	if label, ok := participants[*message.AuthorID]; ok {
		return label
	}
	return strings.TrimSpace(*message.AuthorType) + "/" + message.AuthorID.String()
}

func relativeTime(at time.Time, now time.Time) string {
	at = at.UTC()
	now = now.UTC()
	if at.After(now) {
		at = now
	}
	diff := now.Sub(at)
	if diff < time.Minute {
		return "just now"
	}
	if diff < time.Hour {
		minutes := int(diff.Round(time.Minute) / time.Minute)
		if minutes <= 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	}
	if diff < 24*time.Hour {
		hours := int(diff.Round(time.Hour) / time.Hour)
		if hours <= 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	if diff < 48*time.Hour {
		return "yesterday"
	}
	days := int(diff.Hours() / 24)
	return fmt.Sprintf("%d days ago", days)
}

func asCLIAPIError(err error) (*cliAPIError, bool) {
	var apiErr *cliAPIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

func (c *cliAPIClient) CreateChatSession(ctx context.Context, req chatCreateSessionRequest) (chatSessionEnvelope, error) {
	var response chatSessionEnvelope
	if err := c.request(ctx, http.MethodPost, "/v1/chat-sessions", req, &response); err != nil {
		return chatSessionEnvelope{}, err
	}
	return response, nil
}

func (c *cliAPIClient) SendChatMessage(ctx context.Context, sessionID uuid.UUID, content string) (chatMessageEnvelope, error) {
	path := fmt.Sprintf("/v1/chat-sessions/%s/messages", url.PathEscape(sessionID.String()))
	var response chatMessageEnvelope
	if err := c.request(ctx, http.MethodPost, path, map[string]any{"content": content}, &response); err != nil {
		return chatMessageEnvelope{}, err
	}
	return response, nil
}

func (c *cliAPIClient) ListChatSessions(ctx context.Context, filter chatListSessionsFilter) (chatSessionListEnvelope, error) {
	query := url.Values{}
	if filter.Status != "" && filter.Status != "all" {
		query.Set("status", filter.Status)
	}
	if filter.ScopeType != "" {
		query.Set("scope_type", filter.ScopeType)
	}
	if filter.ScopeID != "" {
		query.Set("scope_id", filter.ScopeID)
	}
	if filter.Limit > 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := "/v1/chat-sessions"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var response chatSessionListEnvelope
	if err := c.request(ctx, http.MethodGet, path, nil, &response); err != nil {
		return chatSessionListEnvelope{}, err
	}
	return response, nil
}

func (c *cliAPIClient) ListChatMessages(ctx context.Context, sessionID uuid.UUID, filter chatListMessagesFilter) (chatMessageListEnvelope, error) {
	query := url.Values{}
	if filter.Limit > 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}
	if filter.BeforeSequence != nil && *filter.BeforeSequence > 0 {
		query.Set("before_sequence", strconv.FormatInt(*filter.BeforeSequence, 10))
	}
	path := fmt.Sprintf("/v1/chat-sessions/%s/messages", url.PathEscape(sessionID.String()))
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var response chatMessageListEnvelope
	if err := c.request(ctx, http.MethodGet, path, nil, &response); err != nil {
		return chatMessageListEnvelope{}, err
	}
	return response, nil
}

func (c *cliAPIClient) ListChatParticipants(ctx context.Context, sessionID uuid.UUID) ([]cliChatParticipant, error) {
	path := fmt.Sprintf("/v1/chat-sessions/%s/participants", url.PathEscape(sessionID.String()))
	var response struct {
		Data []cliChatParticipant `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

func (c *cliAPIClient) CancelChatTurn(ctx context.Context, sessionID uuid.UUID) error {
	path := fmt.Sprintf("/v1/chat-sessions/%s/cancel-turn", url.PathEscape(sessionID.String()))
	return c.request(ctx, http.MethodPost, path, map[string]any{}, nil)
}

func (c *cliAPIClient) OpenEventsStream(ctx context.Context, scopes string) (io.ReadCloser, error) {
	path := c.baseURL + "/v1/events/stream?scopes=" + url.QueryEscape(strings.TrimSpace(scopes))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Accept", "text/event-stream")

	res, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return res.Body, nil
	}
	data, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	apiErr := parseCLIAPIError(http.MethodGet, "/v1/events/stream", res.StatusCode, data)
	return nil, apiErr
}

func parseCLIAPIError(method, path string, status int, body []byte) error {
	apiErr := &cliAPIError{
		Method:     method,
		Path:       path,
		StatusCode: status,
		Body:       strings.TrimSpace(string(body)),
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		apiErr.Code = strings.TrimSpace(envelope.Error.Code)
		apiErr.Message = strings.TrimSpace(envelope.Error.Message)
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(status)
	}
	return apiErr
}
