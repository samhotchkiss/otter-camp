#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/uninstall.sh"

assert_exists() {
  local path="$1"
  local label="$2"
  if [[ ! -e "$path" ]]; then
    echo "assertion failed ($label): expected path to exist: $path" >&2
    exit 1
  fi
}

assert_not_exists() {
  local path="$1"
  local label="$2"
  if [[ -e "$path" ]]; then
    echo "assertion failed ($label): expected path to be absent: $path" >&2
    exit 1
  fi
}

test_remove_local_data_removes_known_paths() {
  local tmp
  tmp="$(mktemp -d)"

  (
    cd "$tmp"
    mkdir -p bridge data/repos web/node_modules web/dist bin
    : > .env
    : > bridge/.env

    assert_exists ".env" "pre .env"
    assert_exists "bridge/.env" "pre bridge env"
    assert_exists "data/repos" "pre repos"
    assert_exists "web/node_modules" "pre node_modules"
    assert_exists "web/dist" "pre dist"
    assert_exists "bin" "pre bin"

    remove_local_data

    assert_not_exists ".env" "post .env"
    assert_not_exists "bridge/.env" "post bridge env"
    assert_not_exists "data/repos" "post repos"
    assert_not_exists "web/node_modules" "post node_modules"
    assert_not_exists "web/dist" "post dist"
    assert_not_exists "bin" "post bin"
  )

  rm -rf "$tmp"
}

test_remove_cli_config_removes_linux_path() {
  local tmp
  tmp="$(mktemp -d)"

  (
    HOME="$tmp/home"
    mkdir -p "$HOME/.config/otter"
    : > "$HOME/.config/otter/config.json"

    uname() {
      echo "Linux"
    }

    remove_cli_config
    assert_not_exists "$HOME/.config/otter" "linux cfg removed"
  )

  rm -rf "$tmp"
}

test_stop_bridge_no_process_is_noop() {
  (
    local kill_calls=0
    local tmp
    tmp="$(mktemp -d)"
    OTTERCAMP_RUNTIME_DIR="$tmp"
    pgrep() {
      return 1
    }
    kill() {
      kill_calls=$((kill_calls + 1))
      return 0
    }

    stop_bridge

    if [[ "$kill_calls" -ne 0 ]]; then
      echo "expected kill not to be called when bridge pgrep returns no pids" >&2
      exit 1
    fi

    rm -rf "$tmp"
  )
}

test_stop_server_stops_pidfile_process_and_removes_pidfile() {
  local tmp pid
  tmp="$(mktemp -d)"
  OTTERCAMP_RUNTIME_DIR="$tmp"

  sleep 30 &
  pid="$!"
  echo "$pid" > "$tmp/ottercamp-server.pid"

  stop_server

  if kill -0 "$pid" 2>/dev/null; then
    echo "expected server pid $pid to be stopped" >&2
    kill "$pid" 2>/dev/null || true
    exit 1
  fi

  assert_not_exists "$tmp/ottercamp-server.pid" "server pid removed"
  rm -rf "$tmp"
}

test_cleanup_runtime_artifacts_removes_pid_and_log_files() {
  local tmp
  tmp="$(mktemp -d)"
  OTTERCAMP_RUNTIME_DIR="$tmp"

  : > "$tmp/ottercamp-server.pid"
  : > "$tmp/ottercamp-bridge.pid"
  : > "$tmp/ottercamp-server.log"
  : > "$tmp/ottercamp-server.error.log"
  : > "$tmp/ottercamp-bridge.log"
  : > "$tmp/ottercamp-bridge.error.log"

  cleanup_runtime_artifacts

  assert_not_exists "$tmp/ottercamp-server.pid" "server pid removed"
  assert_not_exists "$tmp/ottercamp-bridge.pid" "bridge pid removed"
  assert_not_exists "$tmp/ottercamp-server.log" "server log removed"
  assert_not_exists "$tmp/ottercamp-server.error.log" "server error log removed"
  assert_not_exists "$tmp/ottercamp-bridge.log" "bridge log removed"
  assert_not_exists "$tmp/ottercamp-bridge.error.log" "bridge error log removed"
  rm -rf "$tmp"
}

main() {
  test_remove_local_data_removes_known_paths
  test_remove_cli_config_removes_linux_path
  test_stop_bridge_no_process_is_noop
  test_stop_server_stops_pidfile_process_and_removes_pidfile
  test_cleanup_runtime_artifacts_removes_pid_and_log_files
  echo "uninstall.sh tests passed"
}

main "$@"
