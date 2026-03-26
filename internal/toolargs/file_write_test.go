package toolargs

import "testing"

func TestNormalizeFileWriteDecodesRawJSONObject(t *testing.T) {
	normalized := Normalize("file.write", map[string]any{
		"_raw": `{"path":"docs/readme.md","content":"hello","encoding":"utf-8","create_dirs":true}`,
	})

	if got := normalized["path"]; got != "docs/readme.md" {
		t.Fatalf("path = %v, want docs/readme.md", got)
	}
	if got := normalized["content"]; got != "hello" {
		t.Fatalf("content = %v, want hello", got)
	}
	if got := normalized["encoding"]; got != "utf-8" {
		t.Fatalf("encoding = %v, want utf-8", got)
	}
	if got := normalized["create_dirs"]; got != true {
		t.Fatalf("create_dirs = %v, want true", got)
	}
	if _, exists := normalized["_raw"]; exists {
		t.Fatal("expected _raw to be removed after JSON decode")
	}
}

func TestNormalizeFileWriteRecoversMalformedRawArguments(t *testing.T) {
	normalized := Normalize("file.write", map[string]any{
		"_raw": `{"path":"templates/index.html","content":"<html>\n</html>","create_dirs":true`,
	})

	if got := normalized["path"]; got != "templates/index.html" {
		t.Fatalf("path = %v, want templates/index.html", got)
	}
	if got := normalized["content"]; got != "<html>\n</html>" {
		t.Fatalf("content = %v, want HTML payload", got)
	}
	if got := normalized["create_dirs"]; got != true {
		t.Fatalf("create_dirs = %v, want true", got)
	}
	if _, exists := normalized["_raw"]; exists {
		t.Fatal("expected _raw to be removed after normalization")
	}
}

func TestNormalizeFileWriteRecoversMalformedContentWithEmbeddedQuotes(t *testing.T) {
	normalized := Normalize("file.write", map[string]any{
		"_raw": "{\"path\":\"templates/home.html\",\"content\":\"<section class=\"hero\">\\n  <h1>Sam.blog</h1>\\n</section>\",\"create_dirs\":true}",
	})

	if got := normalized["path"]; got != "templates/home.html" {
		t.Fatalf("path = %v, want templates/home.html", got)
	}
	if got := normalized["content"]; got != "<section class=\"hero\">\n  <h1>Sam.blog</h1>\n</section>" {
		t.Fatalf("content = %q, want embedded quotes preserved", got)
	}
	if got := normalized["create_dirs"]; got != true {
		t.Fatalf("create_dirs = %v, want true", got)
	}
}

func TestNormalizeFileWriteRecoversContentAliasFromDecodedJSON(t *testing.T) {
	normalized := Normalize("file.write", map[string]any{
		"_raw": `{"path":"docs/manifest.md","body":"# Manifest\n","create_dirs":true}`,
	})

	if got := normalized["path"]; got != "docs/manifest.md" {
		t.Fatalf("path = %v, want docs/manifest.md", got)
	}
	if got := normalized["content"]; got != "# Manifest\n" {
		t.Fatalf("content = %q, want recovered body alias", got)
	}
	if got := normalized["create_dirs"]; got != true {
		t.Fatalf("create_dirs = %v, want true", got)
	}
}

func TestNormalizeFileWriteRecoversPathOnlyRawWithoutInventingContent(t *testing.T) {
	normalized := Normalize("file.write", map[string]any{
		"_raw": `{"path":"content/posts/stop-preparing-your-kids-for-jobs.md","create_dirs":true}`,
	})

	if got := normalized["path"]; got != "content/posts/stop-preparing-your-kids-for-jobs.md" {
		t.Fatalf("path = %v, want content/posts/stop-preparing-your-kids-for-jobs.md", got)
	}
	if got := normalized["create_dirs"]; got != true {
		t.Fatalf("create_dirs = %v, want true", got)
	}
	if _, exists := normalized["content"]; exists {
		t.Fatalf("unexpected recovered content: %+v", normalized)
	}
	if _, exists := normalized["_raw"]; exists {
		t.Fatal("expected _raw to be removed after normalization")
	}
}

func TestNormalizeFileWriteLeavesNilAndEmptyRawUntouched(t *testing.T) {
	t.Run("nil raw", func(t *testing.T) {
		normalized := Normalize("file.write", map[string]any{"_raw": nil})

		value, exists := normalized["_raw"]
		if !exists {
			t.Fatal("expected nil _raw to remain present")
		}
		if value != nil {
			t.Fatalf("_raw = %v, want nil", value)
		}
		if _, exists := normalized["path"]; exists {
			t.Fatalf("unexpected recovered path for nil _raw: %+v", normalized)
		}
	})

	t.Run("empty raw", func(t *testing.T) {
		normalized := Normalize("file.write", map[string]any{"_raw": "   "})

		if got := normalized["_raw"]; got != "   " {
			t.Fatalf("_raw = %v, want original empty string", got)
		}
		if _, exists := normalized["path"]; exists {
			t.Fatalf("unexpected recovered path for empty _raw: %+v", normalized)
		}
	})
}

func TestFileWriteAttemptFingerprintIgnoresRawEnvelopeOncePathAndContentRecovered(t *testing.T) {
	first := AttemptFingerprint("file.write", map[string]any{
		"_raw": `{"path":"docs/readme.md","content":"hello","create_dirs":true`,
	})
	second := AttemptFingerprint("file.write", map[string]any{
		"path":        "docs/readme.md",
		"content":     "hello",
		"create_dirs": true,
	})

	if first != second {
		t.Fatalf("fingerprint mismatch: %q != %q", first, second)
	}
}

func TestFileReadAttemptFingerprintTracksPath(t *testing.T) {
	first := AttemptFingerprint("file.read", map[string]any{
		"path": "scripts/validate-metrics-alerting.sh",
	})
	second := AttemptFingerprint("file_read", map[string]any{
		"path": "scripts/validate-metrics-alerting.sh",
	})
	third := AttemptFingerprint("file.read", map[string]any{
		"path": "scripts/other.sh",
	})

	if first != second {
		t.Fatalf("same path fingerprints differ: %q vs %q", first, second)
	}
	if first == third {
		t.Fatalf("different file.read paths should not share fingerprint: %q", first)
	}
}

func TestFileListAttemptFingerprintTracksPath(t *testing.T) {
	first := AttemptFingerprint("file.list", map[string]any{
		"path":      "scripts",
		"recursive": true,
	})
	second := AttemptFingerprint("file_list", map[string]any{
		"path":      "scripts",
		"recursive": true,
	})
	third := AttemptFingerprint("file.list", map[string]any{
		"path":      "config",
		"recursive": true,
	})

	if first != second {
		t.Fatalf("same path fingerprints differ: %q vs %q", first, second)
	}
	if first == third {
		t.Fatalf("different file.list paths should not share fingerprint: %q", first)
	}
}

func TestCanonicalToolNameNormalizesFileWriteAlias(t *testing.T) {
	if got := canonicalToolName("file_write"); got != "file.write" {
		t.Fatalf("canonicalToolName(file_write) = %q, want file.write", got)
	}
}

func TestCanonicalToolNameNormalizesFileReadAlias(t *testing.T) {
	if got := canonicalToolName("file_read"); got != "file.read" {
		t.Fatalf("canonicalToolName(file_read) = %q, want file.read", got)
	}
}

func TestCanonicalToolNameNormalizesFileListAlias(t *testing.T) {
	if got := canonicalToolName("file_list"); got != "file.list" {
		t.Fatalf("canonicalToolName(file_list) = %q, want file.list", got)
	}
}

func TestBoolFieldParsesStringValue(t *testing.T) {
	got, ok := boolField(map[string]any{"create_dirs": " true "}, "create_dirs")
	if !ok {
		t.Fatal("expected boolField to parse string boolean")
	}
	if !got {
		t.Fatalf("boolField = %t, want true", got)
	}
}

func TestCloneMapNilReturnsEmptyMap(t *testing.T) {
	cloned := cloneMap(nil)
	if cloned == nil {
		t.Fatal("cloneMap(nil) returned nil")
	}
	if len(cloned) != 0 {
		t.Fatalf("cloneMap(nil) len = %d, want 0", len(cloned))
	}
}
