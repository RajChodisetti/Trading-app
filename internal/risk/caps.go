package risk

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/adapters"
	"github.com/Rajchodisetti/trading-app/internal/observ"
	"github.com/Rajchodisetti/trading-app/internal/portfolio"
)

// PositionCapsManager manages symbol-level position caps and portfolio concentration limits
type PositionCapsManager struct {
	mu              sync.RWMutex
	portfolioMgr    *portfolio.Manager
	quotesAdapter   adapters.QuotesAdapter
	config          CapsConfig
	
	// Runtime state
	symbolCaps      map[string]PositionCap
	dailyTrades     map[string]int        // symbol -> count of trades today
	lastResetTime   time.Time            // when daily trades were last reset
	configVersion   int64                // for race-safe config updates
	
	// Metrics
	metricsEnabled  bool
}

// PositionCap represents limits for a specific symbol
type PositionCap struct {
	Symbol           string    `json:"symbol"`
	MaxPositionUSD   float64   `json:"max_position_usd"`
	MaxPortfolioPct  float64   `json:"max_portfolio_pct"`
	MaxDailyTrades   int       `json:"max_daily_trades"`
	EffectiveUntil   time.Time `json:"effective_until,omitempty"`  // For TTL overrides
	UpdatedBy        string    `json:"updated_by,omitempty"`
	Reason           string    `json:"reason,omitempty"`
}

// CapsConfig defines the configuration for position caps
type CapsConfig struct {
	Enforce                   bool               `json:"enforce" yaml:"enforce"`
	DefaultSymbolCapUSD       float64            `json:"default_symbol_cap_usd" yaml:"default_symbol_cap_usd"`
	DefaultPortfolioPct       float64            `json:"default_portfolio_pct" yaml:"default_portfolio_pct"`
	MaxSingleSymbolPct        float64            `json:"max_single_symbol_pct" yaml:"max_single_symbol_pct"`
	DailyTradeLimit          int                `json:"daily_trade_limit" yaml:"daily_trade_limit"`
	SymbolSpecificCaps       map[string]float64 `json:"symbol_specific_caps" yaml:"symbol_specific_caps"`
	PortfolioCapsEnabled     bool               `json:"portfolio_caps_enabled" yaml:"portfolio_caps_enabled"`
	RTHOpenHour              int                `json:"rth_open_hour" yaml:"rth_open_hour"`        // 9 for 9:30 AM ET
	RTHOpenMinute            int                `json:"rth_open_minute" yaml:"rth_open_minute"`    // 30 for 9:30 AM ET
	PersistPath              string             `json:"persist_path" yaml:"persist_path"`
}

// ExposureInfo contains exposure calculation details for explainability
type ExposureInfo struct {
	CurrentExposureUSD   float64 `json:"current_exposure_usd"`
	ProposedExposureUSD  float64 `json:"proposed_exposure_usd"`
	ConcentrationPct     float64 `json:"concentration_pct"`
	ConcentrationAfterPct float64 `json:"concentration_after_pct"`
	NAV                  float64 `json:"nav"`
	MidPrice             float64 `json:"mid_price"`
	SymbolCapUSD         float64 `json:"symbol_cap_usd"`
	DailyTradesCount     int     `json:"daily_trades_count"`
	DailyTradesLimit     int     `json:"daily_trades_limit"`
}

// NewPositionCapsManager creates a new position caps manager
func NewPositionCapsManager(portfolioMgr *portfolio.Manager, quotesAdapter adapters.QuotesAdapter, config CapsConfig) *PositionCapsManager {
	return &PositionCapsManager{
		portfolioMgr:   portfolioMgr,
		quotesAdapter:  quotesAdapter,
		config:         config,
		symbolCaps:     make(map[string]PositionCap),
		dailyTrades:    make(map[string]int),
		lastResetTime:  time.Now(),
		configVersion:  1,
		metricsEnabled: true,
	}
}

// CanIncrease checks if a proposed trade would violate position caps
// Returns (canProceed, reason, exposureInfo, error)
// Soft semantics: BUY→HOLD if violation, always allow REDUCE/EXIT
func (pcm *PositionCapsManager) CanIncrease(symbol, intent string, quantity int, price float64, currentNAV float64) (bool, string, *ExposureInfo, error) {
	pcm.mu.RLock()
	defer pcm.mu.RUnlock()
	
	// Always allow risk-reducing trades
	if isRiskReducing(intent) {
		return true, "risk_reducing_allowed", &ExposureInfo{}, nil
	}
	
	// Reset daily trades if new trading day
	pcm.resetDailyTradesIfNeeded()
	
	// Get current exposure for symbol
	currentExposure, err := pcm.getCurrentExposure(symbol)
	if err != nil {
		return false, "exposure_calculation_error", nil, err
	}
	
	// Calculate proposed exposure using mid price (fallback to last)
	midPrice := price
	if quote, err := pcm.quotesAdapter.GetQuote(context.Background(), symbol); err == nil && quote != nil {
		if quote.Bid > 0 && quote.Ask > 0 {
			midPrice = (quote.Bid + quote.Ask) / 2
		} else if quote.Last > 0 {
			midPrice = quote.Last
		}
	}
	
	// Round proposed quantity to avoid tiny overflows
	roundedQuantity := roundQuantity(quantity)
	proposedExposure := currentExposure + (float64(roundedQuantity) * midPrice)
	
	// Get symbol cap (default or specific)
	symbolCap := pcm.getSymbolCap(symbol)
	
	// Calculate concentrations
	currentConcentrationPct := (currentExposure / currentNAV) * 100
	proposedConcentrationPct := (proposedExposure / currentNAV) * 100
	
	exposureInfo := &ExposureInfo{
		CurrentExposureUSD:    currentExposure,
		ProposedExposureUSD:   proposedExposure,
		ConcentrationPct:      currentConcentrationPct,
		ConcentrationAfterPct: proposedConcentrationPct,
		NAV:                   currentNAV,
		MidPrice:              midPrice,
		SymbolCapUSD:          symbolCap.MaxPositionUSD,
		DailyTradesCount:      pcm.dailyTrades[symbol],
		DailyTradesLimit:      symbolCap.MaxDailyTrades,
	}
	
	// Check if enforcement is disabled (warn-only mode)
	if !pcm.config.Enforce {
		if proposedExposure > symbolCap.MaxPositionUSD {
			pcm.recordMetric("cap_warnings_total", 1, map[string]string{"symbol": symbol, "reason": "symbol_cap"})
		}
		if proposedConcentrationPct > pcm.config.MaxSingleSymbolPct {
			pcm.recordMetric("cap_warnings_total", 1, map[string]string{"symbol": symbol, "reason": "concentration"})
		}
		if pcm.dailyTrades[symbol] >= symbolCap.MaxDailyTrades {
			pcm.recordMetric("cap_warnings_total", 1, map[string]string{"symbol": symbol, "reason": "daily_trades"})
		}
		return true, "warn_only_mode", exposureInfo, nil
	}
	
	// Check symbol position cap
	if proposedExposure > symbolCap.MaxPositionUSD {
		pcm.recordMetric("cap_blocks_total", 1, map[string]string{"symbol": symbol, "kind": "symbol"})
		reason := fmt.Sprintf("caps_symbol_%.0f_exceeds_%.0f", proposedExposure, symbolCap.MaxPositionUSD)
		return false, reason, exposureInfo, nil
	}
	
	// Check portfolio concentration limit
	if proposedConcentrationPct > pcm.config.MaxSingleSymbolPct {
		pcm.recordMetric("cap_blocks_total", 1, map[string]string{"symbol": symbol, "kind": "concentration"})
		reason := fmt.Sprintf("caps_concentration_%.1f_exceeds_%.1f_pct", proposedConcentrationPct, pcm.config.MaxSingleSymbolPct)
		return false, reason, exposureInfo, nil
	}
	
	// Check daily trade limit
	if pcm.dailyTrades[symbol] >= symbolCap.MaxDailyTrades {
		pcm.recordMetric("cap_blocks_total", 1, map[string]string{"symbol": symbol, "kind": "daily_trades"})
		reason := fmt.Sprintf("caps_daily_trades_%d_exceeds_%d", pcm.dailyTrades[symbol], symbolCap.MaxDailyTrades)
		return false, reason, exposureInfo, nil
	}
	
	return true, "caps_within_limits", exposureInfo, nil
}

// RecordTrade updates trade counters after a successful trade
func (pcm *PositionCapsManager) RecordTrade(symbol, side string, valueUSD float64) {
	pcm.mu.Lock()
	defer pcm.mu.Unlock()
	
	// Increment daily trade counter
	pcm.dailyTrades[symbol]++
	
	// Update metrics
	pcm.recordMetric("daily_trades", float64(pcm.dailyTrades[symbol]), map[string]string{"symbol": symbol})
	
	// Update exposure metrics
	if exposure, err := pcm.getCurrentExposure(symbol); err == nil {
		nav := pcm.getCurrentNAV()
		if nav > 0 {
			concentrationPct := (exposure / nav) * 100
			pcm.recordMetric("exposure_pct", concentrationPct, map[string]string{"symbol": symbol})
		}
	}
}

// GetSymbolCap returns the effective cap for a symbol (including TTL overrides)
func (pcm *PositionCapsManager) GetSymbolCap(symbol string) PositionCap {
	pcm.mu.RLock()
	defer pcm.mu.RUnlock()
	return pcm.getSymbolCap(symbol)
}

// GetExposureInfo returns current exposure information for a symbol
func (pcm *PositionCapsManager) GetExposureInfo(symbol string) (*ExposureInfo, error) {
	pcm.mu.RLock()
	defer pcm.mu.RUnlock()
	
	currentExposure, err := pcm.getCurrentExposure(symbol)
	if err != nil {
		return nil, err
	}
	
	nav := pcm.getCurrentNAV()
	concentrationPct := (currentExposure / nav) * 100
	symbolCap := pcm.getSymbolCap(symbol)
	
	return &ExposureInfo{
		CurrentExposureUSD:   currentExposure,
		ConcentrationPct:     concentrationPct,
		NAV:                  nav,
		SymbolCapUSD:         symbolCap.MaxPositionUSD,
		DailyTradesCount:     pcm.dailyTrades[symbol],
		DailyTradesLimit:     symbolCap.MaxDailyTrades,
	}, nil
}

// UpdateSymbolCap updates a symbol cap with TTL and audit info
func (pcm *PositionCapsManager) UpdateSymbolCap(symbol string, newCapUSD float64, ttl time.Duration, updatedBy, reason string) error {
	pcm.mu.Lock()
	defer pcm.mu.Unlock()
	
	effectiveUntil := time.Now().Add(ttl)
	
	cap := pcm.getSymbolCap(symbol) // Get current or default
	cap.Symbol = symbol
	cap.MaxPositionUSD = newCapUSD
	cap.EffectiveUntil = effectiveUntil
	cap.UpdatedBy = updatedBy
	cap.Reason = reason
	
	pcm.symbolCaps[symbol] = cap
	pcm.configVersion++
	
	// Persist to disk
	if err := pcm.persistConfig(); err != nil {
		return fmt.Errorf("failed to persist config: %w", err)
	}
	
	// Update metrics
	pcm.recordMetric("runtime_overrides_active", 1, map[string]string{"type": "cap", "symbol": symbol})
	
	return nil
}

// GetAllExposures returns exposure information for all symbols with positions
func (pcm *PositionCapsManager) GetAllExposures() (map[string]*ExposureInfo, error) {
	pcm.mu.RLock()
	defer pcm.mu.RUnlock()
	
	positions := pcm.portfolioMgr.GetAllPositions()
	exposures := make(map[string]*ExposureInfo)
	
	for symbol := range positions {
		if info, err := pcm.GetExposureInfo(symbol); err == nil {
			exposures[symbol] = info
		}
	}
	
	return exposures, nil
}

// GetMaxConcentration returns the current maximum single symbol concentration
func (pcm *PositionCapsManager) GetMaxConcentration() (float64, string, error) {
	exposures, err := pcm.GetAllExposures()
	if err != nil {
		return 0, "", err
	}
	
	maxConcentration := 0.0
	maxSymbol := ""
	
	for symbol, info := range exposures {
		if info.ConcentrationPct > maxConcentration {
			maxConcentration = info.ConcentrationPct
			maxSymbol = symbol
		}
	}
	
	// Update metric
	pcm.recordMetric("max_symbol_concentration_pct", maxConcentration, map[string]string{"symbol": maxSymbol})
	
	return maxConcentration, maxSymbol, nil
}

// Helper methods

func (pcm *PositionCapsManager) getSymbolCap(symbol string) PositionCap {
	// Check for existing cap (including TTL overrides)
	if cap, exists := pcm.symbolCaps[symbol]; exists {
		// Check if TTL has expired
		if !cap.EffectiveUntil.IsZero() && time.Now().After(cap.EffectiveUntil) {
			// TTL expired, remove override
			delete(pcm.symbolCaps, symbol)
		} else {
			return cap
		}
	}
	
	// Return default cap
	capUSD := pcm.config.DefaultSymbolCapUSD
	if specificCap, exists := pcm.config.SymbolSpecificCaps[symbol]; exists {
		capUSD = specificCap
	}
	
	return PositionCap{
		Symbol:           symbol,
		MaxPositionUSD:   capUSD,
		MaxPortfolioPct:  pcm.config.DefaultPortfolioPct,
		MaxDailyTrades:   pcm.config.DailyTradeLimit,
	}
}

func (pcm *PositionCapsManager) getCurrentExposure(symbol string) (float64, error) {
	position, exists := pcm.portfolioMgr.GetPosition(symbol)
	if !exists {
		return 0, nil
	}
	
	// Get current quote for mid price
	quote, err := pcm.quotesAdapter.GetQuote(context.Background(), symbol)
	if err != nil {
		return 0, fmt.Errorf("failed to get quote for %s: %w", symbol, err)
	}
	
	var price float64
	if quote.Bid > 0 && quote.Ask > 0 {
		price = (quote.Bid + quote.Ask) / 2 // Mid price
	} else if quote.Last > 0 {
		price = quote.Last // Fallback to last
	} else {
		return 0, fmt.Errorf("no valid price for %s", symbol)
	}
	
	// Exposure = abs(quantity) * price
	exposure := math.Abs(float64(position.Quantity)) * price
	return exposure, nil
}

func (pcm *PositionCapsManager) getCurrentNAV() float64 {
	// This should integrate with NAVTracker from Session 13
	// For now, use a simple calculation
	positions := pcm.portfolioMgr.GetAllPositions()
	totalValue := 0.0
	
	for symbol, position := range positions {
		if quote, err := pcm.quotesAdapter.GetQuote(context.Background(), symbol); err == nil && quote != nil {
			var price float64
			if quote.Bid > 0 && quote.Ask > 0 {
				price = (quote.Bid + quote.Ask) / 2
			} else if quote.Last > 0 {
				price = quote.Last
			}
			if price > 0 {
				totalValue += float64(position.Quantity) * price
			}
		}
	}
	
	// For now, return just the position value
	// In a real implementation, this would integrate with NAVTracker from Session 13
	return totalValue
}

func (pcm *PositionCapsManager) resetDailyTradesIfNeeded() {
	now := time.Now()
	
	// Check if we've passed RTH open
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return // Fallback: don't reset if timezone fails
	}
	
	et := now.In(loc)
	rthOpen := time.Date(et.Year(), et.Month(), et.Day(), pcm.config.RTHOpenHour, pcm.config.RTHOpenMinute, 0, 0, loc)
	
	// If current time is after RTH open and last reset was before RTH open
	if et.After(rthOpen) && pcm.lastResetTime.Before(rthOpen) {
		// Reset all daily trade counters
		for symbol := range pcm.dailyTrades {
			pcm.dailyTrades[symbol] = 0
		}
		pcm.lastResetTime = now
	}
}

func (pcm *PositionCapsManager) persistConfig() error {
	if pcm.config.PersistPath == "" {
		return nil // No persistence configured
	}
	
	data := map[string]interface{}{
		"version":       pcm.configVersion,
		"updated_at":    time.Now(),
		"symbol_caps":   pcm.symbolCaps,
		"daily_trades":  pcm.dailyTrades,
		"last_reset":    pcm.lastResetTime,
	}
	
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	
	// Atomic write: write to temp file then rename
	tempPath := pcm.config.PersistPath + ".tmp"
	if err := os.WriteFile(tempPath, jsonData, 0644); err != nil {
		return err
	}
	
	return os.Rename(tempPath, pcm.config.PersistPath)
}

func (pcm *PositionCapsManager) recordMetric(name string, value float64, labels map[string]string) {
	if !pcm.metricsEnabled {
		return
	}
	
	switch name {
	case "cap_blocks_total", "cap_warnings_total", "runtime_overrides_active":
		observ.IncCounter(name, labels)
	default:
		observ.SetGauge(name, value, labels)
	}
}

// Helper functions

func isRiskReducing(intent string) bool {
	return intent == "REDUCE" || intent == "EXIT" || intent == "STOP" || intent == "HOLD"
}

func roundQuantity(quantity int) int {
	// Simple rounding - could be enhanced based on asset type
	return quantity
}

// CapsGate implements the RiskGate interface for position caps
type CapsGate struct {
	capsManager *PositionCapsManager
}

// NewCapsGate creates a new caps gate
func NewCapsGate(capsManager *PositionCapsManager) *CapsGate {
	return &CapsGate{
		capsManager: capsManager,
	}
}

// Name returns the gate name
func (cg *CapsGate) Name() string {
	return "caps"
}

// Priority returns the gate priority (lower = higher priority)
func (cg *CapsGate) Priority() int {
	return 30 // After session/liquidity, before corroboration/earnings
}

// Evaluate checks if a decision violates position caps
func (cg *CapsGate) Evaluate(ctx DecisionContext, riskData RiskData) (bool, string, error) {
	canIncrease, reason, exposureInfo, err := cg.capsManager.CanIncrease(
		ctx.Symbol, 
		ctx.Intent, 
		ctx.Quantity, 
		ctx.Price,
		riskData.CurrentNAV,
	)
	
	if err != nil {
		return false, "caps_check_error", err
	}
	
	if !canIncrease {
		// Add exposure info to reason for explainability
		detailedReason := fmt.Sprintf("%s_exposure_%.0f_nav_%.0f", 
			reason, 
			exposureInfo.ProposedExposureUSD, 
			exposureInfo.NAV)
		return false, detailedReason, nil
	}
	
	return true, "caps_within_limits", nil
}