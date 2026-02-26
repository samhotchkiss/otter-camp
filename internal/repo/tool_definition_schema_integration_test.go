//go:build integration

package repo

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestEnabledToolDefinitionsHaveSchemaProperties(t *testing.T) {
	pool := testdb.New(t)

	var missing int
	err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM tool_definition
		WHERE is_enabled = true
		  AND NOT (input_schema ? 'properties')
	`).Scan(&missing)
	if err != nil {
		t.Fatalf("count schemas missing properties: %v", err)
	}
	if missing != 0 {
		t.Fatalf("enabled tool schemas missing properties = %d, want 0", missing)
	}
}

func TestKeyToolSchemasExposeRequiredParameters(t *testing.T) {
	pool := testdb.New(t)

	type schemaCheck struct {
		name         string
		requiredKeys []string
		propertyKeys []string
	}

	checks := []schemaCheck{
		{name: "file.read", requiredKeys: []string{"path"}, propertyKeys: []string{"path", "encoding", "max_bytes"}},
		{name: "memory.query", requiredKeys: []string{"query"}, propertyKeys: []string{"query", "scope", "limit"}},
		{name: "task.create", requiredKeys: []string{"project_id", "title"}, propertyKeys: []string{"project_id", "title", "description"}},
		{name: "file.write", requiredKeys: []string{"path", "content"}, propertyKeys: []string{"path", "content"}},
		{name: "git.diff", propertyKeys: []string{"ref", "staged", "file_path"}},
		{name: "browser.navigate", requiredKeys: []string{"url"}, propertyKeys: []string{"url"}},
	}

	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			var raw json.RawMessage
			err := pool.QueryRow(context.Background(), `
				SELECT input_schema
				FROM tool_definition
				WHERE name = $1
			`, check.name).Scan(&raw)
			if err != nil {
				t.Fatalf("load schema: %v", err)
			}

			var schema map[string]any
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatalf("unmarshal schema: %v", err)
			}

			properties, ok := schema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("schema.properties type = %T, want map", schema["properties"])
			}
			for _, key := range check.propertyKeys {
				if _, exists := properties[key]; !exists {
					t.Fatalf("schema missing property %q", key)
				}
			}

			required := map[string]struct{}{}
			if rawRequired, ok := schema["required"].([]any); ok {
				for _, item := range rawRequired {
					if text, ok := item.(string); ok {
						required[text] = struct{}{}
					}
				}
			}
			for _, key := range check.requiredKeys {
				if _, exists := required[key]; !exists {
					t.Fatalf("schema.required missing %q", key)
				}
			}
		})
	}
}
