# Session 16: Live Quote Feeds & System Integration

## Session Overview
**Duration**: 60-90 minutes  
**Type**: Integration & Testing  
**Focus**: Activate live quote feeds safely and resolve test framework issues

## Context from Session 15
✅ **Session 15 Completed**: Real adapter integration architecture with 6 major new files (~2000+ lines)
- Background quote refresher with cache-first decision reads  
- Provider health monitoring with automatic fallback
- Shadow mode for halts/news validation
- VCR and chaos testing frameworks
- Comprehensive observability and feature flags

🐛 **Issue Identified**: Pre-existing test framework inconsistency where AAPL trend-lite signals aren't generated consistently, causing integration test failures unrelated to Session 15 adapter work.

## Session 16 Goals

### Primary Objective: Live Quote Feed Activation
1. **Fix test framework** - Resolve AAPL trend-lite signal generation inconsistencies
2. **Enable Alpha Vantage quotes** - Set `live_feeds.yaml` quotes to shadow mode first
3. **Validate quote cache performance** - Ensure decision p95 <200ms with live data
4. **Test fallback mechanisms** - Verify Mock→Cache→AlphaVantage fallback chain

### Secondary Objectives: Production Readiness
5. **Rate limit validation** - Test 5 req/min and daily budget enforcement  
6. **Health monitoring** - Verify healthy→degraded→failed state transitions
7. **Observability integration** - Confirm metrics flow to decision engine
8. **Compliance verification** - Ensure no raw API data storage

## Technical Implementation Plan

### Stage 1: Test Framework Stabilization (15-20 min)
**Problem**: Mock adapter AAPL quotes vs ticks.json fixture inconsistency
**Root Cause**: Feature processing prioritizes mock quotes over fixture ticks
**Fix Options**:
- Option A: Update fixtures to be consistent with mock adapter values
- Option B: Fix mock adapter to respect fixture overrides  
- Option C: Separate test configs for different test scenarios

**Expected Outcome**: Integration tests pass consistently

### Stage 2: Alpha Vantage Shadow Mode (20-25 min)
**Config Changes** (`config/live_feeds.yaml`):
```yaml
feeds:
  quotes:
    live_enabled: false          # Keep false initially
    shadow_mode: true            # Enable shadow mode
    provider: "alphavantage"     # Switch from mock
```

**Validation Steps**:
1. Background refresher starts Alpha Vantage adapter
2. Cache populated with live quotes (verify staleness <5s RTH)
3. Decision engine still reads from cache (not blocked by rate limits)
4. Shadow metrics show quote comparison (live vs mock)

**Success Criteria**: 
- Decision latency remains <200ms p95
- Cache hit ratio >90%
- No rate limit exhaustion

### Stage 3: Health & Fallback Testing (15-20 min)
**Test Scenarios**:
1. **Rate limit simulation**: Exceed 5 req/min threshold
2. **Provider failure**: Network timeout/error injection
3. **Stale data handling**: Quotes older than freshness ceiling
4. **Recovery validation**: Provider returns to healthy state

**Expected Behavior**:
- Healthy → Degraded → Failed state transitions logged
- Automatic fallback to cache when rate limited
- Mock adapter fallback when all else fails
- Decision engine continues operating (no decisions blocked)

### Stage 4: Observability Integration (10-15 min)
**Metrics Validation**:
```bash
curl http://127.0.0.1:8090/metrics | jq '.provider_status'
curl http://127.0.0.1:8090/metrics | jq '.quote_cache_hit_ratio'
curl http://127.0.0.1:8090/metrics | jq '.rate_budget_remaining'
```

**Dashboard Integration**: Verify new adapter metrics appear in decision logs

### Stage 5: Production Readiness (5-10 min)
**Pre-Flight Checklist**:
- [ ] API key security (env var only, never logged)
- [ ] Rate limiting enforced (token bucket + daily cap)  
- [ ] Error handling graceful (no crash on network issues)
- [ ] Compliance verified (no full content storage)
- [ ] Kill switches functional (`disable_live_quotes: true`)

## Acceptance Criteria

### Must Have ✅
1. **Tests pass**: All integration tests run cleanly
2. **Shadow mode works**: Live quotes flow through cache to decisions  
3. **Performance maintained**: Decision p95 <200ms with live data
4. **Fallback verified**: Rate limits trigger graceful degradation
5. **Observability functional**: Provider health metrics visible

### Should Have 📋
6. **Alpha Vantage integration**: Real quotes for AAPL, NVDA, SPY
7. **Budget management**: Daily cap enforcement with alerts
8. **Error resilience**: Network failures don't crash system
9. **State persistence**: Provider health survives restarts

### Nice to Have ➕
10. **Multiple symbols**: Cache warming for priority symbols
11. **Batch optimization**: Multiple quote requests minimized
12. **Adaptive refresh**: Refresh rate adjusts based on market hours

## Risk Mitigation

### High Risk 🚨
- **API key exposure**: Never log API keys, secure env var handling
- **Rate limit violation**: Could exhaust daily quota, implement conservative limits
- **Decision latency**: Live quotes might slow decision engine below SLA

### Medium Risk ⚠️ 
- **Provider reliability**: Alpha Vantage downtime affects quote quality
- **Data quality**: Stale/invalid quotes could trigger bad decisions
- **Memory usage**: Quote cache growth without proper eviction

### Low Risk ℹ️
- **Configuration complexity**: Feature flags might be confusing
- **Testing overhead**: VCR recordings need maintenance

## Session Handoff Notes

**If Session 16 is incomplete:**
- Core adapter architecture from Session 15 is production-ready
- Test framework issues are pre-existing, not from Session 15 work  
- Live quote feeds can be activated independently of test fixes
- Shadow mode is safest first step before full live activation

**If Session 16 succeeds:**
- Ready for Session 17: Live halts feed integration
- Quote cache architecture validated for other data feeds
- Provider health monitoring proven for scaled deployment

## Files to Focus On
- `config/live_feeds.yaml` - Feature flag configuration
- `internal/adapters/integration_test.go` - Test framework fixes
- `cmd/decision/main.go` - AAPL trend-lite signal debugging
- `fixtures/ticks.json` - Test data consistency
- `scripts/run-tests.sh` - Test harness validation

## Environment Setup
```bash
export ALPHAVANTAGE_API_KEY="your_key_here"  
export LIVE_QUOTES_ENABLED="false"           # Shadow mode first
make test                                     # Baseline test pass
go run ./cmd/decision -oneshot=true          # Manual quote validation
```

---
**Next Session After 16**: Live halts feed integration with NASDAQ/Polygon shadow mode testing