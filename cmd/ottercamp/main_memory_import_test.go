package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/storage"
)

func TestMemoryImportCLIWait(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "import.zip")
	if err := os.WriteFile(filePath, []byte("zip-bytes"), 0o600); err != nil {
		t.Fatalf("write test import file: %v", err)
	}

	orgID := uuid.New()
	importID := uuid.New()
	store := &fakeMemoryImportStore{}
	statusClient := &fakeMemoryImportStatusClient{
		statuses: []cliMemoryImportStatus{
			{
				ID:               importID.String(),
				Status:           "processing",
				TotalRecords:     intPtr(50),
				ProcessedRecords: 10,
				ImportedRecords:  8,
				RejectedRecords:  2,
			},
			{
				ID:               importID.String(),
				Status:           "completed",
				TotalRecords:     intPtr(50),
				ProcessedRecords: 50,
				ImportedRecords:  42,
				RejectedRecords:  8,
			},
		},
	}

	var startCalls int
	restore := overrideMemoryImportDeps(
		t,
		func() (storage.Store, error) {
			return store, nil
		},
		func(_ context.Context, gotOrgID uuid.UUID, fileKey string, _ storage.Store) (uuid.UUID, error) {
			startCalls++
			if gotOrgID != orgID {
				return uuid.Nil, fmt.Errorf("unexpected org id: %s", gotOrgID)
			}
			if !strings.HasPrefix(fileKey, "imports/"+orgID.String()+"/") {
				return uuid.Nil, fmt.Errorf("unexpected file key prefix: %s", fileKey)
			}
			return importID, nil
		},
		func() (memoryImportStatusClient, error) {
			return statusClient, nil
		},
	)
	defer restore()

	exitCode, stdout, stderr := captureCommandOutput(t, func() int {
		return runMemoryImport([]string{
			"--file", filePath,
			"--org-id", orgID.String(),
			"--wait",
		})
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr)
	}
	if startCalls != 1 {
		t.Fatalf("start import calls = %d, want 1", startCalls)
	}
	if statusClient.calls < 2 {
		t.Fatalf("status client calls = %d, want >= 2", statusClient.calls)
	}
	if !strings.Contains(stdout, "status=completed") {
		t.Fatalf("stdout = %q, want completed status", stdout)
	}
	if !strings.Contains(stdout, "imported=42") {
		t.Fatalf("stdout = %q, want imported count", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func overrideMemoryImportDeps(
	t *testing.T,
	storeFactory func() (storage.Store, error),
	startFn memoryImportStartFunc,
	clientFactory func() (memoryImportStatusClient, error),
) func() {
	t.Helper()

	originalStore := newMemoryImportStore
	originalStart := startMemoryImport
	originalClient := newMemoryImportStatusClient
	originalPoll := memoryImportPollInterval
	originalSleep := memoryImportSleep

	newMemoryImportStore = storeFactory
	startMemoryImport = startFn
	newMemoryImportStatusClient = clientFactory
	memoryImportPollInterval = time.Millisecond
	memoryImportSleep = func(time.Duration) {}

	return func() {
		newMemoryImportStore = originalStore
		startMemoryImport = originalStart
		newMemoryImportStatusClient = originalClient
		memoryImportPollInterval = originalPoll
		memoryImportSleep = originalSleep
	}
}

func captureCommandOutput(t *testing.T, run func() int) (int, string, string) {
	t.Helper()

	originalStdout := os.Stdout
	originalStderr := os.Stderr

	stdoutFile, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatalf("create stdout temp file: %v", err)
	}
	stderrFile, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatalf("create stderr temp file: %v", err)
	}
	defer stdoutFile.Close()
	defer stderrFile.Close()

	os.Stdout = stdoutFile
	os.Stderr = stderrFile
	code := run()
	os.Stdout = originalStdout
	os.Stderr = originalStderr

	if _, err := stdoutFile.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek stdout temp file: %v", err)
	}
	if _, err := stderrFile.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek stderr temp file: %v", err)
	}
	stdoutBytes, err := io.ReadAll(stdoutFile)
	if err != nil {
		t.Fatalf("read stdout temp file: %v", err)
	}
	stderrBytes, err := io.ReadAll(stderrFile)
	if err != nil {
		t.Fatalf("read stderr temp file: %v", err)
	}
	return code, string(stdoutBytes), string(stderrBytes)
}

func intPtr(v int) *int {
	return &v
}

type fakeMemoryImportStore struct {
	lastKey string
	puts    int
}

func (s *fakeMemoryImportStore) Put(_ context.Context, key string, r io.Reader, _ storage.PutOptions) error {
	s.puts++
	s.lastKey = key
	_, err := io.Copy(io.Discard, r)
	return err
}

func (s *fakeMemoryImportStore) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (s *fakeMemoryImportStore) Delete(context.Context, string) error {
	return nil
}

func (s *fakeMemoryImportStore) Exists(context.Context, string) (bool, error) {
	return true, nil
}

func (s *fakeMemoryImportStore) List(context.Context, string) ([]storage.ObjectMeta, error) {
	return nil, nil
}

type fakeMemoryImportStatusClient struct {
	statuses []cliMemoryImportStatus
	calls    int
}

func (c *fakeMemoryImportStatusClient) GetMemoryImport(_ context.Context, _ string) (cliMemoryImportStatus, error) {
	if len(c.statuses) == 0 {
		return cliMemoryImportStatus{}, nil
	}
	index := c.calls
	if index >= len(c.statuses) {
		index = len(c.statuses) - 1
	}
	c.calls++
	return c.statuses[index], nil
}
