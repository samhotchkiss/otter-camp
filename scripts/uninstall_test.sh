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
  local kill_calls=0
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
}

main() {
  test_remove_local_data_removes_known_paths
  test_remove_cli_config_removes_linux_path
  test_stop_bridge_no_process_is_noop
  echo "uninstall.sh tests passed"
}

main "$@"
