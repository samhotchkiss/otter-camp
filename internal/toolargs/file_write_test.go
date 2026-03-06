package toolargs

import "testing"

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
