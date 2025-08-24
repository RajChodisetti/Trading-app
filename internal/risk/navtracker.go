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

// NAVTracker provides real-time portfolio NAV tracking with data quality guardrails
type NAVTracker struct {
	portfolioMgr  *portfolio.Manager
	quotesAdapter adapters.QuotesAdapter
	
	// State
	mu            sync.RWMutex
	lastNAV       float64
	lastUpdate    time.Time
	navHistory    []NAVSnapshot
	startOfDayNAV float64
	highWaterMark float64
	
	// Data quality
	quoteStalenessThreshold time.Duration
	frozenUntil            time.Time
	frozenReason           string
	
	// Persistence
	persistPath string
	
	// Config
	config NAVTrackerConfig
}

// NAVSnapshot represents a point-in-time portfolio valuation
type NAVSnapshot struct {
	Timestamp     time.Time            `json:"timestamp"`
	NAV           float64              `json:"nav"`
	DailyPnL      float64              `json:"daily_pnl"`
	UnrealizedPnL float64              `json:"unrealized_pnl"`
	RealizedPnL   float64              `json:"realized_pnl"`
	Positions     map[string]float64   `json:"positions"`        // symbol -> unrealized PnL
	QuoteAges     map[string]time.Duration `json:"quote_ages"`   // symbol -> age since quote
	DataQuality   NAVDataQuality       `json:"data_quality"`
}

// NAVDataQuality tracks data quality metrics for NAV calculation
type NAVDataQuality struct {
	StaleQuotes      []string `json:"stale_quotes"`       // Symbols with stale quotes
	MissingQuotes    []string `json:"missing_quotes"`     // Symbols without quotes
	UsingMidPrice    []string `json:"using_mid_price"`    // Symbols using mid vs last
	UsingLastTrade   []string `json:"using_last_trade"`   // Symbols using last vs mid
	TotalStaleness   time.Duration `json:"total_staleness"` // Max staleness across all quotes
}

// NAVTrackerConfig configures NAV tracking behavior
type NAVTrackerConfig struct {
	UpdateIntervalSeconds     int  `yaml:"update_interval_seconds"`     // How often to recalculate NAV
	QuoteStalenessThresholdMs int  `yaml:"quote_staleness_threshold_ms"` // Max age for fresh quotes
	MaxHistoryEntries         int  `yaml:"max_history_entries"`          // Max NAV history to keep
	UseMidPrice              bool  `yaml:"use_mid_price"`               // Use mid vs last trade price
	PersistPath              string `yaml:"persist_path"`               // File path for persistence
}

// NAVState represents persisted NAV state for restart recovery
type NAVState struct {
	StartOfDayNAV   float64              `json:"start_of_day_nav"`
	HighWaterMark   float64              `json:"high_water_mark"`
	LastUpdate      time.Time            `json:"last_update"`
	LastNAV         float64              `json:"last_nav"`
	TradingDate     string               `json:"trading_date"`     // YYYY-MM-DD
	Positions       map[string]portfolio.Position `json:"positions"` // For reconciliation
}

// NewNAVTracker creates a new real-time NAV tracker
func NewNAVTracker(portfolioMgr *portfolio.Manager, quotesAdapter adapters.QuotesAdapter, config NAVTrackerConfig) *NAVTracker {
	if config.UpdateIntervalSeconds == 0 {
		config.UpdateIntervalSeconds = 1 // Default 1 second updates
	}
	if config.QuoteStalenessThresholdMs == 0 {
		config.QuoteStalenessThresholdMs = 2000 // Default 2 second staleness threshold
	}
	if config.MaxHistoryEntries == 0 {
		config.MaxHistoryEntries = 3600 // Default 1 hour at 1s intervals
	}
	if config.PersistPath == "" {
		config.PersistPath = "data/nav_state.json"
	}

	return &NAVTracker{
		portfolioMgr:            portfolioMgr,
		quotesAdapter:           quotesAdapter,
		navHistory:              make([]NAVSnapshot, 0, config.MaxHistoryEntries),
		quoteStalenessThreshold: time.Duration(config.QuoteStalenessThresholdMs) * time.Millisecond,
		persistPath:             config.PersistPath,
		config:                  config,
	}
}

// Start begins real-time NAV tracking
func (nt *NAVTracker) Start(ctx context.Context) error {
	// Load persisted state
	if err := nt.loadState(); err != nil {
		observ.IncCounter("nav_tracker_load_errors_total", map[string]string{"error": "state_load"})
		// Continue with fresh state - don't fail startup
	}

	// Initialize if needed
	if err := nt.initializeDailyState(); err != nil {
		return fmt.Errorf("failed to initialize daily state: %w", err)
	}

	// Start update loop
	ticker := time.NewTicker(time.Duration(nt.config.UpdateIntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := nt.updateNAV(ctx); err != nil {
				observ.IncCounter("nav_tracker_update_errors_total", map[string]string{"error": "update"})
				// Continue on errors - don't stop tracking
			}
		}
	}
}

// updateNAV performs a real-time NAV calculation with data quality checks
func (nt *NAVTracker) updateNAV(ctx context.Context) error {
	start := time.Now()
	defer func() {
		observ.Observe("nav_update_latency_ms", float64(time.Since(start).Milliseconds()), nil)
	}()

	nt.mu.Lock()
	defer nt.mu.Unlock()

	// Check if we're in a frozen state
	if time.Now().Before(nt.frozenUntil) {
		observ.IncCounter("nav_updates_skipped_total", map[string]string{"reason": "frozen"})
		return nil
	}

	// Get current positions
	positions := nt.portfolioMgr.GetAllPositions()
	if len(positions) == 0 {
		// No positions - NAV is just capital base
		nav := nt.portfolioMgr.GetNAV()
		nt.recordNAVSnapshot(nav, 0, 0, nil, NAVDataQuality{})
		return nil
	}

	// Get quotes for all position symbols
	quotes := make(map[string]*adapters.Quote)
	quoteAges := make(map[string]time.Duration)
	var staleQuotes, missingQuotes, usingMid, usingLast []string

	totalUnrealizedPnL := 0.0
	maxStaleness := time.Duration(0)

	for symbol := range positions {
		quote, err := nt.quotesAdapter.GetQuote(ctx, symbol)
		if err != nil {
			missingQuotes = append(missingQuotes, symbol)
			observ.IncCounter("nav_missing_quotes_total", map[string]string{"symbol": symbol})
			continue
		}

		quotes[symbol] = quote
		age := time.Since(quote.Timestamp)
		quoteAges[symbol] = age
		
		if age > maxStaleness {
			maxStaleness = age
		}

		// Check if quote is stale
		if age > nt.quoteStalenessThreshold {
			staleQuotes = append(staleQuotes, symbol)
			observ.SetGauge("quote_staleness_seconds", age.Seconds(), map[string]string{"symbol": symbol})
		}

		// Determine which price to use and track methodology
		var mtmPrice float64
		pos := positions[symbol]
		
		if nt.config.UseMidPrice && quote.Bid > 0 && quote.Ask > 0 {
			mtmPrice = (quote.Bid + quote.Ask) / 2
			usingMid = append(usingMid, symbol)
		} else if quote.Last > 0 {
			mtmPrice = quote.Last
			usingLast = append(usingLast, symbol)
		} else {
			// Fallback to mid if last is missing
			if quote.Bid > 0 && quote.Ask > 0 {
				mtmPrice = (quote.Bid + quote.Ask) / 2
				usingMid = append(usingMid, symbol)
			} else {
				missingQuotes = append(missingQuotes, symbol)
				continue
			}
		}

		// Calculate unrealized P&L
		unrealizedPnL := float64(pos.Quantity) * (mtmPrice - pos.AvgEntryPrice)
		totalUnrealizedPnL += unrealizedPnL
		
		// Update position unrealized P&L in portfolio manager
		nt.portfolioMgr.UpdateUnrealizedPnL(symbol, mtmPrice)
	}

	// Check data quality - freeze NAV updates if too much stale data
	dataQuality := NAVDataQuality{
		StaleQuotes:    staleQuotes,
		MissingQuotes:  missingQuotes,
		UsingMidPrice:  usingMid,
		UsingLastTrade: usingLast,
		TotalStaleness: maxStaleness,
	}

	// Freeze NAV updates if data quality is poor
	if len(staleQuotes) > 0 && maxStaleness > nt.quoteStalenessThreshold*2 {
		nt.frozenUntil = time.Now().Add(30 * time.Second) // Freeze for 30 seconds
		nt.frozenReason = fmt.Sprintf("excessive_staleness_%ds", int(maxStaleness.Seconds()))
		observ.IncCounter("nav_freezes_total", map[string]string{"reason": "staleness"})
		return nil
	}

	// Calculate full NAV
	capitalBase := nt.portfolioMgr.GetNAV() // This includes realized P&L
	dailyStats := nt.portfolioMgr.GetDailyStats()
	
	// Record NAV snapshot
	positionPnL := make(map[string]float64)
	for symbol, pos := range positions {
		positionPnL[symbol] = pos.UnrealizedPnL
	}

	nt.recordNAVSnapshot(
		capitalBase, 
		dailyStats.PnLToday, 
		totalUnrealizedPnL,
		positionPnL,
		dataQuality,
	)

	// Persist state periodically (every 10 updates)
	if len(nt.navHistory)%10 == 0 {
		if err := nt.persistState(); err != nil {
			observ.IncCounter("nav_persist_errors_total", nil)
		}
	}

	// Update metrics
	observ.SetGauge("portfolio_nav_usd", nt.lastNAV, nil)
	observ.SetGauge("portfolio_daily_pnl_usd", dailyStats.PnLToday, nil)
	observ.SetGauge("portfolio_unrealized_pnl_usd", totalUnrealizedPnL, nil)
	observ.SetGauge("nav_quote_staleness_max_seconds", maxStaleness.Seconds(), nil)
	observ.SetGauge("nav_data_quality_score", nt.calculateDataQualityScore(dataQuality), nil)

	return nil
}

// recordNAVSnapshot adds a new NAV snapshot to history
func (nt *NAVTracker) recordNAVSnapshot(nav, realizedPnL, unrealizedPnL float64, positions map[string]float64, quality NAVDataQuality) {
	now := time.Now()
	
	snapshot := NAVSnapshot{
		Timestamp:     now,
		NAV:           nav,
		DailyPnL:      realizedPnL + unrealizedPnL,
		UnrealizedPnL: unrealizedPnL,
		RealizedPnL:   realizedPnL,
		Positions:     positions,
		QuoteAges:     make(map[string]time.Duration),
		DataQuality:   quality,
	}

	// Update state
	nt.lastNAV = nav
	nt.lastUpdate = now
	
	// Update high water mark
	if nav > nt.highWaterMark {
		nt.highWaterMark = nav
	}

	// Add to history (with size limit)
	nt.navHistory = append(nt.navHistory, snapshot)
	if len(nt.navHistory) > nt.config.MaxHistoryEntries {
		nt.navHistory = nt.navHistory[1:] // Remove oldest
	}
}

// GetCurrentNAV returns the most recent NAV with data quality info
func (nt *NAVTracker) GetCurrentNAV() (float64, NAVDataQuality, time.Time) {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	
	if len(nt.navHistory) == 0 {
		return nt.lastNAV, NAVDataQuality{}, nt.lastUpdate
	}
	
	latest := nt.navHistory[len(nt.navHistory)-1]
	return latest.NAV, latest.DataQuality, latest.Timestamp
}

// GetDrawdowns returns current daily and weekly drawdown percentages
func (nt *NAVTracker) GetDrawdowns() (daily, weekly float64) {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	
	if nt.lastNAV == 0 || nt.startOfDayNAV == 0 {
		return 0, 0
	}

	// Daily drawdown from start of trading day
	daily = math.Max(0, ((nt.startOfDayNAV - nt.lastNAV) / nt.startOfDayNAV) * 100)
	
	// Weekly drawdown from high water mark over last 5 trading days
	weeklyStart := nt.getWeeklyStartNAV()
	if weeklyStart > 0 {
		weekly = math.Max(0, ((weeklyStart - nt.lastNAV) / weeklyStart) * 100)
	}
	
	return daily, weekly
}

// initializeDailyState sets up start-of-day NAV for drawdown calculations
func (nt *NAVTracker) initializeDailyState() error {
	today := time.Now().UTC().Format("2006-01-02")
	
	// Check if we need to reset for new trading day
	if nt.startOfDayNAV == 0 || nt.isNewTradingDay() {
		currentNAV := nt.portfolioMgr.GetNAV()
		nt.startOfDayNAV = currentNAV
		
		// Initialize high water mark if not set
		if nt.highWaterMark == 0 {
			nt.highWaterMark = currentNAV
		}
		
		observ.SetGauge("start_of_day_nav_usd", nt.startOfDayNAV, nil)
		
		// Log the initialization
		observ.IncCounter("nav_daily_resets_total", map[string]string{"date": today})
	}
	
	return nil
}

// isNewTradingDay checks if we've crossed into a new trading day
func (nt *NAVTracker) isNewTradingDay() bool {
	if nt.lastUpdate.IsZero() {
		return true
	}
	
	// Use NYSE trading calendar - market close at 4:00 PM ET
	etLocation, _ := time.LoadLocation("America/New_York")
	
	lastET := nt.lastUpdate.In(etLocation)
	nowET := time.Now().In(etLocation)
	
	// Different calendar date OR crossed 4:00 PM ET
	if lastET.Day() != nowET.Day() || lastET.Month() != nowET.Month() || lastET.Year() != nowET.Year() {
		return true
	}
	
	// Check if we crossed market close (16:00 ET)
	lastMarketClose := time.Date(lastET.Year(), lastET.Month(), lastET.Day(), 16, 0, 0, 0, etLocation)
	nowMarketClose := time.Date(nowET.Year(), nowET.Month(), nowET.Day(), 16, 0, 0, 0, etLocation)
	
	return lastET.Before(lastMarketClose) && nowET.After(nowMarketClose)
}

// getWeeklyStartNAV gets NAV from 5 trading days ago (weekly drawdown)
func (nt *NAVTracker) getWeeklyStartNAV() float64 {
	if len(nt.navHistory) < 5*24*3600/nt.config.UpdateIntervalSeconds {
		// Not enough history - use high water mark
		return nt.highWaterMark
	}
	
	// Get NAV from approximately 5 trading days ago
	// Rough approximation - could be enhanced with actual trading calendar
	targetIndex := len(nt.navHistory) - (5 * 6 * 3600 / nt.config.UpdateIntervalSeconds) // 5 days * 6 hours * 3600 seconds
	if targetIndex < 0 {
		targetIndex = 0
	}
	
	return nt.navHistory[targetIndex].NAV
}

// calculateDataQualityScore returns a quality score from 0-1 based on data quality
func (nt *NAVTracker) calculateDataQualityScore(quality NAVDataQuality) float64 {
	totalSymbols := len(quality.StaleQuotes) + len(quality.MissingQuotes) + len(quality.UsingMidPrice) + len(quality.UsingLastTrade)
	if totalSymbols == 0 {
		return 1.0 // Perfect score if no positions
	}
	
	// Penalize missing quotes heavily, stale quotes moderately
	missingPenalty := float64(len(quality.MissingQuotes)) * 0.8
	stalePenalty := float64(len(quality.StaleQuotes)) * 0.3
	
	score := 1.0 - (missingPenalty+stalePenalty)/float64(totalSymbols)
	return math.Max(0, math.Min(1.0, score))
}

// persistState saves current NAV state to disk
func (nt *NAVTracker) persistState() error {
	state := NAVState{
		StartOfDayNAV: nt.startOfDayNAV,
		HighWaterMark: nt.highWaterMark,
		LastUpdate:    nt.lastUpdate,
		LastNAV:       nt.lastNAV,
		TradingDate:   time.Now().UTC().Format("2006-01-02"),
		Positions:     nt.portfolioMgr.GetAllPositions(),
	}
	
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal NAV state: %w", err)
	}
	
	// Atomic write
	tempPath := nt.persistPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp NAV state: %w", err)
	}
	
	if err := os.Rename(tempPath, nt.persistPath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename NAV state: %w", err)
	}
	
	return nil
}

// loadState loads NAV state from disk
func (nt *NAVTracker) loadState() error {
	data, err := os.ReadFile(nt.persistPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Not an error - just no previous state
		}
		return fmt.Errorf("failed to read NAV state: %w", err)
	}
	
	var state NAVState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to unmarshal NAV state: %w", err)
	}
	
	// Restore state if it's from the same trading day
	today := time.Now().UTC().Format("2006-01-02")
	if state.TradingDate == today {
		nt.startOfDayNAV = state.StartOfDayNAV
		nt.highWaterMark = state.HighWaterMark
		nt.lastUpdate = state.LastUpdate
		nt.lastNAV = state.LastNAV
		
		observ.IncCounter("nav_state_restored_total", map[string]string{"date": today})
	}
	
	return nil
}

// GetNAVHistory returns recent NAV history for analysis
func (nt *NAVTracker) GetNAVHistory(maxEntries int) []NAVSnapshot {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	
	if maxEntries <= 0 || maxEntries >= len(nt.navHistory) {
		// Return copy of all history
		result := make([]NAVSnapshot, len(nt.navHistory))
		copy(result, nt.navHistory)
		return result
	}
	
	// Return last N entries
	start := len(nt.navHistory) - maxEntries
	result := make([]NAVSnapshot, maxEntries)
	copy(result, nt.navHistory[start:])
	return result
}

// IsFrozen returns if NAV updates are currently frozen due to data quality issues
func (nt *NAVTracker) IsFrozen() (bool, string) {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	
	frozen := time.Now().Before(nt.frozenUntil)
	return frozen, nt.frozenReason
}