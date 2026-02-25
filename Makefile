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
	go build ./cmd/ottercamp

build-all: build-web
	goreleaser build --snapshot --clean

test:
	go test ./... -cover

lint:
	go vet ./...
