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

main() {
  test_ensure_dependency_skips_install_when_already_present
  test_ensure_dependency_installs_when_missing_and_confirmed
  test_ensure_dependency_fails_when_user_declines_install
  echo "setup.sh tests passed"
}

main "$@"
