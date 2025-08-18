# CONFIG.md — Adapters, flags, and defaults

This document enumerates all runtime knobs so you can flip between **stubs** and **real** adapters without changing code.

---

## Adapters (environment variables)

```
NEWS_FEED=stub|benzinga|reuters|custom
QUOTES=sim|polygon|broker
HALTS=sim|nasdaq|nyse
SENTIMENT=stub|finbert|vendor
BROKER=paper|alpaca|ibkr
ALERTS=stdout|slack|email
```

**Recommended progression**
1) All stubs (NEWS_FEED=stub, QUOTES=sim, HALTS=sim, SENTIMENT=stub, BROKER=paper, ALERTS=stdout)  
2) ALERTS=slack → BROKER=paper (real paper acct) → HALTS=nasdaq/nyse → NEWS_FEED=one editorial source → QUOTES=live → SENTIMENT=real

---

## Core safety rails

- `TRADING_MODE`:
  - `paper` — send orders to paper adapter
  - `live` — real broker (enable only after extensive paper burn-in)
  - `dry-run` — compute decisions, **don’t** place orders (still log/alert)
- `GLOBAL_PAUSE` (bool) — when true, Decision rejects all new orders

---

## Risk & policy (defaults)

```
POSITIVE_THRESHOLD=0.35
VERY_POSITIVE_THRESHOLD=0.65

PER_SYMBOL_CAP_NAV_PCT=5
PER_ORDER_MAX_USD=25000
DAILY_NEW_EXPOSURE_CAP_NAV_PCT=15

STOP_LOSS_PCT=6           # per-symbol freeze threshold
DAILY_DRAWDOWN_PAUSE_NAV_PCT=3
COOLDOWN_MINUTES=5

REQUIRE_POSITIVE_PR_CORROBORATION=true
CORROBORATION_WINDOW_SECONDS=900  # 15 minutes
```

---

## Liquidity & volatility scaling

```
TARGET_REALIZED_VOL_5M=0.015    # aim for ~1.5% 5m realized vol risk
MAX_SPREAD_BPS=30               # block/truncate orders if wider
```

---

## Session fences & calendars

```
BLOCK_FIRST_MINUTES=5           # avoid first 5m of RTH initially
BLOCK_LAST_MINUTES=5
ALLOW_AFTER_HOURS=false         # or true with stricter guards
```

---

## Example `config.yaml` (local defaults)

```yaml
trading_mode: paper
global_pause: true

thresholds:
  positive: 0.35
  very_positive: 0.65

risk:
  per_symbol_cap_nav_pct: 5
  per_order_max_usd: 25000
  daily_new_exposure_cap_nav_pct: 15
  stop_loss_pct: 6
  daily_drawdown_pause_nav_pct: 3
  cooldown_minutes: 5

corroboration:
  require_positive_pr: true
  window_seconds: 900

liquidity:
  target_realized_vol_5m: 0.015
  max_spread_bps: 30

session:
  block_first_minutes: 5
  block_last_minutes: 5
  allow_after_hours: false

adapters:
  NEWS_FEED: stub
  QUOTES: sim
  HALTS: sim
  SENTIMENT: stub
  BROKER: paper
  ALERTS: stdout
```

> Keep `config.yaml` under version control for defaults, and use environment variables to override in CI/prod.

---

## Secrets

Store provider/broker keys in Vault/SOPS or your secret manager. Never log secrets; never commit them.

---

## Changing config safely

- Prefer **feature flags** and **small diffs**.
- For any change to thresholds/caps, note it in a short ADR and run a short **replay** before enabling in live/paper.
