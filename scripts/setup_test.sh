#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/setup.sh"

assert_equals() {
  local got="$1"
  local want="$2"
  local label="$3"
  if [[ "$got" != "$want" ]]; then
    echo "assertion failed ($label): got '$got' want '$want'" >&2
    exit 1
  fi
}

test_ensure_dependency_skips_install_when_already_present() {
  local install_calls=0

  check_dependency() {
    [[ "$1" == "go" ]]
  }
  prompt_yes_no() {
    return 0
  }
  install_dependency() {
    install_calls=$((install_calls + 1))
    return 0
  }

  ensure_dependency "go" "linux" 0 1
  assert_equals "$install_calls" "0" "skip install when present"
}

test_ensure_dependency_installs_when_missing_and_confirmed() {
  local install_calls=0
  local present_after_install=0

  check_dependency() {
    if [[ "$present_after_install" -eq 1 ]]; then
      return 0
    fi
    return 1
  }
  prompt_yes_no() {
    return 0
  }
  install_dependency() {
    install_calls=$((install_calls + 1))
    present_after_install=1
    return 0
  }

  ensure_dependency "node" "linux" 0 1
  assert_equals "$install_calls" "1" "install called once"
}

test_ensure_dependency_fails_when_user_declines_install() {
  check_dependency() {
    return 1
  }
  prompt_yes_no() {
    return 1
  }
  install_dependency() {
    return 0
  }

  if ensure_dependency "git" "linux" 0 1; then
    echo "expected ensure_dependency to fail when install is declined" >&2
    exit 1
  fi
}

test_write_env_if_missing_sets_4200_defaults() {
  local tmp
  tmp="$(mktemp -d)"

  (
    cd "$tmp"
    write_env_if_missing
    [[ -f .env ]] || {
      echo "expected .env to be created" >&2
      exit 1
    }
    grep -q "^PORT=4200$" .env || {
      echo "expected PORT=4200 in .env" >&2
      exit 1
    }
    grep -q "^VITE_API_URL=$" .env || {
      echo "expected empty VITE_API_URL in .env" >&2
      exit 1
    }
  )
}

test_write_bridge_env_if_missing_targets_4200() {
  local tmp
  tmp="$(mktemp -d)"

  (
    cd "$tmp"
    OPENCLAW_SYNC_SECRET="sync-test"
    OPENCLAW_WS_SECRET="ws-test"
    write_bridge_env_if_missing
    [[ -f bridge/.env ]] || {
      echo "expected bridge/.env to be created" >&2
      exit 1
    }
    grep -q "^OTTERCAMP_URL=http://localhost:4200$" bridge/.env || {
      echo "expected OTTERCAMP_URL=http://localhost:4200 in bridge/.env" >&2
      exit 1
    }
  )
}

test_run_bootstrap_steps_dry_run_lists_core_commands() {
  local output
  output="$(
    DRY_RUN=1
    start_postgres_if_needed() { return 0; }
    write_cli_config() { return 0; }
    write_bridge_env_if_missing() { return 0; }
    pull_ollama_model() { return 0; }
    run_bootstrap_steps 2>&1
  )"

  [[ "$output" == *"go run ./cmd/migrate up"* ]] || {
    echo "expected dry-run migration command output" >&2
    exit 1
  }
  [[ "$output" == *"npm ci"* ]] || {
    echo "expected dry-run npm install command output" >&2
    exit 1
  }
  [[ "$output" == *"go run ./scripts/seed/seed.go"* ]] || {
    echo "expected dry-run seed command output" >&2
    exit 1
  }
  [[ "$output" == *"make prod-local"* ]] || {
    echo "expected dry-run build command output" >&2
    exit 1
  }
}

main() {
  test_ensure_dependency_skips_install_when_already_present
  test_ensure_dependency_installs_when_missing_and_confirmed
  test_ensure_dependency_fails_when_user_declines_install
  test_write_env_if_missing_sets_4200_defaults
  test_write_bridge_env_if_missing_targets_4200
  test_run_bootstrap_steps_dry_run_lists_core_commands
  echo "setup.sh tests passed"
}

main "$@"
