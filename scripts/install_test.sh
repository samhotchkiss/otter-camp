#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/install.sh"

assert_equals() {
	local got="$1"
	local want="$2"
	local label="$3"
	if [[ "$got" != "$want" ]]; then
		echo "assertion failed ($label): got '$got' want '$want'" >&2
		exit 1
	fi
}

test_choose_install_dir_prefers_writable_primary() {
	local primary
	primary="$(mktemp -d)"
	local fallback
	fallback="$(mktemp -d)"

	local got
	got="$(choose_install_dir "$primary" "$fallback")"
	assert_equals "$got" "$primary" "prefer primary"
}

test_choose_install_dir_falls_back_when_primary_not_writable() {
	local parent
	parent="$(mktemp -d)"
	local primary="$parent/primary"
	mkdir -p "$primary"
	chmod 0555 "$primary"
	local fallback
	fallback="$(mktemp -d)/fallback"

	local got
	got="$(choose_install_dir "$primary" "$fallback")"
	assert_equals "$got" "$fallback" "fallback path"
	if [[ ! -d "$fallback" ]]; then
		echo "expected fallback dir to be created: $fallback" >&2
		exit 1
	fi
}

test_run_auth_login_invokes_otter_with_expected_args() {
	local tmp
	tmp="$(mktemp -d)"
	local log="$tmp/auth.log"
	local stub="$tmp/otter"
	cat >"$stub" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >> "$log"
EOF
	chmod +x "$stub"

	run_auth_login "$stub" "tok_123" "org_123" "https://api.example.com"
	local line
	line="$(tail -n 1 "$log")"
	assert_equals "$line" "auth login --token tok_123 --org org_123 --api https://api.example.com" "auth args"
}

test_run_auth_login_skips_when_token_or_org_missing() {
	local tmp
	tmp="$(mktemp -d)"
	local log="$tmp/auth.log"
	local stub="$tmp/otter"
	cat >"$stub" <<EOF
#!/usr/bin/env bash
printf '%s\n' "\$*" >> "$log"
EOF
	chmod +x "$stub"

	run_auth_login "$stub" "" "org_123" ""
	run_auth_login "$stub" "tok_123" "" ""
	if [[ -f "$log" ]]; then
		echo "expected no auth command invocation when token/org missing" >&2
		exit 1
	fi
}

main() {
	test_choose_install_dir_prefers_writable_primary
	test_choose_install_dir_falls_back_when_primary_not_writable
	test_run_auth_login_invokes_otter_with_expected_args
	test_run_auth_login_skips_when_token_or_org_missing
	echo "install.sh tests passed"
}

main "$@"
