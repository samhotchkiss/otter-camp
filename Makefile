.PHONY: dev run build test migrate clean

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
migrate:
	@echo "Running migrations..."
	go run ./cmd/migrate

migrate-down:
	@echo "Rolling back migrations..."
	go run ./cmd/migrate down

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
