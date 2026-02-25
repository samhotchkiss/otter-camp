package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	tuiapp "github.com/samhotchkiss/otter-camp/internal/tui"
)

func runTUICommand(args []string) int {
	flags := flag.NewFlagSet("tui", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	help := flags.Bool("help", false, "show help for tui command")
	nonInteractive := flags.Bool("non-interactive", false, "validate startup path and exit without opening UI")

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

	if *nonInteractive {
		if err := tuiapp.SaveState(statePath, state); err != nil {
			fmt.Fprintf(os.Stderr, "tui state save error: %v\n", err)
			return 1
		}
		return 0
	}

	program := tea.NewProgram(tuiapp.NewModel(state), tea.WithAltScreen())
	finalModel, err := program.Run()
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

func printTUIUsage(w *os.File) {
	fmt.Fprintln(w, "usage: ottercamp tui [--non-interactive]")
}
