# Session 18: Live Mode Promotion & Multi-Provider Foundation

## Session Overview
**Duration**: 60-90 minutes  
**Type**: Production Activation & Architecture Enhancement  
**Focus**: Promote to live mode after validation + foundation for multi-provider scaling

## Context from Session 17
✅ **Session 17 Completed**: Alpha Vantage Shadow Mode & Production Readiness
- Implemented comprehensive live quote adapter with canary rollout
- Added shadow mode with budget-aware sampling and mismatch detection
- Built automated promotion gates checker with 30s intervals
- Created hotpath protection with live call tracking
- Added health monitoring with hysteresis and graceful shutdown
- Validated all acceptance criteria with comprehensive testing

🎯 **Ready for Session 18**: Live mode activation + multi-provider foundation

## Session 18 Goals

### Primary Objective: Live Mode Promotion
1. **Validate promotion gates in production** - Run 30-60 min validation window
2. **Activate live mode safely** - Enable `live_enabled: true` with monitoring
3. **Verify live performance** - Confirm P95 latency <200ms, success rate >99%
4. **Test emergency procedures** - Validate kill switches and rollback mechanisms

### Secondary Objective: Multi-Provider Foundation  
5. **Polygon.io integration** - Add second live provider with same safety framework
6. **Provider failover logic** - Automatic provider switching on health degradation
7. **Comparative analytics** - Cross-provider quality metrics and cost analysis
8. **Scalable adapter factory** - Generic framework for adding new providers

## Technical Implementation Plan

### Stage 1: Live Mode Promotion (25-30 min)
**Pre-flight Validation:**
```bash
# Run promotion gates checker for 30+ minutes
./scripts/check-promotion.sh --window-minutes 30

# Must show PASS status with:
# - Freshness P95 ≤ 5000ms (RTH), ≤ 60000ms (AH)
# - Success rate ≥ 99%
# - Decision P95 ≤ 200ms  
# - hotpath_live_calls_total == 0
# - Shadow mismatch rate ≤ 2%
```

**Live Activation Steps:**
1. **Environment setup**:
   ```yaml
   # config/live_feeds.yaml
   feeds:
     quotes:
       live_enabled: true              # 🚨 Enable live mode
       shadow_mode: true               # Keep shadow for comparison
       provider: "alphavantage"
   ```

2. **Monitoring dashboard**: Real-time metrics validation
3. **Emergency procedures**: Test kill switches and rollback
4. **Performance validation**: Confirm live performance metrics

### Stage 2: Polygon.io Integration (25-30 min)
**Provider Implementation:**
```yaml
# Add to config/live_feeds.yaml
providers:
  polygon:
    api_key_env: "POLYGON_API_KEY"
    rate_limit_per_minute: 100        # Much higher than AV
    daily_request_cap: 50000          # Professional tier
    base_url: "https://api.polygon.io/v2"
    timeout_seconds: 5
    
    # Polygon advantages
    real_time_data: true              # vs AV delayed quotes
    batch_support: true               # vs AV single quotes only
    websocket_streaming: true         # Future enhancement
```

**Multi-Provider Architecture:**
```go
// internal/adapters/provider_manager.go
type ProviderManager struct {
    providers    map[string]QuotesAdapter
    healthStates map[string]HealthState
    failoverRule FailoverStrategy
}

// Failover strategies:
// - Primary/Secondary: AV primary, Polygon backup
// - Load Balancing: Distribute by symbol hash
// - Quality-Based: Route to best-performing provider
```

### Stage 3: Enhanced Analytics (10-15 min)
**Cross-Provider Metrics:**
```go
type ProviderComparison struct {
    Provider       string    `json:"provider"`
    SuccessRate    float64   `json:"success_rate"`
    LatencyP95Ms   int64     `json:"latency_p95_ms"`
    FreshnessP95Ms int64     `json:"freshness_p95_ms"`
    CostPerQuote   float64   `json:"cost_per_quote"`
    DataQuality    float64   `json:"data_quality_score"`
}
```

**Quality Scoring:**
- Freshness weight: 40%
- Spread accuracy: 30% 
- Uptime/reliability: 20%
- Cost efficiency: 10%

## Acceptance Criteria

### Must Have (Live Promotion)
- [ ] Promotion gates pass consistently for ≥30 minutes
- [ ] Live mode activated with monitoring dashboard  
- [ ] Emergency kill switch tested and verified
- [ ] P95 decision latency remains ≤200ms in live mode
- [ ] Success rate maintains ≥99% in live mode

### Should Have (Multi-Provider)  
- [ ] Polygon.io adapter implemented with same safety framework
- [ ] Provider failover tested (AV degraded → Polygon activated)
- [ ] Cross-provider quality metrics dashboard
- [ ] Cost analysis showing $/quote for each provider

### Nice to Have (Architecture)
- [ ] Generic provider factory for easy extension
- [ ] WebSocket streaming foundation (Polygon)  
- [ ] Provider load balancing based on symbol routing
- [ ] Historical provider performance tracking

## Risk Mitigation

### Live Mode Risks
- **Latency spike**: Auto-fallback to cache if P95 >500ms
- **API rate limits**: Budget monitoring with automatic throttling
- **Provider outage**: Immediate fallback to mock with alerts
- **Cost overrun**: Hard daily caps with automatic disable

### Multi-Provider Risks  
- **Configuration complexity**: Use provider profiles with validation
- **State synchronization**: Independent health tracking per provider
- **Failover timing**: Fast detection (10s) with hysteresis (2min)

## Success Metrics

### Live Mode KPIs
- Decision latency P95: <200ms (target: 150ms)
- Quote success rate: >99% (target: 99.5%)
- Cache hit rate: >80% (cost optimization)
- Shadow mismatch rate: <2% (data quality)

### Multi-Provider KPIs  
- Provider availability: >99.9% combined
- Failover time: <10 seconds detection, <30s recovery
- Cost efficiency: <$0.01 per quote (blended)
- Data quality score: >95% across all providers

## Implementation Notes

### Alpha Vantage Production Considerations
- Free tier: 300 requests/day (sufficient for shadow mode)
- Paid tier: 75 requests/min (needed for live mode scaling)
- Data delay: 15-20 minutes (acceptable for paper trading)
- Upgrade trigger: Daily request usage >250

### Polygon.io Evaluation Criteria
- Real-time data: Sub-second freshness
- Higher rate limits: 100+ requests/minute  
- Better reliability: 99.9% uptime SLA
- WebSocket streaming: Future foundation for real-time feeds

### Session Flow
1. **00-10min**: Validate Session 17 implementation, run promotion gates
2. **10-40min**: Live mode activation with monitoring and validation
3. **40-65min**: Polygon.io adapter implementation and testing
4. **65-75min**: Multi-provider failover testing and quality metrics
5. **75-90min**: Documentation, commit, and Session 19 planning

## Tools & Scripts Needed
- [ ] `scripts/validate-live-promotion.sh` - Pre-flight promotion validation
- [ ] `scripts/monitor-live-mode.sh` - Real-time live mode monitoring  
- [ ] `scripts/test-provider-failover.sh` - Multi-provider failover testing
- [ ] Enhanced `/healthz` endpoint with provider comparison

## Dependencies
- **External**: Polygon.io API key and rate limits understanding
- **Internal**: Session 17 promotion gates passing consistently
- **Testing**: Comprehensive multi-provider test scenarios

**Expected Outcome**: Production-ready live quote feeds with multi-provider failover capability, setting foundation for real-time market data at scale.