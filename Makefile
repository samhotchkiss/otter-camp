.PHONY: dev run setup seed prod-local build build-otter install test release-gate migrate migrate-up migrate-down migrate-status migrate-version migrate-dry-run migrate-create clean

# Development
dev:
	@echo "Starting development servers..."
	@make -j2 dev-api dev-web

dev-api:
	@echo "🦦 Starting API server..."
	go run ./cmd/server

dev-web:
	@echo "🎨 Starting frontend..."
	cd web && npm run dev

setup:
	@bash scripts/setup.sh

seed:
	go run ./scripts/seed/seed.go

prod-local:
	cd web && VITE_API_URL= npm run build
	STATIC_DIR=./web/dist go run ./cmd/server

# Build
build:
	go build -o bin/server ./cmd/server

build-otter:
	go build -o bin/otter ./cmd/otter

install:
	go build -o bin/otter ./cmd/otter
	@install_bin_dir="/usr/local/bin"; \
	if [ ! -w "$$install_bin_dir" ]; then \
		install_bin_dir="$$HOME/.local/bin"; \
		mkdir -p "$$install_bin_dir"; \
	fi; \
	ln -sf "$(PWD)/bin/otter" "$$install_bin_dir/otter"; \
	echo "otter installed to $$install_bin_dir/otter"; \
	case ":$$PATH:" in *":$$install_bin_dir:"*) ;; *) \
		echo "Add $$install_bin_dir to PATH to run 'otter' directly." ;; \
	esac; \
	echo "Run 'otter whoami' to verify."

build-web:
	cd web && npm run build

# Database - using scripts/migrate for enhanced migration support
migrate: migrate-up

migrate-up:
	@echo "Running migrations up..."
	go run ./scripts/migrate/migrate.go up

migrate-down:
	@echo "Rolling back migrations..."
	go run ./scripts/migrate/migrate.go down 1

migrate-status:
	@echo "Checking migration status..."
	go run ./scripts/migrate/migrate.go status

migrate-version:
	@echo "Current migration version..."
	go run ./scripts/migrate/migrate.go version

migrate-dry-run:
	@echo "Dry run - showing pending migrations..."
	go run ./scripts/migrate/migrate.go -dry-run up

migrate-create:
	@echo "Creating migration files..."
	go run ./cmd/migrate create $(name)

# Docker
up:
	docker-compose up -d

down:
	docker-compose down

# Testing
test:
	go test ./...

release-gate:
	go run ./cmd/otter release-gate

test-web:
	cd web && npm test

# Cleanup
clean:
	rm -rf bin/
	rm -rf web/dist/

# Railway deployment
deploy:
	railway up
