package scheduling

import (
	"testing"
	"time"
)

func TestCronParserParseExpression(t *testing.T) {
	parser := NewCronParser()

	schedule, err := parser.ParseExpression("*/5 * * * *")
	if err != nil {
		t.Fatalf("ParseExpression valid: %v", err)
	}

	now := time.Date(2026, time.February, 24, 10, 2, 0, 0, time.UTC)
	next := schedule.Next(now)
	want := time.Date(2026, time.February, 24, 10, 5, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
}

func TestCronParserParseExpressionRejectsUnsupportedForms(t *testing.T) {
	parser := NewCronParser()

	tests := []struct {
		name string
		expr string
	}{
		{name: "six fields", expr: "* * * * * *"},
		{name: "empty", expr: ""},
		{name: "alias", expr: "@daily"},
		{name: "invalid", expr: "invalid"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := parser.ValidateExpression(tc.expr); err == nil {
				t.Fatalf("ValidateExpression(%q) expected error", tc.expr)
			}
		})
	}
}
