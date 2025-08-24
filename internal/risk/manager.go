package risk

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/adapters"
	"github.com/Rajchodisetti/trading-app/internal/observ"
	"github.com/Rajchodisetti/trading-app/internal/portfolio"
)

// RiskManager coordinates all risk management components
type RiskManager struct {
	mu sync.RWMutex
	
	// Core components
	navTracker         *NAVTracker
	circuitBreaker     *CircuitBreaker
	volatilityCalc     *VolatilityCalculator
	observabilityMgr   *RiskObservabilityManager
	
	// Configuration
	config RiskManagerConfig
	
	// State
	running        bool
	ctx            context.Context
	cancel         context.CancelFunc
	lastDecisionID string
}

// RiskManagerConfig configures the risk management system
type RiskManagerConfig struct {
	// NAV tracking configuration
	NAVTracker NAVTrackerConfig `yaml:"nav_tracker"`
	
	// Circuit breaker thresholds
	CircuitBreaker CircuitBreakerThresholds `yaml:"circuit_breaker"`
	
	// Volatility calculation settings
	Volatility VolatilityConfig `yaml:"volatility"`
	
	// Observability settings
	EventLogPath      string `yaml:"event_log_path"`
	MetricsEnabled    bool   `yaml:"metrics_enabled"`
	AlertingEnabled   bool   `yaml:"alerting_enabled"`
	
	// Integration settings
	UpdateIntervalSeconds int `yaml:"update_interval_seconds"`
	DecisionTimeoutMs     int `yaml:"decision_timeout_ms"`
}

// DecisionContext provides context for decision evaluation
type DecisionContext struct {
	Symbol        string                 `json:"symbol"`
	Intent        string                 `json:"intent"`
	Quantity      int                    `json:"quantity"`
	Price         float64                `json:"price"`
	Strategy      string                 `json:"strategy"`
	Score         float64                `json:"score"`
	Features      map[string]interface{} `json:"features"`
	CorrelationID string                 `json:"correlation_id"`
	Timestamp     time.Time              `json:"timestamp"`
}

// DecisionResult contains the risk management decision
type DecisionResult struct {
	Approved         bool                   `json:"approved"`
	Intent           string                 `json:"intent"`           // Original or modified intent
	SizeMultiplier   float64                `json:"size_multiplier"`   // Applied size adjustment
	BlockedBy        []string               `json:"blocked_by"`        // Risk gates that blocked
	Warnings         []string               `json:"warnings"`          // Non-blocking warnings
	Context          map[string]interface{} `json:"context"`           // Additional context
	ProcessingTime   time.Duration          `json:"processing_time"`   // Time taken for decision
	DecisionID       string                 `json:"decision_id"`       // Unique decision ID
	RiskScore        float64                `json:"risk_score"`        // Overall risk assessment
}

// RiskGate represents a risk control gate
type RiskGate interface {
	Name() string
	Evaluate(ctx DecisionContext, riskData RiskData) (bool, string, error)
	Priority() int // Lower number = higher priority
}

// RiskData provides current risk state to gates
type RiskData struct {
	CurrentNAV        float64                    `json:"current_nav"`
	DailyDrawdown     float64                    `json:"daily_drawdown"`
	WeeklyDrawdown    float64                    `json:"weekly_drawdown"`
	CircuitState      CircuitBreakerState        `json:"circuit_state"`
	SizeMultiplier    float64                    `json:"size_multiplier"`
	VolatilityRegime  string                     `json:"volatility_regime"`
	DataQuality       NAVDataQuality             `json:"data_quality"`
	PositionExposure  map[string]float64         `json:"position_exposure"`
	ComponentHealth   map[string]ComponentHealth `json:"component_health"`
	QuoteStaleness    time.Duration              `json:"quote_staleness"`
	LastUpdate        time.Time                  `json:"last_update"`
}

// Built-in risk gates

// CircuitBreakerGate blocks orders based on circuit breaker state
type CircuitBreakerGate struct{}

func (g *CircuitBreakerGate) Name() string { return "circuit_breaker" }
func (g *CircuitBreakerGate) Priority() int { return 1 } // Highest priority

func (g *CircuitBreakerGate) Evaluate(ctx DecisionContext, riskData RiskData) (bool, string, error) {
	switch riskData.CircuitState {
	case StateNormal, StateWarning, StateReduced, StateRestricted, StateMinimal:
		return true, "", nil
		
	case StateHalted, StateCoolingOff:
		// Only allow risk-reducing orders
		if ctx.Intent == "REDUCE" || ctx.Intent == "CLOSE" {
			return true, "", nil
		}
		return false, fmt.Sprintf("circuit_breaker_%s", riskData.CircuitState), nil
		
	case StateEmergency:
		// No trading allowed
		return false, "circuit_breaker_emergency", nil
		
	default:
		return false, "circuit_breaker_unknown", nil
	}
}

// DataQualityGate blocks orders when data quality is poor
type DataQualityGate struct {
	MinQualityScore   float64
	MaxStalenessMs    int64
}

func (g *DataQualityGate) Name() string { return "data_quality" }
func (g *DataQualityGate) Priority() int { return 2 }

func (g *DataQualityGate) Evaluate(ctx DecisionContext, riskData RiskData) (bool, string, error) {
	// Check data quality score
	qualityScore := calculateDataQualityScore(riskData.DataQuality)
	if qualityScore < g.MinQualityScore {
		return false, "data_quality_poor", nil
	}
	
	// Check quote staleness
	if riskData.QuoteStaleness.Milliseconds() > g.MaxStalenessMs {
		return false, "quotes_stale", nil
	}
	
	return true, "", nil
}

// VolatilityGate adjusts position sizes based on volatility regime
type VolatilityGate struct {
	VolatilityMultipliers map[string]float64 // regime -> multiplier
}

func (g *VolatilityGate) Name() string { return "volatility" }
func (g *VolatilityGate) Priority() int { return 3 }

func (g *VolatilityGate) Evaluate(ctx DecisionContext, riskData RiskData) (bool, string, error) {
	// This is informational - adjusts sizing but doesn't block
	multiplier, exists := g.VolatilityMultipliers[riskData.VolatilityRegime]
	if !exists {
		multiplier = 1.0
	}
	
	// Return sizing adjustment info in the reason
	reason := fmt.Sprintf("volatility_adjustment_%.2f", multiplier)
	return true, reason, nil
}

// NewRiskManager creates a new integrated risk manager
func NewRiskManager(
	portfolioMgr *portfolio.Manager,
	quotesAdapter adapters.QuotesAdapter,
	config RiskManagerConfig,
) *RiskManager {
	ctx, cancel := context.WithCancel(context.Background())
	
	// Initialize components
	navTracker := NewNAVTracker(portfolioMgr, quotesAdapter, config.NAVTracker)
	circuitBreaker := NewCircuitBreaker("data/circuit_breaker_events.jsonl")
	volatilityCalc := NewVolatilityCalculator(config.Volatility)
	observabilityMgr := NewRiskObservabilityManager(config.EventLogPath)
	
	return &RiskManager{
		navTracker:       navTracker,
		circuitBreaker:   circuitBreaker,
		volatilityCalc:   volatilityCalc,
		observabilityMgr: observabilityMgr,
		config:          config,
		ctx:             ctx,
		cancel:          cancel,
	}
}

// Start begins the risk management system
func (rm *RiskManager) Start() error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	
	if rm.running {
		return fmt.Errorf("risk manager already running")
	}
	
	rm.running = true
	
	// Start NAV tracking
	go func() {
		if err := rm.navTracker.Start(rm.ctx); err != nil {
			observ.IncCounter("risk_manager_errors_total", map[string]string{"component": "nav_tracker"})
		}
	}()
	
	// Start risk monitoring loop
	go rm.monitoringLoop()
	
	// Start component health monitoring
	go rm.healthMonitoringLoop()
	
	observ.IncCounter("risk_manager_starts_total", nil)
	
	return nil
}

// Stop shuts down the risk management system
func (rm *RiskManager) Stop() error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	
	if !rm.running {
		return nil
	}
	
	rm.running = false
	rm.cancel()
	
	observ.IncCounter("risk_manager_stops_total", nil)
	
	return nil
}

// EvaluateDecision evaluates a trading decision against all risk gates
func (rm *RiskManager) EvaluateDecision(ctx DecisionContext) DecisionResult {
	start := time.Now()
	decisionID := fmt.Sprintf("decision_%d", start.UnixNano())
	
	rm.lastDecisionID = decisionID
	
	// Get current risk data
	riskData := rm.getCurrentRiskData()
	
	// Initialize result
	result := DecisionResult{
		Approved:       true,
		Intent:         ctx.Intent,
		SizeMultiplier: 1.0,
		BlockedBy:      make([]string, 0),
		Warnings:       make([]string, 0),
		Context:        make(map[string]interface{}),
		DecisionID:     decisionID,
		RiskScore:      rm.calculateRiskScore(ctx, riskData),
	}
	
	// Create risk gates
	gates := []RiskGate{
		&CircuitBreakerGate{},
		&DataQualityGate{MinQualityScore: 0.8, MaxStalenessMs: 2000},
		&VolatilityGate{VolatilityMultipliers: map[string]float64{
			"quiet":    1.2,  // Increase size in quiet markets
			"normal":   1.0,  // Normal sizing
			"volatile": 0.7,  // Reduce size in volatile markets
		}},
	}
	
	// Evaluate gates in priority order
	for _, gate := range gates {
		approved, reason, err := gate.Evaluate(ctx, riskData)
		
		if err != nil {
			result.Approved = false
			result.BlockedBy = append(result.BlockedBy, fmt.Sprintf("%s_error", gate.Name()))
			rm.observabilityMgr.LogStructuredEvent(
				"gate_error",
				SeverityError,
				"risk_manager",
				fmt.Sprintf("Gate %s evaluation failed: %v", gate.Name(), err),
				map[string]interface{}{"gate": gate.Name(), "error": err.Error()},
				map[string]float64{},
				ctx.CorrelationID,
			)
			continue
		}
		
		if !approved {
			result.Approved = false
			result.BlockedBy = append(result.BlockedBy, reason)
		} else if reason != "" {
			// Gate passed but with warnings/adjustments
			if gate.Name() == "volatility" {
				// Extract volatility adjustment
				if multiplier := parseVolatilityMultiplier(reason); multiplier > 0 {
					result.SizeMultiplier *= multiplier
				}
			}
			result.Warnings = append(result.Warnings, reason)
		}
	}
	
	// Apply circuit breaker size multiplier
	result.SizeMultiplier *= riskData.SizeMultiplier
	
	// Add context
	result.Context = map[string]interface{}{
		"risk_data":         riskData,
		"circuit_state":     string(riskData.CircuitState),
		"volatility_regime": riskData.VolatilityRegime,
		"data_quality_score": calculateDataQualityScore(riskData.DataQuality),
	}
	
	// Record processing time
	result.ProcessingTime = time.Since(start)
	
	// Log decision
	rm.observabilityMgr.LogStructuredEvent(
		"decision_evaluated",
		SeverityInfo,
		"risk_manager",
		fmt.Sprintf("Decision %s: approved=%t, intent=%s", decisionID, result.Approved, result.Intent),
		map[string]interface{}{
			"decision_context": ctx,
			"decision_result":  result,
		},
		map[string]float64{
			"processing_time_ms": float64(result.ProcessingTime.Milliseconds()),
			"risk_score":        result.RiskScore,
			"size_multiplier":   result.SizeMultiplier,
		},
		ctx.CorrelationID,
	)
	
	// Track decision metrics
	rm.observabilityMgr.TrackDecisionLatency(
		result.ProcessingTime,
		len(result.BlockedBy),
		ctx.CorrelationID,
	)
	
	// Update Prometheus metrics
	observ.Observe("risk_decision_processing_time_ms", float64(result.ProcessingTime.Milliseconds()), nil)
	observ.IncCounter("risk_decisions_total", map[string]string{
		"approved": fmt.Sprintf("%t", result.Approved),
		"intent":   ctx.Intent,
	})
	
	if !result.Approved {
		observ.IncCounter("risk_decisions_blocked_total", map[string]string{
			"reason": result.BlockedBy[0], // Primary blocking reason
		})
	}
	
	return result
}

// GetCurrentRiskStatus returns current risk system status
func (rm *RiskManager) GetCurrentRiskStatus() map[string]interface{} {
	riskData := rm.getCurrentRiskData()
	systemHealth := rm.observabilityMgr.GetSystemHealth()
	metrics := rm.observabilityMgr.GetRiskMetrics()
	
	return map[string]interface{}{
		"risk_data":      riskData,
		"system_health":  systemHealth,
		"metrics":        metrics,
		"last_decision":  rm.lastDecisionID,
		"running":        rm.running,
		"update_time":    time.Now(),
	}
}

// GetRiskMetrics returns detailed risk metrics
func (rm *RiskManager) GetRiskMetrics() RiskMetrics {
	return rm.observabilityMgr.GetRiskMetrics()
}

// ManualHalt manually halts trading
func (rm *RiskManager) ManualHalt(userID, reason string) error {
	return rm.circuitBreaker.ManualHalt(userID, reason)
}

// InitiateRecovery starts recovery from halt state
func (rm *RiskManager) InitiateRecovery(userID, reason string, approvals []string) error {
	return rm.circuitBreaker.InitiateRecovery(userID, reason, approvals)
}

// Internal methods

func (rm *RiskManager) getCurrentRiskData() RiskData {
	// Get current NAV and quality
	nav, dataQuality, lastUpdate := rm.navTracker.GetCurrentNAV()
	
	// Get drawdowns
	dailyDD, weeklyDD := rm.navTracker.GetDrawdowns()
	
	// Get circuit breaker state
	circuitState, sizeMultiplier := rm.circuitBreaker.GetState()
	
	// Get volatility regime
	volatilityRegime := rm.volatilityCalc.GetVolatilityRegime()
	
	// Get system health
	systemHealth := rm.observabilityMgr.GetSystemHealth()
	componentHealth := make(map[string]ComponentHealth)
	if details, ok := systemHealth["component_details"].(map[string]ComponentHealth); ok {
		componentHealth = details
	}
	
	return RiskData{
		CurrentNAV:       nav,
		DailyDrawdown:    dailyDD,
		WeeklyDrawdown:   weeklyDD,
		CircuitState:     circuitState,
		SizeMultiplier:   sizeMultiplier,
		VolatilityRegime: volatilityRegime,
		DataQuality:      dataQuality,
		ComponentHealth:  componentHealth,
		QuoteStaleness:   time.Since(lastUpdate),
		LastUpdate:       lastUpdate,
	}
}

func (rm *RiskManager) calculateRiskScore(ctx DecisionContext, riskData RiskData) float64 {
	// Composite risk score from multiple factors
	score := 0.0
	
	// Circuit breaker contribution (0-1, higher = more risky)
	switch riskData.CircuitState {
	case StateNormal:
		score += 0.0
	case StateWarning:
		score += 0.2
	case StateReduced:
		score += 0.4
	case StateRestricted:
		score += 0.6
	case StateMinimal:
		score += 0.8
	case StateHalted, StateCoolingOff, StateEmergency:
		score += 1.0
	}
	
	// Drawdown contribution
	drawdownRisk := (riskData.DailyDrawdown + riskData.WeeklyDrawdown*0.5) / 10.0 // Normalize
	if drawdownRisk > 1.0 {
		drawdownRisk = 1.0
	}
	score += drawdownRisk
	
	// Data quality contribution (inverted)
	qualityScore := calculateDataQualityScore(riskData.DataQuality)
	score += (1.0 - qualityScore)
	
	// Volatility contribution
	switch riskData.VolatilityRegime {
	case "quiet":
		score += 0.1
	case "normal":
		score += 0.3
	case "volatile":
		score += 0.7
	}
	
	// Quote staleness contribution
	if riskData.QuoteStaleness > 5*time.Second {
		score += 0.5
	} else if riskData.QuoteStaleness > 2*time.Second {
		score += 0.2
	}
	
	// Normalize to 0-1 range
	if score > 1.0 {
		score = 1.0
	}
	
	return score
}

func (rm *RiskManager) monitoringLoop() {
	ticker := time.NewTicker(time.Duration(rm.config.UpdateIntervalSeconds) * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-rm.ctx.Done():
			return
		case <-ticker.C:
			rm.updateRiskState()
		}
	}
}

func (rm *RiskManager) updateRiskState() {
	// Get current state
	riskData := rm.getCurrentRiskData()
	
	// Update circuit breaker with latest drawdown data
	correlationID := fmt.Sprintf("monitoring_%d", time.Now().UnixNano())
	rm.circuitBreaker.UpdateDrawdown(
		riskData.DailyDrawdown,
		riskData.WeeklyDrawdown,
		rm.navTracker,
		correlationID,
	)
	
	// Update volatility calculations
	nav, _, _ := rm.navTracker.GetCurrentNAV()
	if prevData, exists := rm.getPreviousRiskData(); exists {
		rm.volatilityCalc.UpdateNAVReturn(prevData.CurrentNAV, nav, time.Now())
	}
	
	// Track observability metrics
	rm.observabilityMgr.TrackNAVUpdate(
		time.Duration(100)*time.Millisecond, // Would track actual latency
		riskData.DataQuality,
		riskData.QuoteStaleness,
		correlationID,
	)
}

func (rm *RiskManager) healthMonitoringLoop() {
	ticker := time.NewTicker(30 * time.Second) // Health check every 30 seconds
	defer ticker.Stop()
	
	for {
		select {
		case <-rm.ctx.Done():
			return
		case <-ticker.C:
			rm.checkComponentHealth()
		}
	}
}

func (rm *RiskManager) checkComponentHealth() {
	components := []struct {
		name   string
		health func() (string, time.Duration, int64, map[string]interface{})
	}{
		{
			name: "nav_tracker",
			health: func() (string, time.Duration, int64, map[string]interface{}) {
				frozen, reason := rm.navTracker.IsFrozen()
				status := "healthy"
				details := map[string]interface{}{}
				
				if frozen {
					status = "degraded"
					details["frozen_reason"] = reason
				}
				
				return status, time.Duration(50) * time.Millisecond, 0, details
			},
		},
		{
			name: "circuit_breaker",
			health: func() (string, time.Duration, int64, map[string]interface{}) {
				state, _ := rm.circuitBreaker.GetState()
				status := "healthy"
				details := map[string]interface{}{"state": string(state)}
				
				if state == StateEmergency {
					status = "unhealthy"
				} else if state == StateHalted || state == StateCoolingOff {
					status = "degraded"
				}
				
				return status, time.Duration(10) * time.Millisecond, 0, details
			},
		},
	}
	
	for _, comp := range components {
		status, responseTime, errorCount, details := comp.health()
		rm.observabilityMgr.TrackComponentHealth(
			comp.name,
			status,
			responseTime,
			errorCount,
			details,
		)
	}
}

func (rm *RiskManager) getPreviousRiskData() (RiskData, bool) {
	// In a real implementation, this would maintain a small history
	// For now, return empty
	return RiskData{}, false
}

// Helper functions

func calculateDataQualityScore(quality NAVDataQuality) float64 {
	totalSymbols := len(quality.StaleQuotes) + len(quality.MissingQuotes) + 
				   len(quality.UsingMidPrice) + len(quality.UsingLastTrade)
	if totalSymbols == 0 {
		return 1.0
	}
	
	missingPenalty := float64(len(quality.MissingQuotes)) * 0.8
	stalePenalty := float64(len(quality.StaleQuotes)) * 0.3
	
	score := 1.0 - (missingPenalty+stalePenalty)/float64(totalSymbols)
	if score < 0 {
		score = 0
	}
	return score
}

func parseVolatilityMultiplier(reason string) float64 {
	// Parse "volatility_adjustment_0.70" format
	var multiplier float64
	if n, _ := fmt.Sscanf(reason, "volatility_adjustment_%f", &multiplier); n == 1 {
		return multiplier
	}
	return 1.0
}