#!/usr/bin/env bash
set -euo pipefail

bold="\033[1m"
green="\033[0;32m"
yellow="\033[0;33m"
red="\033[0;31m"
reset="\033[0m"

AUTO_YES=0
CHECK_ONLY=0
DRY_RUN=0

usage() {
  cat <<'EOF'
Otter Camp local setup script

Usage:
  scripts/setup.sh [--yes] [--dry-run] [--check-only]

Options:
  --yes         Auto-approve dependency installs.
  --dry-run     Print install/bootstrap commands without executing them.
  --check-only  Run dependency checks/prompts only and exit.
EOF
}

log_success() {
  echo -e "${green}✅${reset} $*"
}

log_warn() {
  echo -e "${yellow}⚠️${reset} $*"
}

log_error() {
  echo -e "${red}❌${reset} $*" >&2
}

run_cmd() {
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "↪ $*"
    return 0
  fi
  "$@"
}

detect_platform() {
  local uname_out
  uname_out="$(uname -s 2>/dev/null || true)"
  case "$uname_out" in
    Darwin)
      echo "macos"
      return 0
      ;;
    Linux)
      if [[ -f /proc/version ]] && grep -qi "microsoft" /proc/version; then
        echo "wsl"
        return 0
      fi
      echo "linux"
      return 0
      ;;
    *)
      echo "unknown"
      return 0
      ;;
  esac
}

dependency_label() {
  case "$1" in
    brew) echo "Homebrew" ;;
    go) echo "Go" ;;
    node) echo "Node.js" ;;
    npm) echo "npm" ;;
    git) echo "Git" ;;
    docker) echo "Docker" ;;
    compose) echo "Docker Compose" ;;
    psql) echo "PostgreSQL client" ;;
    ollama) echo "Ollama" ;;
    *) echo "$1" ;;
  esac
}

dependency_description() {
  case "$1" in
    brew) echo "Used to install local dependencies on macOS." ;;
    go) echo "Builds and runs the Otter Camp API and CLI." ;;
    node) echo "Builds the web dashboard." ;;
    npm) echo "Installs frontend packages and runs web scripts." ;;
    git) echo "Required for cloning repos and project workflows." ;;
    docker) echo "Runs local Postgres when you don't have one installed." ;;
    compose) echo "Starts multi-service local stack from docker-compose.yml." ;;
    psql) echo "Lets setup connect to a local PostgreSQL instance without Docker." ;;
    ollama) echo "Provides local embedding model support for memory features." ;;
    *) echo "Required dependency." ;;
  esac
}

check_dependency() {
  case "$1" in
    brew)
      command -v brew >/dev/null 2>&1
      ;;
    go)
      command -v go >/dev/null 2>&1
      ;;
    node)
      command -v node >/dev/null 2>&1
      ;;
    npm)
      command -v npm >/dev/null 2>&1
      ;;
    git)
      command -v git >/dev/null 2>&1
      ;;
    docker)
      command -v docker >/dev/null 2>&1
      ;;
    compose)
      if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
        return 0
      fi
      command -v docker-compose >/dev/null 2>&1
      ;;
    psql)
      command -v psql >/dev/null 2>&1
      ;;
    ollama)
      command -v ollama >/dev/null 2>&1
      ;;
    *)
      return 1
      ;;
  esac
}

prompt_yes_no() {
  local question="$1"
  local default_yes="$2"
  local prompt_suffix="(y/N)"
  if [[ "$default_yes" -eq 1 ]]; then
    prompt_suffix="(Y/n)"
  fi

  local answer=""
  read -r -p "$question $prompt_suffix " answer
  answer="$(echo "$answer" | tr '[:upper:]' '[:lower:]' | xargs)"
  if [[ -z "$answer" ]]; then
    if [[ "$default_yes" -eq 1 ]]; then
      return 0
    fi
    return 1
  fi

  case "$answer" in
    y|yes) return 0 ;;
    n|no) return 1 ;;
    *)
      if [[ "$default_yes" -eq 1 ]]; then
        return 0
      fi
      return 1
      ;;
  esac
}

run_linux_install() {
  local package_name="$1"
  if command -v apt-get >/dev/null 2>&1; then
    if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
      run_cmd apt-get install -y "$package_name"
    elif command -v sudo >/dev/null 2>&1; then
      run_cmd sudo apt-get install -y "$package_name"
    else
      log_error "Need root/sudo to install $package_name via apt-get."
      return 1
    fi
    return 0
  fi

  if command -v yum >/dev/null 2>&1; then
    if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
      run_cmd yum install -y "$package_name"
    elif command -v sudo >/dev/null 2>&1; then
      run_cmd sudo yum install -y "$package_name"
    else
      log_error "Need root/sudo to install $package_name via yum."
      return 1
    fi
    return 0
  fi

  if command -v pacman >/dev/null 2>&1; then
    if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
      run_cmd pacman -S --noconfirm "$package_name"
    elif command -v sudo >/dev/null 2>&1; then
      run_cmd sudo pacman -S --noconfirm "$package_name"
    else
      log_error "Need root/sudo to install $package_name via pacman."
      return 1
    fi
    return 0
  fi

  log_error "No supported Linux package manager found for installing $package_name."
  return 1
}

install_dependency() {
  local dep="$1"
  local platform="$2"

  case "$dep" in
    brew)
      if [[ "$platform" != "macos" ]]; then
        log_error "Homebrew install is only supported on macOS in this script."
        return 1
      fi
      run_cmd /bin/bash -lc "curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh | /bin/bash"
      ;;
    go)
      if [[ "$platform" == "macos" ]]; then
        run_cmd brew install go
      else
        run_linux_install golang-go
      fi
      ;;
    node)
      if [[ "$platform" == "macos" ]]; then
        run_cmd brew install node
      else
        run_linux_install nodejs
      fi
      ;;
    npm)
      if [[ "$platform" == "macos" ]]; then
        run_cmd brew install node
      else
        run_linux_install npm
      fi
      ;;
    git)
      if [[ "$platform" == "macos" ]]; then
        run_cmd brew install git
      else
        run_linux_install git
      fi
      ;;
    docker)
      if [[ "$platform" == "macos" ]]; then
        run_cmd brew install --cask docker-desktop
        # Docker Desktop needs to be launched before docker/compose commands work
        # Ensure Docker Desktop is running and fully ready
        if ! docker info >/dev/null 2>&1; then
          echo
          echo "Docker Desktop needs to start before we can continue."
          echo "Opening Docker Desktop now..."
          open -a Docker
        fi
        echo -n "⏳ Waiting for Docker to be fully ready"
        local docker_wait=0
        while true; do
          # docker info succeeds early; test an actual pull to confirm engine is ready
          if docker info >/dev/null 2>&1 && docker pull hello-world >/dev/null 2>&1; then
            docker rmi hello-world >/dev/null 2>&1 || true
            break
          fi
          echo -n "."
          sleep 3
          docker_wait=$((docker_wait + 3))
          if [[ "$docker_wait" -ge 120 ]]; then
            echo
            log_error "Docker Desktop didn't become fully ready within 2 minutes."
            echo "Please start Docker Desktop manually, then run 'make setup' again."
            exit 1
          fi
        done
        echo
        log_success "Docker Desktop is ready."
      else
        run_linux_install docker.io
      fi
      ;;
    compose)
      if [[ "$platform" == "macos" ]]; then
        # Docker Desktop includes Compose — just verify it's available
        if docker compose version >/dev/null 2>&1; then
          log_success "Docker Desktop includes Docker Compose."
          return 0
        fi
        # Docker not ready yet — the docker install step should have handled startup
        # but if we got here, wait a bit more
        echo -n "⏳ Waiting for Docker Compose"
        local compose_wait=0
        while ! docker compose version >/dev/null 2>&1; do
          echo -n "."
          sleep 3
          compose_wait=$((compose_wait + 3))
          if [[ "$compose_wait" -ge 60 ]]; then
            echo
            log_error "Docker Compose not available. Make sure Docker Desktop is running."
            exit 1
          fi
        done
        echo
        log_success "Docker Desktop includes Docker Compose."
        return 0
      fi
      run_linux_install docker-compose-plugin
      ;;
    ollama)
      if [[ "$platform" == "macos" ]]; then
        run_cmd brew install ollama
        # Start Ollama service
        echo "Starting Ollama service..."
        brew services start ollama 2>/dev/null || true
        # Wait for it to be ready
        echo -n "⏳ Waiting for Ollama"
        local ollama_wait=0
        while ! curl -s http://localhost:11434/api/tags >/dev/null 2>&1; do
          echo -n "."
          sleep 2
          ollama_wait=$((ollama_wait + 2))
          if [[ "$ollama_wait" -ge 30 ]]; then
            echo
            log_warn "Ollama didn't start within 30s — you can start it later with: brew services start ollama"
            break
          fi
        done
        echo
        log_success "Ollama is running."
      else
        if command -v curl >/dev/null 2>&1; then
          run_cmd bash -lc "curl -fsSL https://ollama.com/install.sh | sh"
        else
          log_error "curl is required to install Ollama on Linux."
          return 1
        fi
      fi
      ;;
    *)
      log_error "Unknown dependency: $dep"
      return 1
      ;;
  esac
}

ensure_dependency() {
  local dep="$1"
  local platform="$2"
  local auto_yes="$3"
  local default_yes="$4"

  local label
  label="$(dependency_label "$dep")"

  if check_dependency "$dep"; then
    log_success "$label is installed."
    return 0
  fi

  echo "$label is missing."
  echo "$(dependency_description "$dep")"

  local approved=1
  if [[ "$auto_yes" -eq 0 ]]; then
    if prompt_yes_no "Install $label now?" "$default_yes"; then
      approved=1
    else
      approved=0
    fi
  fi

  if [[ "$approved" -ne 1 ]]; then
    log_error "Cannot continue without $label."
    return 1
  fi

  if ! install_dependency "$dep" "$platform"; then
    log_error "Installation failed for $label."
    return 1
  fi

  if [[ "$DRY_RUN" -eq 1 ]]; then
    log_success "$label install command prepared (dry-run)."
    return 0
  fi

  if ! check_dependency "$dep"; then
    log_error "$label still not detected after install attempt."
    return 1
  fi

  log_success "$label installed."
  return 0
}

generate_secret() {
  if command -v openssl >/dev/null 2>&1; then
    local secret
    if secret="$(openssl rand -hex 24 2>/dev/null)"; then
      echo "$secret"
      return
    fi
  fi
  if command -v head >/dev/null 2>&1 && command -v xxd >/dev/null 2>&1; then
    head -c 24 /dev/urandom | xxd -p
    return
  fi
  date +%s%N | shasum | awk '{print $1}'
}

ensure_dependencies() {
  local platform="$1"

  log_success "Detected platform: $platform"

  if [[ "$platform" == "macos" ]]; then
    ensure_dependency brew "$platform" "$AUTO_YES" 1
  fi

  ensure_dependency go "$platform" "$AUTO_YES" 1
  ensure_dependency node "$platform" "$AUTO_YES" 1
  ensure_dependency npm "$platform" "$AUTO_YES" 1
  ensure_dependency git "$platform" "$AUTO_YES" 1
  ensure_dependency ollama "$platform" "$AUTO_YES" 1

  local has_docker=0
  local has_compose=0
  local has_psql=0

  if check_dependency docker; then
    has_docker=1
  fi
  if check_dependency compose; then
    has_compose=1
  fi
  if check_dependency psql; then
    has_psql=1
  fi

  if [[ "$has_docker" -eq 1 && "$has_compose" -eq 1 ]]; then
    log_success "Docker + Compose detected."
    return 0
  fi
  if [[ "$has_psql" -eq 1 ]]; then
    log_warn "Docker not found; using existing local PostgreSQL."
    return 0
  fi

  ensure_dependency docker "$platform" "$AUTO_YES" 1
  ensure_dependency compose "$platform" "$AUTO_YES" 1
}

write_env_if_missing() {
  if [[ -f .env ]]; then
    log_success ".env already exists (not overwritten)."
    return 0
  fi

  local sync_secret ws_secret webhook_secret auth_secret local_auth_token
  sync_secret="$(generate_secret)"
  ws_secret="$(generate_secret)"
  webhook_secret="$(generate_secret)"
  auth_secret="$(generate_secret)"
  local_auth_token="oc_local_$(generate_secret | cut -c1-32)"

  cat > .env <<EOF
# Generated by scripts/setup.sh on $(date)
APP_ENV=development
PORT=4200
DATABASE_URL=postgres://otter:camp@localhost:5432/ottercamp?sslmode=disable
OPENCLAW_WEBHOOK_SECRET=${webhook_secret}
OPENCLAW_WS_SECRET=${ws_secret}
OPENCLAW_SYNC_SECRET=${sync_secret}
OPENCLAW_SYNC_TOKEN=${sync_secret}
OPENCLAW_AUTH_SECRET=${auth_secret}
LOCAL_AUTH_TOKEN=${local_auth_token}
VITE_API_URL=
GIT_REPO_ROOT=./data/repos
EOF
  log_success "Created .env"
}

load_env() {
  if [[ ! -f .env ]]; then
    log_error ".env is missing."
    return 1
  fi

  local raw line key value
  while IFS= read -r raw || [[ -n "$raw" ]]; do
    raw="${raw%$'\r'}"
    line="${raw#"${raw%%[![:space:]]*}"}"
    if [[ -z "$line" || "${line:0:1}" == "#" ]]; then
      continue
    fi
    if [[ "$line" != *=* ]]; then
      log_error "Invalid .env entry: $raw"
      return 1
    fi

    key="${line%%=*}"
    value="${line#*=}"
    key="${key#"${key%%[![:space:]]*}"}"
    key="${key%"${key##*[![:space:]]}"}"
    value="${value#"${value%%[![:space:]]*}"}"
    value="${value%"${value##*[![:space:]]}"}"

    if [[ ! "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
      log_error "Invalid .env key: $key"
      return 1
    fi

    export "$key=$value"
  done < .env
}

resolve_compose_cmd() {
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    echo "docker compose"
    return 0
  fi
  if command -v docker-compose >/dev/null 2>&1; then
    echo "docker-compose"
    return 0
  fi
  echo ""
}

start_postgres_if_needed() {
  local compose_cmd
  compose_cmd="$(resolve_compose_cmd)"

  if [[ -n "$compose_cmd" ]]; then
    echo "Setting up the database..."
    if [[ "$DRY_RUN" -eq 1 ]]; then
      echo "↪ ${compose_cmd} up -d postgres"
      log_success "Postgres startup command prepared (dry-run)."
      return 0
    fi

    ${compose_cmd} up -d postgres >/dev/null
    local ready=0
    for _ in $(seq 1 60); do
      if docker exec otter-camp-db pg_isready -U otter -d ottercamp >/dev/null 2>&1; then
        ready=1
        break
      fi
      sleep 1
    done
    if [[ "$ready" -ne 1 ]]; then
      log_error "Postgres did not become ready in time."
      return 1
    fi
    log_success "Database is ready."
    return 0
  fi

  if command -v psql >/dev/null 2>&1; then
    log_warn "Using local PostgreSQL from DATABASE_URL."
    return 0
  fi

  log_error "Need Docker+Compose or local PostgreSQL to continue."
  return 1
}

write_cli_config() {
  local cfg_dir
  if [[ "$(uname -s)" == "Darwin" ]]; then
    cfg_dir="${HOME}/Library/Application Support/otter"
  else
    cfg_dir="${XDG_CONFIG_HOME:-${HOME}/.config}/otter"
  fi
  mkdir -p "$cfg_dir"

  cat > "${cfg_dir}/config.json" <<EOF
{
  "apiBaseUrl": "http://localhost:4200",
  "token": "${LOCAL_AUTH_TOKEN}",
  "defaultOrg": ""
}
EOF
  log_success "Wrote CLI config to ${cfg_dir}/config.json"
}

detect_openclaw_gateway() {
  # Try to read OpenClaw config to auto-detect gateway token and port.
  local oc_config
  if [[ -f "$HOME/.openclaw/openclaw.json" ]]; then
    oc_config="$HOME/.openclaw/openclaw.json"
  elif [[ -f "$HOME/.config/openclaw/openclaw.json" ]]; then
    oc_config="$HOME/.config/openclaw/openclaw.json"
  fi

  DETECTED_OC_PORT=""
  DETECTED_OC_TOKEN=""

  if [[ -n "$oc_config" ]] && command -v node >/dev/null 2>&1; then
    DETECTED_OC_PORT=$(node -e "try{const c=JSON.parse(require('fs').readFileSync('$oc_config','utf8'));console.log(c.gateway?.port||'')}catch{}" 2>/dev/null || true)
    DETECTED_OC_TOKEN=$(node -e "try{const c=JSON.parse(require('fs').readFileSync('$oc_config','utf8'));console.log(c.gateway?.token||'')}catch{}" 2>/dev/null || true)
  fi

  if [[ -z "$DETECTED_OC_PORT" ]]; then
    DETECTED_OC_PORT="18791"
  fi
}

write_bridge_env_if_missing() {
  mkdir -p bridge
  if [[ -f bridge/.env ]]; then
    log_success "bridge/.env already exists (not overwritten)."
    return 0
  fi

  detect_openclaw_gateway

  local oc_token_line="OPENCLAW_TOKEN=your-openclaw-gateway-token"
  local needs_manual_token=1
  if [[ -n "$DETECTED_OC_TOKEN" ]]; then
    oc_token_line="OPENCLAW_TOKEN=${DETECTED_OC_TOKEN}"
    needs_manual_token=0
  fi

  cat > bridge/.env <<EOF
# Generated by scripts/setup.sh
# Bridge connects OpenClaw <-> Otter Camp (both local)
OPENCLAW_HOST=127.0.0.1
OPENCLAW_PORT=${DETECTED_OC_PORT}
${oc_token_line}
OTTERCAMP_URL=http://localhost:${PORT:-4200}
OTTERCAMP_TOKEN=${OPENCLAW_SYNC_SECRET}
OPENCLAW_WS_SECRET=${OPENCLAW_WS_SECRET}
EOF

  if [[ "$needs_manual_token" -eq 1 ]]; then
    log_warn "Created bridge/.env — set OPENCLAW_TOKEN to your gateway token."
    echo "  Find it in ~/.openclaw/openclaw.json under gateway.token"
  else
    log_success "Created bridge/.env (auto-configured from OpenClaw)"
  fi
}

pull_ollama_model() {
  if ! check_dependency ollama; then
    return 0
  fi
  echo "Preparing embedding model..."
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "↪ ollama pull nomic-embed-text"
    return 0
  fi
  ollama pull nomic-embed-text >/dev/null 2>&1 || log_warn "Could not pull Ollama model automatically."
}

run_bootstrap_steps() {
  start_postgres_if_needed

  mkdir -p data/repos
  log_success "Prepared local repo storage."

  echo "Running database migrations..."
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "↪ go run ./cmd/migrate up"
  else
    if ! go run ./cmd/migrate up 2>&1; then
      # Check if it's a dirty state error and auto-fix
      local dirty_ver
      dirty_ver=$(go run ./cmd/migrate up 2>&1 | sed -n 's/.*Dirty database version \([0-9]*\).*/\1/p' || true)
      if [[ -n "$dirty_ver" ]]; then
        log_warn "Database migration $dirty_ver was dirty — auto-fixing..."
        go run ./cmd/migrate force "$dirty_ver"
        go run ./cmd/migrate up
      else
        log_error "Migration failed. Check the error above."
        exit 1
      fi
    fi
  fi
  log_success "Migrations complete."

  echo "Installing frontend packages..."
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "↪ (cd web && npm ci)"
  else
    (cd web && npm ci --silent)
  fi
  log_success "Frontend packages installed."

  echo "Seeding starter workspace..."
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "↪ go run ./scripts/seed/seed.go"
  else
    go run ./scripts/seed/seed.go
  fi
  log_success "Seed complete."

  echo "Building local production assets..."
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "↪ make prod-local"
  else
    make prod-local >/dev/null
  fi
  log_success "Build complete."

  write_cli_config
  write_bridge_env_if_missing
  pull_ollama_model
}

parse_args() {
  while (($# > 0)); do
    case "$1" in
      --yes)
        AUTO_YES=1
        shift
        ;;
      --check-only)
        CHECK_ONLY=1
        shift
        ;;
      --dry-run)
        DRY_RUN=1
        shift
        ;;
      --help|-h)
        usage
        exit 0
        ;;
      *)
        log_error "Unknown option: $1"
        usage
        exit 1
        ;;
    esac
  done
}

main() {
  parse_args "$@"

  echo -e "${bold}🦦 Otter Camp local setup${reset}"
  echo

  # OpenClaw is required — check first before anything else
  if ! command -v openclaw >/dev/null 2>&1; then
    log_error "OpenClaw not found."
    echo
    echo "Otter Camp requires OpenClaw to manage your agents."
    echo "Install it first:"
    echo
    echo "  npm install -g openclaw"
    echo
    echo "Or visit: https://docs.openclaw.ai/install"
    echo
    echo "Then run this setup again."
    exit 1
  fi
  log_success "OpenClaw detected"

  local platform
  platform="$(detect_platform)"
  if [[ "$platform" == "unknown" ]]; then
    log_error "Unsupported platform. This script supports macOS, Linux, and WSL."
    exit 1
  fi

  ensure_dependencies "$platform"

  if [[ "$CHECK_ONLY" -eq 1 ]]; then
    log_success "Dependency checks complete."
    exit 0
  fi

  write_env_if_missing
  load_env
  run_bootstrap_steps

  echo
  echo -e "${bold}Setup complete.${reset}"
  echo
  echo "Start the server:  make dev"
  echo "Start the bridge:  npx tsx bridge/openclaw-bridge.ts continuous"
  echo "Dashboard:         http://localhost:${PORT:-4200}"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
