package risk

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// CircuitBreakerState represents the current circuit breaker state
type CircuitBreakerState string

const (
	StateNormal      CircuitBreakerState = "normal"       // All trading allowed, 100% position sizes
	StateWarning     CircuitBreakerState = "warning"      // 2% daily, 5% weekly - enhanced monitoring
	StateReduced     CircuitBreakerState = "reduced"      // 70% position sizes, no aggressive positions
	StateRestricted  CircuitBreakerState = "restricted"   // 50% position sizes, risk-reducing only
	StateMinimal     CircuitBreakerState = "minimal"      // 25% position sizes, critical positions only
	StateHalted      CircuitBreakerState = "halted"       // 3% daily, 8% weekly - no new BUY positions
	StateCoolingOff  CircuitBreakerState = "cooling_off"  // Post-halt cooldown, only risk-reducing orders
	StateEmergency   CircuitBreakerState = "emergency"    // Manual intervention required, all trading stopped
)

// CircuitBreakerEvent represents an event in the circuit breaker system
type CircuitBreakerEvent struct {
	ID          string                 `json:"id"`
	Timestamp   time.Time              `json:"timestamp"`
	Type        string                 `json:"type"`
	Data        map[string]interface{} `json:"data"`
	CorrelationID string               `json:"correlation_id"`
	UserID      string                 `json:"user_id,omitempty"`
	Reason      string                 `json:"reason,omitempty"`
}

// CircuitBreakerEventType constants
const (
	EventNavUpdated        = "nav_updated"
	EventThresholdBreached = "threshold_breached"
	EventStateChanged      = "state_changed"
	EventManualOverride    = "manual_override"
	EventConfigChanged     = "config_changed"
	EventRecoveryInitiated = "recovery_initiated"
	EventCoolingOffExpired = "cooling_off_expired"
)

// CircuitBreaker manages trading circuit breaker logic with graduated responses
type CircuitBreaker struct {
	mu sync.RWMutex
	
	// Current state
	state          CircuitBreakerState
	stateEnteredAt time.Time
	sizeMultiplier float64
	coolingOffUntil time.Time
	
	// Thresholds (can be volatility-adjusted)
	thresholds CircuitBreakerThresholds
	
	// Manual overrides
	manualHalt     bool
	manualRecovery bool
	overrideUser   string
	overrideReason string
	
	// Event sourcing
	events      []CircuitBreakerEvent
	eventLog    string // File path for event persistence
	lastEventID int64
	
	// Recovery requirements
	recoveryRequirements RecoveryRequirements
	
	// Metrics
	stateStartTime map[CircuitBreakerState]time.Time
	triggerCounts  map[string]int
}

// CircuitBreakerThresholds defines drawdown thresholds (can be volatility-adjusted)
type CircuitBreakerThresholds struct {
	// Base thresholds (can be multiplied by volatility factor)
	DailyWarningPct     float64 `json:"daily_warning_pct"`     // 2.0
	DailyReducedPct     float64 `json:"daily_reduced_pct"`     // 2.5
	DailyRestrictedPct  float64 `json:"daily_restricted_pct"`  // 3.0
	DailyMinimalPct     float64 `json:"daily_minimal_pct"`     // 3.5
	DailyHaltPct        float64 `json:"daily_halt_pct"`        // 4.0
	
	WeeklyWarningPct    float64 `json:"weekly_warning_pct"`    // 5.0
	WeeklyReducedPct    float64 `json:"weekly_reduced_pct"`    // 6.0
	WeeklyRestrictedPct float64 `json:"weekly_restricted_pct"` // 7.0
	WeeklyMinimalPct    float64 `json:"weekly_minimal_pct"`    // 8.0
	WeeklyHaltPct       float64 `json:"weekly_halt_pct"`       // 10.0
	
	// Volatility adjustment
	VolatilityMultiplier float64 `json:"volatility_multiplier"` // Multiplier based on recent volatility
	MaxVolatilityFactor  float64 `json:"max_volatility_factor"` // Cap on volatility adjustment
	
	// Size multipliers for each state
	NormalSize      float64 `json:"normal_size"`      // 1.0
	WarningSize     float64 `json:"warning_size"`     // 1.0
	ReducedSize     float64 `json:"reduced_size"`     // 0.7
	RestrictedSize  float64 `json:"restricted_size"`  // 0.5
	MinimalSize     float64 `json:"minimal_size"`     // 0.25
	HaltedSize      float64 `json:"halted_size"`      // 0.0
	CoolingOffSize  float64 `json:"cooling_off_size"` // 0.0
	EmergencySize   float64 `json:"emergency_size"`   // 0.0
}

// RecoveryRequirements defines requirements for recovery from halt states
type RecoveryRequirements struct {
	RequiredApprovals     []string      `json:"required_approvals"`      // User IDs required for recovery
	CooldownPeriod        time.Duration `json:"cooldown_period"`         // Minimum time in cooling off
	MaxDrawdownForAuto    float64       `json:"max_drawdown_for_auto"`   // Max DD% for automatic recovery
	MinStabilityPeriod    time.Duration `json:"min_stability_period"`    // Time below threshold for recovery
	MaxDailyHalts         int           `json:"max_daily_halts"`         // Max halts per day before emergency
	QuoteFreshnessRequired bool         `json:"quote_freshness_required"` // Require fresh quotes for recovery
}

// NewCircuitBreaker creates a new circuit breaker with default configuration
func NewCircuitBreaker(eventLogPath string) *CircuitBreaker {
	cb := &CircuitBreaker{
		state:          StateNormal,
		stateEnteredAt: time.Now(),
		sizeMultiplier: 1.0,
		thresholds:     getDefaultThresholds(),
		eventLog:       eventLogPath,
		events:         make([]CircuitBreakerEvent, 0),
		stateStartTime: make(map[CircuitBreakerState]time.Time),
		triggerCounts:  make(map[string]int),
		recoveryRequirements: RecoveryRequirements{
			RequiredApprovals:     []string{}, // Will be populated from config
			CooldownPeriod:        30 * time.Minute,
			MaxDrawdownForAuto:    1.5, // Auto recovery only if DD < 1.5%
			MinStabilityPeriod:    10 * time.Minute,
			MaxDailyHalts:         3,
			QuoteFreshnessRequired: true,
		},
	}
	
	cb.stateStartTime[StateNormal] = time.Now()
	
	// Load persisted events
	if err := cb.loadEvents(); err != nil {
		observ.IncCounter("circuit_breaker_load_errors_total", nil)
	}
	
	// Replay events to rebuild state
	if err := cb.replayEvents(); err != nil {
		observ.IncCounter("circuit_breaker_replay_errors_total", nil)
	}
	
	return cb
}

// UpdateDrawdown processes new drawdown data and updates circuit breaker state
func (cb *CircuitBreaker) UpdateDrawdown(dailyDD, weeklyDD float64, navTracker *NAVTracker, correlationID string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	// Get current NAV via public method
	currentNAV, _, _ := navTracker.GetCurrentNAV()
	
	// Record NAV update event
	cb.addEvent(EventNavUpdated, map[string]interface{}{
		"daily_drawdown_pct":  dailyDD,
		"weekly_drawdown_pct": weeklyDD,
		"current_nav":         currentNAV,
	}, correlationID, "", "")
	
	// Check if we're manually overridden
	if cb.manualHalt {
		cb.setState(StateEmergency, "manual_halt", correlationID)
		return
	}
	
	// Check for cooling off expiration
	if cb.state == StateCoolingOff && time.Now().After(cb.coolingOffUntil) {
		cb.addEvent(EventCoolingOffExpired, map[string]interface{}{
			"cooling_off_duration_minutes": time.Since(cb.stateEnteredAt).Minutes(),
		}, correlationID, "", "cooling_off_period_completed")
		cb.setState(StateNormal, "cooling_off_expired", correlationID)
	}
	
	// Get current volatility-adjusted thresholds
	adjustedThresholds := cb.getVolatilityAdjustedThresholds()
	
	// Determine new state based on drawdowns
	newState := cb.determineStateFromDrawdown(dailyDD, weeklyDD, adjustedThresholds)
	
	// Check if we need to transition states
	if newState != cb.state {
		reason := cb.getTransitionReason(dailyDD, weeklyDD, adjustedThresholds, newState)
		
		// Record threshold breach event
		cb.addEvent(EventThresholdBreached, map[string]interface{}{
			"threshold_type":     reason,
			"daily_drawdown":     dailyDD,
			"weekly_drawdown":    weeklyDD,
			"previous_state":     string(cb.state),
			"new_state":          string(newState),
			"adjusted_threshold": cb.getThresholdForState(newState, adjustedThresholds),
		}, correlationID, "", reason)
		
		// Check for emergency conditions (too many halts)
		if newState == StateHalted || newState == StateEmergency {
			haltCount := cb.getDailyHaltCount()
			if haltCount >= cb.recoveryRequirements.MaxDailyHalts {
				newState = StateEmergency
				reason = fmt.Sprintf("max_daily_halts_exceeded_%d", haltCount)
			}
		}
		
		cb.setState(newState, reason, correlationID)
		
		// If transitioning to halted, start cooling off timer
		if newState == StateHalted {
			cb.coolingOffUntil = time.Now().Add(cb.recoveryRequirements.CooldownPeriod)
		}
	}
	
	// Update metrics
	cb.updateMetrics(dailyDD, weeklyDD)
}

// setState changes the circuit breaker state and records the event
func (cb *CircuitBreaker) setState(newState CircuitBreakerState, reason, correlationID string) {
	previousState := cb.state
	previousTime := cb.stateEnteredAt
	
	cb.state = newState
	cb.stateEnteredAt = time.Now()
	cb.sizeMultiplier = cb.getSizeMultiplierForState(newState)
	
	// Update state timing metrics
	if previousState != newState {
		duration := time.Since(previousTime)
		observ.Observe("circuit_breaker_state_duration_seconds", 
			duration.Seconds(), 
			map[string]string{"state": string(previousState)})
	}
	
	cb.stateStartTime[newState] = time.Now()
	
	// Increment trigger counter
	cb.triggerCounts[reason]++
	
	// Record state change event
	cb.addEvent(EventStateChanged, map[string]interface{}{
		"previous_state":    string(previousState),
		"new_state":         string(newState),
		"size_multiplier":   cb.sizeMultiplier,
		"state_duration_ms": time.Since(previousTime).Milliseconds(),
		"trigger_count":     cb.triggerCounts[reason],
	}, correlationID, "", reason)
	
	// Update metrics
	observ.SetGauge("circuit_breaker_state", cb.stateToFloat(newState), nil)
	observ.IncCounter("circuit_breaker_transitions_total", map[string]string{
		"from": string(previousState),
		"to":   string(newState),
		"reason": reason,
	})
}

// ManualHalt manually halts trading with user information
func (cb *CircuitBreaker) ManualHalt(userID, reason string) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	correlationID := fmt.Sprintf("manual_halt_%d", time.Now().UnixNano())
	
	cb.manualHalt = true
	cb.overrideUser = userID
	cb.overrideReason = reason
	
	cb.addEvent(EventManualOverride, map[string]interface{}{
		"action":     "halt",
		"user_id":    userID,
		"reason":     reason,
		"prev_state": string(cb.state),
	}, correlationID, userID, reason)
	
	cb.setState(StateEmergency, "manual_halt", correlationID)
	
	return cb.persistEvent(cb.events[len(cb.events)-1])
}

// InitiateRecovery starts the recovery process from halt states
func (cb *CircuitBreaker) InitiateRecovery(userID, reason string, approvals []string) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	// Validate recovery requirements
	if cb.state != StateHalted && cb.state != StateCoolingOff && cb.state != StateEmergency {
		return fmt.Errorf("cannot recover from state %s", cb.state)
	}
	
	// Check required approvals
	if len(cb.recoveryRequirements.RequiredApprovals) > 0 {
		if !cb.hasRequiredApprovals(approvals) {
			return fmt.Errorf("insufficient approvals for recovery")
		}
	}
	
	correlationID := fmt.Sprintf("recovery_%d", time.Now().UnixNano())
	
	cb.manualHalt = false
	cb.manualRecovery = true
	cb.overrideUser = userID
	cb.overrideReason = reason
	
	cb.addEvent(EventRecoveryInitiated, map[string]interface{}{
		"user_id":    userID,
		"reason":     reason,
		"approvals":  approvals,
		"prev_state": string(cb.state),
	}, correlationID, userID, reason)
	
	// Start with cooling off period
	cb.setState(StateCoolingOff, "manual_recovery", correlationID)
	cb.coolingOffUntil = time.Now().Add(cb.recoveryRequirements.CooldownPeriod)
	
	return cb.persistEvent(cb.events[len(cb.events)-1])
}

// GetState returns current circuit breaker state and size multiplier
func (cb *CircuitBreaker) GetState() (CircuitBreakerState, float64) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state, cb.sizeMultiplier
}

// CanTrade checks if an intent can be executed given current circuit breaker state
func (cb *CircuitBreaker) CanTrade(intent string) (bool, string) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	
	switch cb.state {
	case StateNormal, StateWarning:
		return true, ""
		
	case StateReduced, StateRestricted, StateMinimal:
		// Allow all trades but with reduced sizing
		return true, ""
		
	case StateHalted, StateCoolingOff:
		// Only allow risk-reducing trades
		if intent == "REDUCE" || intent == "CLOSE" {
			return true, ""
		}
		return false, fmt.Sprintf("circuit_breaker_%s", cb.state)
		
	case StateEmergency:
		// No trading allowed
		return false, "circuit_breaker_emergency"
		
	default:
		return false, "circuit_breaker_unknown_state"
	}
}

// GetSizeMultiplier returns the current position size multiplier
func (cb *CircuitBreaker) GetSizeMultiplier() float64 {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.sizeMultiplier
}

// GetStatus returns comprehensive circuit breaker status
func (cb *CircuitBreaker) GetStatus() map[string]interface{} {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	
	return map[string]interface{}{
		"state":                string(cb.state),
		"state_entered_at":     cb.stateEnteredAt,
		"size_multiplier":      cb.sizeMultiplier,
		"manual_halt":          cb.manualHalt,
		"manual_recovery":      cb.manualRecovery,
		"cooling_off_until":    cb.coolingOffUntil,
		"override_user":        cb.overrideUser,
		"override_reason":      cb.overrideReason,
		"daily_halt_count":     cb.getDailyHaltCount(),
		"time_in_current_state": time.Since(cb.stateEnteredAt),
		"thresholds":           cb.thresholds,
		"trigger_counts":       cb.triggerCounts,
	}
}

// Helper methods

func (cb *CircuitBreaker) determineStateFromDrawdown(dailyDD, weeklyDD float64, thresholds CircuitBreakerThresholds) CircuitBreakerState {
	// Check daily thresholds first (typically more restrictive)
	if dailyDD >= thresholds.DailyHaltPct {
		return StateHalted
	}
	if dailyDD >= thresholds.DailyMinimalPct {
		return StateMinimal
	}
	if dailyDD >= thresholds.DailyRestrictedPct {
		return StateRestricted
	}
	if dailyDD >= thresholds.DailyReducedPct {
		return StateReduced
	}
	if dailyDD >= thresholds.DailyWarningPct {
		return StateWarning
	}
	
	// Check weekly thresholds
	if weeklyDD >= thresholds.WeeklyHaltPct {
		return StateHalted
	}
	if weeklyDD >= thresholds.WeeklyMinimalPct {
		return StateMinimal
	}
	if weeklyDD >= thresholds.WeeklyRestrictedPct {
		return StateRestricted
	}
	if weeklyDD >= thresholds.WeeklyReducedPct {
		return StateReduced
	}
	if weeklyDD >= thresholds.WeeklyWarningPct {
		return StateWarning
	}
	
	return StateNormal
}

func (cb *CircuitBreaker) getSizeMultiplierForState(state CircuitBreakerState) float64 {
	switch state {
	case StateNormal:
		return cb.thresholds.NormalSize
	case StateWarning:
		return cb.thresholds.WarningSize
	case StateReduced:
		return cb.thresholds.ReducedSize
	case StateRestricted:
		return cb.thresholds.RestrictedSize
	case StateMinimal:
		return cb.thresholds.MinimalSize
	case StateHalted:
		return cb.thresholds.HaltedSize
	case StateCoolingOff:
		return cb.thresholds.CoolingOffSize
	case StateEmergency:
		return cb.thresholds.EmergencySize
	default:
		return 0.0
	}
}

func (cb *CircuitBreaker) getVolatilityAdjustedThresholds() CircuitBreakerThresholds {
	// TODO: Implement volatility adjustment based on recent ATR/EWMA
	// For now, return base thresholds
	adjustedThresholds := cb.thresholds
	
	// Example volatility adjustment (would be calculated from market data)
	volatilityFactor := math.Min(cb.thresholds.VolatilityMultiplier, cb.thresholds.MaxVolatilityFactor)
	if volatilityFactor != 1.0 {
		adjustedThresholds.DailyWarningPct *= volatilityFactor
		adjustedThresholds.DailyReducedPct *= volatilityFactor
		adjustedThresholds.DailyRestrictedPct *= volatilityFactor
		adjustedThresholds.DailyMinimalPct *= volatilityFactor
		adjustedThresholds.DailyHaltPct *= volatilityFactor
		
		adjustedThresholds.WeeklyWarningPct *= volatilityFactor
		adjustedThresholds.WeeklyReducedPct *= volatilityFactor
		adjustedThresholds.WeeklyRestrictedPct *= volatilityFactor
		adjustedThresholds.WeeklyMinimalPct *= volatilityFactor
		adjustedThresholds.WeeklyHaltPct *= volatilityFactor
	}
	
	return adjustedThresholds
}

func (cb *CircuitBreaker) getTransitionReason(dailyDD, weeklyDD float64, thresholds CircuitBreakerThresholds, newState CircuitBreakerState) string {
	switch newState {
	case StateWarning:
		if dailyDD >= thresholds.DailyWarningPct {
			return "daily_warning_threshold"
		}
		return "weekly_warning_threshold"
	case StateReduced:
		if dailyDD >= thresholds.DailyReducedPct {
			return "daily_reduced_threshold"
		}
		return "weekly_reduced_threshold"
	case StateRestricted:
		if dailyDD >= thresholds.DailyRestrictedPct {
			return "daily_restricted_threshold"
		}
		return "weekly_restricted_threshold"
	case StateMinimal:
		if dailyDD >= thresholds.DailyMinimalPct {
			return "daily_minimal_threshold"
		}
		return "weekly_minimal_threshold"
	case StateHalted:
		if dailyDD >= thresholds.DailyHaltPct {
			return "daily_halt_threshold"
		}
		return "weekly_halt_threshold"
	default:
		return "threshold_recovery"
	}
}

func (cb *CircuitBreaker) getThresholdForState(state CircuitBreakerState, thresholds CircuitBreakerThresholds) float64 {
	switch state {
	case StateWarning:
		return math.Min(thresholds.DailyWarningPct, thresholds.WeeklyWarningPct)
	case StateReduced:
		return math.Min(thresholds.DailyReducedPct, thresholds.WeeklyReducedPct)
	case StateRestricted:
		return math.Min(thresholds.DailyRestrictedPct, thresholds.WeeklyRestrictedPct)
	case StateMinimal:
		return math.Min(thresholds.DailyMinimalPct, thresholds.WeeklyMinimalPct)
	case StateHalted:
		return math.Min(thresholds.DailyHaltPct, thresholds.WeeklyHaltPct)
	default:
		return 0
	}
}

func (cb *CircuitBreaker) getDailyHaltCount() int {
	count := 0
	today := time.Now().UTC().Format("2006-01-02")
	
	for _, event := range cb.events {
		if event.Type == EventStateChanged &&
			event.Timestamp.UTC().Format("2006-01-02") == today {
			if newState, ok := event.Data["new_state"].(string); ok {
				if newState == string(StateHalted) || newState == string(StateEmergency) {
					count++
				}
			}
		}
	}
	
	return count
}

func (cb *CircuitBreaker) hasRequiredApprovals(approvals []string) bool {
	if len(cb.recoveryRequirements.RequiredApprovals) == 0 {
		return true // No approvals required
	}
	
	approvalMap := make(map[string]bool)
	for _, approval := range approvals {
		approvalMap[approval] = true
	}
	
	for _, required := range cb.recoveryRequirements.RequiredApprovals {
		if !approvalMap[required] {
			return false
		}
	}
	
	return true
}

func (cb *CircuitBreaker) stateToFloat(state CircuitBreakerState) float64 {
	switch state {
	case StateNormal:
		return 0
	case StateWarning:
		return 1
	case StateReduced:
		return 2
	case StateRestricted:
		return 3
	case StateMinimal:
		return 4
	case StateHalted:
		return 5
	case StateCoolingOff:
		return 6
	case StateEmergency:
		return 7
	default:
		return -1
	}
}

func (cb *CircuitBreaker) updateMetrics(dailyDD, weeklyDD float64) {
	observ.SetGauge("circuit_breaker_state", cb.stateToFloat(cb.state), nil)
	observ.SetGauge("circuit_breaker_size_multiplier", cb.sizeMultiplier, nil)
	observ.SetGauge("drawdown_daily_pct", dailyDD, nil)
	observ.SetGauge("drawdown_weekly_pct", weeklyDD, nil)
	observ.SetGauge("circuit_breaker_time_in_state_seconds", time.Since(cb.stateEnteredAt).Seconds(), nil)
	observ.SetGauge("circuit_breaker_daily_halt_count", float64(cb.getDailyHaltCount()), nil)
	
	if cb.state == StateCoolingOff && !cb.coolingOffUntil.IsZero() {
		observ.SetGauge("circuit_breaker_cooling_off_remaining_seconds", 
			time.Until(cb.coolingOffUntil).Seconds(), nil)
	}
}

func getDefaultThresholds() CircuitBreakerThresholds {
	return CircuitBreakerThresholds{
		// Daily thresholds
		DailyWarningPct:     2.0,
		DailyReducedPct:     2.5,
		DailyRestrictedPct:  3.0,
		DailyMinimalPct:     3.5,
		DailyHaltPct:        4.0,
		
		// Weekly thresholds
		WeeklyWarningPct:    5.0,
		WeeklyReducedPct:    6.0,
		WeeklyRestrictedPct: 7.0,
		WeeklyMinimalPct:    8.0,
		WeeklyHaltPct:       10.0,
		
		// Volatility adjustment
		VolatilityMultiplier: 1.0,
		MaxVolatilityFactor:  2.0,
		
		// Size multipliers
		NormalSize:      1.0,
		WarningSize:     1.0,
		ReducedSize:     0.7,
		RestrictedSize:  0.5,
		MinimalSize:     0.25,
		HaltedSize:      0.0,
		CoolingOffSize:  0.0,
		EmergencySize:   0.0,
	}
}

// Event sourcing methods are implemented in events.go