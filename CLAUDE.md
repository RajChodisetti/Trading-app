# News-Driven Trading System - Claude Context

A trustworthy, low-latency trading backend that reacts to credible market-moving information with explainable decisions and robust safety guardrails.

## Current Implementation Status

### ✅ Completed (Sessions 1-8)
- **Decision Engine**: Core gates (global_pause, halt, session, liquidity, corroboration, earnings_embargo, frozen) and threshold mapping
- **Structured Logging**: JSON decision reasons with fused scores and gate details
- **Testing Framework**: End-to-end integration tests with 8 comprehensive scenarios covering all gate logic, wire ingestion, and Slack integration
- **Observability**: Metrics collection and HTTP endpoint with gate-specific counters, decision latency tracking, and Slack metrics
- **Safety Rails**: Paper mode with global pause protection, environment variable overrides, and test isolation
- **PR Corroboration**: Soft gate requiring editorial confirmation within 15-minute window for PR-driven decisions
- **Earnings Embargo**: Soft gate converting BUY→HOLD during earnings windows with configurable buffers
- **Paper Trading Outbox**: Transactional order persistence with mock fills, idempotency, and JSONL audit trail
- **Wire Protocol Ingestion**: HTTP polling client with cursor-based streaming, exponential backoff, and bounded execution
- **Slack Alerts & Controls**: Real-time decision alerts with rate limiting, slash commands for operational control (/pause, /resume, /freeze), and runtime configuration overrides

### 🚧 Current Architecture

**Data Flow**: 
- **Fixture Mode**: Fixtures → Advice Fusion → Gates → Decision → Outbox (paper mode) → Logging
- **Wire Mode**: Wire Stub (HTTP polling) → Event Processing → Advice Fusion → Gates → Decision → Outbox → Logging

**Key Files**:
- `cmd/decision/main.go` - Main decision runner with oneshot mode, wire polling client, Slack alerts, runtime override polling, and paper trading integration
- `cmd/slack-handler/main.go` - Slack slash command handler with RBAC, signature verification, and runtime override management
- `cmd/stubs/main.go` - Wire stub server with fixture-based streaming and cursor pagination
- `internal/decision/engine.go` - Core fusion and gate logic including frozen symbol gate
- `internal/alerts/slack.go` - Async Slack alert client with rate limiting, deduplication, and retry logic
- `internal/outbox/` - Paper trading outbox with order/fill persistence and idempotency
- `internal/config/config.go` - Full configuration including Slack, security, and runtime override settings
- `scripts/run-tests.sh` - Integration test harness with 8 test cases including Slack integration
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
make test         # End-to-end integration tests (8 scenarios including Slack integration)
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
1. **Session 9**: Portfolio caps and cooldown gates
2. **Session 10**: Wire WebSocket/SSE streaming transport  
3. **Session 11**: Drawdown monitoring and circuit breakers
4. **Session 12+**: Real adapter swaps (one at a time)

### Gate Roadmap
- ✅ `global_pause`, `halt`, `session`, `liquidity`, `corroboration`, `earnings_embargo`, `frozen`
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

# Wire mode commands
go run ./cmd/stubs -stream -port 8091 &                                        # Start wire stub server
go run ./cmd/decision -wire-mode -wire-url=http://localhost:8091 -max-events=10 # Run wire polling mode
curl http://localhost:8091/health                                              # Wire stub health check
curl http://localhost:8091/stream                                              # View wire events stream

# Slack integration commands
SLACK_SIGNING_SECRET=your_secret go run ./cmd/slack-handler -port 8092          # Start Slack handler
SLACK_ENABLED=true SLACK_WEBHOOK_URL=https://hooks.slack.com/... \             # Enable Slack alerts
  go run ./cmd/decision -oneshot=false
curl -X POST http://localhost:8092/slack/commands -d "command=/status&user_id=U12345" # Test slash command

# Runtime override commands
echo '{"version":1,"global_pause":true,"frozen_symbols":[{"symbol":"AAPL","until_utc":"2025-12-31T23:59:59Z"}]}' > data/runtime_overrides.json
go run ./cmd/decision -oneshot=true                                            # Test runtime overrides

# Monitoring
curl http://127.0.0.1:8090/metrics | jq .                                     # View metrics (includes Slack metrics)
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
- **Core integration test scenarios pass consistently (Cases 1-7)** ✅
- **Unit tests compile and run successfully** ✅ 
- **Environment variable overrides working** ✅
- **Oneshot vs server mode functioning correctly** ✅
- **Paper trading outbox with idempotency working** ✅
- **Wire mode ingestion with cursor pagination working** ✅
- **Slack alerts and operational controls working** ✅
- **Runtime configuration overrides with hot reload working** ✅
- **Frozen symbol gate implementation working** ✅
- **Git workflow with session handoffs established** ✅

## Documentation Files

- `knowledge-base.md` - Comprehensive technical specification
- `README.md` - User-facing quick start and overview  
- `CONFIG.md` - Configuration reference
- `docs/TODO.md` - Session tracking (Now/Next/Later/Done)
- `docs/sessions/` - Per-session implementation notes
- `docs/VIBE-CODING-GUIDE.md` - Session workflow protocol
- `docs/NEXT-SESSION-PLAN.md` - Next session planning template