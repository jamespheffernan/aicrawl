#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/live-provider-smoke.sh --provider chatgpt|claude --cdp-url http://127.0.0.1:9222 [--mode dry-run|write]
  scripts/live-provider-smoke.sh --provider chatgpt --cdp-url http://127.0.0.1:9222 --chatgpt-app-cache ~/Library/Application\ Support/com.openai.chat [--mode dry-run|write]
  scripts/live-provider-smoke.sh --provider chatgpt|claude --profile /path/to/profile [--browser /path/to/browser] [--mode dry-run|write]

Options:
  --provider <id>             chatgpt or claude.
  --cdp-url <url>             Chrome DevTools HTTP endpoint for an already open logged-in provider page.
  --profile <dir>             Dedicated browser profile path for profile-launch smoke.
  --browser <path>            Browser executable for profile-launch smoke.
  --remote-debugging-port <n> Remote debugging port for profile-launch smoke.
  --chatgpt-app-cache <dir>   ChatGPT macOS app cache root; discovers conversation IDs from filenames only.
  --max-conversations <n>     Bounded candidate/detail count. Default: 1.
  --mode <dry-run|write>      dry-run counts list candidates; write imports bounded details into temp archive. Default: dry-run.
  --keep-output               Keep temp HOME and JSON output; prints the temp path.
  -h, --help                  Show this help.

The script never prints message bodies. It builds the current checkout, uses an
isolated temp HOME/XDG runtime, and summarizes only aggregate JSON fields.
EOF
}

provider=""
cdp_url=""
profile=""
browser=""
remote_debugging_port=""
chatgpt_app_cache=""
max_conversations="1"
mode="dry-run"
keep_output="0"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --provider)
      provider="${2:-}"
      shift 2
      ;;
    --cdp-url)
      cdp_url="${2:-}"
      shift 2
      ;;
    --profile)
      profile="${2:-}"
      shift 2
      ;;
    --browser)
      browser="${2:-}"
      shift 2
      ;;
    --remote-debugging-port)
      remote_debugging_port="${2:-}"
      shift 2
      ;;
    --chatgpt-app-cache)
      chatgpt_app_cache="${2:-}"
      shift 2
      ;;
    --max-conversations)
      max_conversations="${2:-}"
      shift 2
      ;;
    --mode)
      mode="${2:-}"
      shift 2
      ;;
    --keep-output)
      keep_output="1"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$provider" in
  chatgpt|claude) ;;
  "")
    echo "--provider is required" >&2
    usage >&2
    exit 2
    ;;
  *)
    echo "--provider must be chatgpt or claude" >&2
    exit 2
    ;;
esac

case "$mode" in
  dry-run|write) ;;
  *)
    echo "--mode must be dry-run or write" >&2
    exit 2
    ;;
esac

if [[ -z "$cdp_url" && -z "$profile" ]]; then
  echo "either --cdp-url or --profile is required" >&2
  exit 2
fi
if [[ -n "$chatgpt_app_cache" && "$provider" != "chatgpt" ]]; then
  echo "--chatgpt-app-cache is only supported with --provider chatgpt" >&2
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
smoke_dir="$(mktemp -d)"

cleanup() {
  if [[ "$keep_output" != "1" ]]; then
    rm -rf "$smoke_dir"
  fi
}
trap cleanup EXIT

bin="$smoke_dir/aicrawl"
out="$smoke_dir/${provider}-${mode}.json"
err="$smoke_dir/${provider}-${mode}.stderr"
home="$smoke_dir/home"
mkdir -p "$home"

(
  cd "$repo_root"
  GOWORK=off go build -o "$bin" ./cmd/aicrawl
)

export HOME="$home"
export XDG_CONFIG_HOME="$home/.config"
export XDG_CACHE_HOME="$home/.cache"
export XDG_DATA_HOME="$home/.local/share"
export XDG_STATE_HOME="$home/.local/state"

cmd=("$bin" sync web --provider "$provider" --max-conversations "$max_conversations" --json)
if [[ -n "$cdp_url" ]]; then
  cmd+=(--cdp-url "$cdp_url")
else
  cmd+=(--profile "$profile")
fi
if [[ -n "$browser" ]]; then
  cmd+=(--browser "$browser")
fi
if [[ -n "$remote_debugging_port" ]]; then
  cmd+=(--remote-debugging-port "$remote_debugging_port")
fi
if [[ -n "$chatgpt_app_cache" ]]; then
  cmd+=(--chatgpt-app-cache "$chatgpt_app_cache")
fi
if [[ "$mode" == "dry-run" ]]; then
  cmd+=(--dry-run)
fi

if ! "${cmd[@]}" >"$out" 2>"$err"; then
  echo "live ${provider} ${mode} smoke failed" >&2
  if [[ -s "$err" ]]; then
    sed -n '1,40p' "$err" >&2
  fi
  if [[ "$keep_output" == "1" ]]; then
    echo "temp output: $smoke_dir" >&2
  fi
  exit 1
fi

python3 - "$out" "$mode" "$provider" <<'PY'
import json
import sys

path, mode, provider = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path, "r", encoding="utf-8") as f:
    data = json.load(f)

print(f"provider {provider}")
print(f"mode {mode}")
if mode == "dry-run":
    source = data.get("source") or {}
    freshness = data.get("freshness") or {}
    print("auth_state", data.get("auth_state", ""))
    print("endpoint_contract_state", data.get("endpoint_contract_state", ""))
    print("freshness_state", freshness.get("state", ""))
    print("source_kind", source.get("kind", ""))
    print("candidate_conversations", source.get("conversations", 0))
    print("warnings", len(data.get("warnings") or []) + len(source.get("warnings") or []))
else:
    print("source_kind", data.get("source_kind", ""))
    print("conversations", data.get("conversations", 0))
    print("messages", data.get("messages", 0))
    print("attachments", data.get("attachments", 0))
    print("already_imported", data.get("already_imported", False))
    print("warnings", len(data.get("warnings") or []))
PY

if [[ "$keep_output" == "1" ]]; then
  echo "temp output: $smoke_dir"
fi
