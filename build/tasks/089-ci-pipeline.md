# 089: CI Pipeline

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | S (≤1 day) |
| Spec refs | doc 21 §CIPipeline, doc 21 §CoverageRequirements |
| Spec status | finished |
| Depends on | 001–088 |
| Blocks | — |

## Scope

GitHub Actions CI workflow and Makefile targets for the OtterCamp project. Implements
four CI stages (lint, unit tests, integration tests, build) with fail-fast on unit test
failure, coverage reporting, Go version matrix, test result caching, and a total budget
of 15 minutes. Also provides Makefile targets for local development use.

### Must build

**File:** `.github/workflows/ci.yml`

**File:** `Makefile` (additions to existing Makefile or new file if none exists)

---

#### `.github/workflows/ci.yml`

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

env:
  GO_VERSION: "1.21"

jobs:
  lint:
    name: Lint
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v4
        with:
          version: latest
          args: --timeout=3m

  unit-tests:
    name: Unit Tests
    runs-on: ubuntu-latest
    timeout-minutes: 5
    needs: lint
    strategy:
      matrix:
        go: ["1.21"]
      fail-fast: true
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ matrix.go }}
          cache: true
      - name: Run unit tests
        run: make test-unit
      - name: Upload coverage report
        uses: actions/upload-artifact@v4
        with:
          name: coverage-unit-go${{ matrix.go }}
          path: coverage-unit.out
          retention-days: 7
      - name: Coverage gate
        run: |
          go tool cover -func=coverage-unit.out | tail -1
          # Fail if coverage drops below 90% overall
          COVERAGE=$(go tool cover -func=coverage-unit.out | tail -1 | awk '{print $3}' | sed 's/%//')
          if [ $(echo "$COVERAGE < 90.0" | bc -l) -eq 1 ]; then
            echo "Coverage ${COVERAGE}% is below the 90% minimum"
            exit 1
          fi

  integration-tests:
    name: Integration Tests
    runs-on: ubuntu-latest
    timeout-minutes: 10
    needs: unit-tests
    services:
      postgres:
        image: pgvector/pgvector:pg16
        env:
          POSTGRES_USER: ottercamp
          POSTGRES_PASSWORD: ottercamp
          POSTGRES_DB: ottercamp_test
        ports:
          - 5432:5432
        options: >-
          --health-cmd pg_isready
          --health-interval 5s
          --health-timeout 5s
          --health-retries 10
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ env.GO_VERSION }}
          cache: true
      - name: Run integration tests
        env:
          DATABASE_URL: postgres://ottercamp:ottercamp@localhost:5432/ottercamp_test?sslmode=disable
        run: make test-integration
      - name: Upload integration coverage
        uses: actions/upload-artifact@v4
        with:
          name: coverage-integration
          path: coverage-integration.out
          retention-days: 7

  build:
    name: Build
    runs-on: ubuntu-latest
    timeout-minutes: 3
    needs: unit-tests
    strategy:
      matrix:
        go: ["1.21"]
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ matrix.go }}
          cache: true
      - name: Build binary
        run: make build
      - name: Verify binary exists
        run: |
          test -f ./bin/ottercamp
          ./bin/ottercamp version
      - name: Upload binary artifact
        uses: actions/upload-artifact@v4
        with:
          name: ottercamp-linux-amd64-go${{ matrix.go }}
          path: ./bin/ottercamp
          retention-days: 7
```

Note: E2E tests (`make test-e2e`) are intentionally not in the main CI workflow —
they require the full binary and a running server. They may be in a separate optional
workflow (`.github/workflows/e2e.yml`) triggered manually or on schedule, not on every
PR push. The `test-e2e` Makefile target is provided for local use and optional CI
integration.

---

#### `Makefile` targets

The Makefile must define these targets:

```makefile
.PHONY: build test-unit test-integration test-e2e lint clean

BINARY_NAME = ottercamp
BIN_DIR = ./bin
CMD_DIR = ./cmd/ottercamp
GOFLAGS =

build:
	go build $(GOFLAGS) -o $(BIN_DIR)/$(BINARY_NAME) $(CMD_DIR)

test-unit:
	go test ./... -short -count=1 \
	  -coverprofile=coverage-unit.out \
	  -covermode=atomic \
	  $(GOFLAGS)

test-integration:
	go test ./... -tags integration -count=1 \
	  -coverprofile=coverage-integration.out \
	  -covermode=atomic \
	  -timeout 8m \
	  $(GOFLAGS)

test-e2e: build
	go test ./e2e/... -tags e2e -count=1 \
	  -timeout 12m \
	  -parallel 4 \
	  $(GOFLAGS)

lint:
	golangci-lint run --timeout=3m

clean:
	rm -rf $(BIN_DIR)
	rm -f coverage-unit.out coverage-integration.out

coverage-report: test-unit
	go tool cover -html=coverage-unit.out -o coverage-unit.html
	@echo "Coverage report written to coverage-unit.html"
```

---

#### `.golangci.yml` (linter configuration)

```yaml
run:
  timeout: 3m
  go: "1.21"

linters:
  enable:
    - errcheck
    - govet
    - staticcheck
    - unused
    - gofmt
    - goimports
    - revive
  disable:
    - exhaustruct

linters-settings:
  revive:
    rules:
      - name: exported
        disabled: false
  goimports:
    local-prefixes: github.com/YOUR_ORG/otter-camp

issues:
  exclude-rules:
    # Test files may use t.Fatal in non-test helper functions
    - path: _test\.go
      linters: [revive]
    # Generated migration files may not follow all conventions
    - path: internal/db/migrations/
      linters: [gofmt, goimports]
```

Note: replace `github.com/YOUR_ORG/otter-camp` with the actual module path from `go.mod`.

### Must NOT build

- E2E test execution as a required CI gate (E2E runs are optional/scheduled)
- Docker image building in CI (that is in task 090)
- Security scanning or SAST tools (not in scope for Sprint 1)
- Deployment automation (not in scope for Sprint 1)

## Acceptance Criteria

- [ ] `.github/workflows/ci.yml` is valid YAML and passes GitHub Actions syntax check
- [ ] Lint stage completes in under 30 seconds on a typical codebase (golangci-lint with 3-minute timeout)
- [ ] Unit test stage uses `go test ./... -short` (no DB required); passes without PostgreSQL service
- [ ] Integration test stage uses PostgreSQL 16 + pgvector service container; tests run with `-tags integration`
- [ ] Build stage produces `./bin/ottercamp` binary and runs `ottercamp version` successfully
- [ ] `make test-unit` runs without DB and produces `coverage-unit.out`
- [ ] `make test-integration` runs with `DATABASE_URL` env and produces `coverage-integration.out`
- [ ] `make test-e2e` depends on `make build` and runs `./e2e/...` with `-tags e2e -parallel 4 -timeout 12m`
- [ ] `make lint` runs `golangci-lint` with 3-minute timeout
- [ ] `make build` produces `./bin/ottercamp`
- [ ] Coverage gate in unit-tests job fails the build if coverage drops below 90%
- [ ] Total CI pipeline runtime (lint + unit + integration + build in parallel) is under 15 minutes
- [ ] Unit-tests job has `fail-fast: true` — integration and build do not run if unit tests fail
- [ ] Go version matrix covers 1.21 only (Sprint 1 minimum)

## Tests Required

**Unit tests:** None — this task IS a CI infrastructure file.

**Integration tests:** None — this task IS CI infrastructure.

**E2E tests:** None — this task defines the E2E test runner configuration.

## Implementer Notes

**Go version matrix:**
The matrix uses only `["1.21"]` for Sprint 1. Adding 1.22+ is deferred to Sprint 2 when
Go 1.22+ features may be adopted.

**pgvector service container:**
The integration test stage uses `pgvector/pgvector:pg16` Docker image which includes
PostgreSQL 16 with the pgvector extension pre-installed. This avoids needing to
`CREATE EXTENSION pgvector` in a plain PostgreSQL image. The `--health-cmd pg_isready`
option ensures the service is ready before tests start.

**Test result caching:**
`actions/setup-go@v5` with `cache: true` caches the Go module cache and build cache.
This is the primary caching mechanism. Test result caching (skipping tests if no source
changed) is NOT implemented in Sprint 1 — all tests run on every push.

**Coverage gate:**
The 90% coverage gate applies to the overall unit test coverage. The 95% critical paths
requirement (doc 21) is not enforced automatically in CI — it is a code review guideline.
The CI gate catches regressions: if a PR drops overall coverage by more than 1%, the
build fails. The gate threshold is 90% (overall floor).

**New .go files without _test.go:**
The `.golangci.yml` may include a custom linter check or a separate `make lint-coverage`
target (not in Sprint 1 scope). For now, flagging new .go files without corresponding
_test.go files is a code review convention, not an automated CI gate.

**testdata/responses/ fixture files:**
Test fixture files in `testdata/responses/` (for recorded provider responses) must be
committed to the repository. They are not in `.gitignore`. The CI workflow checks out
the full repo including these fixtures.

**E2E in CI:**
The E2E workflow (`.github/workflows/e2e.yml`) is out of scope for this task file — this
task only defines the main CI workflow. A separate optional E2E workflow can be added
in Sprint 2 or as a follow-on task.
