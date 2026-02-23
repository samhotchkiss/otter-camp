# 004: Object Storage Abstraction

| Field | Value |
|-------|-------|
| Layer | L0 |
| Size | S (≤1 day) |
| Spec refs | doc 08 §ObjectStorage, doc 06 §FileBackedMemory, doc 16 §RunArtifact |
| Spec status | finished |
| Depends on | 001 |
| Blocks | 009, 012, 036, 038, 041, 058, 059 |

## Scope

Build a unified object storage interface with two adapter implementations: a local filesystem
adapter (default for development and testing) and an S3-compatible adapter (for production).
This abstraction is used by chat artifacts, run artifacts, prompt/response capture, memory file
backing, backup/restore, and secret master key file loading.

### Must build

**`internal/storage/` package:**

`Store` interface:
```go
type Store interface {
    Put(ctx context.Context, key string, r io.Reader, opts PutOptions) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, key string) error
    Exists(ctx context.Context, key string) (bool, error)
    List(ctx context.Context, prefix string) ([]ObjectMeta, error)
}

type PutOptions struct {
    ContentType  string
    ContentLength int64  // -1 = unknown
}

type ObjectMeta struct {
    Key          string
    Size         int64
    LastModified time.Time
}
```

**Filesystem adapter** (`storage/fs.go`):
- Stores objects as files under a root directory
- Key is used as the relative file path (path traversal rejected: keys must not contain `..` or absolute paths)
- `Put` writes atomically via a temp file + rename (prevents partial writes)
- `List` walks the directory tree under the prefix
- `OTTERCAMP_STORAGE_ROOT` env var (default: `./data/objects`)

**S3-compatible adapter** (`storage/s3.go`):
- Uses `aws-sdk-go-v2` (S3 client)
- `OTTERCAMP_S3_BUCKET`, `OTTERCAMP_S3_ENDPOINT` (optional; for MinIO/Cloudflare R2), `OTTERCAMP_S3_REGION`, `OTTERCAMP_S3_ACCESS_KEY_ID`, `OTTERCAMP_S3_SECRET_ACCESS_KEY` env vars
- Multipart upload for objects > 5MB
- `List` uses paginated `ListObjectsV2`

**Factory** (`storage/new.go`):
- `storage.New(cfg Config) (Store, error)` — returns filesystem adapter when `OTTERCAMP_STORAGE_BACKEND=fs` (default), S3 adapter when `OTTERCAMP_STORAGE_BACKEND=s3`

**Key conventions** (documented, not enforced by this task):
- Chat artifacts: `orgs/{org_id}/chat/{session_id}/artifacts/{artifact_id}`
- Run artifacts: `orgs/{org_id}/runs/{run_id}/artifacts/{artifact_id}`
- Prompt/response captures: `orgs/{org_id}/invocations/{invocation_id}/prompt` and `.../response`
- Memory file backing: `orgs/{org_id}/memory/{memory_id}`
- Backup archives: `backups/{timestamp}/{filename}`

**`ottercamp backup` CLI command stub** (wired to storage; full logic deferred to task 068):
- `ottercamp backup --output-dir ./backups` — placeholder that prints "backup not yet implemented" and exits 0 (stub only; real logic in task 068)

### Must NOT build
- Backup/restore implementation (task 068)
- Any domain-level artifact storage calls (those are in the tasks that use the Store interface)
- Presigned URL generation (not required by the spec)

## Acceptance Criteria

- [ ] `storage.New(cfg)` returns a filesystem adapter by default (no env vars required)
- [ ] Filesystem adapter `Put` + `Get` round-trip: data written by `Put` is returned identically by `Get`
- [ ] Filesystem adapter `Put` is atomic: a concurrent reader never sees a partial write (verified by writing a large object and reading concurrently in a test)
- [ ] A key containing `..` (e.g., `../../etc/passwd`) is rejected by the filesystem adapter with a typed error at `Put` time
- [ ] `Exists` returns false for a key that was never written; true after `Put`; false again after `Delete`
- [ ] `List` with prefix `orgs/abc/` returns only keys that start with that prefix
- [ ] S3 adapter: unit test with a mock S3 client verifies `Put` calls `PutObject` and `Get` calls `GetObject` with the correct key
- [ ] `storage.New` returns an error when `OTTERCAMP_STORAGE_BACKEND=s3` and required S3 env vars are absent

## Tests Required

Following the architecture in doc 21 (`OTTERCAMP_MODE=test`):

**Unit tests:**
- `fs.Adapter`: all interface methods including path traversal rejection, atomic put, delete-then-exists
- `s3.Adapter`: mock `aws-sdk-go-v2` S3 client; test `Put`, `Get`, `Delete`, `List`, error propagation from AWS SDK
- `storage.New`: env var → adapter selection; missing required vars → typed config error

**Integration tests:**
- Filesystem adapter against a real temp directory (via `t.TempDir()`): CRUD round-trip, concurrent puts, large object (>1MB)
- S3 adapter integration test is opt-in, gated by `OTTERCAMP_TEST_S3_ENDPOINT` env var being set (MinIO or real S3). Skip if not set.

**E2E tests:**
- None — covered by dedicated E2E task 081

## Implementer Notes

- The filesystem adapter is the only adapter required for `OTTERCAMP_MODE=test`. Tests must never require S3.
- Atomic put implementation: write to `{key}.tmp` first, then `os.Rename` to `{key}`. On Linux/macOS, rename is atomic across same-filesystem paths.
- `io.ReadCloser` returned by `Get` must be closed by the caller. The filesystem adapter returns an `*os.File`; the S3 adapter returns the `GetObjectOutput.Body`.
- Key validation: enforce that keys match `^[a-zA-Z0-9/_.-]+$` and do not start with `/`. This prevents filesystem escapes and S3 key edge cases.
- The `ContentLength` field in `PutOptions` is passed as the `Content-Length` header to S3 and used for progress tracking. Pass `-1` when streaming from an unknown-length reader (S3 SDK handles chunked upload).
- Object storage is not used in the L0 schema layer (task 003) or the database infrastructure layer (task 002). It is first used by the secret store (task 009, for master key file loading) and later heavily by artifact storage in L3/L4 tasks.
