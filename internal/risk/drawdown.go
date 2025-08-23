package risk

import (
	"time"
	
	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// DrawdownManager manages drawdown monitoring and circuit breakers
type DrawdownManager struct {
	startOfDayNAV   float64
	startOfWeekNAV  float64
	lastUpdateTime  time.Time
	sizeMultiplier  float64
	warningActive   bool
	pauseActive     bool
}

// NewDrawdownManager creates a new drawdown manager
func NewDrawdownManager() *DrawdownManager {
	return &DrawdownManager{
		sizeMultiplier: 1.0, // Default full size
		warningActive:  false,
		pauseActive:    false,
	}
}

// UpdateNAV updates the current NAV and recalculates drawdowns
func (dm *DrawdownManager) UpdateNAV(currentNAV float64, now time.Time, config DrawdownConfig) {
	// Initialize start-of-day NAV if needed
	if dm.startOfDayNAV == 0 || isNewTradingDay(dm.lastUpdateTime, now) {
		dm.startOfDayNAV = currentNAV
	}
	
	// Initialize start-of-week NAV if needed  
	if dm.startOfWeekNAV == 0 || isNewTradingWeek(dm.lastUpdateTime, now) {
		dm.startOfWeekNAV = currentNAV
	}
	
	dm.lastUpdateTime = now
	
	// Calculate drawdowns
	dailyDrawdownPct := dm.calculateDrawdown(dm.startOfDayNAV, currentNAV)
	weeklyDrawdownPct := dm.calculateDrawdown(dm.startOfWeekNAV, currentNAV)
	
	// Update metrics
	observ.SetGauge("drawdown_pct_daily", dailyDrawdownPct, nil)
	observ.SetGauge("drawdown_pct_weekly", weeklyDrawdownPct, nil)
	observ.SetGauge("size_multiplier_current", dm.sizeMultiplier, nil)
	
	// Check thresholds and update state
	dm.checkThresholds(dailyDrawdownPct, weeklyDrawdownPct, config)
}

// CheckDrawdownGates returns if drawdown should pause new buys
func (dm *DrawdownManager) CheckDrawdownGates(intent string, config DrawdownConfig) (bool, string) {
	if !config.Enabled {
		return false, ""
	}
	
	// Only affect BUY intents, allow REDUCE
	if intent != "BUY_1X" && intent != "BUY_5X" {
		return false, ""
	}
	
	// Check if pause is active
	if dm.pauseActive {
		return true, "drawdown_pause"
	}
	
	return false, ""
}

// GetSizeMultiplier returns the current size multiplier for position sizing
func (dm *DrawdownManager) GetSizeMultiplier() float64 {
	return dm.sizeMultiplier
}

// IsWarningActive returns if drawdown warning is active
func (dm *DrawdownManager) IsWarningActive() bool {
	return dm.warningActive
}

// IsPauseActive returns if drawdown pause is active
func (dm *DrawdownManager) IsPauseActive() bool {
	return dm.pauseActive
}

// GetDrawdowns returns current daily and weekly drawdown percentages
func (dm *DrawdownManager) GetDrawdowns(currentNAV float64) (float64, float64) {
	dailyDrawdown := dm.calculateDrawdown(dm.startOfDayNAV, currentNAV)
	weeklyDrawdown := dm.calculateDrawdown(dm.startOfWeekNAV, currentNAV)
	return dailyDrawdown, weeklyDrawdown
}

// calculateDrawdown computes drawdown percentage from start to current NAV
func (dm *DrawdownManager) calculateDrawdown(startNAV, currentNAV float64) float64 {
	if startNAV <= 0 {
		return 0.0
	}
	
	drawdown := ((startNAV - currentNAV) / startNAV) * 100
	
	// Drawdown is positive when we lose money
	if drawdown < 0 {
		return 0.0 // No drawdown if we gained money
	}
	
	return drawdown
}

// checkThresholds evaluates drawdown thresholds and updates state
func (dm *DrawdownManager) checkThresholds(dailyDrawdownPct, weeklyDrawdownPct float64, config DrawdownConfig) {
	// Reset states
	previousWarning := dm.warningActive
	previousPause := dm.pauseActive
	
	dm.warningActive = false
	dm.pauseActive = false
	dm.sizeMultiplier = 1.0
	
	// Check daily pause threshold (highest priority)
	if dailyDrawdownPct >= config.DailyPausePct {
		dm.pauseActive = true
		dm.sizeMultiplier = 0.0 // No new positions
		
		if !previousPause {
			observ.IncCounter("drawdown_pauses_total", map[string]string{"type": "daily"})
		}
	} else if weeklyDrawdownPct >= config.WeeklyPausePct {
		// Check weekly pause threshold
		dm.pauseActive = true
		dm.sizeMultiplier = 0.0
		
		if !previousPause {
			observ.IncCounter("drawdown_pauses_total", map[string]string{"type": "weekly"})
		}
	} else if dailyDrawdownPct >= config.DailyWarningPct {
		// Check daily warning threshold
		dm.warningActive = true
		dm.sizeMultiplier = config.SizeMultiplierOnWarningPct / 100.0
		
		if !previousWarning {
			observ.IncCounter("drawdown_warnings_total", map[string]string{"type": "daily"})
		}
	} else if weeklyDrawdownPct >= config.WeeklyWarningPct {
		// Check weekly warning threshold
		dm.warningActive = true
		dm.sizeMultiplier = config.SizeMultiplierOnWarningPct / 100.0
		
		if !previousWarning {
			observ.IncCounter("drawdown_warnings_total", map[string]string{"type": "weekly"})
		}
	}
}

// isNewTradingDay checks if we've crossed into a new trading day (UTC boundary)
func isNewTradingDay(last, current time.Time) bool {
	if last.IsZero() {
		return true
	}
	
	lastDate := last.UTC().Format("2006-01-02")
	currentDate := current.UTC().Format("2006-01-02")
	
	return lastDate != currentDate
}

// isNewTradingWeek checks if we've crossed into a new trading week (Monday 00:00 UTC)
func isNewTradingWeek(last, current time.Time) bool {
	if last.IsZero() {
		return true
	}
	
	// Get Monday of the week for both times
	lastMonday := getMondayOfWeek(last.UTC())
	currentMonday := getMondayOfWeek(current.UTC())
	
	return !lastMonday.Equal(currentMonday)
}

// getMondayOfWeek returns the Monday 00:00 UTC of the week containing the given time
func getMondayOfWeek(t time.Time) time.Time {
	// Get the weekday (0 = Sunday, 1 = Monday, ..., 6 = Saturday)
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7 // Make Sunday = 7 for easier calculation
	}
	
	// Calculate days to subtract to get to Monday
	daysToMonday := weekday - 1
	
	monday := t.AddDate(0, 0, -daysToMonday)
	
	// Set to 00:00 UTC
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, time.UTC)
}

// DrawdownConfig represents drawdown monitoring configuration
type DrawdownConfig struct {
	Enabled                    bool
	DailyWarningPct            float64
	DailyPausePct              float64
	WeeklyWarningPct           float64
	WeeklyPausePct             float64
	SizeMultiplierOnWarningPct float64
}