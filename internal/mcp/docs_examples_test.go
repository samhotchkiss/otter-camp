package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDocsReferencedExamplesCompile(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("unable to resolve test file path")
	}
	docPath := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "docs", "MCP.md"))

	docBytes, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read docs file %s: %v", docPath, err)
	}

	jsonBlocks := markdownJSONBlocks(string(docBytes))
	if len(jsonBlocks) == 0 {
		t.Fatalf("expected at least one JSON code block in %s", docPath)
	}

	allowedMethods := map[string]struct{}{
		"initialize":            {},
		"tools/list":            {},
		"tools/call":            {},
		"resources/list":        {},
		"resources/read":        {},
		"resources/subscribe":   {},
		"resources/unsubscribe": {},
		"prompts/list":          {},
		"prompts/get":           {},
	}

	foundMethodExample := false
	for i, block := range jsonBlocks {
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(block), &payload); err != nil {
			t.Fatalf("json block %d invalid: %v\n%s", i+1, err, block)
		}
		methodRaw, hasMethod := payload["method"]
		if !hasMethod {
			continue
		}
		method, _ := methodRaw.(string)
		if method == "" {
			t.Fatalf("json block %d contains empty method", i+1)
		}
		if _, ok := allowedMethods[method]; !ok {
			t.Fatalf("json block %d contains unsupported method %q", i+1, method)
		}
		foundMethodExample = true

		if method == "tools/call" {
			params, _ := payload["params"].(map[string]interface{})
			name, _ := params["name"].(string)
			if strings.TrimSpace(name) == "" {
				t.Fatalf("json block %d tools/call example missing params.name", i+1)
			}
		}
	}

	if !foundMethodExample {
		t.Fatalf("expected at least one JSON-RPC method example in %s", docPath)
	}
}

func markdownJSONBlocks(markdown string) []string {
	lines := strings.Split(markdown, "\n")
	inBlock := false
	lang := ""
	current := make([]string, 0)
	blocks := make([]string, 0)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			fenceLang := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "```")))
			if !inBlock {
				inBlock = true
				lang = fenceLang
				current = current[:0]
				continue
			}

			if lang == "json" {
				block := strings.TrimSpace(strings.Join(current, "\n"))
				if block != "" {
					blocks = append(blocks, block)
				}
			}
			inBlock = false
			lang = ""
			current = current[:0]
			continue
		}
		if inBlock {
			current = append(current, line)
		}
	}

	return blocks
}
