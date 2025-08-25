# Session 17: Live Quote Feeds - Alpha Vantage Shadow Mode & Production Readiness

## Session Overview
**Duration**: 60-90 minutes  
**Type**: Integration & Production Activation  
**Focus**: Enable Alpha Vantage shadow mode with promotion gates for live quote feed activation

## Context from Session 16
✅ **Session 16 Stage 1 Completed**: Test framework deterministically fixed
- Implemented `TEST_MODE=fixtures` environment routing for deterministic testing
- Fixed critical wire mode bug in nested payload parsing (news sentiment + trend-lite now working)
- Resolved all integration test failures (after_hours, pr_only, wire_mode)
- Applied consistent fixture-based testing across all test cases

🎯 **Ready for Stage 2**: Live adapter integration with safety gates and promotion criteria

## Session 17 Goals

### Primary Objective: Alpha Vantage Shadow Mode Activation
1. **Enable Alpha Vantage shadow mode** - With hysteresis and promotion gates  
2. **Validate quote cache performance** - Ensure decision p95 <200ms, hotpath isolation
3. **Test fallback mechanisms** - Verify Mock→Cache→AlphaVantage with auto-recovery
4. **Production readiness validation** - Meet 30-60min stability window for promotion gates

### Secondary Objectives: Enhanced Observability
5. **Rate limit + adaptive cadence** - Budget-aware refresh intervals with priority symbols
6. **Health monitoring with hysteresis** - Prevent flapping with consecutive-breach rules
7. **Observability + compliance** - /healthz endpoint, payload scanning, structured logs
8. **Live activation criteria** - Validate promotion gates and enable live mode safely

## Technical Implementation Plan

### Stage 2: Alpha Vantage Shadow Mode with Promotion Gates (20-25 min)
**Config Changes** (`config/live_feeds.yaml`):
```yaml
feeds:
  quotes:
    live_enabled: false                    # Keep false until promotion gates met
    shadow_mode: true                      # Enable shadow mode
    provider: "alphavantage"               # Switch from mock
    refresh_interval_ms: 800               # Base refresh rate
    freshness_ceiling_seconds: 5           # RTH staleness limit  
    hysteresis_seconds: 3                  # Prevent flapping
    consecutive_breach_to_degrade: 3       # Require 3 consecutive failures
    consecutive_ok_to_recover: 5           # Require 5 consecutive successes
    daily_request_cap: 300                 # Conservative daily limit
    fallback_to_mock: true                 # Ultimate fallback
    # Priority symbol ordering for adaptive cadence
    priority_symbols: ["AAPL", "NVDA", "SPY", "TSLA", "QQQ"]
```

**Promotion Gates (30-60min window required)**:
- ✅ p95 freshness ≤ 5s RTH (≤ 60s AH) 
- ✅ p95 decision latency ≤ 200ms
- ✅ Success rate ≥ 99% (non-error fetches)
- ✅ Zero liquidity-gate stalls from quote staleness
- ✅ `hotpath_live_calls_total == 0` (cache isolation verified)

**Shadow Comparison Heuristics**:
- Spread ratio `spread_live/spread_mock` within [0.5, 2.0]
- Mid difference ≤ X bps for large caps
- Timestamp delta ≤ freshness ceiling
- Emit `shadow_mismatch_total{kind="spread|mid|staleness"}`

**Evidence Collection**: 5-10 screenshots/log snippets of stable metrics for session doc

### Stage 3: Health & Fallback Testing with Auto-Recovery (15-20 min)
**Test Scenarios**:
1. **Rate limit simulation**: Exceed daily cap threshold → adaptive cadence + fallback
2. **DNS failure simulation**: Point base URL to unroutable host → timeout handling
3. **Stale data handling**: Force quotes older than freshness ceiling → degraded state
4. **Auto-recovery validation**: Restore provider → consecutive-ok recovery to healthy
5. **Kill switch test**: Flip `disable_live_quotes=true` mid-run → graceful exit + cache/mock continuation

**Expected Behavior with Hysteresis**:
- Health state changes only after K consecutive breaches/successes (no flapping)
- Automatic fallback: Live → Cache → Mock with structured transition logging
- Auto-recovery cool-off: require 5 consecutive healthy probes before leaving failed
- Decision engine continues operating (zero blocked decisions)

### Stage 4: Enhanced Observability + Compliance (10-15 min)
**New /healthz Endpoint** (easier than jq on /metrics):
```bash
curl http://127.0.0.1:8090/healthz | jq
# Expected: {"provider":"alphavantage","status":"healthy","freshness_p95_s":2.1,"error_rate_5m":0.0,"fallback":"none"}
```

**Compliance Scanning**:
```bash
# Unit test to scan logs/metrics for API keys or raw payloads
go test -run TestComplianceGuard ./internal/adapters
```

**Key Metrics to Validate**:
- `hotpath_live_calls_total == 0` (decision isolation)
- `fallback_activations_total{to="cache|mock"}` + recovery histograms
- `shadow_mismatch_total{kind="spread|mid|staleness"}` < 2%
- Health transition events in structured logs

### Stage 5: Production Readiness & Live Activation (5-10 min)
**Enhanced Pre-Flight Checklist**:
- [ ] **Hotpath isolation**: `hotpath_live_calls_total == 0` verified
- [ ] **Promotion gates met**: 30-60min window of stable metrics documented
- [ ] **Kill switches functional**: `disable_live_quotes=true` → graceful degradation
- [ ] **Compliance verified**: No payloads/API keys found in logs (TestComplianceGuard passes)
- [ ] **Auto-recovery tested**: Failed → Healthy transition in <5min after restoration
- [ ] **Adaptive cadence**: Budget-aware refresh interval adjustment verified

**Live Activation Process**:
1. Validate all promotion gates over 30-60min window
2. Document evidence collection (metrics screenshots)
3. Update `live_enabled: true` in config
4. Monitor hotpath isolation and fallback behavior
5. Verify decision latency remains <200ms

## Enhanced Acceptance Criteria

### Must Have ✅ (Production Critical)
1. **Shadow mode active**: Alpha Vantage running in shadow mode with comparison metrics
2. **Hotpath isolation**: `hotpath_live_calls_total == 0` throughout session
3. **Promotion gates met**: ≥30min window of p95 freshness ≤5s, latency ≤200ms, success rate ≥99%
4. **Fallback chain verified**: Live → Cache → Mock with structured logging
5. **Compliance validated**: TestComplianceGuard passes (no API keys/payloads in logs)

### Should Have 📋 (Enhanced Safety)
6. **Shadow mismatch ratio**: <2% of samples across priority symbols
7. **Auto-recovery validated**: Failed → Healthy in <5min with consecutive-ok rules
8. **Health hysteresis**: State changes only after K consecutive breaches (no flapping)
9. **Adaptive cadence**: Refresh interval widens when budget <15%, shrinks when >40%

### Nice to Have ➕ (Production-Ready)
10. **Structured health events**: from/to/reason/window_stats in logs
11. **Symbol prioritization**: Positions > watchlist > rest during budget constraints
12. **Shadow comparison alerts**: Spread/mid/staleness mismatch detection
13. **Live mode activation**: If promotion gates met, enable live mode safely

## Risk Mitigation

### High Risk 🚨 (Tightened)
- **Latency spikes**: Hotpath isolation ensures decision loop reads only from cache (`cache_miss_total == 0`)
- **Quota burn**: Adaptive cadence + per-symbol priority + daily cap hard stop
- **Health flapping**: Hysteresis (3 consecutive failures) + K-consecutive recovery rule

### Medium Risk ⚠️ (New Controls)
- **Provider reliability**: Shadow mode + automatic fallback with auto-recovery
- **Data quality**: Shadow comparison heuristics catch suspicious spread/mid/staleness differences
- **Live activation premature**: Require 30-60min stability window before live mode

### Low Risk ℹ️ (Operational)  
- **Promotion timing**: Clear stability gates prevent premature live activation
- **Configuration complexity**: /healthz JSON endpoint simplifies monitoring vs metrics scraping
- **Compliance audit**: TestComplianceGuard scans for payload/API key leakage

## Session Handoff Notes

**Session 16 Achievements**:
✅ **Test Framework Deterministic**: Fixed all integration test failures
✅ **Wire Mode Fixed**: Nested payload parsing for news sentiment + trend-lite working
✅ **Consistent Testing**: All tests use pure fixture data with TEST_MODE routing

**If Session 17 succeeds:**
- Ready for Session 18: Live halts feed integration
- Quote cache architecture validated for other data feeds  
- Provider health monitoring proven for scaled deployment
- Live quote feeds activated safely with promotion gates

## Files to Focus On
- `config/live_feeds.yaml` - Feature flag configuration (create this file)
- `internal/adapters/integration_test.go` - Shadow mode and health testing
- `internal/adapters/quotes.go` - Alpha Vantage integration and caching
- `cmd/decision/main.go` - Hotpath isolation verification
- `internal/observability/` - Health endpoint and compliance scanning

## Environment Setup
```bash
# Required for live integration
export ALPHAVANTAGE_API_KEY="your_key_here"  
export LIVE_QUOTES_ENABLED="false"           # Shadow mode first

# Validation pipeline
make test                                     # Should pass consistently now
go test -run TestComplianceGuard ./internal/adapters  # API key/payload scanning
go run ./cmd/decision -oneshot=true          # Manual quote validation
curl http://127.0.0.1:8090/healthz | jq     # Health endpoint validation
```

## Actionable TODO Items (Ready for Session 17)
- [ ] Create live_feeds.yaml config file with shadow mode settings
- [ ] Implement health hysteresis & consecutive-breach rules  
- [ ] Add /healthz JSON endpoint with provider status & freshness
- [ ] Add `hotpath_live_calls_total` guard metric & CI assertion
- [ ] Write TestComplianceGuard to scan logs for payload/API keys
- [ ] Document promotion criteria checklist (append to session doc)
- [ ] Enable Alpha Vantage shadow mode and collect evidence
- [ ] Validate promotion gates over 30-60min window

## Ready-to-Use Config (Create config/live_feeds.yaml)
```yaml
feeds:
  quotes:
    live_enabled: false                    # Keep false until promotion gates met
    shadow_mode: true                      # Enable shadow mode  
    provider: "alphavantage"               # Switch from mock
    refresh_interval_ms: 800               # Base refresh rate
    freshness_ceiling_seconds: 5           # RTH staleness limit
    hysteresis_seconds: 3                  # Prevent flapping
    consecutive_breach_to_degrade: 3       # Require 3 consecutive failures
    consecutive_ok_to_recover: 5           # Require 5 consecutive successes  
    daily_request_cap: 300                 # Conservative daily limit
    fallback_to_mock: true                 # Ultimate fallback
    priority_symbols: ["AAPL", "NVDA", "SPY", "TSLA", "QQQ"]
```

---
**Next Session After 17**: Live halts feed integration with NASDAQ/Polygon shadow mode + same promotion gate methodology