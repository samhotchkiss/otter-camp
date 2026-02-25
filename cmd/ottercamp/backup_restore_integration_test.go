//go:build integration

package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupAndRestoreCommands(t *testing.T) {
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("MkdirAll fake bin: %v", err)
	}

	pgDumpScript := filepath.Join(fakeBin, "pg_dump")
	if err := os.WriteFile(pgDumpScript, []byte(`#!/bin/sh
set -eu
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--file" ]; then
    out="$2"
    shift 2
    continue
  fi
  shift
done
printf 'CREATE TABLE backup_probe(id int);\nINSERT INTO backup_probe VALUES (1);\n' > "$out"
`), 0o755); err != nil {
		t.Fatalf("WriteFile pg_dump script: %v", err)
	}

	psqlScript := filepath.Join(fakeBin, "psql")
	if err := os.WriteFile(psqlScript, []byte(`#!/bin/sh
set -eu
capture="${PSQL_CAPTURE_PATH:-}"
file=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-f" ]; then
    file="$2"
    shift 2
    continue
  fi
  shift
done
if [ -n "$capture" ]; then
  cp "$file" "$capture"
fi
`), 0o755); err != nil {
		t.Fatalf("WriteFile psql script: %v", err)
	}

	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OTTERCAMP_DATABASE_URL", "postgres://example/testdb")

	objectsRoot := filepath.Join(tmp, "objects")
	if err := os.MkdirAll(filepath.Join(objectsRoot, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll objects root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(objectsRoot, "nested", "sample.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile object sample: %v", err)
	}
	t.Setenv("OTTERCAMP_STORAGE_BACKEND", "fs")
	t.Setenv("OTTERCAMP_STORAGE_ROOT", objectsRoot)

	archivePath := filepath.Join(tmp, "backup.tar.gz")
	code, _, stderr := captureCommandOutput(t, func() int {
		return runBackupCreate([]string{"--output", archivePath, "--include-objects"})
	})
	if code != 0 {
		t.Fatalf("runBackupCreate exit=%d stderr=%q", code, stderr)
	}

	entries := readArchiveEntries(t, archivePath)
	if _, ok := entries["ottercamp_backup.sql"]; !ok {
		t.Fatalf("archive missing sql dump: %+v", entries)
	}
	if _, ok := entries["objects/nested/sample.txt"]; !ok {
		t.Fatalf("archive missing objects payload: %+v", entries)
	}

	if err := os.RemoveAll(objectsRoot); err != nil {
		t.Fatalf("RemoveAll objects root: %v", err)
	}
	if err := os.MkdirAll(objectsRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll objects root restore: %v", err)
	}

	capturePath := filepath.Join(tmp, "restored.sql")
	t.Setenv("PSQL_CAPTURE_PATH", capturePath)
	code, _, stderr = captureCommandOutput(t, func() int {
		return runBackupRestore([]string{"--input", archivePath, "--force"})
	})
	if code != 0 {
		t.Fatalf("runBackupRestore exit=%d stderr=%q", code, stderr)
	}

	sqlData, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("ReadFile capture sql: %v", err)
	}
	if !strings.Contains(string(sqlData), "backup_probe") {
		t.Fatalf("unexpected restored sql payload: %q", string(sqlData))
	}

	restoredObject, err := os.ReadFile(filepath.Join(objectsRoot, "nested", "sample.txt"))
	if err != nil {
		t.Fatalf("ReadFile restored object: %v", err)
	}
	if string(restoredObject) != "hello" {
		t.Fatalf("restored object contents = %q, want %q", restoredObject, "hello")
	}
}

func readArchiveEntries(t *testing.T, archivePath string) map[string][]byte {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("Open archive: %v", err)
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("NewReader gzip: %v", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	entries := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if h.FileInfo().IsDir() {
			continue
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read entry %s: %v", h.Name, err)
		}
		entries[h.Name] = content
	}
	return entries
}
