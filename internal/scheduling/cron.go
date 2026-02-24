package scheduling

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// CronSchedule is the minimal interface used by scheduling components.
type CronSchedule interface {
	Next(t time.Time) time.Time
}

// CronParser validates and parses 5-field cron expressions.
type CronParser struct {
	parser cron.Parser
}

func NewCronParser() *CronParser {
	return &CronParser{
		parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

func (p *CronParser) ParseExpression(expr string) (CronSchedule, error) {
	if p == nil {
		p = NewCronParser()
	}

	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return nil, fmt.Errorf("cron expression is required")
	}
	if strings.HasPrefix(trimmed, "@") {
		return nil, fmt.Errorf("cron aliases are not supported: %s", trimmed)
	}

	fields := strings.Fields(trimmed)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must contain exactly 5 fields, got %d", len(fields))
	}

	schedule, err := p.parser.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	return schedule, nil
}

func (p *CronParser) ValidateExpression(expr string) error {
	_, err := p.ParseExpression(expr)
	return err
}
