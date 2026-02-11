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

stop_bridge() {
  local pids
  pids=$(pgrep -f "openclaw-bridge.ts" 2>/dev/null || true)
  if [[ -n "$pids" ]]; then
    echo "Stopping bridge process(es)..."
    echo "$pids" | xargs kill 2>/dev/null || true
    sleep 1
    log_success "Bridge stopped."
  fi
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

  stop_bridge
  stop_docker
  remove_local_data
  remove_cli_config

  echo
  echo -e "${bold}Uninstall complete.${reset}"
  echo "Run 'scripts/setup.sh' to start fresh."
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
