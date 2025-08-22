# Session 9 Plan: Portfolio Caps and Cooldown Gates

## Overview
Add portfolio risk management with position caps (per-symbol and total exposure limits) and cooldown gates (minimum time between trades) with integration into the runtime override system and Slack alerts. Builds on Session 8's operational control infrastructure.

## Acceptance Criteria
- **Core Behavior**: Trading decisions respect position caps and cooldown periods; exceed limits → REJECT; Slack alerts for cap violations
- **Success Evidence**: `make test` Case 9 passes with cap and cooldown enforcement; `/limits` slash command shows current exposures; runtime overrides can adjust caps dynamically

## Implementation Plan

### 1. Portfolio State Management (25-30 min)
- Add `internal/portfolio/state.go`:
  - Position tracking per symbol with quantity, notional value, entry prices
  - Daily exposure tracking with rolling windows
  - Persistent state in `data/portfolio_state.json` with atomic updates
  - Thread-safe read/write operations with mutex protection
- Integrate with paper trading outbox:
  - Update positions on order execution (fills)
  - Calculate P&L and exposure metrics
  - Track daily trading volumes and trade counts

### 2. Portfolio Gates Implementation (25-30 min)
- Extend `internal/decision/engine.go`:
  - Add `caps` gate: check per-symbol position limits and total portfolio exposure
  - Add `cooldown` gate: enforce minimum time between trades per symbol
  - Integrate with existing gate logic in RiskState and Evaluate function
- Add portfolio configuration:
  - `max_position_size_usd`: per-symbol position limit
  - `max_portfolio_exposure_pct`: total portfolio exposure as % of capital
  - `daily_trade_limit_per_symbol`: max trades per symbol per day
  - `cooldown_minutes_per_symbol`: minimum time between trades
  - `max_daily_exposure_increase_pct`: daily new exposure limit

### 3. Runtime Portfolio Controls (15-20 min)
- Extend runtime overrides system:
  - Add portfolio limits to `data/runtime_overrides.json`
  - Support dynamic adjustment of caps, cooldowns, and exposure limits
  - Add `/limits` slash command to view current positions and exposures
  - Add `/reset-limits` command to clear position tracking (emergency use)
- Portfolio slash commands:
  - `/position SYMBOL`: Show current position, P&L, and exposure
  - `/exposure`: Show total portfolio exposure and daily limits
  - `/cooldown SYMBOL [minutes]`: Set/view cooldown period for symbol

## Configuration Knobs
```yaml
portfolio:
  enabled: true                           # enable portfolio tracking
  state_file_path: "data/portfolio_state.json"
  max_position_size_usd: 25000           # per-symbol position limit
  max_portfolio_exposure_pct: 15         # total exposure as % of capital
  daily_trade_limit_per_symbol: 5        # max trades per symbol per day
  cooldown_minutes_per_symbol: 5         # minimum time between trades
  max_daily_exposure_increase_pct: 10    # daily new exposure limit
  reset_daily_limits_at_hour: 9          # UTC hour to reset daily counters
  position_decay_days: 30                # days to keep position history

risk_limits:
  stop_loss_pct: 6                       # auto-sell threshold (future session)
  daily_drawdown_pause_pct: 3            # pause trading on daily loss
  max_correlation_exposure: 0.5          # max exposure to correlated positions
```

## Expected Files Changed
- `internal/portfolio/state.go` - New portfolio state management
- `internal/decision/engine.go` - Add caps and cooldown gates
- `internal/config/config.go` - Portfolio and RiskLimits configuration structs
- `cmd/decision/main.go` - Portfolio state integration and gate updates
- `cmd/slack-handler/main.go` - Portfolio slash commands
- `config/config.yaml` - Portfolio configuration section
- `scripts/run-tests.sh` - Case 9: Portfolio caps and cooldown test
- `data/portfolio_state.json` - Portfolio state persistence (created)

## Success Evidence Patterns
```bash
# Terminal 1: Run decision engine with portfolio limits
GLOBAL_PAUSE=false go run ./cmd/decision -oneshot=false -duration-seconds=10

# Terminal 2: Check portfolio state after trading
cat data/portfolio_state.json | jq .

# Terminal 3: Test portfolio commands
curl -X POST http://localhost:8092/slack/commands \
  -d "command=/position&text=AAPL&user_id=U12345"

curl -X POST http://localhost:8092/slack/commands \
  -d "command=/exposure&user_id=U12345"

# Expected: Position tracking, cap enforcement, cooldown respect
# Expected: Slack alerts for cap violations or limit approaches
```

## Portfolio State Structure
```json
{
  "version": 123456789,
  "updated_at": "2025-08-22T16:30:00Z",
  "positions": {
    "AAPL": {
      "quantity": 15,
      "avg_entry_price": 210.50,
      "current_notional": 3157.50,
      "unrealized_pnl": 45.00,
      "last_trade_at": "2025-08-22T16:25:00Z",
      "trade_count_today": 3
    }
  },
  "daily_stats": {
    "date": "2025-08-22",
    "total_exposure_usd": 12650.00,
    "exposure_pct_of_capital": 8.5,
    "new_exposure_today": 3200.00,
    "trades_today": 7,
    "pnl_today": 125.50
  },
  "capital_base": 150000.00
}
```

## Alert Integration
- **Cap Violation Alerts**: "🚨 Position cap violated: AAPL position would exceed $25k limit"
- **Cooldown Alerts**: "⏰ Cooldown active: AAPL traded 2min ago, minimum 5min interval"
- **Exposure Alerts**: "📊 Portfolio exposure: 14.8% of capital (approaching 15% limit)"
- **Daily Limit Alerts**: "📈 Daily trade limit: AAPL 4/5 trades used today"

## Metrics & Observability
- **Portfolio metrics**: `portfolio_exposure_usd`, `portfolio_positions_count`, `daily_trades_count`
- **Gate metrics**: `caps_gate_blocks_total`, `cooldown_gate_blocks_total`
- **Limit metrics**: `position_cap_violations_total`, `daily_limit_hits_total`
- **P&L metrics**: `portfolio_unrealized_pnl`, `daily_realized_pnl`

## Risk Mitigation
- **State corruption**: Atomic writes with backup and recovery mechanisms
- **Cooldown bypass**: Strict timestamp comparison with server time validation
- **Cap evasion**: Real-time position calculation before every decision
- **Memory leaks**: Position cleanup for expired/closed positions

## Test Strategy
- **Position tracking**: Verify position updates from paper trading fills
- **Cap enforcement**: Test rejection when position would exceed limits
- **Cooldown timing**: Validate minimum intervals between symbol trades  
- **Runtime updates**: Test dynamic cap adjustment via runtime overrides
- **Portfolio commands**: Validate slash command responses and state queries

## Session Notes Template
```markdown
### Implementation Details
- Portfolio state persistence with atomic JSON updates and version tracking
- Caps and cooldown gates integrated into existing gate evaluation pipeline
- Position tracking from paper trading fills with P&L calculation
- Portfolio slash commands with detailed exposure and position reporting

### Edge Cases Handled  
- State file corruption: Backup and recovery with graceful degradation
- Clock skew issues: Cooldown enforcement with timestamp validation
- Concurrent updates: Mutex protection for portfolio state modifications
- Cap edge cases: Exact limit handling and fractional position scenarios
```

This session establishes comprehensive portfolio risk management while building on Session 8's operational control and alert infrastructure.