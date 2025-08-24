package risk

import (
	"math"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// VolatilityCalculator computes portfolio volatility metrics for threshold adjustment
type VolatilityCalculator struct {
	mu sync.RWMutex
	
	// Historical data
	navReturns   []float64         // Recent NAV returns for volatility calculation
	priceHistory map[string][]PricePoint // Per-symbol price history for ATR
	
	// Calculated metrics  
	currentVolatility float64       // Current portfolio volatility (annualized)
	ewmaVolatility    float64       // EWMA-based volatility estimate
	atrValues         map[string]float64 // Average True Range per symbol
	
	// Configuration
	config VolatilityConfig
}

// PricePoint represents a price observation
type PricePoint struct {
	Timestamp time.Time
	High      float64
	Low       float64
	Close     float64
}

// VolatilityConfig configures volatility calculations
type VolatilityConfig struct {
	LookbackDays        int     `yaml:"lookback_days"`         // Days of history for volatility calculation
	EwmaAlpha          float64 `yaml:"ewma_alpha"`           // EWMA decay factor (0.94 typical)
	ATRPeriod          int     `yaml:"atr_period"`           // ATR calculation period (14 typical)
	VolatilityFloor    float64 `yaml:"volatility_floor"`     // Minimum volatility multiplier
	VolatilityCeiling  float64 `yaml:"volatility_ceiling"`   // Maximum volatility multiplier
	QuietMarketThreshold float64 `yaml:"quiet_market_threshold"` // Vol below this = tighten thresholds
	VolatileMarketThreshold float64 `yaml:"volatile_market_threshold"` // Vol above this = widen thresholds
	UpdateIntervalMinutes int     `yaml:"update_interval_minutes"` // How often to recalculate
}

// NewVolatilityCalculator creates a new volatility calculator
func NewVolatilityCalculator(config VolatilityConfig) *VolatilityCalculator {
	if config.LookbackDays == 0 {
		config.LookbackDays = 21 // Default 21 trading days
	}
	if config.EwmaAlpha == 0 {
		config.EwmaAlpha = 0.94 // RiskMetrics standard
	}
	if config.ATRPeriod == 0 {
		config.ATRPeriod = 14 // Standard ATR period
	}
	if config.VolatilityFloor == 0 {
		config.VolatilityFloor = 0.5 // Never less than 50% of base thresholds
	}
	if config.VolatilityCeiling == 0 {
		config.VolatilityCeiling = 3.0 // Never more than 300% of base thresholds
	}
	if config.QuietMarketThreshold == 0 {
		config.QuietMarketThreshold = 0.10 // 10% annualized
	}
	if config.VolatileMarketThreshold == 0 {
		config.VolatileMarketThreshold = 0.30 // 30% annualized
	}
	if config.UpdateIntervalMinutes == 0 {
		config.UpdateIntervalMinutes = 5 // Update every 5 minutes
	}

	return &VolatilityCalculator{
		navReturns:   make([]float64, 0),
		priceHistory: make(map[string][]PricePoint),
		atrValues:    make(map[string]float64),
		config:       config,
	}
}

// UpdateNAVReturn adds a new NAV return observation
func (vc *VolatilityCalculator) UpdateNAVReturn(previousNAV, currentNAV float64, timestamp time.Time) {
	if previousNAV <= 0 {
		return // Can't calculate return without previous NAV
	}
	
	vc.mu.Lock()
	defer vc.mu.Unlock()
	
	// Calculate return
	navReturn := (currentNAV - previousNAV) / previousNAV
	
	// Add to returns history
	vc.navReturns = append(vc.navReturns, navReturn)
	
	// Keep only recent history (limit memory usage)
	maxObservations := vc.config.LookbackDays * 24 * 12 // Assume 5-minute intervals
	if len(vc.navReturns) > maxObservations {
		vc.navReturns = vc.navReturns[len(vc.navReturns)-maxObservations:]
	}
	
	// Update EWMA volatility
	vc.updateEWMAVolatility(navReturn)
	
	// Recalculate current volatility
	vc.calculateCurrentVolatility()
	
	// Update metrics
	observ.SetGauge("portfolio_volatility_current", vc.currentVolatility, nil)
	observ.SetGauge("portfolio_volatility_ewma", vc.ewmaVolatility, nil)
	observ.SetGauge("volatility_adjustment_factor", vc.GetVolatilityMultiplier(), nil)
}

// UpdatePricePoint adds price data for ATR calculation
func (vc *VolatilityCalculator) UpdatePricePoint(symbol string, high, low, close float64, timestamp time.Time) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	
	pricePoint := PricePoint{
		Timestamp: timestamp,
		High:      high,
		Low:       low,
		Close:     close,
	}
	
	// Add to history
	if vc.priceHistory[symbol] == nil {
		vc.priceHistory[symbol] = make([]PricePoint, 0)
	}
	
	vc.priceHistory[symbol] = append(vc.priceHistory[symbol], pricePoint)
	
	// Keep only recent history
	maxPoints := vc.config.ATRPeriod * 2 // Keep extra for reliable ATR calculation
	if len(vc.priceHistory[symbol]) > maxPoints {
		vc.priceHistory[symbol] = vc.priceHistory[symbol][len(vc.priceHistory[symbol])-maxPoints:]
	}
	
	// Calculate ATR for this symbol
	vc.calculateATR(symbol)
}

// calculateCurrentVolatility computes portfolio volatility from NAV returns
func (vc *VolatilityCalculator) calculateCurrentVolatility() {
	if len(vc.navReturns) < 10 {
		return // Need minimum observations
	}
	
	// Calculate standard deviation of returns
	mean := vc.mean(vc.navReturns)
	variance := 0.0
	
	for _, ret := range vc.navReturns {
		diff := ret - mean
		variance += diff * diff
	}
	
	variance /= float64(len(vc.navReturns) - 1) // Sample variance
	stdDev := math.Sqrt(variance)
	
	// Annualize (assuming returns are calculated every 5 minutes)
	// 252 trading days * 78 intervals per day (5-min intervals in 6.5 hour trading day)
	annualizationFactor := math.Sqrt(252 * 78)
	vc.currentVolatility = stdDev * annualizationFactor
}

// updateEWMAVolatility updates the EWMA-based volatility estimate
func (vc *VolatilityCalculator) updateEWMAVolatility(newReturn float64) {
	if vc.ewmaVolatility == 0 {
		// Initialize with first observation (squared)
		vc.ewmaVolatility = newReturn * newReturn
		return
	}
	
	// EWMA formula: σ²(t) = α * r²(t) + (1-α) * σ²(t-1)
	alpha := 1.0 - vc.config.EwmaAlpha // Convert to decay factor
	vc.ewmaVolatility = alpha*newReturn*newReturn + vc.config.EwmaAlpha*vc.ewmaVolatility
	
	// Convert variance to volatility and annualize
	annualizationFactor := math.Sqrt(252 * 78)
	vc.ewmaVolatility = math.Sqrt(vc.ewmaVolatility) * annualizationFactor
}

// calculateATR computes Average True Range for a symbol
func (vc *VolatilityCalculator) calculateATR(symbol string) {
	history := vc.priceHistory[symbol]
	if len(history) < vc.config.ATRPeriod {
		return // Need enough history
	}
	
	// Calculate True Range for each period
	trValues := make([]float64, 0)
	
	for i := 1; i < len(history); i++ {
		current := history[i]
		previous := history[i-1]
		
		// True Range = max(H-L, abs(H-Cp), abs(L-Cp))
		hl := current.High - current.Low
		hcp := math.Abs(current.High - previous.Close)
		lcp := math.Abs(current.Low - previous.Close)
		
		tr := math.Max(hl, math.Max(hcp, lcp))
		trValues = append(trValues, tr)
	}
	
	if len(trValues) < vc.config.ATRPeriod {
		return
	}
	
	// Calculate ATR as average of recent True Range values
	recentTR := trValues[len(trValues)-vc.config.ATRPeriod:]
	atr := vc.mean(recentTR)
	
	vc.atrValues[symbol] = atr
	
	// Update metrics
	observ.SetGauge("symbol_atr", atr, map[string]string{"symbol": symbol})
}

// GetVolatilityMultiplier returns the current volatility adjustment factor
func (vc *VolatilityCalculator) GetVolatilityMultiplier() float64 {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	
	// Use EWMA volatility if available, otherwise current volatility
	vol := vc.ewmaVolatility
	if vol == 0 {
		vol = vc.currentVolatility
	}
	
	if vol == 0 {
		return 1.0 // No volatility data - use base thresholds
	}
	
	// Determine multiplier based on volatility regime
	var multiplier float64
	
	if vol < vc.config.QuietMarketThreshold {
		// Quiet market - tighten thresholds (multiply by less than 1.0)
		multiplier = 0.7 + 0.3*(vol/vc.config.QuietMarketThreshold)
	} else if vol > vc.config.VolatileMarketThreshold {
		// Volatile market - widen thresholds (multiply by more than 1.0) 
		excess := vol - vc.config.VolatileMarketThreshold
		multiplier = 1.0 + math.Min(2.0, excess*5) // Cap at 3x for extreme volatility
	} else {
		// Normal volatility regime - use base thresholds with slight adjustment
		normalized := (vol - vc.config.QuietMarketThreshold) / 
					 (vc.config.VolatileMarketThreshold - vc.config.QuietMarketThreshold)
		multiplier = 0.8 + 0.4*normalized // Range from 0.8 to 1.2
	}
	
	// Apply floor and ceiling
	multiplier = math.Max(vc.config.VolatilityFloor, multiplier)
	multiplier = math.Min(vc.config.VolatilityCeiling, multiplier)
	
	return multiplier
}

// GetPortfolioVolatility returns current portfolio volatility metrics
func (vc *VolatilityCalculator) GetPortfolioVolatility() (current, ewma float64) {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	return vc.currentVolatility, vc.ewmaVolatility
}

// GetSymbolATR returns the ATR for a specific symbol
func (vc *VolatilityCalculator) GetSymbolATR(symbol string) (float64, bool) {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	atr, exists := vc.atrValues[symbol]
	return atr, exists
}

// GetVolatilityRegime returns a description of the current volatility regime
func (vc *VolatilityCalculator) GetVolatilityRegime() string {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	
	vol := vc.ewmaVolatility
	if vol == 0 {
		vol = vc.currentVolatility
	}
	
	if vol == 0 {
		return "unknown"
	}
	
	if vol < vc.config.QuietMarketThreshold {
		return "quiet"
	} else if vol > vc.config.VolatileMarketThreshold {
		return "volatile"
	} else {
		return "normal"
	}
}

// GetRiskMetrics returns comprehensive risk metrics for monitoring
func (vc *VolatilityCalculator) GetRiskMetrics() map[string]interface{} {
	vc.mu.RLock()
	defer vc.mu.RUnlock()
	
	metrics := map[string]interface{}{
		"current_volatility":      vc.currentVolatility,
		"ewma_volatility":        vc.ewmaVolatility,
		"volatility_multiplier":  vc.GetVolatilityMultiplier(),
		"volatility_regime":      vc.GetVolatilityRegime(),
		"nav_return_count":       len(vc.navReturns),
		"atr_symbols":           len(vc.atrValues),
	}
	
	// Add recent statistics
	if len(vc.navReturns) > 0 {
		recent := vc.navReturns
		if len(recent) > 100 {
			recent = recent[len(recent)-100:] // Last 100 observations
		}
		
		metrics["recent_mean_return"] = vc.mean(recent)
		metrics["recent_return_count"] = len(recent)
		
		// Calculate downside deviation (volatility of negative returns)
		negativeReturns := make([]float64, 0)
		for _, ret := range recent {
			if ret < 0 {
				negativeReturns = append(negativeReturns, ret)
			}
		}
		
		if len(negativeReturns) > 0 {
			downside := vc.standardDeviation(negativeReturns)
			metrics["downside_volatility"] = downside * math.Sqrt(252*78) // Annualized
		}
	}
	
	// Add ATR summary
	atrSummary := make(map[string]float64)
	for symbol, atr := range vc.atrValues {
		atrSummary[symbol] = atr
	}
	metrics["atr_values"] = atrSummary
	
	return metrics
}

// ResetHistory clears historical data (useful for backtesting or fresh starts)
func (vc *VolatilityCalculator) ResetHistory() {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	
	vc.navReturns = make([]float64, 0)
	vc.priceHistory = make(map[string][]PricePoint)
	vc.atrValues = make(map[string]float64)
	vc.currentVolatility = 0
	vc.ewmaVolatility = 0
	
	observ.IncCounter("volatility_calculator_resets_total", nil)
}

// Helper methods

func (vc *VolatilityCalculator) mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func (vc *VolatilityCalculator) standardDeviation(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	
	mean := vc.mean(values)
	variance := 0.0
	
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	
	variance /= float64(len(values) - 1)
	return math.Sqrt(variance)
}