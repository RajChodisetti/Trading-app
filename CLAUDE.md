# News-Driven Trading System - Claude Context

A trustworthy, low-latency trading backend that reacts to credible market-moving information with explainable decisions and robust safety guardrails.

## Current Implementation Status

### ✅ Completed (Sessions 1-17)
- **Decision Engine**: Core gates (global_pause, halt, session, liquidity, corroboration, earnings_embargo, frozen) and threshold mapping
- **Structured Logging**: JSON decision reasons with fused scores and gate details
- **Testing Framework**: End-to-end integration tests with 10 comprehensive scenarios covering all gate logic, wire ingestion, Slack integration, and risk controls
- **Observability**: Metrics collection and HTTP endpoint with gate-specific counters, decision latency tracking, and Slack metrics
- **Safety Rails**: Paper mode with global pause protection, environment variable overrides, and test isolation
- **PR Corroboration**: Soft gate requiring editorial confirmation within 15-minute window for PR-driven decisions
- **Earnings Embargo**: Soft gate converting BUY→HOLD during earnings windows with configurable buffers
- **Paper Trading Outbox**: Transactional order persistence with mock fills, idempotency, and JSONL audit trail
- **Wire Protocol Ingestion**: HTTP polling client with cursor-based streaming, exponential backoff, and bounded execution
- **Slack Alerts & Controls**: Real-time decision alerts with rate limiting, slash commands for operational control (/pause, /resume, /freeze), and runtime configuration overrides
- **Advanced Risk Controls**: Stop-loss with 24h cooldown, sector exposure limits (40% max), drawdown monitoring (2%/3% daily thresholds), and Slack dashboard for real-time portfolio monitoring
- **SSE Streaming Transport**: Real-time Server-Sent Events transport with reconnection logic, gap detection, backpressure handling, and 700% performance improvement over HTTP polling
- **Live Quote Feeds (Session 17)**: Alpha Vantage shadow mode with canary rollout, budget-aware sampling, hotpath protection, health monitoring with hysteresis, promotion gates automation, and comprehensive production readiness validation

### 🚧 Current Architecture

**Data Flow**: 
- **Fixture Mode**: Fixtures → Advice Fusion → Gates → Decision → Outbox (paper mode) → Logging
- **Wire Mode**: Wire Stub (SSE streaming/HTTP fallback) → Event Processing → Advice Fusion → Gates → Decision → Outbox → Logging
- **Shadow Mode**: Live Provider (Alpha Vantage) → Cache → Shadow Comparison → Decision Engine (hotpath isolated) → Outbox → Promotion Gates

**Key Files**:
- `cmd/decision/main.go` - Main decision runner with oneshot mode, SSE streaming client, Slack alerts, runtime override polling, and paper trading integration
- `cmd/slack-handler/main.go` - Slack slash command handler with RBAC, signature verification, and runtime override management
- `cmd/stubs/main.go` - Wire stub server with fixture-based streaming and cursor pagination
- `internal/decision/engine.go` - Core fusion and gate logic including frozen symbol gate
- `internal/alerts/slack.go` - Async Slack alert client with rate limiting, deduplication, and retry logic
- `internal/outbox/` - Paper trading outbox with order/fill persistence and idempotency
- `internal/transport/` - Transport abstraction with SSE and HTTP clients, reconnection logic, and streaming metrics
- `internal/config/config.go` - Full configuration including Slack, security, and runtime override settings
- `internal/adapters/live_quotes.go` - Live quote adapter with shadow mode, canary rollout, health monitoring, and cache bounds
- `internal/adapters/live_integration.go` - Integration layer with comprehensive metrics and promotion gate evaluation
- `internal/adapters/state_persistence.go` - State persistence and graceful shutdown management
- `internal/observ/metrics.go` - Enhanced health monitoring with `/healthz` endpoint for promotion gates
- `scripts/check-promotion.sh` - Automated promotion gates checker with rolling window validation
- `scripts/demo-shadow-mode.sh` - Live demonstration script for shadow mode functionality
- `config/live_feeds.yaml` - Live feed configuration with canary approach and adaptive tiers
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

### SSE Streaming Transport Structure
```json
{
  "v": 1,
  "type": "news|tick|halt|earnings",
  "id": "aapl-positive-1",
  "ts_utc": "2025-08-23T21:44:25Z",
  "payload": {...}
}
```

## Planned Development (Sessions 12+)

### Next Sessions (Priority Order)
1. **Session 18**: Live mode promotion + multi-provider foundation (Polygon.io)
2. **Session 19**: Real-time WebSocket feeds + streaming architecture
3. **Session 20+**: Additional live feeds (halts, news, sentiment) with same promotion gate framework

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

# Wire mode commands (SSE streaming)
go run ./cmd/stubs -stream -port 8091 &                                        # Start wire stub server with SSE
go run ./cmd/decision -wire-mode -wire-url=http://localhost:8091 -max-events=10 # Run with SSE streaming transport
curl http://localhost:8091/health                                              # Wire stub health check
curl -H "Accept: text/event-stream" http://localhost:8091/stream               # Test SSE stream endpoint

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
curl http://127.0.0.1:8090/healthz | jq .                                     # Comprehensive health with promotion gates

# Shadow mode and live feeds (Session 17+)
ALPHAVANTAGE_API_KEY=your_key ./scripts/demo-shadow-mode.sh                    # Demo shadow mode functionality
./scripts/check-promotion.sh --window-minutes 30                              # Check promotion gates
QUOTES=alphavantage GLOBAL_PAUSE=false go run ./cmd/decision -oneshot=false   # Run with Alpha Vantage
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
- **SSE streaming transport with 700% performance improvement** ✅
- **Slack alerts and operational controls working** ✅
- **Runtime configuration overrides with hot reload working** ✅
- **Frozen symbol gate implementation working** ✅
- **Transport abstraction with graceful fallback working** ✅
- **Git workflow with session handoffs established** ✅

## Documentation Files

- `knowledge-base.md` - Comprehensive technical specification
- `README.md` - User-facing quick start and overview  
- `CONFIG.md` - Configuration reference
- `docs/TODO.md` - Session tracking (Now/Next/Later/Done)
- `docs/sessions/` - Per-session implementation notes
- `docs/VIBE-CODING-GUIDE.md` - Session workflow protocol
- `docs/NEXT-SESSION-PLAN.md` - Next session planning template