package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	clitools "github.com/samhotchkiss/otter-camp/internal/cli"
	tuiapp "github.com/samhotchkiss/otter-camp/internal/tui"
)

func runTUICommand(args []string) int {
	flags := flag.NewFlagSet("tui", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	help := flags.Bool("help", false, "show help for tui command")
	nonInteractive := flags.Bool("non-interactive", false, "validate startup path and exit without opening UI")
	serverURLFlag := flags.String("server-url", "", "server URL (or OTTERCAMP_SERVER_URL)")
	apiKeyFlag := flags.String("api-key", "", "API key (or OTTERCAMP_API_KEY)")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printTUIUsage(os.Stdout)
			return 0
		}
		fmt.Fprintf(os.Stderr, "tui argument error: %v\n", err)
		printTUIUsage(os.Stderr)
		return 1
	}
	if *help {
		printTUIUsage(os.Stdout)
		return 0
	}
	if len(flags.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "tui does not accept positional arguments: %s\n", flags.Args()[0])
		printTUIUsage(os.Stderr)
		return 1
	}

	statePath, err := tuiapp.ResolveStatePath(os.Getenv, os.UserHomeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui state path error: %v\n", err)
		return 1
	}
	state, err := tuiapp.LoadState(statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui state load error: %v\n", err)
		return 1
	}
	stateExists, err := tuiapp.StateFileExists(statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui state stat error: %v\n", err)
		return 1
	}

	if *nonInteractive {
		if err := tuiapp.SaveState(statePath, state); err != nil {
			fmt.Fprintf(os.Stderr, "tui state save error: %v\n", err)
			return 1
		}
		return 0
	}

	serverURL, apiKey, err := resolveTUIRealtimeCredentials(*serverURLFlag, *apiKeyFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui realtime setup error: %v\n", err)
		return 1
	}

	runtimeHints := tuiapp.DetectRuntimeHints(os.Getenv)
	runtimeHints.FirstRun = !stateExists
	program := tea.NewProgram(tuiapp.NewModelWithRuntime(state, runtimeHints), tea.WithAltScreen())
	client := &tuiapp.RealtimeClient{
		Connector: tuiapp.HTTPSSEConnector{
			Client: &http.Client{},
			URL:    strings.TrimRight(serverURL, "/") + "/v1/events/stream",
			APIKey: apiKey,
			Scopes: "org",
		},
		Reducer: tuiapp.NewEventReducer(slog.New(slog.NewTextHandler(io.Discard, nil))),
		OnStateChange: func(state tuiapp.ConnectionState, degraded bool) {
			program.Send(tuiapp.ConnectionStateMsg{State: state, Degraded: degraded})
		},
		OnEvent: func(event tuiapp.EventEnvelope, applied bool) {
			if !applied {
				return
			}
			if strings.HasPrefix(event.EventType, "chat.") {
				program.Send(tuiapp.ChatEnvelopeMsg{Envelope: event})
				return
			}
			if strings.HasPrefix(event.EventType, "task.") {
				program.Send(tuiapp.WorkspaceEnvelopeMsg{Envelope: event})
			}
		},
		OnReplaySynced: func() {
			program.Send(tuiapp.ReplaySyncedMsg{})
		},
	}
	realtimeCtx, cancelRealtime := context.WithCancel(context.Background())
	defer cancelRealtime()
	go func() {
		_ = client.Run(realtimeCtx)
	}()

	finalModel, err := program.Run()
	cancelRealtime()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tui runtime error: %v\n", err)
		return 1
	}

	nextState := state
	switch typed := finalModel.(type) {
	case tuiapp.Model:
		nextState = typed.State()
	case *tuiapp.Model:
		nextState = typed.State()
	}
	if err := tuiapp.SaveState(statePath, nextState); err != nil {
		fmt.Fprintf(os.Stderr, "tui state save error: %v\n", err)
		return 1
	}

	return 0
}

func resolveTUIRealtimeCredentials(serverURLFlag, apiKeyFlag string) (string, string, error) {
	creds, err := credentialStore.Load()
	if err != nil {
		return "", "", err
	}

	serverURL := strings.TrimSpace(serverURLFlag)
	if serverURL == "" {
		serverURL = strings.TrimSpace(globalServerURL)
	}
	serverURL = clitools.ResolveServerURL(serverURL, creds)

	apiKey := strings.TrimSpace(apiKeyFlag)
	if apiKey == "" {
		apiKey = strings.TrimSpace(globalAPIKey)
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(creds.APIKey)
	}
	if apiKey == "" {
		return "", "", fmt.Errorf("api key required via --api-key, OTTERCAMP_API_KEY, or ~/.ottercamp/credentials")
	}

	return serverURL, apiKey, nil
}

func printTUIUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp tui [--server-url URL] [--api-key KEY] [--non-interactive]")
}
