#!/usr/bin/env bash
set -euo pipefail

bold="\033[1m"
green="\033[0;32m"
yellow="\033[0;33m"
red="\033[0;31m"
reset="\033[0m"

FORCE=0

usage() {
  cat <<'EOF'
Otter Camp uninstall / reset script

Removes all local data so you can re-run setup from scratch.

Usage:
  scripts/uninstall.sh [--force]

Options:
  --force    Skip confirmation prompt.

What gets removed:
  • Docker containers and volumes (postgres data)
  • .env and bridge/.env
  • data/repos/ (local git repos)
  • web/node_modules/ and web/dist/
  • bin/ (compiled binaries)
  • CLI config (~/.config/otter/ or ~/Library/Application Support/otter/)
  • Bridge process (if running)

What is NOT removed:
  • System dependencies (Go, Node, Docker, Ollama)
  • Ollama models
  • The otter-camp source code itself
  • Your OpenClaw installation or config
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

runtime_dir() {
  echo "${OTTERCAMP_RUNTIME_DIR:-/tmp}"
}

runtime_file() {
  local name="$1"
  echo "$(runtime_dir)/${name}"
}

stop_pid_file_process() {
  local pid_file="$1"
  local label="$2"
  if [[ ! -f "$pid_file" ]]; then
    return 0
  fi

  local pid
  pid="$(tr -d '[:space:]' < "$pid_file" 2>/dev/null || true)"
  if [[ -n "$pid" ]] && [[ "$pid" =~ ^[0-9]+$ ]]; then
    kill "$pid" 2>/dev/null || true
    sleep 1
  fi

  rm -f "$pid_file"
  log_success "${label} PID file removed."
}

stop_processes_by_pattern() {
  local pattern="$1"
  local label="$2"
  local pids
  pids=$(pgrep -f "$pattern" 2>/dev/null || true)
  if [[ -n "$pids" ]]; then
    echo "Stopping ${label} process(es)..."
    echo "$pids" | xargs kill 2>/dev/null || true
    sleep 1
    log_success "${label} stopped."
  fi
}

stop_server() {
  local server_pid_file
  server_pid_file="$(runtime_file "ottercamp-server.pid")"
  local server_pattern
  server_pattern="${OTTERCAMP_SERVER_PATTERN:-[.]\/bin\/server}"
  stop_pid_file_process "$server_pid_file" "Server"
  stop_processes_by_pattern "$server_pattern" "Server"
}

stop_bridge() {
  local bridge_pid_file
  bridge_pid_file="$(runtime_file "ottercamp-bridge.pid")"
  local bridge_pattern
  bridge_pattern="${OTTERCAMP_BRIDGE_PATTERN:-openclaw-bridge.ts}"
  stop_pid_file_process "$bridge_pid_file" "Bridge"
  stop_processes_by_pattern "$bridge_pattern" "Bridge"
}

stop_docker() {
  local compose_cmd=""
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    compose_cmd="docker compose"
  elif command -v docker-compose >/dev/null 2>&1; then
    compose_cmd="docker-compose"
  fi

  if [[ -n "$compose_cmd" ]] && [[ -f docker-compose.yml ]]; then
    echo "Stopping Docker containers..."
    ${compose_cmd} down -v 2>/dev/null || true
    log_success "Docker containers and volumes removed."
  else
    # Try removing container directly if docker is available
    if command -v docker >/dev/null 2>&1; then
      docker rm -f otter-camp-db 2>/dev/null || true
      docker volume rm otter-camp_postgres_data 2>/dev/null || true
      docker volume rm postgres_data 2>/dev/null || true
    fi
  fi
}

remove_local_data() {
  local items=(
    ".env"
    "bridge/.env"
    "data/repos"
    "web/node_modules"
    "web/dist"
    "bin"
  )

  for item in "${items[@]}"; do
    if [[ -e "$item" ]]; then
      rm -rf "$item"
      log_success "Removed $item"
    fi
  done
}

remove_cli_config() {
  local cfg_dir
  if [[ "$(uname -s)" == "Darwin" ]]; then
    cfg_dir="${HOME}/Library/Application Support/otter"
  else
    cfg_dir="${XDG_CONFIG_HOME:-${HOME}/.config}/otter"
  fi

  if [[ -d "$cfg_dir" ]]; then
    rm -rf "$cfg_dir"
    log_success "Removed CLI config ($cfg_dir)"
  fi
}

cleanup_runtime_artifacts() {
  local artifacts=(
    "$(runtime_file "ottercamp-server.pid")"
    "$(runtime_file "ottercamp-bridge.pid")"
    "$(runtime_file "ottercamp-server.log")"
    "$(runtime_file "ottercamp-server.error.log")"
    "$(runtime_file "ottercamp-bridge.log")"
    "$(runtime_file "ottercamp-bridge.error.log")"
  )

  local path
  for path in "${artifacts[@]}"; do
    if [[ -e "$path" ]]; then
      rm -f "$path"
      log_success "Removed $path"
    fi
  done
}

is_port_in_use() {
  local port="$1"
  if ! [[ "$port" =~ ^[0-9]+$ ]]; then
    return 1
  fi
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1
    return $?
  fi
  log_warn "lsof not found; skipping port verification for :$port"
  return 1
}

verify_port_released() {
  local port="${PORT:-4200}"
  if is_port_in_use "$port"; then
    log_error "Port $port is still in use after uninstall."
    return 1
  fi
  log_success "Port $port is free."
}

main() {
  while (($# > 0)); do
    case "$1" in
      --force) FORCE=1; shift ;;
      --help|-h) usage; exit 0 ;;
      *) log_error "Unknown option: $1"; usage; exit 1 ;;
    esac
  done

  echo -e "${bold}🦦 Otter Camp uninstall${reset}"
  echo
  echo "This will remove all local Otter Camp data (database, repos, config)."
  echo "Your source code and system dependencies will not be touched."
  echo

  if [[ "$FORCE" -eq 0 ]]; then
    read -r -p "Continue? (y/N) " answer
    case "$answer" in
      y|Y|yes|YES) ;;
      *) echo "Aborted."; exit 0 ;;
    esac
    echo
  fi

  stop_server
  stop_bridge
  stop_docker
  remove_local_data
  remove_cli_config
  cleanup_runtime_artifacts
  verify_port_released

  echo
  echo -e "${bold}Uninstall complete.${reset}"
  echo "Run 'scripts/setup.sh' to start fresh."
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
