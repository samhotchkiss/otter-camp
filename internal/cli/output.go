package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

const (
	OutputModeTable = "table"
	OutputModeJSON  = "json"
	OutputModeQuiet = "quiet"
)

type OutputFormatter struct {
	mode    string
	writer  io.Writer
	noColor bool
}

func NewOutputFormatter(mode string, writer io.Writer, noColor bool) (OutputFormatter, error) {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	if normalized == "" {
		normalized = OutputModeTable
	}
	switch normalized {
	case OutputModeTable, OutputModeJSON, OutputModeQuiet:
	default:
		return OutputFormatter{}, fmt.Errorf("invalid output mode %q", mode)
	}
	if writer == nil {
		writer = io.Discard
	}
	return OutputFormatter{mode: normalized, writer: writer, noColor: noColor}, nil
}

func (f OutputFormatter) Mode() string {
	return f.mode
}

func (f OutputFormatter) WriteJSON(value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f.writer, "%s\n", encoded)
	return err
}

func (f OutputFormatter) WriteQuiet(value string) error {
	_, err := fmt.Fprintln(f.writer, strings.TrimSpace(value))
	return err
}

func (f OutputFormatter) WriteTable(headers []string, rows [][]string) error {
	tw := tabwriter.NewWriter(f.writer, 0, 4, 2, ' ', 0)
	if len(headers) > 0 {
		formatted := make([]string, 0, len(headers))
		for _, header := range headers {
			trimmed := strings.TrimSpace(header)
			if trimmed == "" {
				formatted = append(formatted, "")
				continue
			}
			if f.noColor {
				formatted = append(formatted, trimmed)
				continue
			}
			formatted = append(formatted, "\033[1m"+trimmed+"\033[0m")
		}
		if _, err := fmt.Fprintln(tw, strings.Join(formatted, "\t")); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}
