Knowledge Transfer (KT) — News-Driven Trading System

This document is the living “brain dump” for humans and AIs who join (or revisit) the project at any time. It explains what we’re building, why, what exists today, how it works, how to extend it, and how to prove it works. It’s intentionally verbose and self-contained.

0) TL;DR (fast orientation)

We’re building a backend-only, news-driven trading platform that:

Ingests market events (halts/quotes) and news from reliable sources

Runs pluggable strategies (sentiment, trend, custom)

Fuses their advice and applies risk/policy gates

Executes (paper first, then real) with audit, alerts, and circuit breakers

Our dev approach is vibe coding with safety rails: paper mode, global pause, stubs first, one vertical slice at a time.

The repo already runs a minimal Decision Engine with fixtures + tests. We can prove gates (global pause, halts) and threshold mapping using make test.

1) Vision & Scope
1.1 Product Vision

A trustworthy, low-latency trading backend that reacts to credible market-moving information, decides consistently using explainable, configurable logic, and executes safely with robust guardrails.

1.2 What “credible, quick” means

Editorial / regulatory first (Reuters, AP, SEC/EDGAR, exchange notices); PRs allowed but de-weighted or require corroboration.

Minimize sponsored/promotional contamination via source weighting and corroboration windows.

1.3 Non-Goals (MVP)

No frontend UI (CLI/Slack ops only)

No options/derivatives (equities first)

No multi-region HA or portfolio optimization (later phases)

2) Architecture (mental model)

We use ports & adapters. Internal modules speak canonical contracts; providers plug in behind small interfaces.

Planes

Data Plane: Ingestion → Feature/Strategy → Fusion → Decision & Policy

Execution Plane: Order Outbox → Broker Adapters (paper → live)

Cross-cutting: Observability (metrics/logs), Alerts/Controls, Replay/Audit

Gates (short-circuit safety before any trade)

global_pause, halt, session, liquidity/spread, cooldown, caps, drawdown, earnings_embargo, PR_corroboration

Intent Mapping

BUY_5X, BUY_1X, REDUCE, HOLD, SKIP, or REJECT (when a gate blocks)

3) Current State (what exists today)
3.1 Files & folders (core)
Makefile
docker-compose.yml           # NATS+Postgres infra (not yet wired by code)
README.md
CONFIG.md
contracts/contracts.proto    # canonical message shapes
config/config.example.yaml   # copy -> config/config.yaml
fixtures/                    # halts/news/ticks + extra scenarios
scripts/seed.sh              # posts fixtures to stub servers
scripts/replay.sh            # prints fixtures (placeholder)
scripts/run-tests.sh         # end-to-end sanity checks
scripts/new-session.sh       # creates docs/sessions/<session>.md

# Go code (skeleton decision path)
cmd/decision/main.go         # loads config, runs decisions, logs reasons, metrics (oneshot flag)
cmd/stubs/main.go            # optional tiny servers :8081/2/3 for halts/news/ticks (seeding target)
internal/decision/engine.go  # fuse + Evaluate (gates + thresholds → intent)
internal/observ/metrics.go   # minimal counters/histograms + /metrics JSON
internal/observ/logging.go   # structured JSON logs
internal/config/config.go    # YAML config loader

3.2 What we can do right now

Run the Decision engine against fixtures and see reasons and metrics:

cp config/config.example.yaml config/config.yaml
go mod tidy
go run ./cmd/decision -config config/config.yaml -oneshot=true


Run automated sanity tests (two profiles: paused + resumed):

make test


Start local stubs for halts/news/ticks and seed fixtures:

go run ./cmd/stubs
make seed      # posts fixtures to :8081/halts, :8082/news, :8083/ticks

3.3 What’s intentionally stubbed

No real news/quotes/broker integrations yet (we use fixtures or stubs).

No message bus wiring (NATS) or Postgres persistence in the code path yet.

Sentiment is simulated via fixture weights; a real NLP will plug in later.

4) How we develop (Vibe Coding protocol)
4.1 Session rhythm

Every session has a Session Card with two parts:

Development

Theme, Acceptance, Rails (paper mode, pause), Contracts touched, Changes

Test Run & Edge Cases

Commands, Expected, Evidence (decision logs + metrics), Verdict

We capture each as docs/sessions/session-YYYY-MM-DD-XX.md. Use:

scripts/new-session.sh "Halts gate + reason logging"


We maintain a rolling TODO at docs/TODO.md (Now/Next/Later/Done).

4.2 Safety rails (always on early)

trading_mode: paper

global_pause: true

tiny notional defaults (e.g., base_usd: 2000, caps strict)

“one adapter at a time” rollout when integrating real providers

4.3 Contracts-first

All modules communicate using contracts.proto (“shape of the world”). This enables:

Loose coupling & easy adapter swaps

Deterministic replays

Codegen for multiple languages (we currently generate Go types)

5) Configuration, Flags & Envs
5.1 config/config.yaml (local defaults)

Rails

trading_mode: paper         # paper | live | dry-run
global_pause: true


Thresholds

thresholds:
  positive: 0.35
  very_positive: 0.65
base_usd: 2000


Risk/Policy (caps, drawdown, cooldown, corroboration, liquidity, session)

Adapters (currently stubbed): NEWS_FEED, QUOTES, HALTS, SENTIMENT, BROKER, ALERTS

5.2 Make targets

make up — start infra (NATS, Postgres) for later slices

make proto — generate Go types from contracts.proto

make seed — POST fixtures to stubs

make test — run end-to-end sanity tests

make doctor — toolchain check

make init — create config.yaml if missing

6) Data Contracts (canonical shapes)

High-level summary from contracts/contracts.proto:

NewsItem {id, provider, published_at_utc, headline, body, urls[], tickers[], is_press_release, is_correction, supersedes_id, source_weight, headline_hash}

MarketTick {ts_utc, symbol, bid/ask/last, vwap_1m/5m/30m, rel_volume, realized_vol, halted, pre/post, ssr_active}

Advice {symbol, score[-1..1], confidence[0..1], tags[], rationale, strategy, version, ttl_s}

ProposedAction {symbol, intent, fused_score, base_amount_usd, scaled_notional, reason_json}

Order / Execution — for the execution plane (to be used when we wire the outbox)

RiskState — e.g., global_pause, frozen_symbols[]

These contracts stabilize integration and enable replay/audit.

7) Decision Engine (today’s logic)
7.1 Fusion

A simple weighted sum with confidence & source weight, squashed to [-1..1]. (Expect future upgrades: novelty, decay, anti-double-count, regime weights.)

7.2 Gates (implemented so far)

global_pause: blocks all new orders

halt: blocks symbols with active halt

session: blocks pre/post-market trading when configured

liquidity: blocks symbols with spreads exceeding max_spread_bps  

corroboration: soft gate that converts would-be BUY to HOLD when positive PR is primary driver (>50% weight) but lacks editorial/regulatory confirmation within 15-minute window

Implemented gates: global_pause (hard), halt (hard), session (hard), liquidity (hard), corroboration (soft)

Gates to add soon: caps, cooldown, earnings_embargo, drawdown.

7.3 Threshold Mapping

fused_score >= 0.65 → BUY_5X

fused_score >= 0.35 → BUY_1X

else → HOLD (unless gates force REJECT)

7.4 Reason Object

Every decision logs a structured reason:

{
  "fused_score": 0.59,
  "per_strategy": {"SentimentV2": 0.38, "TrendLite": 0.21},
  "gates_passed": ["no_halt","caps_ok","session_ok"],
  "gates_blocked": ["global_pause"],
  "policy": "positive>=0.35; very_positive>=0.65",
  "what_would_change_it": "resume trading"
}

8) Testing & Fixtures
8.1 Why fixtures

Before real feeds, fixtures give deterministic, documented inputs to prove behavior and prevent regressions.

8.2 Core fixtures

fixtures/halts.json — NVDA halted (tests halt gate)

fixtures/news.json — AAPL editorial (+), BIOX PR (+), duplicate headline (tests dedupe)

fixtures/ticks.json — AAPL trend up, NVDA tick halted

8.3 Extra fixtures (scenarios)

news_correction_supersedes.json — correction flips direction (should trigger REVISION/reduce)

ticks_after_hours_wide_spread.json — session/liquidity guard

news_pr_needs_corroboration.json — PR requires editorial within window

earnings_calendar.json — embargo window

8.4 Test runner

scripts/run-tests.sh executes two end-to-end cases:

Paused: both AAPL/NVDA should be REJECT

Resumed: AAPL becomes BUY_1X, NVDA stays REJECT (halt)

Run via make test.

9) Observability
9.1 Metrics (current)

decisions_total{symbol,intent}

decision_latency_ms{symbol}

decision_gate_blocks_total{gate,symbol}

In dev, /metrics serves a JSON dump (loopback, 127.0.0.1:8090)

9.2 Logs

All logs are structured JSON (easy to grep/ingest). Key events: startup, advice, decision, done.

10) Operational Controls (planned)

Slack command handlers (or CLI) for:

/pause all, /resume

/freeze <SYMBOL> <duration>

/limits show

Alerting destinations: stdout → Slack → email

11) Security & Secrets

Local dev uses no secrets.

When integrating providers/brokers:

Store API keys in a secret manager (Vault/SOPS); never in logs

Use idempotency keys for broker orders; mask PII; encrypt at rest

12) Rollout Strategy (provider swaps)

Alerts → Slack (low risk)

Broker → paper (Alpaca/IBKR)

Halts → NASDAQ/NYSE

News → one editorial/aggregator

Quotes → live

Sentiment → FinBERT/vendor

One at a time, behind flags; 10-minute canary; strict caps; global_pause near you.

13) Session Plan (near-term roadmap)

Session 1 (done): Halts gate + reason logging; fixture run

Session 2 (done): Metrics + -oneshot runner; make test flow

Session 4 (completed): Added PR corroboration window logic with soft gate implementation. When positive PR is primary driver (>50% weight), requires editorial/regulatory corroboration within 15-minute window or converts BUY to HOLD.

Session 5 (next): Add earnings embargo gate with calendar fixture

Session 6: Transactional paper outbox (idempotent), mock fills and reconciliation counter

Session 7: Wire stubs → ingestion loop (HTTP pull or WebSocket); still paper; still paused

Session 8: Slack alerts + controls (/pause, /freeze) with RBAC stub

Session 9+: First real adapter swap (alerts or halts), then news, etc.

Each session produces a docs/sessions/session-YYYY-MM-DD-XX.md with evidence and updates docs/TODO.md.

14) Edge Cases (taxonomy)

Market structure: halts (LUDP), SSR, corporate actions, splits/renames

Session: pre/post hours, first/last minutes blocks

Liquidity: spreads, rel-volume, volatility buckets

Content quality: PR vs editorial, corrections/supersedes, duplicates

Risk: caps (symbol/sector/daily), cooldowns, drawdown, earnings embargos

Operations: reconnect/backfill on feed outages, idempotency (no dup orders)

Fixtures exist or will be added for each.

15) How to Extend (playbooks)
15.1 Add a new gate

Add config knob (e.g., liquidity.max_spread_bps)

Compute feature (e.g., SpreadBps from tick bid/ask)

In Evaluate, append gate to GatesBlocked when violated

Add fixture(s) + an assertion in run-tests.sh

Update README + session doc

15.2 Add a new strategy

Define interface Process(inputs) -> Advice

Register it in the orchestrator; give it a weight in config

Add to fusion (ensure anti-double-count if correlated)

Add fixtures and tests (e.g., trend negativity → reduce)

15.3 Swap a provider (adapter)

Implement Subscribe() for provider; map payload → contracts

Dedupe (headline_hash + window) and tag trust signals

Track latency end-to-end; add reconnect/backfill

Canary on 3–5 symbols; keep global_pause until healthy

16) Runbooks (common tasks)
16.1 First-time setup
make init
make proto
go mod tidy
go run ./cmd/decision -config config/config.yaml -oneshot=true
make test

16.2 Manual inspection with metrics
go run ./cmd/decision -config config/config.yaml -oneshot=false
curl -s http://127.0.0.1:8090/metrics | jq .

16.3 Seed stubs
go run ./cmd/stubs
make seed              # or scripts/seed.sh core|extra|all

17) FAQ

Q: Why is everything REJECT initially?
A: global_pause: true. Flip to false in config.yaml to observe BUY on AAPL (NVDA remains blocked by halt).

Q: Why do I see a macOS firewall prompt?
A: If you run with metrics server on (-oneshot=false), we bind to 127.0.0.1:8090. The test runner uses -oneshot=true to exit cleanly.

Q: Where do I write session notes?
A: scripts/new-session.sh "Theme" creates docs/sessions/session-YYYY-MM-DD-XX.md. Fill Development + Test Run sections and update docs/TODO.md.

Q: How do I add a new scenario test?
A: Drop a fixture into fixtures/, extend scripts/seed.sh (optional), and add assertions to scripts/run-tests.sh.

18) Glossary

AH / RTH — After Hours / Regular Trading Hours

VWAP — Volume-Weighted Average Price

LUDP — Limit Up/Limit Down (volatility halts)

SSR — Short-Sale Restriction

PR Corroboration — Require editorial confirmation for PR-only positivity

Novelty/Decay — Boost newer, unseen signals; decay old/repeated ones

19) Contribution & Style

Prefer small sessions (60–90 minutes), one slice each.

Contracts changes require a note in the session doc and PRD alignment.

Every change ships with evidence (decision logs + metrics or tests).

Keep logs structured; keep fixtures tiny and focused.

20) Success Criteria (MVP)

p95 news→decision ≤ 300 ms (simulated now; measured later with real feeds)

Zero duplicate orders (idempotent outbox, to be added)

≥95% decisions include complete reason

Replay reproducibility ≥99%

Closing

This project is intentionally incremental. The current skeleton already demonstrates gates and threshold mapping with deterministic fixtures and a test harness. From here, we’ll add gates (session/liquidity, corroboration, embargo), then build the vertical slice to wire ingestion → decision → (paper) execution, and finally begin provider swaps one at a time, under strict caps and canaries.

Keep sessions small, always show evidence, and let the fixtures/tests be your compass.