# Session 14 Plan: Portfolio Caps and Cooldown Gates

## Overview

Implement comprehensive position limits and trade spacing controls to prevent overconcentration and excessive trading frequency. Build upon Session 13's circuit breaker foundation to add symbol-level caps, daily trading limits, and cooldown periods that ensure disciplined position sizing and trade execution.

## What's Changed vs. Previous Sessions

- **Position Management**: Move from portfolio-wide circuit breakers to granular symbol-level controls
- **Trade Frequency Control**: Implement cooldown periods to prevent rapid-fire trading and overtrading
- **Exposure Limits**: Add configurable caps for individual symbols, sectors, and total portfolio exposure
- **Integration with Risk Framework**: Seamlessly integrate with existing circuit breakers and risk gates
- **Operational Flexibility**: Slack controls for adjusting limits and overriding restrictions during market opportunities

## Acceptance Criteria

- **Symbol-Level Position Caps**: Maximum position sizes per symbol (e.g., max $50K in AAPL)
- **Daily Trading Limits**: Maximum number of trades per symbol per day (e.g., max 5 trades in TSLA/day)
- **Trade Cooldown Periods**: Minimum time between trades for same symbol (e.g., 30 seconds between NVDA trades)
- **Portfolio Exposure Limits**: Total portfolio concentration limits (e.g., max 20% in any single symbol)
- **Risk Gate Integration**: New CapsGate and CooldownGate that integrate with existing risk architecture
- **Dynamic Configuration**: Real-time limit adjustments via Slack without system restart
- **Comprehensive Testing**: Edge cases including limit breaches, cooldown violations, and recovery scenarios

## Implementation Plan

### 1) Position Caps Management (25 min)

**Symbol-Level Position Tracking:**
```go
type PositionCapsManager struct {
    mu           sync.RWMutex
    symbolCaps   map[string]PositionCap
    currentExposure map[string]float64
    portfolioMgr *portfolio.Manager
    config       CapsConfig
}

type PositionCap struct {
    Symbol           string    `json:"symbol"`
    MaxPositionUSD   float64   `json:"max_position_usd"`
    MaxPortfolioPct  float64   `json:"max_portfolio_pct"`
    MaxDailyTrades   int       `json:"max_daily_trades"`
    CurrentTrades    int       `json:"current_trades"`
    LastResetTime    time.Time `json:"last_reset_time"`
}

type CapsConfig struct {
    DefaultSymbolCapUSD     float64            `json:"default_symbol_cap_usd"`
    DefaultPortfolioPct     float64            `json:"default_portfolio_pct"`
    MaxSingleSymbolPct      float64            `json:"max_single_symbol_pct"`
    SymbolSpecificCaps      map[string]float64 `json:"symbol_specific_caps"`
    DailyTradeLimit         int                `json:"daily_trade_limit"`
    ConcentrationLimitPct   float64            `json:"concentration_limit_pct"`
}
```

**Key Features:**
- Real-time position exposure tracking per symbol
- Configurable caps with defaults and symbol-specific overrides
- Portfolio concentration monitoring (max % in any single symbol)
- Daily trade counting with automatic reset at market open
- Integration with existing portfolio manager

### 2) Trade Cooldown System (20 min)

**Cooldown Period Enforcement:**
```go
type CooldownManager struct {
    mu               sync.RWMutex
    lastTradeTimes   map[string]time.Time  // symbol -> last trade time
    cooldownPeriods  map[string]time.Duration // symbol -> cooldown period
    globalCooldown   time.Duration
    config           CooldownConfig
}

type CooldownConfig struct {
    DefaultCooldownSec    int                       `json:"default_cooldown_sec"`
    SymbolCooldowns       map[string]int            `json:"symbol_cooldowns"`
    GlobalCooldownSec     int                       `json:"global_cooldown_sec"`
    IntentSpecificCooldowns map[string]int          `json:"intent_cooldowns"`
    VolatilityAdjustments bool                      `json:"volatility_adjustments"`
}

func (cm *CooldownManager) CanTrade(symbol, intent string, timestamp time.Time) (bool, time.Duration, error) {
    cm.mu.RLock()
    defer cm.mu.RUnlock()
    
    lastTradeTime, exists := cm.lastTradeTimes[symbol]
    if !exists {
        return true, 0, nil // First trade for this symbol
    }
    
    cooldownPeriod := cm.getCooldownPeriod(symbol, intent)
    timeSinceLastTrade := timestamp.Sub(lastTradeTime)
    
    if timeSinceLastTrade < cooldownPeriod {
        remaining := cooldownPeriod - timeSinceLastTrade
        return false, remaining, nil
    }
    
    return true, 0, nil
}
```

**Cooldown Logic:**
- **Symbol-Specific**: Different cooldown periods per symbol (e.g., 30s for AAPL, 60s for TSLA)
- **Intent-Specific**: Longer cooldowns for aggressive positions (BUY_5X) vs conservative (REDUCE)
- **Volatility-Aware**: Longer cooldowns during high volatility periods
- **Global Override**: Minimum cooldown between any trades across all symbols

### 3) Risk Gate Integration (20 min)

**CapsGate Implementation:**
```go
type CapsGate struct {
    capsManager *PositionCapsManager
    logger      *log.Logger
}

func (cg *CapsGate) Evaluate(ctx DecisionContext, riskData RiskData) (bool, string, error) {
    // Check symbol position cap
    currentExposure := cg.capsManager.GetSymbolExposure(ctx.Symbol)
    proposedExposure := currentExposure + (ctx.Quantity * ctx.Price)
    
    symbolCap := cg.capsManager.GetSymbolCap(ctx.Symbol)
    if proposedExposure > symbolCap.MaxPositionUSD {
        return false, fmt.Sprintf("position_cap_exceeded_%.0f_of_%.0f", 
            proposedExposure, symbolCap.MaxPositionUSD), nil
    }
    
    // Check portfolio concentration limit
    portfolioValue := riskData.CurrentNAV
    concentrationPct := (proposedExposure / portfolioValue) * 100
    
    if concentrationPct > symbolCap.MaxPortfolioPct {
        return false, fmt.Sprintf("concentration_limit_%.1f_exceeds_%.1f_pct", 
            concentrationPct, symbolCap.MaxPortfolioPct), nil
    }
    
    // Check daily trade limit
    if symbolCap.CurrentTrades >= symbolCap.MaxDailyTrades {
        return false, fmt.Sprintf("daily_trade_limit_%d_exceeded", 
            symbolCap.MaxDailyTrades), nil
    }
    
    return true, "position_within_limits", nil
}
```

**CooldownGate Implementation:**
```go
type CooldownGate struct {
    cooldownMgr *CooldownManager
}

func (cg *CooldownGate) Evaluate(ctx DecisionContext, riskData RiskData) (bool, string, error) {
    canTrade, remaining, err := cg.cooldownMgr.CanTrade(ctx.Symbol, ctx.Intent, ctx.Timestamp)
    if err != nil {
        return false, "cooldown_check_error", err
    }
    
    if !canTrade {
        return false, fmt.Sprintf("cooldown_active_%ds_remaining", 
            int(remaining.Seconds())), nil
    }
    
    return true, "cooldown_cleared", nil
}
```

### 4) Slack Controls and Monitoring (15 min)

**Position Caps Dashboard:**
```
📊 *Position Caps Status* (Updated: 14:23:15 EST)
┌─ Symbol Exposures:
├── AAPL: $42,350 / $50,000 (84.7%) ✅
├── NVDA: $38,120 / $40,000 (95.3%) ⚠️ 
├── TSLA: $15,670 / $30,000 (52.2%) ✅
└── SPY:  $25,890 / $60,000 (43.1%) ✅

┌─ Daily Trade Counts:
├── AAPL: 3/5 trades ✅
├── NVDA: 5/5 trades ⚠️ LIMIT REACHED
├── TSLA: 1/5 trades ✅ 
└── SPY:  2/8 trades ✅

┌─ Portfolio Concentration:
├── Max Single Symbol: 18.2% (NVDA) / 20.0% limit ✅
├── Top 3 Concentration: 52.1% / 60.0% limit ✅
└── Cash Reserve: $28,450 (12.8%) ✅

🔄 *Active Cooldowns*:
├── NVDA: 23s remaining (last trade: 14:22:52)
└── AAPL: 7s remaining (last trade: 14:23:08)
```

**Slack Commands:**
- `/caps` - Show position caps and current exposures
- `/cooldowns` - Show active cooldowns and trade history
- `/set-cap AAPL 60000` - Adjust symbol position cap
- `/set-cooldown TSLA 45` - Adjust symbol cooldown period
- `/override-cap NVDA temp_emergency` - Temporary cap override with reason
- `/reset-trades AAPL` - Reset daily trade counter (with confirmation)

### 5) Configuration and Persistence (10 min)

**Dynamic Configuration:**
```go
type CapsAndCooldownConfig struct {
    SymbolCaps map[string]PositionCap `json:"symbol_caps"`
    Cooldowns  CooldownConfig         `json:"cooldowns"`
    UpdatedAt  time.Time              `json:"updated_at"`
    UpdatedBy  string                 `json:"updated_by"`
    Reason     string                 `json:"reason"`
}

func (config *CapsAndCooldownConfig) UpdateSymbolCap(symbol string, newCapUSD float64, updatedBy, reason string) error {
    config.mu.Lock()
    defer config.mu.Unlock()
    
    if cap, exists := config.SymbolCaps[symbol]; exists {
        cap.MaxPositionUSD = newCapUSD
        config.SymbolCaps[symbol] = cap
    } else {
        config.SymbolCaps[symbol] = PositionCap{
            Symbol:         symbol,
            MaxPositionUSD: newCapUSD,
            MaxPortfolioPct: config.DefaultPortfolioPct,
        }
    }
    
    config.UpdatedAt = time.Now()
    config.UpdatedBy = updatedBy
    config.Reason = reason
    
    return config.persist()
}
```

**Persistence Strategy:**
- Configuration changes persisted to `data/caps_cooldown_config.json`
- Trade history and cooldown state maintained in memory with periodic snapshots
- Automatic daily reset of trade counters at market open
- Audit trail of all configuration changes with user and reason

### 6) Testing and Edge Cases (10 min)

**Comprehensive Test Scenarios:**
```go
func TestPositionCapsScenarios(t *testing.T) {
    testCases := []struct {
        name           string
        currentExposure float64
        proposedTrade   Trade
        shouldPass     bool
        expectedReason string
    }{
        {
            name: "within_symbol_cap",
            currentExposure: 45000,
            proposedTrade: Trade{Symbol: "AAPL", Quantity: 10, Price: 200}, // +$2K
            shouldPass: true,
        },
        {
            name: "exceeds_symbol_cap", 
            currentExposure: 48000,
            proposedTrade: Trade{Symbol: "AAPL", Quantity: 25, Price: 200}, // +$5K = $53K
            shouldPass: false,
            expectedReason: "position_cap_exceeded",
        },
        {
            name: "exceeds_concentration_limit",
            // Test portfolio concentration scenarios
        },
        {
            name: "daily_trade_limit_reached",
            // Test daily trade counting
        },
    }
}

func TestCooldownScenarios(t *testing.T) {
    testCases := []struct {
        name           string
        lastTradeTime  time.Time
        currentTime    time.Time
        cooldownPeriod time.Duration
        shouldPass     bool
    }{
        {
            name: "cooldown_expired",
            lastTradeTime: time.Now().Add(-60 * time.Second),
            currentTime: time.Now(),
            cooldownPeriod: 30 * time.Second,
            shouldPass: true,
        },
        {
            name: "cooldown_active",
            lastTradeTime: time.Now().Add(-15 * time.Second), 
            currentTime: time.Now(),
            cooldownPeriod: 30 * time.Second,
            shouldPass: false,
        },
    }
}
```

## Success Metrics

- **Position Control**: 100% enforcement of symbol caps with 0 breaches in testing
- **Trade Frequency**: Cooldown violations <0.1%, average cooldown compliance 99.9%
- **Performance Impact**: Position/cooldown checks add <10ms to decision latency
- **Operational Efficiency**: Slack cap adjustments applied within 5s, no system restart required
- **Risk Reduction**: Demonstrated prevention of overconcentration in backtesting scenarios

## Dependencies

- **Existing Infrastructure**: Risk manager, circuit breakers, portfolio manager, Slack integration
- **Real-Time Data**: Current position values and portfolio NAV for exposure calculations
- **Configuration Storage**: Persistent storage for caps and cooldown settings
- **Time Management**: Accurate timestamp tracking for cooldown enforcement

## Risk Mitigation

- **Fail-Safe Defaults**: Conservative caps applied if configuration fails to load
- **Grace Periods**: Brief grace periods for position caps during volatile market conditions
- **Emergency Overrides**: Always available manual overrides via Slack for urgent market opportunities
- **Audit Trail**: Complete logging of all cap adjustments, overrides, and violations
- **Gradual Implementation**: Start with warning-only mode, progressively enforce restrictions

## Integration Points

### Enhanced Decision Engine Integration:
```go
// Add to risk gate evaluation
gates := []RiskGate{
    circuitBreakerGate,
    dataQualityGate,
    volatilityGate,
    capsGate,           // New
    cooldownGate,       // New
}

for _, gate := range gates {
    passed, reason, err := gate.Evaluate(decisionCtx, riskData)
    if !passed {
        return DecisionResult{
            Approved:  false,
            BlockedBy: []string{reason},
        }
    }
}
```

### Portfolio Manager Integration:
```go
// Update position tracking after successful trades
func (pm *PositionManager) ExecuteTrade(trade Trade) error {
    err := pm.updatePosition(trade)
    if err != nil {
        return err
    }
    
    // Update caps manager
    capsManager.RecordTrade(trade.Symbol, trade.Value(), time.Now())
    
    // Update cooldown manager  
    cooldownManager.RecordTrade(trade.Symbol, time.Now())
    
    return nil
}
```

## Evidence Required

- Position caps prevent overexposure with accurate real-time tracking
- Cooldown periods enforced correctly with sub-second precision timing
- Slack controls allow real-time cap adjustments without system disruption
- Integration with circuit breakers maintains all existing risk protections
- Performance benchmarks show minimal impact on decision latency (<10ms overhead)

## Next Session Preview

**Session 15: Real Adapter Integration** - Replace mock adapters with live market data feeds, starting with Alpha Vantage quotes adapter, implementing error handling, rate limiting, and graceful degradation while maintaining all existing risk controls and circuit breaker functionality.

This session completes the core position management and trading discipline framework, providing granular control over individual symbol exposures and trade frequency while seamlessly integrating with the existing risk management infrastructure from Session 13.