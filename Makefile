.PHONY: build install test-unit test-integration test-e2e test-tui-quality lint clean coverage-report

BINARY_NAME = ottercamp
BIN_DIR = ./bin
CMD_DIR = ./cmd/ottercamp
GOFLAGS =

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILT_AT ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X github.com/samhotchkiss/otter-camp/internal/version.Version=$(VERSION) \
	-X github.com/samhotchkiss/otter-camp/internal/version.Commit=$(COMMIT) \
	-X github.com/samhotchkiss/otter-camp/internal/version.BuiltAt=$(BUILT_AT)

build:
	mkdir -p $(BIN_DIR)
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) $(CMD_DIR)

install:
	./scripts/install.sh

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

test-tui-quality:
	./build/verify-tui-quality-gates.sh

lint:
	golangci-lint run --timeout=3m

clean:
	rm -rf $(BIN_DIR)
	rm -f coverage-unit.out coverage-integration.out

coverage-report: test-unit
	go tool cover -html=coverage-unit.out -o coverage-unit.html
	@echo "Coverage report written to coverage-unit.html"
