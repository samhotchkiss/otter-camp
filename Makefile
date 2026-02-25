.PHONY: build-web build build-all test lint

build-web:
	@if [ -f web/package.json ]; then \
		cd web && npm ci && npm run build; \
	else \
		echo "web/package.json not found; skipping npm build (expected in backend-only environments)"; \
	fi
	mkdir -p internal/web/web/dist
	rm -rf internal/web/web/dist/*
	@if [ -d web/dist ]; then \
		cp -R web/dist/. internal/web/web/dist/; \
	fi

build: build-web
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" ./cmd/ottercamp

build-all: build-web
	goreleaser build --snapshot --clean

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
