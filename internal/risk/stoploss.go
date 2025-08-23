package risk

import (
	"time"
	
	"github.com/Rajchodisetti/trading-app/internal/observ"
	"github.com/Rajchodisetti/trading-app/internal/outbox"
)

// StopLossTrigger represents a stop-loss trigger event
type StopLossTrigger struct {
	Symbol          string    `json:"symbol"`
	PositionID      string    `json:"position_id"`
	TriggerType     string    `json:"trigger_type"` // "absolute" or "trailing"
	TriggerPrice    float64   `json:"trigger_price"`
	CurrentPrice    float64   `json:"current_price"`
	EntryVWAP       float64   `json:"entry_vwap"`
	LossPct         float64   `json:"loss_pct"`
	TimestampUTC    time.Time `json:"timestamp_utc"`
	TradingSession  string    `json:"trading_session"` // "RTH" or "AH"
}

// StopLossManager manages stop-loss triggers and cooldowns
type StopLossManager struct {
	triggers      map[string]StopLossTrigger // symbol -> last trigger
	cooldowns     map[string]time.Time       // symbol -> cooldown until
	outboxManager *outbox.Outbox
}

// NewStopLossManager creates a new stop-loss manager
func NewStopLossManager(outboxMgr *outbox.Outbox) *StopLossManager {
	return &StopLossManager{
		triggers:      make(map[string]StopLossTrigger),
		cooldowns:     make(map[string]time.Time),
		outboxManager: outboxMgr,
	}
}

// CheckStopLoss evaluates if a position should trigger a stop-loss
func (slm *StopLossManager) CheckStopLoss(symbol string, currentPrice, entryVWAP float64, config StopLossConfig, isAfterHours bool, now time.Time) (bool, error) {
	if !config.Enabled {
		return false, nil
	}
	
	// Skip if after hours and not allowed
	if isAfterHours && !config.AllowAfterHours {
		return false, nil
	}
	
	// Check cooldown
	if cooldownUntil, exists := slm.cooldowns[symbol]; exists && now.Before(cooldownUntil) {
		observ.SetGauge("stop_cooldown_active", 1, map[string]string{"symbol": symbol})
		return false, nil
	} else if exists && now.After(cooldownUntil) {
		// Cooldown expired, clean up
		delete(slm.cooldowns, symbol)
		observ.SetGauge("stop_cooldown_active", 0, map[string]string{"symbol": symbol})
	}
	
	// Calculate loss percentage from entry VWAP
	if entryVWAP <= 0 || currentPrice <= 0 {
		return false, nil // Invalid prices
	}
	
	lossPct := ((entryVWAP - currentPrice) / entryVWAP) * 100
	
	// Check if loss exceeds threshold
	shouldTrigger := lossPct >= config.DefaultStopLossPct
	
	if shouldTrigger {
		// Check for idempotency - only trigger once per position per day
		positionID := generatePositionID(symbol, now)
		triggerKey := symbol + "_" + positionID
		
		if _, exists := slm.triggers[triggerKey]; exists {
			// Already triggered today for this position
			observ.IncCounter("stop_triggers_duplicate_total", map[string]string{"symbol": symbol, "type": "absolute"})
			return false, nil
		}
		
		// Create trigger record
		trigger := StopLossTrigger{
			Symbol:         symbol,
			PositionID:     positionID,
			TriggerType:    "absolute",
			TriggerPrice:   currentPrice,
			CurrentPrice:   currentPrice,
			EntryVWAP:      entryVWAP,
			LossPct:        lossPct,
			TimestampUTC:   now,
			TradingSession: getTradingSession(isAfterHours),
		}
		
		// Record the trigger
		slm.triggers[triggerKey] = trigger
		
		// Set cooldown
		slm.cooldowns[symbol] = now.Add(time.Duration(config.CooldownHours) * time.Hour)
		observ.SetGauge("stop_cooldown_active", 1, map[string]string{"symbol": symbol})
		
		// Emit stop-loss order
		err := slm.emitStopOrder(trigger)
		if err != nil {
			return false, err
		}
		
		// Update metrics
		observ.IncCounter("stop_triggers_total", map[string]string{"symbol": symbol, "type": "absolute"})
		observ.IncCounter("stop_orders_sent_total", map[string]string{"symbol": symbol})
		
		return true, nil
	}
	
	return false, nil
}

// IsInCooldown checks if a symbol is in stop-loss cooldown
func (slm *StopLossManager) IsInCooldown(symbol string, now time.Time) bool {
	if cooldownUntil, exists := slm.cooldowns[symbol]; exists {
		return now.Before(cooldownUntil)
	}
	return false
}

// GetCooldownUntil returns the cooldown expiry time for a symbol
func (slm *StopLossManager) GetCooldownUntil(symbol string) (time.Time, bool) {
	cooldownUntil, exists := slm.cooldowns[symbol]
	return cooldownUntil, exists
}

// emitStopOrder creates a paper SELL order for the stop-loss
func (slm *StopLossManager) emitStopOrder(trigger StopLossTrigger) error {
	if slm.outboxManager == nil {
		return nil // No outbox available
	}
	
	order := outbox.Order{
		ID:             "stop_" + trigger.Symbol + "_" + trigger.PositionID,
		Symbol:         trigger.Symbol,
		Intent:         "REDUCE", // Reduce position due to stop-loss
		Timestamp:      trigger.TimestampUTC,
		Status:         "pending",
		IdempotencyKey: trigger.Symbol + "_stop_" + trigger.PositionID,
	}
	
	return slm.outboxManager.WriteOrder(order)
}

// generatePositionID creates a unique position ID for stop-loss tracking
func generatePositionID(symbol string, now time.Time) string {
	// Use UTC date as position bucket for idempotency
	return symbol + "_" + now.UTC().Format("2006-01-02")
}

// getTradingSession determines if current time is regular trading hours
func getTradingSession(isAfterHours bool) string {
	if isAfterHours {
		return "AH"
	}
	return "RTH"
}

// StopLossConfig represents stop-loss configuration
type StopLossConfig struct {
	Enabled              bool
	DefaultStopLossPct   float64
	EmergencyStopLossPct float64
	AllowAfterHours      bool
	CooldownHours        int
}