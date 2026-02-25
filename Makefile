.PHONY: build test lint

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" ./cmd/ottercamp

test:
	go test ./... -cover

lint:
	go vet ./...

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILT_AT ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X github.com/samhotchkiss/otter-camp/internal/version.Version=$(VERSION) \
	-X github.com/samhotchkiss/otter-camp/internal/version.Commit=$(COMMIT) \
	-X github.com/samhotchkiss/otter-camp/internal/version.BuiltAt=$(BUILT_AT)
