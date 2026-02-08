#!/usr/bin/env bash
set -euo pipefail

usage() {
	cat <<'EOF'
otter install script

Usage:
  scripts/install.sh [options]

Options:
  --bin-dir <dir>   Install directory override
  --token <token>   Auth token to configure via `otter auth login`
  --org <org-id>    Default org id for auth setup
  --api <url>       API base URL for auth setup
  --skip-auth       Skip auth setup
  --help            Show this help text

Environment fallbacks:
  OTTER_TOKEN, OTTER_ORG, OTTER_API
EOF
}

script_dir() {
	cd "$(dirname "${BASH_SOURCE[0]}")" && pwd
}

repo_root() {
	cd "$(script_dir)/.." && pwd
}

choose_install_dir() {
	local primary="$1"
	local fallback="$2"

	if [[ -d "$primary" && -w "$primary" ]]; then
		echo "$primary"
		return 0
	fi

	if [[ ! -e "$primary" ]] && mkdir -p "$primary" 2>/dev/null && [[ -w "$primary" ]]; then
		echo "$primary"
		return 0
	fi

	mkdir -p "$fallback"
	echo "$fallback"
}

path_contains_dir() {
	local dir="$1"
	case ":$PATH:" in
	*":$dir:"*) return 0 ;;
	*) return 1 ;;
	esac
}

build_otter_binary() {
	local root="$1"
	local output="$2"
	(
		cd "$root"
		go build -o "$output" ./cmd/otter
	)
}

run_auth_login() {
	local otter_bin="$1"
	local token="$2"
	local org="$3"
	local api="${4:-}"

	if [[ -z "$token" || -z "$org" ]]; then
		return 0
	fi

	local args=(auth login --token "$token" --org "$org")
	if [[ -n "$api" ]]; then
		args+=(--api "$api")
	fi
	"$otter_bin" "${args[@]}"
}

main() {
	local install_dir=""
	local token="${OTTER_TOKEN:-}"
	local org="${OTTER_ORG:-}"
	local api="${OTTER_API:-}"
	local skip_auth=0

	while (($# > 0)); do
		case "$1" in
		--bin-dir)
			[[ $# -ge 2 ]] || {
				echo "missing value for --bin-dir" >&2
				exit 1
			}
			install_dir="$2"
			shift 2
			;;
		--token)
			[[ $# -ge 2 ]] || {
				echo "missing value for --token" >&2
				exit 1
			}
			token="$2"
			shift 2
			;;
		--org)
			[[ $# -ge 2 ]] || {
				echo "missing value for --org" >&2
				exit 1
			}
			org="$2"
			shift 2
			;;
		--api)
			[[ $# -ge 2 ]] || {
				echo "missing value for --api" >&2
				exit 1
			}
			api="$2"
			shift 2
			;;
		--skip-auth)
			skip_auth=1
			shift
			;;
		--help | -h)
			usage
			exit 0
			;;
		*)
			echo "unknown option: $1" >&2
			usage >&2
			exit 1
			;;
		esac
	done

	if [[ -z "$install_dir" ]]; then
		install_dir="$(choose_install_dir "/usr/local/bin" "$HOME/.local/bin")"
	else
		mkdir -p "$install_dir"
	fi

	local target="$install_dir/otter"
	local tmp_bin
	tmp_bin="$(mktemp "${TMPDIR:-/tmp}/otter.XXXXXX")"
	trap 'rm -f "$tmp_bin"' EXIT

	echo "Building otter CLI..."
	build_otter_binary "$(repo_root)" "$tmp_bin"
	chmod +x "$tmp_bin"
	mv "$tmp_bin" "$target"
	trap - EXIT

	echo "Installed otter to $target"
	if ! path_contains_dir "$install_dir"; then
		echo "Add $install_dir to PATH to run 'otter' directly."
	fi

	if [[ "$skip_auth" -eq 1 ]]; then
		echo "Skipped auth setup."
	else
		run_auth_login "$target" "$token" "$org" "$api"
		if [[ -n "$token" && -n "$org" ]]; then
			echo "Configured auth with provided token and org."
		else
			echo "Run: otter auth login --token <your-token> --org <org-id>"
		fi
	fi

	echo "Run 'otter whoami' to verify."
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	main "$@"
fi
