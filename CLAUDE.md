# News-Driven Trading System - Claude Context

A trustworthy, low-latency trading backend that reacts to credible market-moving information with explainable decisions and robust safety guardrails.

## Current Implementation Status

### ✅ Completed (Sessions 1-6)
- **Decision Engine**: Core gates (global_pause, halt, session, liquidity, corroboration, earnings_embargo) and threshold mapping
- **Structured Logging**: JSON decision reasons with fused scores and gate details
- **Testing Framework**: End-to-end integration tests with 6 comprehensive scenarios covering all gate logic
- **Observability**: Metrics collection and HTTP endpoint with gate-specific counters and decision latency tracking
- **Safety Rails**: Paper mode with global pause protection, environment variable overrides, and test isolation
- **PR Corroboration**: Soft gate requiring editorial confirmation within 15-minute window for PR-driven decisions
- **Earnings Embargo**: Soft gate converting BUY→HOLD during earnings windows with configurable buffers
- **Paper Trading Outbox**: Transactional order persistence with mock fills, idempotency, and JSONL audit trail

### 🚧 Current Architecture

**Data Flow**: Fixtures → Advice Fusion → Gates → Decision → Outbox (paper mode) → Logging  
**Key Files**:
- `cmd/decision/main.go` - Main decision runner with oneshot mode and paper trading integration
- `internal/decision/engine.go` - Core fusion and gate logic  
- `internal/outbox/` - Paper trading outbox with order/fill persistence and idempotency
- `scripts/run-tests.sh` - Integration test harness with 6 test cases
- `fixtures/` - Deterministic test scenarios

## Development Workflow

### Session-Based Development
Each development session follows the "Vibe Coding" protocol (see `docs/VIBE-CODING-GUIDE.md`):

**Session Structure (60-90 min max)**:
1. **Pre-Session** (5 min): Check TODO, run health checks, create session card
2. **Planning** (10-15 min): Define acceptance criteria, identify contracts/fixtures
3. **Implementation** (30-50 min): Red-Green-Refactor with evidence collection
4. **Validation** (10-15 min): End-to-end testing and edge cases
5. **Post-Session** (5-10 min): Document evidence, update TODO.md, git commit/push, update next session plan

**Key Principles**:
- One vertical slice per session
- Evidence-driven development (logs + metrics)
- Safety rails always on (paper mode, global pause)
- Time-boxed with clear acceptance criteria

### Safety-First Approach
- **Always**: `trading_mode: paper` and `global_pause: true`
- **Test First**: Fixtures before real data feeds
- **One Adapter**: Swap providers incrementally  
- **Evidence Required**: All changes must show decision logs + metrics

### Testing Strategy
```bash
make test         # End-to-end integration tests (6 scenarios including paper outbox)
go test ./...     # Unit tests for decision engine and outbox
make doctor       # Tool/dependency check
```

## Key Contracts & Data Shapes

### Decision Output Structure
```json
{
  "symbol": "AAPL",
  "intent": "BUY_1X|BUY_5X|REDUCE|HOLD|REJECT",  
  "reason": {
    "fused_score": 0.59,
    "per_strategy": {"AAPL": 0.69},
    "gates_passed": [],
    "gates_blocked": ["global_pause"],
    "policy": "positive>=0.35; very_positive>=0.65"
  }
}
```

### Paper Trading Outbox Structure
```json
{"type":"order","data":{"id":"order_AAPL_1755803877176311000","symbol":"AAPL","intent":"BUY_1X","timestamp":"2025-08-21T19:17:57.176311Z","status":"pending","idempotency_key":"1b7668105ed23807"},"event":"2025-08-21T19:17:57.567881Z"}
{"type":"fill","data":{"order_id":"order_AAPL_1755803877176311000","symbol":"AAPL","quantity":1.0,"price":210.02,"side":"BUY","timestamp":"2025-08-21T19:17:58.176311Z","latency_ms":1500,"slippage_bps":3},"event":"2025-08-21T19:17:58.568881Z"}
```

### Configuration Overrides
Environment variables override config.yaml:
- `TRADING_MODE` (paper/live)
- `GLOBAL_PAUSE` (true/false)  
- `NEWS_FEED`, `QUOTES`, `HALTS`, `SENTIMENT`, `BROKER`, `ALERTS`

## Planned Development (Sessions 3+)

### Next Sessions (Priority Order)
1. **Session 7**: Wire stub ingestion loop (HTTP/WebSocket/NATS)
2. **Session 8**: Slack alerts and controls (/pause, /freeze)
3. **Session 9**: Portfolio caps and cooldown gates
4. **Session 10+**: Real adapter swaps (one at a time)

### Gate Roadmap
- ✅ `global_pause`, `halt`, `session`, `liquidity`, `corroboration`, `earnings_embargo`
- 🔜 `caps` (symbol/daily limits), `cooldown` (trade spacing)

## Troubleshooting & Operations

### Common Issues (Recently Fixed)
- **Tests timeout**: Fixed duplicate metrics server startup in oneshot mode
- **Environment variable conflicts**: Fixed test script to clear GLOBAL_PAUSE/TRADING_MODE for config file control  
- **Timing-dependent test failures**: Updated fixture timestamps to maintain corroboration windows
- **No decisions**: Ensure global_pause=false via config or environment override

### Debug Commands
```bash
go run ./cmd/decision -oneshot=true                                            # Single run with output
go run ./cmd/decision -oneshot=false                                           # Metrics server mode
GLOBAL_PAUSE=false go run ./cmd/decision -oneshot=true                         # Override config with env vars
go run ./cmd/decision -earnings fixtures/earnings_calendar.json -oneshot=true  # Test with earnings calendar
curl http://127.0.0.1:8090/metrics | jq .                                     # View metrics
curl http://127.0.0.1:8090/health                                             # Health check endpoint
```

## Recent Improvements (Session 5+)

### 🔧 Major Fixes Applied
- **Fixed duplicate metrics server bug**: Removed hardcoded server startup that ignored oneshot flag
- **Added environment variable overrides**: `GLOBAL_PAUSE` and `TRADING_MODE` now properly override config.yaml
- **Resolved test timing issues**: Updated fixture timestamps to maintain active corroboration/embargo windows  
- **Enhanced test isolation**: Test script clears env vars to allow config file control
- **Improved error handling**: Better diagnostics for missing configs/fixtures

### 🎯 Current System Reliability
- **All 6 integration test scenarios pass consistently** ✅
- **Unit tests compile and run successfully** ✅ 
- **Environment variable overrides working** ✅
- **Oneshot vs server mode functioning correctly** ✅
- **Paper trading outbox with idempotency working** ✅
- **Git workflow with session handoffs established** ✅

## Documentation Files

- `knowledge-base.md` - Comprehensive technical specification
- `README.md` - User-facing quick start and overview  
- `CONFIG.md` - Configuration reference
- `docs/TODO.md` - Session tracking (Now/Next/Later/Done)
- `docs/sessions/` - Per-session implementation notes
- `docs/VIBE-CODING-GUIDE.md` - Session workflow protocol
- `docs/NEXT-SESSION-PLAN.md` - Next session planning template