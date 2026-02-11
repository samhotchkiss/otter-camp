#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/setup.sh"

TMP_DIRS=()

track_tmp_dir() {
  local tmp
  tmp="$(mktemp -d)"
  TMP_DIRS+=("$tmp")
  echo "$tmp"
}

cleanup_tmp_dirs() {
  local dir
  for dir in "${TMP_DIRS[@]-}"; do
    if [[ -n "$dir" ]]; then
      rm -rf "$dir"
    fi
  done
  return 0
}

trap cleanup_tmp_dirs EXIT

setup_main() {
  main "$@"
}

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
  tmp="$(track_tmp_dir)"

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
  tmp="$(track_tmp_dir)"

  (
    cd "$tmp"
    OPENCLAW_SYNC_SECRET="sync-test"
    OPENCLAW_WS_SECRET="ws-test"
    PORT=4200
    write_bridge_env_if_missing
    [[ -f bridge/.env ]] || {
      echo "expected bridge/.env to be created" >&2
      exit 1
    }
    grep -q "OTTERCAMP_URL=http://localhost:4200" bridge/.env || {
      echo "expected OTTERCAMP_URL containing localhost:4200 in bridge/.env" >&2
      exit 1
    }
  )
}

test_install_dependency_brew_dry_run_avoids_network_eval() {
  local tmp marker
  tmp="$(track_tmp_dir)"
  marker="$tmp/curl-called"
  cat > "$tmp/curl" <<'EOF'
#!/usr/bin/env bash
echo "curl invoked" >&2
touch "${SETUP_TEST_CURL_MARKER}"
printf 'echo install'
EOF
  chmod +x "$tmp/curl"

  (
    PATH="$tmp:$PATH"
    DRY_RUN=1
    export SETUP_TEST_CURL_MARKER="$marker"
    install_dependency brew macos
  )

  [[ ! -f "$marker" ]] || {
    echo "expected brew install dry-run not to invoke curl" >&2
    exit 1
  }
}

test_generate_secret_falls_back_without_openssl() {
  local tmp counter secret1 secret2
  tmp="$(track_tmp_dir)"
  counter="$tmp/counter"
  cat > "$tmp/openssl" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
  cat > "$tmp/head" <<'EOF'
#!/usr/bin/env bash
count=0
if [[ -f "${SETUP_TEST_SECRET_COUNTER}" ]]; then
  read -r count < "${SETUP_TEST_SECRET_COUNTER}"
fi
count=$((count + 1))
printf '%s\n' "$count" > "${SETUP_TEST_SECRET_COUNTER}"
printf 'fallback-secret-%02d' "$count"
EOF
  cat > "$tmp/xxd" <<'EOF'
#!/usr/bin/env bash
cat
EOF
  chmod +x "$tmp/openssl" "$tmp/head" "$tmp/xxd"

  secret1="$(
    PATH="$tmp:$PATH"
    export SETUP_TEST_SECRET_COUNTER="$counter"
    generate_secret
  )"
  secret2="$(
    PATH="$tmp:$PATH"
    export SETUP_TEST_SECRET_COUNTER="$counter"
    generate_secret
  )"

  [[ -n "$secret1" && -n "$secret2" ]] || {
    echo "expected generate_secret fallback to produce non-empty values" >&2
    exit 1
  }
  [[ "$secret1" != "$secret2" ]] || {
    echo "expected generate_secret fallback outputs to differ between calls" >&2
    exit 1
  }
}

test_load_env_treats_values_as_literals() {
  local tmp marker_cmd marker_backtick
  tmp="$(track_tmp_dir)"
  marker_cmd="$tmp/cmd-subst-marker"
  marker_backtick="$tmp/backtick-marker"

  (
    cd "$tmp"
    {
      echo "SAFE=value"
      printf 'MALICIOUS=$(touch %s)\n' "$marker_cmd"
      printf 'BACKTICK=`touch %s`\n' "$marker_backtick"
    } > .env

    load_env

    [[ "$SAFE" == "value" ]] || {
      echo "expected SAFE variable to load" >&2
      exit 1
    }
    [[ "$MALICIOUS" == "\$(touch $marker_cmd)" ]] || {
      echo "expected MALICIOUS value to be literal" >&2
      exit 1
    }
    [[ "$BACKTICK" == "\`touch $marker_backtick\`" ]] || {
      echo "expected BACKTICK value to be literal" >&2
      exit 1
    }
    [[ ! -f "$marker_cmd" && ! -f "$marker_backtick" ]] || {
      echo "expected load_env not to execute command substitutions" >&2
      exit 1
    }
  )
}

test_load_env_rejects_invalid_entries() {
  local tmp
  tmp="$(track_tmp_dir)"

  (
    cd "$tmp"
    cat > .env <<'EOF'
VALID_KEY=value
NOT A VALID LINE
EOF
    if load_env; then
      echo "expected load_env to reject malformed entries" >&2
      exit 1
    fi
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

test_main_completion_mentions_init_and_dashboard() {
  local output
  output="$(
    detect_platform() { echo "linux"; }
    ensure_dependencies() { return 0; }
    write_env_if_missing() { return 0; }
    load_env() { return 0; }
    run_bootstrap_steps() { return 0; }
    setup_main --dry-run --yes 2>&1 || true
  )"

  [[ "$output" == *"Run: otter init"* ]] || {
    echo "expected setup completion to mention otter init" >&2
    exit 1
  }
  [[ "$output" == *"Dashboard: http://localhost:4200"* ]] || {
    echo "expected setup completion to mention localhost:4200 dashboard" >&2
    exit 1
  }
}

run_tests() {
  test_ensure_dependency_skips_install_when_already_present
  test_ensure_dependency_installs_when_missing_and_confirmed
  test_ensure_dependency_fails_when_user_declines_install
  test_write_env_if_missing_sets_4200_defaults
  test_write_bridge_env_if_missing_targets_4200
  test_install_dependency_brew_dry_run_avoids_network_eval
  test_generate_secret_falls_back_without_openssl
  test_load_env_treats_values_as_literals
  test_load_env_rejects_invalid_entries
  test_run_bootstrap_steps_dry_run_lists_core_commands
  test_main_completion_mentions_init_and_dashboard
  echo "setup.sh tests passed"
}

run_tests "$@"
