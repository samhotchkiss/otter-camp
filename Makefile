.PHONY: dev run build test migrate migrate-up migrate-down migrate-create clean

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

# Build
build:
	go build -o bin/server ./cmd/server

build-web:
	cd web && npm run build

# Database
migrate: migrate-up

migrate-up:
	@echo "Running migrations up..."
	go run ./cmd/migrate up

migrate-down:
	@echo "Rolling back migrations..."
	go run ./cmd/migrate down

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

test-web:
	cd web && npm test

# Cleanup
clean:
	rm -rf bin/
	rm -rf web/dist/

# Railway deployment
deploy:
	railway up
