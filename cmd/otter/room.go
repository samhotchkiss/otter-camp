package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/ottercli"
)

type roomCommandClient interface {
	GetRoomStats(roomID string) (ottercli.RoomTokenStats, error)
}

type roomClientFactory func(orgOverride string) (roomCommandClient, error)

type roomStatsOptions struct {
	RoomID string
	Org    string
	JSON   bool
}

func handleRoom(args []string) {
	if err := runRoomCommand(args, newRoomCommandClient, os.Stdout); err != nil {
		die(err.Error())
	}
}

func runRoomCommand(args []string, factory roomClientFactory, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: otter room <stats> ...")
	}

	switch args[0] {
	case "stats":
		opts, err := parseRoomStatsOptions(args[1:])
		if err != nil {
			return err
		}
		client, err := factory(opts.Org)
		if err != nil {
			return err
		}
		return runRoomStats(client, opts, out)
	default:
		return errors.New("usage: otter room <stats> ...")
	}
}

func parseRoomStatsOptions(args []string) (roomStatsOptions, error) {
	var (
		roomID  string
		org     string
		jsonOut bool
	)

	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--json":
			jsonOut = true
		case arg == "--org":
			if i+1 >= len(args) {
				return roomStatsOptions{}, errors.New("usage: otter room stats <room-id> [--org <org-id>] [--json]")
			}
			i++
			org = strings.TrimSpace(args[i])
			if org == "" {
				return roomStatsOptions{}, errors.New("usage: otter room stats <room-id> [--org <org-id>] [--json]")
			}
		case strings.HasPrefix(arg, "--org="):
			org = strings.TrimSpace(strings.TrimPrefix(arg, "--org="))
			if org == "" {
				return roomStatsOptions{}, errors.New("usage: otter room stats <room-id> [--org <org-id>] [--json]")
			}
		case strings.HasPrefix(arg, "-"):
			return roomStatsOptions{}, errors.New("usage: otter room stats <room-id> [--org <org-id>] [--json]")
		default:
			if roomID != "" {
				return roomStatsOptions{}, errors.New("usage: otter room stats <room-id> [--org <org-id>] [--json]")
			}
			roomID = arg
		}
	}

	if roomID == "" {
		return roomStatsOptions{}, errors.New("usage: otter room stats <room-id> [--org <org-id>] [--json]")
	}

	return roomStatsOptions{
		RoomID: roomID,
		Org:    org,
		JSON:   jsonOut,
	}, nil
}

func runRoomStats(client roomCommandClient, opts roomStatsOptions, out io.Writer) error {
	stats, err := client.GetRoomStats(opts.RoomID)
	if err != nil {
		return err
	}
	if opts.JSON {
		printJSONTo(out, stats)
		return nil
	}

	roomLabel := strings.TrimSpace(stats.RoomName)
	if roomLabel == "" {
		roomLabel = stats.RoomID
	}

	fmt.Fprintf(out, "Room: %s\n", roomLabel)
	fmt.Fprintf(out, "Total tokens: %s\n", formatInt64WithCommas(stats.TotalTokens))
	fmt.Fprintf(out, "Conversations: %s\n", formatInt64WithCommas(int64(stats.ConversationCount)))
	fmt.Fprintf(out, "Avg tokens/conversation: %s\n", formatInt64WithCommas(stats.AvgTokensPerConversation))
	fmt.Fprintf(out, "Last 7 days: %s tokens\n", formatInt64WithCommas(stats.Last7DaysTokens))
	return nil
}

func newRoomCommandClient(orgOverride string) (roomCommandClient, error) {
	cfg, err := ottercli.LoadConfig()
	if err != nil {
		return nil, err
	}
	return ottercli.NewClient(cfg, orgOverride)
}

func formatInt64WithCommas(value int64) string {
	raw := strconv.FormatInt(value, 10)
	negative := strings.HasPrefix(raw, "-")
	if negative {
		raw = raw[1:]
	}
	if len(raw) <= 3 {
		if negative {
			return "-" + raw
		}
		return raw
	}

	var builder strings.Builder
	prefix := len(raw) % 3
	if prefix == 0 {
		prefix = 3
	}
	builder.WriteString(raw[:prefix])
	for i := prefix; i < len(raw); i += 3 {
		builder.WriteByte(',')
		builder.WriteString(raw[i : i+3])
	}
	if negative {
		return "-" + builder.String()
	}
	return builder.String()
}
