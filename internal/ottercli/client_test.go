package ottercli

import "testing"

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"My Project", "my-project"},
		{"sprint-42-docs", "sprint-42-docs"},
		{"  Hello World  ", "hello-world"},
		{"A/B Test!", "a-b-test"},
		{"", "project"},
		{"---", "project"},
		{"UPPER CASE", "upper-case"},
	}
	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestProjectSlug(t *testing.T) {
	p := Project{Name: "My Cool Project"}
	if got := p.Slug(); got != "my-cool-project" {
		t.Errorf("Slug() = %q, want %q", got, "my-cool-project")
	}
}
