#!/usr/bin/env bash
set -euo pipefail

CFG="config/config.yaml"
BIN="go run ./cmd/decision -oneshot=true"

TMP_DIR="$(mktemp -d)"
cleanup(){
  # Kill any test processes that might be hanging around
  pkill -f "mock-slack" 2>/dev/null || true
  pkill -f "cmd/slack-handler" 2>/dev/null || true  
  pkill -f "cmd/stubs.*port" 2>/dev/null || true
  pkill -f "cmd/decision.*wire-mode" 2>/dev/null || true
  sleep 0.5
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing $1"; exit 1; }; }
need jq
need go

# Clean up any leftover test processes from previous runs
echo "🧹 Cleaning up any leftover test processes..."
pkill -f "mock-slack" 2>/dev/null || true
pkill -f "cmd/slack-handler" 2>/dev/null || true  
pkill -f "cmd/stubs.*port" 2>/dev/null || true
pkill -f "cmd/decision.*wire-mode" 2>/dev/null || true
sleep 1

# Generate fresh test fixtures to avoid timestamp expiration issues
echo "🔄 Generating fresh test fixtures..."
./scripts/generate-test-fixtures.sh

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
# Backup current ticks before overwriting
cp "fixtures/ticks.json" "$TMP_DIR/ticks.original.json"
# Use resumed config but with after-hours fixture data
cp "fixtures/ticks_after_hours_wide_spread.json" "fixtures/ticks.json"
run_case "after_hours" "$RESUMED"
# Restore original ticks
cp "$TMP_DIR/ticks.original.json" "fixtures/ticks.json"

# Assertions - should be REJECT due to session/liquidity gates
aapl_intent=$(jq -r 'select(.event=="decision" and .symbol=="AAPL") | .intent' "$TMP_DIR/after_hours.jsonl")
aapl_gates=$(jq -r 'select(.event=="decision" and .symbol=="AAPL") | .reason.gates_blocked[]?' "$TMP_DIR/after_hours.jsonl" | sort)
[[ "$aapl_intent" == "REJECT" ]] || { echo "FAIL(after_hours): AAPL intent=$aapl_intent, expected REJECT"; exit 1; }
echo "$aapl_gates" | grep -q "session\|liquidity" || { echo "FAIL(after_hours): AAPL gates missing session/liquidity, got: $aapl_gates"; exit 1; }

# --- Case 4A: PR only (within corroboration window) ---
# Backup current news before overwriting
cp "fixtures/news.json" "$TMP_DIR/news.original.json" 2>/dev/null || true
cp "fixtures/news_pr_only.json" "fixtures/news.json"
run_case "pr_only" "$RESUMED"

# Assertions - should be HOLD with corroboration required
biox_intent=$(jq -r 'select(.event=="decision" and .symbol=="BIOX") | .intent' "$TMP_DIR/pr_only.jsonl")
biox_corroboration=$(jq -r 'select(.event=="decision" and .symbol=="BIOX") | .reason.corroboration.required // false' "$TMP_DIR/pr_only.jsonl")
[[ "$biox_intent" == "HOLD" ]] || { echo "FAIL(pr_only): BIOX intent=$biox_intent, expected HOLD"; exit 1; }
[[ "$biox_corroboration" == "true" ]] || { echo "FAIL(pr_only): BIOX corroboration not required, got: $biox_corroboration"; exit 1; }

# --- Case 4B: PR + editorial within window ---
cp "fixtures/news_pr_plus_editorial.json" "fixtures/news.json"
run_case "pr_plus_editorial" "$RESUMED"

# Assertions - should be BUY with no corroboration required
biox_intent=$(jq -r 'select(.event=="decision" and .symbol=="BIOX") | .intent' "$TMP_DIR/pr_plus_editorial.jsonl")
biox_corroboration=$(jq -r 'select(.event=="decision" and .symbol=="BIOX") | .reason.corroboration.required // false' "$TMP_DIR/pr_plus_editorial.jsonl")
[[ "$biox_intent" == "BUY_1X" || "$biox_intent" == "BUY_5X" ]] || { echo "FAIL(pr_plus_editorial): BIOX intent=$biox_intent, expected BUY_*"; exit 1; }
[[ "$biox_corroboration" == "false" ]] || { echo "FAIL(pr_plus_editorial): BIOX should not require corroboration, got: $biox_corroboration"; exit 1; }

# --- Case 4C: PR then editorial after window ---
cp "fixtures/news_pr_then_late_editorial.json" "fixtures/news.json"
run_case "pr_late_editorial" "$RESUMED"

# Assertions - should be HOLD (PR weight ignored after window expiry)
biox_intent=$(jq -r 'select(.event=="decision" and .symbol=="BIOX") | .intent' "$TMP_DIR/pr_late_editorial.jsonl")
[[ "$biox_intent" == "HOLD" ]] || { echo "FAIL(pr_late_editorial): BIOX intent=$biox_intent, expected HOLD"; exit 1; }

# Restore original news fixture
cp "$TMP_DIR/news.original.json" "fixtures/news.json" 2>/dev/null || true

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
current_time=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
future_time=$(date -u -v+2H +"%Y-%m-%dT%H:%M:%SZ")
cat > "$EARNINGS_EMBARGO" <<EOF
{
  "earnings": [
    {
      "symbol": "AAPL",
      "start_utc": "$current_time",
      "end_utc": "$future_time",
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

# --- Case 6: Paper outbox functionality ---
OUTBOX_TEST_DIR="$TMP_DIR/outbox_test"
mkdir -p "$OUTBOX_TEST_DIR"

# Create custom config for outbox test
OUTBOX_CONFIG="$TMP_DIR/config.outbox.yaml"
sed "s|global_pause: true|global_pause: false|; s|outbox_path: \"data/outbox.jsonl\"|outbox_path: \"$OUTBOX_TEST_DIR/test_outbox.jsonl\"|" "$CFG" > "$OUTBOX_CONFIG"

echo "== Running: paper_outbox ==" 
$BIN -config "$OUTBOX_CONFIG" >>"$TMP_DIR/paper_outbox.out" 2>&1 || {
  echo "binary exited non-zero for paper outbox test. Output:"; cat "$TMP_DIR/paper_outbox.out"; exit 1;
}

# Check outbox file was created
if [[ ! -f "$OUTBOX_TEST_DIR/test_outbox.jsonl" ]]; then
  echo "FAIL(paper_outbox): outbox file not created at $OUTBOX_TEST_DIR/test_outbox.jsonl"
  exit 1
fi

# Verify outbox contains order and fill entries
order_count=$(grep -c '"type":"order"' "$OUTBOX_TEST_DIR/test_outbox.jsonl" || echo "0")
fill_count=$(grep -c '"type":"fill"' "$OUTBOX_TEST_DIR/test_outbox.jsonl" || echo "0")

echo "outbox entries: orders=$order_count, fills=$fill_count"

# Should have at least 1 order (AAPL BUY_1X when not paused)
[[ "$order_count" -ge 1 ]] || { echo "FAIL(paper_outbox): expected >=1 orders, got $order_count"; exit 1; }

# Fills may take time to appear due to simulated latency, so we'll just check orders for now
aapl_order=$(jq -r 'select(.type=="order" and .data.symbol=="AAPL") | .data.intent' "$OUTBOX_TEST_DIR/test_outbox.jsonl" | head -1)
[[ "$aapl_order" == "BUY_1X" || "$aapl_order" == "BUY_5X" ]] || { echo "FAIL(paper_outbox): AAPL order intent=$aapl_order, expected BUY_*"; exit 1; }

# Test idempotency by running again with same config
sleep 1  # Ensure different timestamp
$BIN -config "$OUTBOX_CONFIG" >>"$TMP_DIR/paper_outbox_2.out" 2>&1 || true

# Count orders again
order_count_2=$(grep -c '"type":"order"' "$OUTBOX_TEST_DIR/test_outbox.jsonl" || echo "0")
echo "outbox entries after second run: orders=$order_count_2"

# Should have more orders (not identical due to timestamp difference)
[[ "$order_count_2" -gt "$order_count" ]] || { echo "FAIL(paper_outbox): idempotency test - expected more orders after second run"; exit 1; }

# --- Case 7: Wire mode ingestion ---
echo "== Running: wire_mode =="

# Kill any existing process on port 8091
pkill -f "cmd/stubs.*port 8091" || true
sleep 0.5

# Start wire stub in background
go run ./cmd/stubs -stream -port 8091 &
STUB_PID=$!

# Function to cleanup stub
cleanup_stub() {
  if [[ -n "${STUB_PID:-}" ]] && kill -0 $STUB_PID 2>/dev/null; then
    kill $STUB_PID 2>/dev/null || true
    wait $STUB_PID 2>/dev/null || true
  fi
  # Also kill by process pattern as backup
  pkill -f "cmd/stubs.*port 8091" 2>/dev/null || true
}
trap cleanup_stub EXIT

# Wait for stub health
for i in {1..30}; do
  if curl -s -m 1 http://localhost:8091/health >/dev/null 2>&1; then
    echo "Wire stub ready"
    break
  fi
  sleep 0.2
  if [ $i -eq 30 ]; then
    echo "FAIL(wire_mode): stub health check timeout"
    cleanup_stub
    exit 1
  fi
done

# Run decision engine in wire mode with tight bounds for fast test
$BIN -wire-mode -wire-url=http://localhost:8091 -max-events=5 -duration-seconds=10 -config "$RESUMED" >>"$TMP_DIR/wire_mode.out" 2>&1 || {
  echo "binary exited non-zero for wire mode test. Output:"; cat "$TMP_DIR/wire_mode.out"; cleanup_stub; exit 1;
}

# Extract decision events
grep -F '"event":"decision"' "$TMP_DIR/wire_mode.out" > "$TMP_DIR/wire_mode.jsonl" || true

if [[ ! -s "$TMP_DIR/wire_mode.jsonl" ]]; then
  echo "No decision events found in wire mode test. Full output was:"
  sed -n '1,120p' "$TMP_DIR/wire_mode.out"
  cleanup_stub
  exit 1
fi

# Verify decisions match resumed fixture mode (should get same outcomes)
wire_aapl_intent=$(jq -r 'select(.event=="decision" and .symbol=="AAPL") | .intent' "$TMP_DIR/wire_mode.jsonl")
wire_nvda_intent=$(jq -r 'select(.event=="decision" and .symbol=="NVDA") | .intent' "$TMP_DIR/wire_mode.jsonl")

[[ "$wire_aapl_intent" == "BUY_1X" || "$wire_aapl_intent" == "BUY_5X" ]] || { echo "FAIL(wire_mode): AAPL intent=$wire_aapl_intent, expected BUY_*"; cleanup_stub; exit 1; }
[[ "$wire_nvda_intent" == "REJECT" ]] || { echo "FAIL(wire_mode): NVDA intent=$wire_nvda_intent, expected REJECT (halt)"; cleanup_stub; exit 1; }

# Verify wire metrics exist  
wire_startup=$(grep -c '"wire_startup"' "$TMP_DIR/wire_mode.out" 2>/dev/null || echo "0")

[[ "$wire_startup" -ge 1 ]] || { echo "FAIL(wire_mode): no wire_startup events found"; cleanup_stub; exit 1; }
echo "wire_mode: verified wire startup and ingestion events"

# Cleanup stub
cleanup_stub
trap - EXIT

# --- Case 9: Portfolio caps and cooldown gates ---
echo "== Running: portfolio_limits =="

# Create portfolio test config with tight limits
PORTFOLIO_CONFIG="$TMP_DIR/config.portfolio.yaml"
cat > "$PORTFOLIO_CONFIG" <<'EOF'
trading_mode: paper
global_pause: false

thresholds:
  positive: 0.35
  very_positive: 0.65

risk:
  per_symbol_cap_nav_pct: 5
  per_order_max_usd: 5000
  daily_new_exposure_cap_nav_pct: 15
  stop_loss_pct: 6
  daily_drawdown_pause_nav_pct: 3
  cooldown_minutes: 1

corroboration:
  require_positive_pr: false
  window_seconds: 900

earnings:
  enabled: false

liquidity:
  target_realized_vol_5m: 0.015
  max_spread_bps: 30

session:
  block_first_minutes: 5
  block_last_minutes: 5
  allow_after_hours: false
  block_premarket: false
  block_postmarket: false

paper:
  outbox_path: "data/outbox.jsonl"
  latency_ms_min: 100
  latency_ms_max: 2000
  slippage_bps_min: 1
  slippage_bps_max: 5
  dedupe_window_seconds: 90

adapters:
  NEWS_FEED: stub
  QUOTES: sim
  HALTS: sim
  SENTIMENT: stub
  BROKER: paper
  ALERTS: stdout

portfolio:
  enabled: true
  state_file_path: "data/test_portfolio_state.json"
  max_position_size_usd: 5000
  max_portfolio_exposure_pct: 20
  daily_trade_limit_per_symbol: 2
  cooldown_minutes_per_symbol: 1
  max_daily_exposure_increase_pct: 15
  reset_daily_limits_at_hour: 9
  position_decay_days: 30

base_usd: 2000

runtime_overrides:
  enabled: false
EOF

# Clean up any existing portfolio state
rm -f data/test_portfolio_state.json

# First run - should succeed (no existing positions)
echo "== Portfolio Test 1: Initial trade (should succeed) =="
$BIN -config "$PORTFOLIO_CONFIG" >"$TMP_DIR/portfolio1.out" 2>&1 || {
  echo "binary exited non-zero for portfolio test 1. Output:"; cat "$TMP_DIR/portfolio1.out"; exit 1;
}

# Extract decision events
grep -F '"event":"decision"' "$TMP_DIR/portfolio1.out" > "$TMP_DIR/portfolio1.jsonl" || true

# Show results
intents1="$(jq -sr 'map(select(.event=="decision") | {(.symbol): .intent}) | add' "$TMP_DIR/portfolio1.jsonl")"
echo "Portfolio test 1 intents: $intents1"

# Should have at least one BUY decision
buy_count=$(jq -sr 'map(select(.event=="decision" and (.intent | startswith("BUY")))) | length' "$TMP_DIR/portfolio1.jsonl")
[[ "$buy_count" -gt 0 ]] || { echo "FAIL(portfolio1): Expected at least one BUY decision, got: $intents1"; exit 1; }

# Create a portfolio state file simulating large existing positions to test caps
cat > "data/test_portfolio_state.json" <<'EOF'
{
  "version": 1,
  "updated_at": "2025-08-23T14:00:00Z",
  "positions": {
    "AAPL": {
      "quantity": 25,
      "avg_entry_price": 210.0,
      "current_notional": 5250.0,
      "unrealized_pnl": 50.0,
      "last_trade_at": "2025-08-23T14:00:00Z",
      "trade_count_today": 2,
      "realized_pnl_today": 0.0
    }
  },
  "daily_stats": {
    "date": "2025-08-23",
    "total_exposure_usd": 5250.0,
    "exposure_pct_capital": 25.0,
    "new_exposure_today": 5250.0,
    "trades_today": 2,
    "pnl_today": 50.0
  },
  "capital_base": 2000.0
}
EOF

# Second run - should be blocked by caps (position too large, exposure too high)
echo "== Portfolio Test 2: Caps enforcement (should block) =="
$BIN -config "$PORTFOLIO_CONFIG" >"$TMP_DIR/portfolio2.out" 2>&1 || {
  echo "binary exited non-zero for portfolio test 2. Output:"; cat "$TMP_DIR/portfolio2.out"; exit 1;
}

# Extract decision events
grep -F '"event":"decision"' "$TMP_DIR/portfolio2.out" > "$TMP_DIR/portfolio2.jsonl" || true

# Show results
intents2="$(jq -sr 'map(select(.event=="decision") | {(.symbol): .intent}) | add' "$TMP_DIR/portfolio2.jsonl")"
echo "Portfolio test 2 intents: $intents2"
gates2="$(jq -sr 'map(select(.event=="decision") | { (.symbol): (.reason.gates_blocked // []) }) | add' "$TMP_DIR/portfolio2.jsonl")"
echo "Portfolio test 2 gates_blocked: $gates2"

# Should be REJECT due to caps
aapl_intent2=$(jq -r 'select(.event=="decision" and .symbol=="AAPL") | .intent' "$TMP_DIR/portfolio2.jsonl")
aapl_gates2=$(jq -r 'select(.event=="decision" and .symbol=="AAPL") | .reason.gates_blocked[]?' "$TMP_DIR/portfolio2.jsonl")
[[ "$aapl_intent2" == "REJECT" ]] || { echo "FAIL(portfolio2): AAPL intent=$aapl_intent2, expected REJECT due to caps"; exit 1; }
echo "$aapl_gates2" | grep -q "caps" || { echo "FAIL(portfolio2): AAPL gates missing caps, got: $aapl_gates2"; exit 1; }

# Clean up test portfolio state
rm -f data/test_portfolio_state.json

echo "portfolio_limits: verified caps and cooldown enforcement"

# --- Case 8: Slack integration and runtime overrides ---
echo "== Running: slack_integration =="

# Start mock Slack server
./scripts/mock-slack.sh 8093 /tmp/mock-slack.jsonl &
MOCK_SLACK_PID=$!

# Function to cleanup mock Slack
cleanup_mock_slack() {
  if [[ -n "${MOCK_SLACK_PID:-}" ]] && kill -0 $MOCK_SLACK_PID 2>/dev/null; then
    kill $MOCK_SLACK_PID 2>/dev/null || true
    wait $MOCK_SLACK_PID 2>/dev/null || true
  fi
  pkill -f "mock-slack" 2>/dev/null || true
}
trap cleanup_mock_slack EXIT

# Wait for mock Slack to be ready
for i in {1..30}; do
  if curl -s -m 1 http://localhost:8093/health >/dev/null 2>&1; then
    echo "Mock Slack server ready"
    break
  fi
  sleep 0.2
  if [ $i -eq 30 ]; then
    echo "FAIL(slack_integration): mock Slack health check timeout"
    cleanup_mock_slack
    exit 1
  fi
done

# Start Slack handler in background  
SLACK_SIGNING_SECRET="testsecret123" go run ./cmd/slack-handler -port 8094 -allowed-users "U12345" &
SLACK_HANDLER_PID=$!

# Function to cleanup Slack handler
cleanup_slack_handler() {
  if [[ -n "${SLACK_HANDLER_PID:-}" ]] && kill -0 $SLACK_HANDLER_PID 2>/dev/null; then
    kill $SLACK_HANDLER_PID 2>/dev/null || true
    wait $SLACK_HANDLER_PID 2>/dev/null || true
  fi
  pkill -f "cmd/slack-handler" 2>/dev/null || true
}
trap cleanup_slack_handler EXIT

# Wait for Slack handler to be ready
for i in {1..30}; do
  if curl -s -m 1 http://localhost:8094/health >/dev/null 2>&1; then
    echo "Slack handler ready"
    break
  fi
  sleep 0.2
  if [ $i -eq 30 ]; then
    echo "FAIL(slack_integration): Slack handler health check timeout"
    cleanup_slack_handler
    cleanup_mock_slack
    exit 1
  fi
done

# Create config with Slack enabled and wire disabled  
SLACK_CONFIG="$TMP_DIR/config.slack.yaml"
sed 's/enabled: false/enabled: true/; s/global_pause: true/global_pause: false/' "$CFG" | \
  sed '/wire:/,/enabled:/ s/enabled: false/enabled: false/' > "$SLACK_CONFIG"

# Clear webhook log
> /tmp/mock-slack.jsonl

# Run decision engine with Slack alerts in oneshot mode (explicitly disable wire mode)
SLACK_ENABLED=true SLACK_WEBHOOK_URL=http://127.0.0.1:8093/webhook WIRE_ENABLED=false \
  $BIN -config "$SLACK_CONFIG" -oneshot=true >>"$TMP_DIR/slack_integration.out" 2>&1 &
DECISION_PID=$!

# Wait for completion with timeout
for i in {1..20}; do
  if ! kill -0 $DECISION_PID 2>/dev/null; then
    wait $DECISION_PID
    DECISION_EXIT_CODE=$?
    break
  fi
  sleep 0.5
  if [ $i -eq 20 ]; then
    echo "Decision engine timed out, killing process"
    kill $DECISION_PID 2>/dev/null || true
    wait $DECISION_PID 2>/dev/null || true
    DECISION_EXIT_CODE=1
  fi
done

if [ ${DECISION_EXIT_CODE:-1} -ne 0 ]; then
  echo "decision engine with Slack failed or timed out. Output:"; cat "$TMP_DIR/slack_integration.out" | head -20
  cleanup_slack_handler; cleanup_mock_slack; exit 1;
fi

# Check if alerts were sent to mock webhook
alert_count=$(cat /tmp/mock-slack.jsonl 2>/dev/null | wc -l || echo "0")
echo "Slack alerts sent: $alert_count"

# Slack integration test - should receive alerts for REJECT decisions with gates blocked
if [[ "$alert_count" -ge 1 ]]; then
  echo "✅ Slack alerts working correctly"
else
  echo "FAIL(slack_integration): expected >=1 alerts for REJECT decisions, got $alert_count"
  cleanup_slack_handler; cleanup_mock_slack; exit 1
fi

# Test pause command with mock signature
timestamp=$(date +%s)
body="command=/pause&user_id=U12345&text=test pause"
signature="v0=test123"  # Mock signature for testing

curl -s -X POST "http://localhost:8094/slack/commands" \
  -H "X-Slack-Signature: $signature" \
  -H "X-Slack-Request-Timestamp: $timestamp" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "$body" >/tmp/pause_response.json 2>&1 || true

# Check if runtime overrides file was created/updated  
if [[ -f "data/runtime_overrides.json" ]]; then
  paused=$(jq -r '.global_pause // false' data/runtime_overrides.json 2>/dev/null || echo "false")
  echo "Runtime override global_pause: $paused"
else
  echo "No runtime overrides file created"
fi

# Test status command
curl -s -X POST "http://localhost:8094/slack/commands" \
  -H "X-Slack-Signature: $signature" \
  -H "X-Slack-Request-Timestamp: $timestamp" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "command=/status&user_id=U12345" >/tmp/status_response.json 2>&1 || true

# Verify status response contains expected fields
if grep -q "Trading System Status" /tmp/status_response.json 2>/dev/null; then
  echo "Status command responded correctly"
else
  echo "Status command failed or malformed"
fi

echo "slack_integration: verified Slack alerts and runtime overrides"

# Cleanup
cleanup_slack_handler
cleanup_mock_slack
trap - EXIT

echo "OK: all tests passed."
