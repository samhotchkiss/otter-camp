# 009: Secret Store

| Field | Value |
|-------|-------|
| Layer | L1 |
| Size | M (1–2 days) |
| Spec refs | doc 08 §Secrets, doc 08 §SecretCLI, doc 08 §SecretRefResolution |
| Spec status | finished |
| Depends on | 003, 004 |
| Blocks | 012, 020 |

## Scope

Build the secret store: `secret` table DDL and migration, AES-256-GCM encryption and
decryption, nonce generation, master key loading (env var → key file → KMS stub), key
version tracking, `ref:<slug>` resolution helper, deletion safety check, and the
`secret set / list / delete` CLI commands.

### Must build

**Migration:**
- `0011_secret.sql`

**`secret` table** (doc 08):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `slug text not null` — URL-safe, lowercase alphanumeric + hyphens; unique within org
- `display_name text not null`
- `description text`
- `ciphertext bytea not null` — AES-256-GCM encrypted value
- `nonce bytea not null` — 12-byte GCM nonce (unique per encryption operation)
- `key_version integer not null default 1` — which master key version was used to encrypt
- `created_by_type text not null check (created_by_type in ('human', 'agent', 'system'))`
- `created_by_id uuid not null`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Unique constraint: `(organization_id, slug)`
- Index: `(organization_id)` for list queries

**`internal/crypto/` package:**
- `crypto.MasterKey` — wraps a 32-byte AES key with a version number
- `crypto.Encrypt(key MasterKey, plaintext []byte) (ciphertext, nonce []byte, err error)` — generates a fresh 12-byte nonce via `crypto/rand`, AES-256-GCM seal
- `crypto.Decrypt(key MasterKey, ciphertext, nonce []byte) (plaintext []byte, err error)` — AES-256-GCM open
- `crypto.LoadMasterKey(version int) (MasterKey, error)` — load strategy (in priority order):
  1. `OTTERCAMP_MASTER_KEY` env var (base64-encoded 32 bytes)
  2. `OTTERCAMP_MASTER_KEY_FILE` env var pointing to a file (reads first 32 bytes)
  3. KMS stub: `OTTERCAMP_KMS_KEY_ID` env var set → return `ErrKMSNotImplemented` (stub for future)
  4. If none of the above: return `ErrNoMasterKey` with a clear message

**`internal/secret/` package:**

`Service` interface:
```go
type Service interface {
    Set(ctx context.Context, orgID uuid.UUID, slug, displayName, description, value string, by Principal) error
    Get(ctx context.Context, orgID uuid.UUID, slug string) (string, error)  // returns plaintext
    List(ctx context.Context, orgID uuid.UUID) ([]*SecretInfo, error)       // never returns plaintext
    Delete(ctx context.Context, orgID uuid.UUID, slug string, by Principal) error
    ResolveRef(ctx context.Context, orgID uuid.UUID, ref string) (string, error)  // ref = "ref:<slug>"
    CheckDeleteSafety(ctx context.Context, orgID uuid.UUID, slug string) ([]string, error)  // returns blocking references
}

type SecretInfo struct {
    ID          uuid.UUID
    Slug        string
    DisplayName string
    Description string
    KeyVersion  int
    CreatedAt   time.Time
    UpdatedAt   time.Time
    // No plaintext or ciphertext fields
}
```

**`ResolveRef`:**
- Input: `"ref:my-secret-slug"` → resolves to plaintext value of the secret with that slug
- Input without `"ref:"` prefix → returned as-is (passthrough for non-secret values)
- Used by MCP connection transport config (task 020) and CLI execution environment construction (task 058)

**`CheckDeleteSafety`:**
- Before deleting a secret, scan known reference columns across the DB:
  - `mcp_connection.transport_config jsonb` — scan for `"ref:<slug>"` string occurrences
  - `mcp_secret_binding.secret_ref` (text) — exact match
- Returns a list of human-readable descriptions of blocking references (e.g. `"mcp_connection 'my-server' transport_config"`)
- If any blocking references exist, `Delete` returns `ErrSecretInUse`

**Repository:**
- `SecretRepo`: `Create`, `GetBySlug`, `List`, `Update`, `Delete`
- `GetBySlug` never returns plaintext — that requires calling `crypto.Decrypt` at the service layer

**CLI commands** (wired to `Service`):
- `ottercamp secret set --slug <s> --display-name <n> --description <d>` — reads value from stdin or `--value` flag (stdin preferred — avoids shell history exposure)
- `ottercamp secret list` — prints table of slug, display_name, key_version, updated_at (no plaintext)
- `ottercamp secret delete --slug <s>` — calls `CheckDeleteSafety` first; prints blocking references and asks for confirmation if any; `--force` flag bypasses confirmation

### Must NOT build
- MCP secret binding table or resolution beyond `ref:<slug>` in transport_config (task 020)
- CLI execution environment injection (task 058)
- Secret rotation (not specified in V2 scope)
- HTTP endpoints for secrets (not in the spec — secrets are managed via CLI only in V2)

## Acceptance Criteria

- [ ] Migration `0011` applies cleanly; `(organization_id, slug)` unique constraint enforced
- [ ] `crypto.Encrypt` + `crypto.Decrypt` round-trip: plaintext in → ciphertext out → plaintext back
- [ ] Two calls to `crypto.Encrypt` with the same plaintext produce different ciphertexts (different nonces)
- [ ] `crypto.Decrypt` with a tampered ciphertext (1 byte flipped) returns an error (GCM authentication failure)
- [ ] `secret.Service.Set` stores ciphertext in DB; `Service.Get` returns the original plaintext
- [ ] `Service.List` returns `SecretInfo` structs with no plaintext or ciphertext fields; verified by reflection that no field named `Value`, `Plaintext`, or `Ciphertext` is present on `SecretInfo`
- [ ] `Service.Delete` with an active `mcp_connection` referencing `ref:<slug>` returns `ErrSecretInUse`; `--force` CLI flag bypasses the check and deletes
- [ ] `ResolveRef("ref:my-slug")` returns the decrypted plaintext; `ResolveRef("not-a-ref")` returns `"not-a-ref"` unchanged
- [ ] `crypto.LoadMasterKey`: `OTTERCAMP_MASTER_KEY` env var set → key loaded; neither env var set → `ErrNoMasterKey`; key file path set → reads from file
- [ ] `ottercamp secret set` reads value from stdin (not echoed); `ottercamp secret list` shows no plaintext

## Tests Required

Following the architecture in doc 21 (`OTTERCAMP_MODE=test`):

**Unit tests:**
- `crypto.Encrypt` / `crypto.Decrypt`: round-trip, tamper detection, nonce uniqueness
- `crypto.LoadMasterKey`: each loading strategy in isolation (mock env vars)
- `secret.Service.ResolveRef`: `ref:` prefix handling, passthrough for non-ref values
- `CheckDeleteSafety`: mock DB returning known references; verify returned list

**Integration tests:**
- `SecretRepo` against real PostgreSQL:
  - Set → Get round-trip (verify ciphertext stored in DB, plaintext returned by service)
  - List returns no plaintext fields
  - Unique slug constraint: duplicate slug within same org returns error
  - Delete → Get returns `ErrNotFound`
  - `CheckDeleteSafety` with a real `mcp_connection` row referencing the secret (seed data)

**E2E tests:**
- None — covered by dedicated E2E task 081

## Implementer Notes

- The `secret` table intentionally stores both `ciphertext` and `nonce` separately. Do not concatenate them. The nonce must be stored separately so it can be passed directly to `cipher.AEAD.Open` without any byte-slicing — reduces implementation error surface.
- Key version 1 is the current and only version. Key rotation (re-encrypting all secrets with a new master key) is out of scope for V2. The `key_version` column is present so the rotation path can be added later without a schema change.
- `crypto.LoadMasterKey` does NOT accept a hardcoded default or a zeroed key. A missing master key is a configuration error that prevents startup, not a graceful degradation. The error message should include instructions for how to set the key.
- The `ref:<slug>` syntax is used inline in text fields (e.g., `transport_config jsonb`). The resolution scan in `CheckDeleteSafety` uses PostgreSQL's `::text` cast + `LIKE '%ref:<slug>%'` against jsonb fields. This is a safety check, not a full-text index — it may have false positives (a slug that appears in non-reference contexts) but never false negatives.
- `created_by_type` follows the 3-type polymorphic convention (`human`, `agent`, `system`). Secrets created via CLI are `created_by_type='human'`; secrets created during bootstrap are `created_by_type='system'` with the sentinel UUID.
