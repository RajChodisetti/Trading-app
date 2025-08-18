# News-Driven Trading System

A trustworthy, low-latency trading backend that reacts to credible market-moving information, decides consistently using explainable, configurable logic, and executes safely with robust guardrails.

> ⚠️ This is research automation, not financial advice. Always start in **paper** mode with **global_pause=true**.

## Current Status

The system currently runs a minimal Decision Engine with:
- ✅ Halts and global pause gates implemented
- ✅ Threshold mapping (BUY_1X, BUY_5X, REJECT)
- ✅ Structured decision logging with reasons  
- ✅ End-to-end tests with fixtures
- ✅ Metrics and observability

## Quick Start

1. **Prerequisites**: Go (>=1.21), protoc, jq, Docker (optional)
2. **Setup**:
```bash
make init         # Initialize project structure
make proto        # Generate Go code from protobuf  
make doctor       # Check tool dependencies
```

3. **Run Decision Engine**:
```bash
make test         # Run automated end-to-end tests
go run ./cmd/decision -config config/config.yaml -oneshot=true
```

4. **Start Services** (optional):
```bash
make up           # Start Docker services
make seed         # Load fixtures into stub feeders
```

---

## Architecture

**Data Plane**: Ingestion → Feature/Strategy → Fusion → Decision & Policy  
**Execution Plane**: Order Outbox → Broker Adapters (paper → live)  
**Cross-cutting**: Observability, Alerts/Controls, Replay/Audit

### Current Gates
- `global_pause` - blocks all new orders
- `halt` - blocks symbols with active halt
- Coming: session, liquidity/spread, caps, cooldown, earnings_embargo, PR_corroboration

### Safety Rails
- `TRADING_MODE=paper` — orders go to paper adapter (never live money)
- `GLOBAL_PAUSE=true` — Decision Engine rejects any new orders  
- Strict caps and conservative thresholds

---

## Adapters & env flags

You can run entirely on stubs at first. Later, flip one flag at a time to integrate a real provider.

```
NEWS_FEED=stub|benzinga|reuters|custom
QUOTES=sim|polygon|broker
HALTS=sim|nasdaq|nyse
SENTIMENT=stub|finbert|vendor
BROKER=paper|alpaca|ibkr
ALERTS=stdout|slack|email
```

**Example (stubs only):**
```bash
NEWS_FEED=stub QUOTES=sim HALTS=sim SENTIMENT=stub BROKER=paper ALERTS=stdout GLOBAL_PAUSE=true make up
```

---

## Testing & Validation

### Automated Tests
```bash
make test         # Runs end-to-end integration tests
go test ./...     # Runs unit tests
```

The test suite validates:
1. **Paused mode**: All symbols rejected due to global_pause
2. **Resumed mode**: AAPL gets BUY_1X, NVDA blocked by halt
3. **Gate logic**: Proper reason logging with gates_blocked arrays
4. **Threshold mapping**: Score to intent conversion

### Key Test Cases
- **Halts gate**: NVDA halted → REJECT with `gates_blocked=["halt"]`
- **Global pause**: All orders blocked when `global_pause=true` 
- **Positive signals**: AAPL editorial + trend → BUY_1X (when resumed)
- **Decision reasons**: Complete structured logging with fused scores

---

## Observability

### Current Metrics (JSON at :8090/metrics)
- `decisions_total{symbol,intent}` - Decision outcomes by symbol/intent
- `decision_latency_ms{symbol}` - Decision processing time  
- `decision_gate_blocks_total{gate,symbol}` - Gate blocking frequency

### Structured Logs  
All logs are JSON with key events: `startup`, `advice`, `decision`, `done`

### Future Metrics
- `ingest_latency_ms` — news/event → bus  
- `orders_sent_total` / `broker_rejects_total`  
- `drawdown_events_total` / `paused_state`

---

## Decision reason log (shape)

Every decision should emit a structured **reason** object (JSON). Example:

```json
{
  "symbol":"AAPL",
  "fused_score":0.55,
  "per_strategy":[
    {"name":"SentimentV2","contrib":0.38},
    {"name":"TrendLite","contrib":0.17}
  ],
  "gates_passed":["no_halt","caps_ok","session_ok"],
  "gates_blocked":["global_pause"],
  "policy":"positive>=0.35, very_positive>=0.65",
  "what_would_change_it":"resume trading or stronger corroboration"
}
```

---

## Contracts

Canonical message types live in `contracts/contracts.proto`. Generate code with:

```bash
make proto
```

(Or run protoc manually if you haven’t added a Makefile yet.)

---

## Folder starter layout (suggested)

```
/cmd
  /ingestion
  /decision
  /execution
/internal
  /advice        # fusion, weights
  /decision      # thresholds, gates
  /risk          # caps, drawdown, cooldowns
  /exec          # outbox, broker adapters
  /observ        # metrics, logging
  /fixtures      # tiny JSON for halts/news/ticks
/contracts
  contracts.proto
/config
  config.yaml (local)
  config.example.yaml
/docs
  PRD.md
  CONFIG.md
```

---

## Troubleshooting

- **No decisions emitted?** Check `GLOBAL_PAUSE`; verify stubs are sending fixtures.  
- **Duplicates?** Ensure dedupe window/hash is enabled.  
- **Latency spikes?** Inspect `ingest_latency_ms` and strategy timings; fall back to stubs.

---

## License / Compliance

Use providers only according to their ToS; do not redistribute their content. Keep secrets out of logs. This project is provided “as is,” for research and automation.

Happy vibing! ☕
