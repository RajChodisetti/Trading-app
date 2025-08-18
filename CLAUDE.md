# News-Driven Trading System - Claude Context

A trustworthy, low-latency trading backend that reacts to credible market-moving information with explainable decisions and robust safety guardrails.

## Current Implementation Status

### ✅ Completed (Sessions 1-5)
- **Decision Engine**: Core gates (global_pause, halt, session, liquidity, corroboration, earnings_embargo) and threshold mapping
- **Structured Logging**: JSON decision reasons with fused scores and gate details
- **Testing Framework**: End-to-end integration tests with fixtures
- **Observability**: Metrics collection and HTTP endpoint
- **Safety Rails**: Paper mode with global pause protection
- **PR Corroboration**: Soft gate requiring editorial confirmation within 15-minute window for PR-driven decisions
- **Earnings Embargo**: Soft gate converting BUY→HOLD during earnings windows with configurable buffers

### 🚧 Current Architecture

**Data Flow**: Fixtures → Advice Fusion → Gates → Decision → Logging  
**Key Files**:
- `cmd/decision/main.go` - Main decision runner with oneshot mode
- `internal/decision/engine.go` - Core fusion and gate logic  
- `scripts/run-tests.sh` - Integration test harness
- `fixtures/` - Deterministic test scenarios

## Development Workflow

### Session-Based Development
Each development session follows the "Vibe Coding" protocol (see `docs/VIBE-CODING-GUIDE.md`):

**Session Structure (60-90 min max)**:
1. **Pre-Session** (5 min): Check TODO, run health checks, create session card
2. **Planning** (10-15 min): Define acceptance criteria, identify contracts/fixtures
3. **Implementation** (30-50 min): Red-Green-Refactor with evidence collection
4. **Validation** (10-15 min): End-to-end testing and edge cases
5. **Post-Session** (5 min): Document evidence, update TODO.md

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
make test         # End-to-end integration tests (paused/resumed scenarios)
go test ./...     # Unit tests for decision engine
make doctor       # Tool/dependency check
```

## Key Contracts & Data Shapes

### Decision Output Structure
```json
{
  "symbol": "AAPL",
  "intent": "BUY_1X|BUY_5X|REJECT",  
  "reason": {
    "fused_score": 0.59,
    "per_strategy": {"AAPL": 0.69},
    "gates_passed": [],
    "gates_blocked": ["global_pause"],
    "policy": "positive>=0.35; very_positive>=0.65"
  }
}
```

### Configuration Overrides
Environment variables override config.yaml:
- `TRADING_MODE` (paper/live)
- `GLOBAL_PAUSE` (true/false)  
- `NEWS_FEED`, `QUOTES`, `HALTS`, `SENTIMENT`, `BROKER`, `ALERTS`

## Planned Development (Sessions 3+)

### Next Sessions (Priority Order)
1. **Session 6**: Transactional paper outbox with mock fills
3. **Session 7**: Wire stub ingestion loop (HTTP/WebSocket)
4. **Session 8**: Slack alerts and controls (/pause, /freeze)
5. **Session 9+**: Real adapter swaps (one at a time)

### Gate Roadmap
- ✅ `global_pause`, `halt`, `session`, `liquidity`, `corroboration`, `earnings_embargo`
- 🔜 `caps` (symbol/daily limits), `cooldown` (trade spacing)

## Troubleshooting & Operations

### Common Issues
- **Tests timeout**: Check for hanging `select{}` statements
- **Null intents in tests**: Verify jq parsing in run-tests.sh
- **No decisions**: Ensure global_pause=false and check fixtures

### Debug Commands
```bash
go run ./cmd/decision -oneshot=true          # Single run with output
go run ./cmd/decision -oneshot=false         # Metrics server mode
go run ./cmd/decision -earnings fixtures/earnings_calendar.json -oneshot=true  # Test with earnings calendar
curl http://127.0.0.1:8090/metrics | jq .   # View metrics
```

## Documentation Files

- `knowledge-base.md` - Comprehensive technical specification
- `README.md` - User-facing quick start and overview  
- `CONFIG.md` - Configuration reference
- `docs/TODO.md` - Session tracking (Now/Next/Later/Done)
- `docs/sessions/` - Per-session implementation notes