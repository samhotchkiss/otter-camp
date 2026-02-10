package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type initOptions struct {
	Mode string
}

func handleInit(args []string) {
	if err := runInitCommand(args, os.Stdin, os.Stdout); err != nil {
		die(err.Error())
	}
}

func runInitCommand(args []string, in io.Reader, out io.Writer) error {
	opts, err := parseInitOptions(args)
	if err != nil {
		return err
	}

	mode := opts.Mode
	if mode == "" {
		mode, err = promptInitMode(in, out)
		if err != nil {
			return err
		}
	}

	switch mode {
	case "local":
		fmt.Fprintln(out, "Local install selected.")
		fmt.Fprintln(out, "Preparing local onboarding steps...")
		return nil
	case "hosted":
		fmt.Fprintln(out, "Visit otter.camp/setup to get started.")
		return nil
	default:
		return errors.New("--mode must be local or hosted")
	}
}

func parseInitOptions(args []string) (initOptions, error) {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	mode := flags.String("mode", "", "onboarding mode (local|hosted)")
	if err := flags.Parse(args); err != nil {
		return initOptions{}, err
	}
	if len(flags.Args()) != 0 {
		return initOptions{}, errors.New("usage: otter init [--mode <local|hosted>]")
	}

	normalizedMode := strings.ToLower(strings.TrimSpace(*mode))
	if normalizedMode != "" && normalizedMode != "local" && normalizedMode != "hosted" {
		return initOptions{}, errors.New("--mode must be local or hosted")
	}

	return initOptions{Mode: normalizedMode}, nil
}

func promptInitMode(in io.Reader, out io.Writer) (string, error) {
	if in == nil {
		return "", errors.New("input reader is required")
	}

	reader := bufio.NewReader(in)
	for {
		fmt.Fprintln(out, "Welcome to Otter Camp!")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "How are you setting up?")
		fmt.Fprintln(out, "  [1] Local install (run everything on this machine)")
		fmt.Fprintln(out, "  [2] Hosted (connect to otter.camp)")
		fmt.Fprint(out, "> ")

		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}

		choice := strings.ToLower(strings.TrimSpace(line))
		switch choice {
		case "", "1", "local":
			return "local", nil
		case "2", "hosted":
			return "hosted", nil
		}

		if errors.Is(err, io.EOF) {
			return "", errors.New("init mode selection required")
		}
		fmt.Fprintln(out, "Please enter 1 for local or 2 for hosted.")
	}
}
