#!/usr/bin/env bash
set -euo pipefail

CFG="config/config.yaml"
BIN="go run ./cmd/decision -oneshot=true"

TMP_DIR="$(mktemp -d)"
cleanup(){ rm -rf "$TMP_DIR"; }
trap cleanup EXIT

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing $1"; exit 1; }; }
need jq
need go

run_case () {
  local name="$1" cfgfile="$2"
  echo "== Running: $name =="
  local out="$TMP_DIR/$name.out"
  local jsonl="$TMP_DIR/$name.jsonl"

  # Run the binary; ensure it exits (uses -oneshot=true by default)
  # Clear environment variables to allow config file control
  unset GLOBAL_PAUSE TRADING_MODE
  $BIN -config "$cfgfile" 2>"$TMP_DIR/$name.stderr" >"$out" || {
    echo "binary exited non-zero. stderr:"; cat "$TMP_DIR/$name.stderr"; exit 1;
  }

  # Extract decision events (don’t let pipefail kill us)
  grep -F '"event":"decision"' "$out" > "$jsonl" || true

  if [[ ! -s "$jsonl" ]]; then
    echo "No decision events found in $name. Full output was:"
    sed -n '1,120p' "$out"
    echo "(stderr:)"; sed -n '1,120p' "$TMP_DIR/$name.stderr"
    exit 1
  fi

  # Summaries
  intents="$(jq -sr 'map(select(.event=="decision") | {(.symbol): .intent}) | add' "$jsonl")"
  echo "intents: $intents"
  gates="$(jq -sr 'map(select(.event=="decision") | { (.symbol): (.reason.gates_blocked // []) }) | add' "$jsonl")"
  echo "gates_blocked: $gates"
}


# --- Case 1: paused (use config as-is) ---
run_case "paused" "$CFG"

# Assertions
aapl_intent=$(jq -r 'select(.event=="decision" and .symbol=="AAPL") | .intent' "$TMP_DIR/paused.jsonl")
nvda_intent=$(jq -r 'select(.event=="decision" and .symbol=="NVDA") | .intent' "$TMP_DIR/paused.jsonl")
[[ "$aapl_intent" == "REJECT" ]] || { echo "FAIL(paused): AAPL intent=$aapl_intent, expected REJECT"; exit 1; }
[[ "$nvda_intent" == "REJECT" ]] || { echo "FAIL(paused): NVDA intent=$nvda_intent, expected REJECT"; exit 1; }

# --- Case 2: resumed (global_pause=false) ---
RESUMED="$TMP_DIR/config.resumed.yaml"
# flip the global_pause line; keep a backup with sed if needed
sed 's/global_pause: true/global_pause: false/' "$CFG" > "$RESUMED"

run_case "resumed" "$RESUMED"

# Assertions
aapl_intent=$(jq -r 'select(.event=="decision" and .symbol=="AAPL") | .intent' "$TMP_DIR/resumed.jsonl")
nvda_intent=$(jq -r 'select(.event=="decision" and .symbol=="NVDA") | .intent' "$TMP_DIR/resumed.jsonl")
[[ "$aapl_intent" == "BUY_1X" || "$aapl_intent" == "BUY_5X" ]] || { echo "FAIL(resumed): AAPL intent=$aapl_intent, expected BUY_*"; exit 1; }
[[ "$nvda_intent" == "REJECT" ]] || { echo "FAIL(resumed): NVDA intent=$nvda_intent, expected REJECT (halt)"; exit 1; }

# --- Case 3: after-hours + wide spread ---
# Use resumed config but with after-hours fixture data
cp "fixtures/ticks_after_hours_wide_spread.json" "fixtures/ticks.json"
run_case "after_hours" "$RESUMED"
# Restore original ticks
git checkout fixtures/ticks.json 2>/dev/null || cp "fixtures/ticks.backup.json" "fixtures/ticks.json" 2>/dev/null || true

# Assertions - should be REJECT due to session/liquidity gates
aapl_intent=$(jq -r 'select(.event=="decision" and .symbol=="AAPL") | .intent' "$TMP_DIR/after_hours.jsonl")
aapl_gates=$(jq -r 'select(.event=="decision" and .symbol=="AAPL") | .reason.gates_blocked[]?' "$TMP_DIR/after_hours.jsonl" | sort)
[[ "$aapl_intent" == "REJECT" ]] || { echo "FAIL(after_hours): AAPL intent=$aapl_intent, expected REJECT"; exit 1; }
echo "$aapl_gates" | grep -q "session\|liquidity" || { echo "FAIL(after_hours): AAPL gates missing session/liquidity, got: $aapl_gates"; exit 1; }

# --- Case 4A: PR only (within corroboration window) ---
cp "fixtures/news_pr_only.json" "fixtures/news.json"
run_case "pr_only" "$RESUMED"

# Assertions - should be HOLD with corroboration gate
biox_intent=$(jq -r 'select(.event=="decision" and .symbol=="BIOX") | .intent' "$TMP_DIR/pr_only.jsonl")
biox_gates=$(jq -r 'select(.event=="decision" and .symbol=="BIOX") | .reason.gates_blocked[]?' "$TMP_DIR/pr_only.jsonl")
[[ "$biox_intent" == "HOLD" ]] || { echo "FAIL(pr_only): BIOX intent=$biox_intent, expected HOLD"; exit 1; }
echo "$biox_gates" | grep -q "corroboration" || { echo "FAIL(pr_only): BIOX gates missing corroboration, got: $biox_gates"; exit 1; }

# --- Case 4B: PR + editorial within window ---
cp "fixtures/news_pr_plus_editorial.json" "fixtures/news.json"
run_case "pr_plus_editorial" "$RESUMED"

# Assertions - should be BUY with no corroboration gate
biox_intent=$(jq -r 'select(.event=="decision" and .symbol=="BIOX") | .intent' "$TMP_DIR/pr_plus_editorial.jsonl")
biox_gates=$(jq -r 'select(.event=="decision" and .symbol=="BIOX") | .reason.gates_blocked[]?' "$TMP_DIR/pr_plus_editorial.jsonl")
[[ "$biox_intent" == "BUY_1X" || "$biox_intent" == "BUY_5X" ]] || { echo "FAIL(pr_plus_editorial): BIOX intent=$biox_intent, expected BUY_*"; exit 1; }
echo "$biox_gates" | grep -q "corroboration" && { echo "FAIL(pr_plus_editorial): BIOX should not have corroboration gate, got: $biox_gates"; exit 1; } || true

# --- Case 4C: PR then editorial after window ---
cp "fixtures/news_pr_then_late_editorial.json" "fixtures/news.json"
run_case "pr_late_editorial" "$RESUMED"

# Assertions - should be HOLD (PR weight ignored after window expiry)
biox_intent=$(jq -r 'select(.event=="decision" and .symbol=="BIOX") | .intent' "$TMP_DIR/pr_late_editorial.jsonl")
[[ "$biox_intent" == "HOLD" ]] || { echo "FAIL(pr_late_editorial): BIOX intent=$biox_intent, expected HOLD"; exit 1; }

# Restore original news fixture
git checkout fixtures/news.json 2>/dev/null || cp "fixtures/news.backup.json" "fixtures/news.json" 2>/dev/null || true

# --- Case 5: Earnings embargo ---
# Create clean market conditions (no session/liquidity issues) for earnings embargo test
CLEAN_TICKS="$TMP_DIR/ticks_clean.json"
cat > "$CLEAN_TICKS" <<'EOF'
{
  "ticks": [
    {
      "ts_utc": "2025-08-17T19:35:00Z",
      "symbol": "AAPL",
      "bid": 210.00,
      "ask": 210.02,
      "last": 210.01,
      "vwap_5m": 209.80,
      "rel_volume": 0.8,
      "halted": false,
      "premarket": false,
      "postmarket": false
    },
    {
      "symbol": "NVDA",
      "bid": 450.00,
      "ask": 450.05,
      "last": 450.02,
      "vwap_5m": 449.80,
      "rel_volume": 0.7,
      "halted": true,
      "premarket": false,
      "postmarket": false
    },
    {
      "symbol": "BIOX",
      "bid": 15.00,
      "ask": 15.02,
      "last": 14.98,
      "vwap_5m": 15.02,
      "rel_volume": 0.6,
      "halted": false,
      "premarket": false,
      "postmarket": false
    }
  ]
}
EOF

# Create a custom earnings calendar with AAPL in embargo window (current time + some margin)
EARNINGS_EMBARGO="$TMP_DIR/earnings_embargo.json"
cat > "$EARNINGS_EMBARGO" <<'EOF'
{
  "earnings": [
    {
      "symbol": "AAPL",
      "start_utc": "2025-08-21T18:30:00Z",
      "end_utc": "2025-08-21T19:30:00Z",
      "status": "confirmed"
    }
  ]
}
EOF

# Backup original ticks and use clean ticks
cp "fixtures/ticks.json" "$TMP_DIR/ticks.backup.json" 2>/dev/null || true
cp "$CLEAN_TICKS" "fixtures/ticks.json"

echo "== Running: earnings_embargo =="
$BIN -config "$RESUMED" -earnings "$EARNINGS_EMBARGO" >"$TMP_DIR/earnings_embargo.out" 2>&1 || {
  echo "binary exited non-zero for earnings embargo test. Output:"; cat "$TMP_DIR/earnings_embargo.out"; exit 1;
}

# Extract decision events for earnings embargo test
grep -F '"event":"decision"' "$TMP_DIR/earnings_embargo.out" > "$TMP_DIR/earnings_embargo.jsonl" || true

if [[ ! -s "$TMP_DIR/earnings_embargo.jsonl" ]]; then
  echo "No decision events found in earnings embargo test. Full output was:"
  sed -n '1,120p' "$TMP_DIR/earnings_embargo.out"
  exit 1
fi

# Show results summary
intents="$(jq -sr 'map(select(.event=="decision") | {(.symbol): .intent}) | add' "$TMP_DIR/earnings_embargo.jsonl")"
echo "intents: $intents"
gates="$(jq -sr 'map(select(.event=="decision") | { (.symbol): (.reason.gates_blocked // []) }) | add' "$TMP_DIR/earnings_embargo.jsonl")"
echo "gates_blocked: $gates"

# Assertions - AAPL should be HOLD due to earnings embargo (instead of BUY)
aapl_intent=$(jq -r 'select(.event=="decision" and .symbol=="AAPL") | .intent' "$TMP_DIR/earnings_embargo.jsonl")
aapl_gates=$(jq -r 'select(.event=="decision" and .symbol=="AAPL") | .reason.gates_blocked[]?' "$TMP_DIR/earnings_embargo.jsonl")
[[ "$aapl_intent" == "HOLD" ]] || { echo "FAIL(earnings_embargo): AAPL intent=$aapl_intent, expected HOLD"; exit 1; }
echo "$aapl_gates" | grep -q "earnings_embargo" || { echo "FAIL(earnings_embargo): AAPL gates missing earnings_embargo, got: $aapl_gates"; exit 1; }

# Restore original ticks fixture
cp "$TMP_DIR/ticks.backup.json" "fixtures/ticks.json" 2>/dev/null || git checkout fixtures/ticks.json 2>/dev/null || true

echo "OK: all tests passed."
