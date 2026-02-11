.PHONY: dev run setup bootstrap seed prod-build prod-local start stop status build build-otter install test release-gate migrate migrate-up migrate-down migrate-status migrate-version migrate-dry-run migrate-create clean uninstall

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

bootstrap:
	@bash scripts/bootstrap.sh

seed:
	go run ./scripts/seed/seed.go

prod-build:
	cd web && VITE_API_URL= npm run build
	go build -o bin/server ./cmd/server

prod-local: prod-build
	STATIC_DIR=./web/dist ./bin/server

start: prod-build
	@echo "🦦 Starting Otter Camp on http://localhost:$${PORT:-4200}"
	@# Kill any existing instances
	@if [ -f /tmp/ottercamp-server.pid ]; then kill $$(cat /tmp/ottercamp-server.pid) 2>/dev/null || true; rm -f /tmp/ottercamp-server.pid; fi
	@pkill -f 'openclaw-bridge.ts continuous' 2>/dev/null || true
	@sleep 1
	@if [ -f bridge/.env ]; then \
		echo "🌉 Starting bridge..."; \
		set -a; . bridge/.env; set +a; \
		nohup npx tsx bridge/openclaw-bridge.ts continuous &> /tmp/ottercamp-bridge.log & \
		echo "$$!" > /tmp/ottercamp-bridge.pid; \
		echo "   Bridge PID: $$! (log: /tmp/ottercamp-bridge.log)"; \
	fi
	@STATIC_DIR=./web/dist nohup ./bin/server &> /tmp/ottercamp-server.log &\
		echo "$$!" > /tmp/ottercamp-server.pid; \
		echo "   Server PID: $$! (log: /tmp/ottercamp-server.log)"; \
		sleep 2; \
		if curl -s http://localhost:$${PORT:-4200}/health > /dev/null 2>&1; then \
			echo "✅ Otter Camp is running at http://localhost:$${PORT:-4200}"; \
		else \
			echo "❌ Server failed to start. Check /tmp/ottercamp-server.log"; \
			tail -10 /tmp/ottercamp-server.log; \
		fi

stop:
	@echo "Stopping Otter Camp..."
	@if [ -f /tmp/ottercamp-server.pid ]; then \
		kill $$(cat /tmp/ottercamp-server.pid) 2>/dev/null && echo "   Server stopped" || echo "   Server not running"; \
		rm -f /tmp/ottercamp-server.pid; \
	else \
		echo "   Server: no PID file"; \
	fi
	@pkill -f 'openclaw-bridge.ts continuous' 2>/dev/null && echo "   Bridge stopped" || echo "   Bridge not running"
	@rm -f /tmp/ottercamp-bridge.pid

status:
	@echo "🦦 Otter Camp Status"
	@if curl -s http://localhost:$${PORT:-4200}/health > /dev/null 2>&1; then \
		echo "   Server: ✅ running"; \
	else \
		echo "   Server: ❌ not running"; \
	fi
	@if curl -s http://localhost:8787/health > /dev/null 2>&1; then \
		echo "   Bridge: ✅ running"; \
	else \
		echo "   Bridge: ❌ not running"; \
	fi

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

# Database migrations
migrate: migrate-up

migrate-up:
	@echo "Running migrations up..."
	go run ./cmd/migrate up

migrate-down:
	@echo "Rolling back migrations..."
	go run ./cmd/migrate down 1

migrate-status:
	@echo "Checking migration status..."
	go run ./cmd/migrate status

migrate-version:
	@echo "Current migration version..."
	go run ./cmd/migrate version

migrate-dry-run:
	@echo "Dry run - showing pending migrations..."
	go run ./cmd/migrate -dry-run up

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

uninstall:
	@bash scripts/uninstall.sh

# Railway deployment
deploy:
	railway up
