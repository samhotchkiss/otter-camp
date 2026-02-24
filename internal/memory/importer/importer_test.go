package importer

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateRecordJSON(t *testing.T) {
	t.Run("missing content rejected", func(t *testing.T) {
		raw := []byte(`{"memory_type":"semantic"}`)
		if _, err := validateRecordJSON(raw); err == nil {
			t.Fatal("validateRecordJSON error = nil, want error")
		}
	})

	t.Run("invalid memory_type rejected", func(t *testing.T) {
		raw := []byte(`{"content":"hello","memory_type":"invalid"}`)
		if _, err := validateRecordJSON(raw); err == nil {
			t.Fatal("validateRecordJSON error = nil, want error")
		}
	})

	t.Run("trust_tier capped at 0.6", func(t *testing.T) {
		raw := []byte(`{"content":"hello","memory_type":"semantic","trust_tier":0.9}`)
		record, err := validateRecordJSON(raw)
		if err != nil {
			t.Fatalf("validateRecordJSON: %v", err)
		}
		if record.TrustTier == nil || *record.TrustTier != 0.6 {
			t.Fatalf("trust_tier = %v, want 0.6", record.TrustTier)
		}
	})
}

func TestValidateUncompressedSize(t *testing.T) {
	if err := validateUncompressedSize(51 * 1024 * 1024); err == nil {
		t.Fatal("validateUncompressedSize(51MB) error = nil, want error")
	}
	if err := validateUncompressedSize(49 * 1024 * 1024); err != nil {
		t.Fatalf("validateUncompressedSize(49MB): %v", err)
	}
}

func TestParseAndValidateArchiveCountsRejected(t *testing.T) {
	archive := buildZipArchive(t, map[string]string{
		"import.jsonl": strings.Join([]string{
			`{"content":"valid","memory_type":"semantic"}`,
			`{"memory_type":"semantic"}`,
			`{"content":"also valid","memory_type":"episodic"}`,
		}, "\n"),
	})

	valid, rejected, total, err := parseAndValidateArchive(archive)
	if err != nil {
		t.Fatalf("parseAndValidateArchive: %v", err)
	}
	if total != 3 {
		t.Fatalf("total records = %d, want 3", total)
	}
	if rejected != 1 {
		t.Fatalf("rejected records = %d, want 1", rejected)
	}
	if len(valid) != 2 {
		t.Fatalf("valid records = %d, want 2", len(valid))
	}
}

func buildZipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()

	buf := bytes.NewBuffer(nil)
	zw := zip.NewWriter(buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

var _ = json.Valid
