#!/usr/bin/env bash
set -euo pipefail

# Minimal placeholder "replay".
# Later, this should call your real replay binary (e.g., ./cmd/replay).
# For now, it prints the fixtures so you can confirm file shapes.

color_jq() {
  if command -v jq >/dev/null 2>&1; then
    jq -C .
  else
    cat
  fi
}

echo "[replay] Showing fixtures:"
for f in fixtures/halts.json fixtures/news.json fixtures/ticks.json; do
  if [[ -f "$f" ]]; then
    echo "===== $f ====="
    cat "$f" | color_jq
    echo
  else
    echo "[replay] Missing $f"
  fi
done

echo "[replay] Replace this script to feed fixtures into your pipeline when ready."
