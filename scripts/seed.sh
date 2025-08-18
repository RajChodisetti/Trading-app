#!/usr/bin/env bash
set -euo pipefail

# Seed fixtures into local stub services.
# Defaults expect the Go stub server from cmd/stubs/main.go:
#   :8081 -> /halts
#   :8082 -> /news and /earnings
#   :8083 -> /ticks
#
# Usage:
#   scripts/seed.sh                 # seeds "all" fixtures
#   scripts/seed.sh core            # only halts/news/ticks
#   scripts/seed.sh extra           # only the advanced scenarios
#   scripts/seed.sh all             # (default) everything
#   scripts/seed.sh --dry-run       # print what would happen
#   scripts/seed.sh --quiet         # reduce output noise
#
# Override endpoints with env vars if needed:
#   HALTS_URL, NEWS_URL, EARNINGS_URL, TICKS_URL
#
# Requires: curl (and optionally jq for pretty logs on your side)

# -------- Configurable endpoints --------
HALTS_URL="${HALTS_URL:-http://localhost:8081/halts}"
NEWS_URL="${NEWS_URL:-http://localhost:8082/news}"
EARNINGS_URL="${EARNINGS_URL:-http://localhost:8082/earnings}"
TICKS_URL="${TICKS_URL:-http://localhost:8083/ticks}"

# -------- CLI flags --------
DRY_RUN=0
QUIET=0
PROFILE="all"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run|-n) DRY_RUN=1 ;;
    --quiet|-q)   QUIET=1 ;;
    core|extra|all) PROFILE="$1" ;;
    *) echo "usage: $0 [core|extra|all] [--dry-run] [--quiet]"; exit 1 ;;
  esac
  shift
done

# -------- Helpers --------
have() { command -v "$1" >/dev/null 2>&1; }

red()   { printf "\033[31m%s\033[0m\n" "$*" ; }
green() { printf "\033[32m%s\033[0m\n" "$*" ; }
yellow(){ printf "\033[33m%s\033[0m\n" "$*" ; }
blue()  { printf "\033[34m%s\033[0m\n" "$*" ; }

info()  { [[ "$QUIET" -eq 1 ]] || echo "$@"; }
warn()  { [[ "$QUIET" -eq 1 ]] || yellow "$*"; }
err()   { red "$*" 1>&2; }

die()   { err "$*"; exit 1; }

need() {
  have "$1" || die "missing required tool: $1"
}

health_url_of() {
  # Turn "http://host:port/path" into "http://host:port/health"
  local url="$1"
  echo "$url" | sed -E 's#(https?://[^/]+)/?.*#\1/health#'
}

check_health() {
  local url="$1"
  local hurl; hurl="$(health_url_of "$url")"
  curl -fsS -m 2 "$hurl" >/dev/null 2>&1
}

post_json() {
  local url="$1"; shift
  local file="$1"; shift

  [[ -f "$file" ]] || { warn "[seed] missing file $file (skipping)"; return 0; }

  if [[ "$DRY_RUN" -eq 1 ]]; then
    info "[dry-run] POST $file  ->  $url"
    return 0
  fi

  if ! check_health "$url"; then
    warn "[seed] stub not healthy at $(health_url_of "$url") (skipping $file)"
    return 0
  fi

  info "[seed] POST $file -> $url"
  # retry a couple times; treat connection refused as retryable
  if curl -fsS -m 10 --retry-connrefused --retry 2 \
      -H 'Content-Type: application/json' \
      --data @"$file" \
      -X POST "$url" >/dev/null; then
    [[ "$QUIET" -eq 1 ]] || green "[ok] $file"
  else
    warn "[seed] failed to POST $file -> $url"
  fi
}

# -------- Fixtures --------
# Core fixtures (basic acceptance tests)
CORE_PAIRS=(
  "$HALTS_URL|fixtures/halts.json"
  "$NEWS_URL|fixtures/news.json"
  "$TICKS_URL|fixtures/ticks.json"
)

# Extra fixtures (advanced scenarios)
EXTRA_PAIRS=(
  "$NEWS_URL|fixtures/news_correction_supersedes.json"
  "$TICKS_URL|fixtures/ticks_after_hours_wide_spread.json"
  "$NEWS_URL|fixtures/news_pr_needs_corroboration.json"
  "$EARNINGS_URL|fixtures/earnings_calendar.json"
)

# -------- Start --------
need curl

blue "[seed] profile=$PROFILE dry-run=$DRY_RUN quiet=$QUIET"
blue "[seed] endpoints:"
blue "       HALTS_URL=$HALTS_URL"
blue "       NEWS_URL=$NEWS_URL"
blue "       EARNINGS_URL=$EARNINGS_URL"
blue "       TICKS_URL=$TICKS_URL"

case "$PROFILE" in
  core)
    PAIRS=("${CORE_PAIRS[@]}")
    ;;
  extra)
    PAIRS=("${EXTRA_PAIRS[@]}")
    ;;
  all)
    PAIRS=("${CORE_PAIRS[@]}" "${EXTRA_PAIRS[@]}")
    ;;
esac

# Ensure fixtures dir exists
[[ -d fixtures ]] || warn "[seed] fixtures/ directory not found in CWD: $(pwd)"

# Do the posts
for entry in "${PAIRS[@]}"; do
  url="${entry%%|*}"
  file="${entry##*|}"
  post_json "$url" "$file"
done

green "[seed] done."
