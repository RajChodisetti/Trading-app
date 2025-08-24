package risk

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// CooldownManager manages trade cooldown periods to prevent overtrading
type CooldownManager struct {
	mu                 sync.RWMutex
	config             CooldownConfig
	lastTradeTimes     map[string]TradeInfo  // symbol -> last trade info
	globalLastTrade    time.Time            // global cooldown across all symbols
	configVersion      int64                // for race-safe config updates
	metricsEnabled     bool
}

// TradeInfo stores information about the last trade for cooldown calculations
type TradeInfo struct {
	Timestamp time.Time `json:"timestamp"`
	Side      string    `json:"side"`      // "BUY", "SELL", "REDUCE", "EXIT"
	Intent    string    `json:"intent"`    // "BUY_1X", "BUY_5X", "REDUCE", etc.
}

// CooldownConfig defines cooldown policies
type CooldownConfig struct {
	Enforce                    bool                    `json:"enforce" yaml:"enforce"`
	DefaultCooldownSec         int                     `json:"default_cooldown_sec" yaml:"default_cooldown_sec"`
	GlobalCooldownSec          int                     `json:"global_cooldown_sec" yaml:"global_cooldown_sec"`
	SymbolCooldowns           map[string]int          `json:"symbol_cooldowns" yaml:"symbol_cooldowns"`
	IntentSpecificCooldowns   map[string]int          `json:"intent_cooldowns" yaml:"intent_cooldowns"`
	SameSideCooldownSec       int                     `json:"same_side_cooldown_sec" yaml:"same_side_cooldown_sec"`
	VolatilityAdjustments     bool                    `json:"volatility_adjustments" yaml:"volatility_adjustments"`
	OppositeTradesAllowed     bool                    `json:"opposite_trades_allowed" yaml:"opposite_trades_allowed"`
	PersistPath               string                  `json:"persist_path" yaml:"persist_path"`
}

// CooldownInfo contains cooldown check details for explainability
type CooldownInfo struct {
	Symbol              string        `json:"symbol"`
	LastTradeTime       time.Time     `json:"last_trade_time"`
	LastTradeSide       string        `json:"last_trade_side"`
	LastTradeIntent     string        `json:"last_trade_intent"`
	TimeSinceLastTrade  time.Duration `json:"time_since_last_trade"`
	CooldownPeriod      time.Duration `json:"cooldown_period"`
	RemainingCooldown   time.Duration `json:"remaining_cooldown"`
	CooldownType        string        `json:"cooldown_type"`      // "same_side", "global", "intent_specific"
	OppositeTradeAllowed bool         `json:"opposite_trade_allowed"`
}

// NewCooldownManager creates a new cooldown manager
func NewCooldownManager(config CooldownConfig) *CooldownManager {
	return &CooldownManager{
		config:         config,
		lastTradeTimes: make(map[string]TradeInfo),
		configVersion:  1,
		metricsEnabled: true,
	}
}

// CanTrade checks if a trade is allowed given cooldown restrictions
// Returns (canTrade, cooldownInfo, error)
// Soft semantics: BUY→HOLD if violation, always allow REDUCE/EXIT
func (cm *CooldownManager) CanTrade(symbol, intent string, timestamp time.Time) (bool, *CooldownInfo, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	side := intentToSide(intent)
	
	// Always allow risk-reducing trades regardless of cooldown
	if isRiskReducing(intent) {
		return true, &CooldownInfo{
			Symbol:               symbol,
			OppositeTradeAllowed: true,
		}, nil
	}
	
	lastTrade, hasLastTrade := cm.lastTradeTimes[symbol]
	
	// If no previous trade, always allow
	if !hasLastTrade {
		return true, &CooldownInfo{
			Symbol: symbol,
		}, nil
	}
	
	timeSinceLastTrade := timestamp.Sub(lastTrade.Timestamp)
	
	// Check if this is an opposite-side trade
	isOppositeSide := cm.isOppositeSide(lastTrade.Side, side)
	
	// Allow opposite-side trades if configured
	if isOppositeSide && cm.config.OppositeTradesAllowed {
		return true, &CooldownInfo{
			Symbol:               symbol,
			LastTradeTime:        lastTrade.Timestamp,
			LastTradeSide:        lastTrade.Side,
			LastTradeIntent:      lastTrade.Intent,
			TimeSinceLastTrade:   timeSinceLastTrade,
			OppositeTradeAllowed: true,
		}, nil
	}
	
	// Calculate appropriate cooldown period
	cooldownPeriod := cm.getCooldownPeriod(symbol, intent, side, lastTrade)
	
	cooldownInfo := &CooldownInfo{
		Symbol:             symbol,
		LastTradeTime:      lastTrade.Timestamp,
		LastTradeSide:      lastTrade.Side,
		LastTradeIntent:    lastTrade.Intent,
		TimeSinceLastTrade: timeSinceLastTrade,
		CooldownPeriod:     cooldownPeriod,
		CooldownType:       cm.getCooldownType(symbol, intent, side, lastTrade),
	}
	
	// Check if cooldown has expired
	if timeSinceLastTrade >= cooldownPeriod {
		return true, cooldownInfo, nil
	}
	
	// Check if enforcement is disabled (warn-only mode)
	if !cm.config.Enforce {
		cm.recordMetric("cooldown_warnings_total", 1, map[string]string{"symbol": symbol, "side": side})
		cooldownInfo.RemainingCooldown = cooldownPeriod - timeSinceLastTrade
		return true, cooldownInfo, nil  // Allow with warning
	}
	
	// Cooldown is active - block the trade
	remaining := cooldownPeriod - timeSinceLastTrade
	cooldownInfo.RemainingCooldown = remaining
	
	cm.recordMetric("cooldown_blocks_total", 1, map[string]string{"symbol": symbol, "side": side})
	
	return false, cooldownInfo, nil
}

// RecordTrade updates trade timing after a successful trade
func (cm *CooldownManager) RecordTrade(symbol, intent string, timestamp time.Time) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	side := intentToSide(intent)
	
	cm.lastTradeTimes[symbol] = TradeInfo{
		Timestamp: timestamp,
		Side:      side,
		Intent:    intent,
	}
	
	// Update global last trade time
	cm.globalLastTrade = timestamp
	
	// Persist state
	cm.persistState()
}

// UpdateCooldown updates a symbol's cooldown period with TTL and audit info
func (cm *CooldownManager) UpdateCooldown(symbol string, newCooldownSec int, ttl time.Duration, updatedBy, reason string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	// Store in symbol-specific cooldowns with metadata
	if cm.config.SymbolCooldowns == nil {
		cm.config.SymbolCooldowns = make(map[string]int)
	}
	
	cm.config.SymbolCooldowns[symbol] = newCooldownSec
	cm.configVersion++
	
	// In a full implementation, we'd store TTL and audit info
	// For now, just persist the config
	if err := cm.persistConfig(); err != nil {
		return fmt.Errorf("failed to persist cooldown config: %w", err)
	}
	
	// Update metrics
	cm.recordMetric("runtime_overrides_active", 1, map[string]string{"type": "cooldown", "symbol": symbol})
	
	return nil
}

// GetCooldownInfo returns current cooldown information for a symbol
func (cm *CooldownManager) GetCooldownInfo(symbol string) *CooldownInfo {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	lastTrade, exists := cm.lastTradeTimes[symbol]
	if !exists {
		return &CooldownInfo{
			Symbol: symbol,
		}
	}
	
	now := time.Now()
	timeSinceLastTrade := now.Sub(lastTrade.Timestamp)
	cooldownPeriod := cm.getCooldownPeriod(symbol, lastTrade.Intent, lastTrade.Side, lastTrade)
	
	remaining := time.Duration(0)
	if timeSinceLastTrade < cooldownPeriod {
		remaining = cooldownPeriod - timeSinceLastTrade
	}
	
	return &CooldownInfo{
		Symbol:             symbol,
		LastTradeTime:      lastTrade.Timestamp,
		LastTradeSide:      lastTrade.Side,
		LastTradeIntent:    lastTrade.Intent,
		TimeSinceLastTrade: timeSinceLastTrade,
		CooldownPeriod:     cooldownPeriod,
		RemainingCooldown:  remaining,
		CooldownType:       cm.getCooldownType(symbol, lastTrade.Intent, lastTrade.Side, lastTrade),
	}
}

// GetAllCooldowns returns cooldown information for all symbols with recent trades
func (cm *CooldownManager) GetAllCooldowns() map[string]*CooldownInfo {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	
	cooldowns := make(map[string]*CooldownInfo)
	
	for symbol := range cm.lastTradeTimes {
		cooldowns[symbol] = cm.GetCooldownInfo(symbol)
	}
	
	return cooldowns
}

// Helper methods

func (cm *CooldownManager) getCooldownPeriod(symbol, intent, side string, lastTrade TradeInfo) time.Duration {
	var cooldownSec int
	
	// Check intent-specific cooldown first (e.g., BUY_5X has longer cooldown)
	if intentCooldown, exists := cm.config.IntentSpecificCooldowns[intent]; exists {
		cooldownSec = intentCooldown
	} else if symbolCooldown, exists := cm.config.SymbolCooldowns[symbol]; exists {
		// Symbol-specific cooldown
		cooldownSec = symbolCooldown
	} else if cm.isSameSide(lastTrade.Side, side) {
		// Same-side trades use same-side cooldown
		cooldownSec = cm.config.SameSideCooldownSec
		if cooldownSec == 0 {
			cooldownSec = cm.config.DefaultCooldownSec
		}
	} else {
		// Default cooldown for different sides
		cooldownSec = cm.config.DefaultCooldownSec
	}
	
	// Apply global minimum if configured
	if cm.config.GlobalCooldownSec > cooldownSec {
		cooldownSec = cm.config.GlobalCooldownSec
	}
	
	return time.Duration(cooldownSec) * time.Second
}

func (cm *CooldownManager) getCooldownType(symbol, intent, side string, lastTrade TradeInfo) string {
	if _, exists := cm.config.IntentSpecificCooldowns[intent]; exists {
		return "intent_specific"
	}
	if _, exists := cm.config.SymbolCooldowns[symbol]; exists {
		return "symbol_specific"
	}
	if cm.isSameSide(lastTrade.Side, side) {
		return "same_side"
	}
	return "global"
}

func (cm *CooldownManager) isSameSide(lastSide, currentSide string) bool {
	return lastSide == currentSide
}

func (cm *CooldownManager) isOppositeSide(lastSide, currentSide string) bool {
	// BUY vs SELL are opposite
	// REDUCE/EXIT are always considered risk-reducing regardless of last side
	return (lastSide == "BUY" && currentSide == "SELL") || (lastSide == "SELL" && currentSide == "BUY")
}

func (cm *CooldownManager) persistState() error {
	if cm.config.PersistPath == "" {
		return nil
	}
	
	data := map[string]interface{}{
		"version":          cm.configVersion,
		"updated_at":       time.Now(),
		"last_trade_times": cm.lastTradeTimes,
		"global_last_trade": cm.globalLastTrade,
	}
	
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	
	// Atomic write
	tempPath := cm.config.PersistPath + ".tmp"
	if err := os.WriteFile(tempPath, jsonData, 0644); err != nil {
		return err
	}
	
	return os.Rename(tempPath, cm.config.PersistPath)
}

func (cm *CooldownManager) persistConfig() error {
	configPath := cm.config.PersistPath + ".config"
	
	jsonData, err := json.MarshalIndent(cm.config, "", "  ")
	if err != nil {
		return err
	}
	
	// Atomic write
	tempPath := configPath + ".tmp"
	if err := os.WriteFile(tempPath, jsonData, 0644); err != nil {
		return err
	}
	
	return os.Rename(tempPath, configPath)
}

func (cm *CooldownManager) recordMetric(name string, value float64, labels map[string]string) {
	if !cm.metricsEnabled {
		return
	}
	
	switch name {
	case "cooldown_blocks_total", "cooldown_warnings_total", "runtime_overrides_active":
		observ.IncCounter(name, labels)
	default:
		observ.SetGauge(name, value, labels)
	}
}

// Helper functions

func intentToSide(intent string) string {
	switch intent {
	case "BUY_1X", "BUY_5X":
		return "BUY"
	case "SELL", "SHORT":
		return "SELL"
	case "REDUCE", "EXIT", "STOP":
		return "REDUCE"
	default:
		return "UNKNOWN"
	}
}

// CooldownGate implements the RiskGate interface for trade cooldowns
type CooldownGate struct {
	cooldownManager *CooldownManager
}

// NewCooldownGate creates a new cooldown gate
func NewCooldownGate(cooldownManager *CooldownManager) *CooldownGate {
	return &CooldownGate{
		cooldownManager: cooldownManager,
	}
}

// Name returns the gate name
func (cg *CooldownGate) Name() string {
	return "cooldown"
}

// Priority returns the gate priority (lower = higher priority)
func (cg *CooldownGate) Priority() int {
	return 35 // After caps, before corroboration/earnings
}

// Evaluate checks if a decision violates cooldown restrictions
func (cg *CooldownGate) Evaluate(ctx DecisionContext, riskData RiskData) (bool, string, error) {
	canTrade, cooldownInfo, err := cg.cooldownManager.CanTrade(ctx.Symbol, ctx.Intent, ctx.Timestamp)
	
	if err != nil {
		return false, "cooldown_check_error", err
	}
	
	if !canTrade {
		// Create explainable reason with remaining time
		remainingSec := int(cooldownInfo.RemainingCooldown.Seconds())
		reason := fmt.Sprintf("cooldown_%s_remaining_%ds", 
			cooldownInfo.CooldownType, 
			remainingSec)
		return false, reason, nil
	}
	
	return true, "cooldown_cleared", nil
}