# Session 10 Plan: Advanced Risk Controls & Real-Time Monitoring

## Overview

Add proactive risk controls (stop-loss, sector exposure limits, drawdown suspension) plus real-time monitoring via metrics and Slack. Keep it paper-only, deterministic, and reversible.

## What's Changed vs. Draft

- **Scope split**: do sector exposure limits (deterministic) now; defer true correlation math (rolling returns) to a later session.
- **Soft vs hard gates clarified**: drawdown/sector limits are soft for reductions (BUY→HOLD; REDUCE allowed). Stop-loss produces orders and should not be blocked by other soft gates.
- **Clear semantics**: UTC boundaries, RTH vs AH behavior, idempotency, single-shot stops, and cooldown are specified.
- **Deterministic tests**: fixtures drive P&L moves and exposure; health dashboard is Slack-only (no web UI).

## Acceptance Criteria

- **Stop-loss**: When a position breaches configured loss thresholds, exactly one paper SELL order is emitted (idempotent), with cooldown preventing immediate re-entry for that symbol.
- **Sector exposure limits (first version)**: New buys that push gross sector exposure above the configured cap are downgraded to HOLD with `gates_blocked:["sector_limit"]`; reductions remain allowed.
- **Drawdown suspension**: When daily drawdown surpasses the pause threshold, new buys are downgraded to HOLD with `gates_blocked:["drawdown_pause"]`; reductions allowed. Weekly thresholds produce warnings and/or size reductions.
- **Monitoring**: Slack `/dashboard` returns current NAV, P&L (daily/weekly), exposures by sector, active gates, and health color (Green/Yellow/Red). Metrics expose stop triggers, blocks, and drawdown states.
- **Tests**: `make test` Case 10 passes: stop-loss triggers once, sector cap blocks buys, drawdown pause activates, and dashboard/metrics reflect states.

## Implementation Plan

### 1) Automatic Stop-Loss (25–30 min)

**Policy**
- **Reference price**: mark-to-market using mid; fallback to last if bid/ask missing.
- **Trigger types**: absolute (%) from entry VWAP (baseline) and optional trailing (%) from max favorable price (defer trailing to later if time is tight).
- **Session**: configurable; default RTH only (do not trigger stops AH unless `stop_loss.allow_after_hours=true`).
- **Idempotency**: exactly one stop order per open position per trigger window; dedupe via (symbol, position_id, trigger_level_bucket, day).

**Behavior**
- **On breach**: enqueue paper SELL to outbox with reason `stop_loss`.
- **Cooldown**: prevent new buys in the symbol for `stop_loss_cooldown_hours` after a trigger (exposed as `gates_blocked:["cooldown_stop"]`).
- **Interplay**: stop orders bypass sector/drawdown soft gates; if the symbol is halted, queue intent to reduce and keep retrying when unhalted (emit `stop_deferred_total`).

**Metrics**
- `stop_triggers_total{symbol,type="absolute|trailing"}`
- `stop_orders_sent_total{symbol}`
- `stop_deferred_total{reason="halt|liquidity"}`
- `stop_cooldown_active{symbol}` gauge

**Tests**
- Fixture drops AAPL by >6% from entry → one `paper_order` SELL; second pass within cooldown → no new order.

### 2) Sector Exposure Limits (20–25 min)

**Policy (v1)**
- Map each symbol to one sector (static mapping in config for now).
- Compute gross sector exposure = sum of absolute notionals in sector / NAV * 100.
- If proposed BUY would exceed `max_sector_exposure_pct`, downgrade to HOLD with `gates_blocked:["sector_limit"]`. Always allow REDUCE.

**Metrics**
- `sector_exposure_pct{sector}`
- `sector_limit_blocks_total{sector}`

**Tests**
- Seed positions so Tech is at 38% of NAV; attempt BUY pushing >40% → blocked with gate; REDUCE allowed.
- (Later session: real correlation matrix or beta-adjusted exposure.)

### 3) Drawdown Protection & Circuit (15–20 min)

**Policy**
- **Daily drawdown** measured from start-of-trading-day NAV (UTC date; configurable market calendar later). Include realized + unrealized P&L.
- **Weekly drawdown** measured from Monday 00:00 UTC NAV (loose for now).
- **Responses**:
  - **Warning**: set position size multiplier (e.g., 50%) for new buys.
  - **Pause**: BUY→HOLD with `gates_blocked:["drawdown_pause"]`; reductions allowed.

**Metrics**
- `drawdown_pct_daily`, `drawdown_pct_weekly` gauges
- `drawdown_warnings_total`, `drawdown_pauses_total`
- `size_multiplier_current`

**Tests**
- Fixture that simulates cumulative P&L down beyond thresholds → verify multiplier and eventual pause; verify buys blocked, reduces allowed.

### 4) Real-Time Monitoring (15–20 min)

**Slack `/dashboard` (ephemeral)**
- **Fields**: NAV, daily/weekly P&L, sector exposures, active gates (drawdown_pause, cooldown_stop, sector_limit counts), size multiplier, last 5 orders.
- **Health color**:
  - **Green**: all metrics under 70% of limits
  - **Yellow**: ≥70% of any limit or active warning
  - **Red**: any pause active or stop triggered in last X minutes

**Optional** `/risk set size_multiplier=0.5 ttl=60m` for temporary size cuts (writes runtime override with TTL).

**Metrics**
- Keep Prometheus as source of truth; the Slack response is just a snapshot.

**Tests**
- Unit-ish: call dashboard endpoint via the Slack handler (local HTTP) and assert JSON fields present with sane ranges.

## Configuration Extensions

```yaml
risk_controls:
  stop_loss:
    enabled: true
    default_stop_loss_pct: 6
    emergency_stop_loss_pct: 10
    allow_after_hours: false
    cooldown_hours: 24

  sector_limits:
    enabled: true
    max_sector_exposure_pct: 40
    sector_map:
      AAPL: tech
      MSFT: tech
      GOOGL: tech
      NVDA: tech
      JPM: finance
      BAC: finance
      GS: finance
      BIOX: biotech
      GILD: biotech
      MRNA: biotech

  drawdown:
    enabled: true
    daily_warning_pct: 2.0
    daily_pause_pct: 3.0
    weekly_warning_pct: 5.0
    weekly_pause_pct: 8.0
    size_multiplier_on_warning_pct: 50

monitoring:
  dashboard_recent_trades: 5
  health_check_interval_minutes: 5
```

Keep correlation math out of this session; the `sector_limits` block gives you a usable, deterministic proxy.

## Expected Files Changed

- `internal/risk/stoploss.go` — stop detection, cooldown, metrics
- `internal/risk/sector_limits.go` — exposure calc and gate  
- `internal/risk/drawdown.go` — rolling P&L, size multiplier, pause state
- `internal/portfolio/state.go` — positions with entry VWAPs, sector tags
- `internal/decision/engine.go` — incorporate sector_limit & drawdown_pause gates (soft for buys)
- `cmd/decision/main.go` — wire risk modules + metrics
- `cmd/slack-handler/main.go` — `/dashboard` + optional `/risk set size_multiplier=...`
- `config/config.yaml` — new risk_controls + monitoring
- `scripts/run-tests.sh` — Case 10 with three subcases (stop, sector block, drawdown)

## Success Evidence Patterns

```bash
# 10A: Stop-loss triggers once and enqueues SELL
go run ./cmd/decision -oneshot=false -duration-seconds=10 \
 | egrep '"event":"paper_order"|"event":"paper_fill"|stop_loss'

# 10B: Sector exposure blocks BUY, allows REDUCE
make test  # includes an assertion for gates_blocked:["sector_limit"]

# 10C: Drawdown warning→multiplier, then pause→BUY→HOLD
go run ./cmd/decision -oneshot=false -duration-seconds=10
# Slack: /dashboard shows Red when paused; metrics show drawdown_pauses_total>0
```

## Risk Mitigation & Edge Cases

- **Halts during stop**: don't spam orders; mark `stop_pending` and retry on unhalt.
- **Missing quotes**: don't trigger stops on stale data; require fresh tick (< Xs).
- **After-hours gaps**: if `allow_after_hours=false`, don't fire stops; instead mark `stop_armed` and evaluate at RTH open tick.
- **Re-entry after stop**: cooldown enforced via gate; `what_would_change_it: "cooldown_until=..."``.
- **Competing gates**: list them all; buys may be blocked by multiple soft gates simultaneously.
- **NAV source**: keep NAV synthetic (positions + cash) in paper mode; persist in state for daily/weekly resets.

## Test Strategy

**Deterministic fixtures that:**
- Decrease AAPL by 6–7% from entry (stop triggers once).
- Attempt incremental Tech buys to breach 40% sector cap.
- Accumulate negative ticks to cross drawdown warning & pause thresholds.

**Assertions:**
- Exactly one stop order; no duplicates on second run.
- `gates_blocked` contains `sector_limit` for capped attempts.
- `size_multiplier_current=0.5` at warning; `drawdown_pause` gate activates at pause.
- `/dashboard` shows correct health color transitions.

## Session Goals

1. Ship single-shot stop-loss with cooldown and paper execution.
2. Enforce sector exposure cap (deterministic proxy for correlation).
3. Activate drawdown warning/pause with clear, explainable reasons.
4. Provide real-time visibility via Slack and metrics.