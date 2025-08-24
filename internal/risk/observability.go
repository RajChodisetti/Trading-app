package risk

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// RiskObservabilityManager provides comprehensive monitoring for the risk management system
type RiskObservabilityManager struct {
	mu sync.RWMutex
	
	// Structured event logging
	eventLogger *StructuredEventLogger
	
	// Real-time metrics
	metrics *RiskMetrics
	
	// Alerting thresholds
	alertThresholds AlertThresholds
	
	// Performance tracking
	performanceTracker *PerformanceTracker
	
	// Health monitoring
	healthMonitor *HealthMonitor
}

// StructuredEventLogger logs structured events for post-mortem analysis
type StructuredEventLogger struct {
	logPath   string
	buffer    []StructuredEvent
	maxBuffer int
	mu        sync.RWMutex
}

// StructuredEvent represents a structured risk event for monitoring
type StructuredEvent struct {
	EventID       string                 `json:"event_id"`
	Timestamp     time.Time              `json:"timestamp"`
	Type          string                 `json:"type"`
	Severity      string                 `json:"severity"`
	Component     string                 `json:"component"`
	Message       string                 `json:"message"`
	Context       map[string]interface{} `json:"context"`
	Metrics       map[string]float64     `json:"metrics"`
	CorrelationID string                 `json:"correlation_id"`
	Duration      time.Duration          `json:"duration,omitempty"`
	ErrorCode     string                 `json:"error_code,omitempty"`
	UserID        string                 `json:"user_id,omitempty"`
}

// RiskMetrics tracks comprehensive risk system metrics
type RiskMetrics struct {
	// NAV tracking metrics
	NAVUpdateLatency      *LatencyMetric `json:"nav_update_latency"`
	NAVDataQualityScore   float64        `json:"nav_data_quality_score"`
	NAVFreezeEvents       int64          `json:"nav_freeze_events"`
	QuoteStalenessSeconds float64        `json:"quote_staleness_seconds"`
	
	// Circuit breaker metrics
	CircuitBreakerState       float64        `json:"circuit_breaker_state"`
	StateTransitions          int64          `json:"state_transitions"`
	TimeInCurrentState        time.Duration  `json:"time_in_current_state"`
	DrawdownSlope             float64        `json:"drawdown_slope"` // % per minute
	ThresholdBreaches         int64          `json:"threshold_breaches"`
	ManualOverrides           int64          `json:"manual_overrides"`
	AutoRecoveries           int64          `json:"auto_recoveries"`
	
	// Performance metrics
	DecisionLatency          *LatencyMetric `json:"decision_latency"`
	OrderSuppressions        int64          `json:"order_suppressions"`
	AlertLatency             *LatencyMetric `json:"alert_latency"`
	ApprovalLatency          *LatencyMetric `json:"approval_latency"`
	
	// System health
	ComponentHealth          map[string]float64 `json:"component_health"`
	ErrorRates               map[string]float64 `json:"error_rates"`
	ThroughputMetrics        map[string]float64 `json:"throughput_metrics"`
}

// LatencyMetric tracks latency distribution
type LatencyMetric struct {
	P50   time.Duration `json:"p50"`
	P90   time.Duration `json:"p90"`
	P95   time.Duration `json:"p95"`
	P99   time.Duration `json:"p99"`
	Max   time.Duration `json:"max"`
	Count int64         `json:"count"`
}

// AlertThresholds defines when to trigger operational alerts
type AlertThresholds struct {
	NAVUpdateLatencyMs       float64 `json:"nav_update_latency_ms"`       // Alert if > 500ms
	QuoteStalenessSeconds    float64 `json:"quote_staleness_seconds"`     // Alert if > 5s
	DrawdownSlopePerMinute   float64 `json:"drawdown_slope_per_minute"`   // Alert if > 0.5%/min
	DecisionLatencyMs        float64 `json:"decision_latency_ms"`         // Alert if > 100ms
	AlertLatencyMs           float64 `json:"alert_latency_ms"`            // Alert if > 5000ms
	DataQualityScore         float64 `json:"data_quality_score"`          // Alert if < 0.8
	ComponentHealthScore     float64 `json:"component_health_score"`      // Alert if < 0.9
	ErrorRatePercent         float64 `json:"error_rate_percent"`          // Alert if > 1%
}

// PerformanceTracker tracks system performance and identifies bottlenecks
type PerformanceTracker struct {
	mu sync.RWMutex
	
	requestTimes map[string][]time.Duration // Component -> response times
	maxSamples   int
	
	// Resource utilization
	cpuUsage    float64
	memoryUsage float64
	goroutines  int
}

// HealthMonitor monitors component health and system vitals
type HealthMonitor struct {
	mu sync.RWMutex
	
	componentStatus map[string]ComponentHealth
	lastHealthCheck time.Time
	healthyThreshold time.Duration
}

// ComponentHealth represents the health status of a system component
type ComponentHealth struct {
	Status       string                 `json:"status"`        // healthy, degraded, unhealthy
	LastCheck    time.Time              `json:"last_check"`
	ResponseTime time.Duration          `json:"response_time"`
	ErrorCount   int64                  `json:"error_count"`
	Details      map[string]interface{} `json:"details"`
	Uptime       time.Duration          `json:"uptime"`
}

// Event type constants
const (
	EventNAVUpdate              = "nav_update"
	EventDrawdownThresholdHit   = "drawdown_threshold_hit"
	EventCircuitBreakerTriggered = "circuit_breaker_triggered"
	EventManualIntervention     = "manual_intervention"
	EventSystemError            = "system_error"
	EventPerformanceDegradation = "performance_degradation"
	EventDataQualityIssue       = "data_quality_issue"
	EventRecoveryCompleted      = "recovery_completed"
)

// Severity constants
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
	SeverityError    = "error"
)

// NewRiskObservabilityManager creates a new observability manager
func NewRiskObservabilityManager(logPath string) *RiskObservabilityManager {
	return &RiskObservabilityManager{
		eventLogger: &StructuredEventLogger{
			logPath:   logPath,
			buffer:    make([]StructuredEvent, 0),
			maxBuffer: 1000,
		},
		metrics: &RiskMetrics{
			NAVUpdateLatency:  &LatencyMetric{},
			DecisionLatency:   &LatencyMetric{},
			AlertLatency:      &LatencyMetric{},
			ApprovalLatency:   &LatencyMetric{},
			ComponentHealth:   make(map[string]float64),
			ErrorRates:        make(map[string]float64),
			ThroughputMetrics: make(map[string]float64),
		},
		alertThresholds: AlertThresholds{
			NAVUpdateLatencyMs:     500,
			QuoteStalenessSeconds:  5,
			DrawdownSlopePerMinute: 0.5,
			DecisionLatencyMs:      100,
			AlertLatencyMs:         5000,
			DataQualityScore:       0.8,
			ComponentHealthScore:   0.9,
			ErrorRatePercent:       1.0,
		},
		performanceTracker: &PerformanceTracker{
			requestTimes: make(map[string][]time.Duration),
			maxSamples:   1000,
		},
		healthMonitor: &HealthMonitor{
			componentStatus:  make(map[string]ComponentHealth),
			healthyThreshold: 30 * time.Second,
		},
	}
}

// LogStructuredEvent logs a structured event with full context
func (rom *RiskObservabilityManager) LogStructuredEvent(
	eventType, severity, component, message string,
	context map[string]interface{},
	metrics map[string]float64,
	correlationID string,
) {
	event := StructuredEvent{
		EventID:       fmt.Sprintf("%s_%d", eventType, time.Now().UnixNano()),
		Timestamp:     time.Now(),
		Type:          eventType,
		Severity:      severity,
		Component:     component,
		Message:       message,
		Context:       context,
		Metrics:       metrics,
		CorrelationID: correlationID,
	}
	
	rom.eventLogger.LogEvent(event)
	
	// Update observability metrics
	observ.IncCounter("risk_events_total", map[string]string{
		"event_type": eventType,
		"severity":   severity,
		"component":  component,
	})
	
	// Check alert thresholds
	rom.checkAlertThresholds(event)
}

// TrackNAVUpdate tracks NAV update performance and quality
func (rom *RiskObservabilityManager) TrackNAVUpdate(
	latency time.Duration,
	dataQuality NAVDataQuality,
	staleness time.Duration,
	correlationID string,
) {
	// Update latency metrics
	rom.updateLatencyMetric(rom.metrics.NAVUpdateLatency, latency)
	
	// Calculate data quality score
	qualityScore := rom.calculateDataQualityScore(dataQuality)
	rom.metrics.NAVDataQualityScore = qualityScore
	rom.metrics.QuoteStalenessSeconds = staleness.Seconds()
	
	// Log structured event
	context := map[string]interface{}{
		"stale_quotes":    len(dataQuality.StaleQuotes),
		"missing_quotes":  len(dataQuality.MissingQuotes),
		"using_mid_price": len(dataQuality.UsingMidPrice),
		"staleness_max":   staleness.Seconds(),
	}
	
	metrics := map[string]float64{
		"latency_ms":       float64(latency.Milliseconds()),
		"quality_score":    qualityScore,
		"staleness_seconds": staleness.Seconds(),
	}
	
	severity := SeverityInfo
	if latency > 500*time.Millisecond {
		severity = SeverityWarning
	}
	if staleness > 5*time.Second {
		severity = SeverityCritical
	}
	
	rom.LogStructuredEvent(
		EventNAVUpdate,
		severity,
		"nav_tracker",
		"NAV update completed",
		context,
		metrics,
		correlationID,
	)
	
	// Update Prometheus metrics
	observ.Observe("nav_update_latency_ms", float64(latency.Milliseconds()), nil)
	observ.SetGauge("nav_data_quality_score", qualityScore, nil)
	observ.SetGauge("nav_quote_staleness_seconds", staleness.Seconds(), nil)
}

// TrackCircuitBreakerEvent tracks circuit breaker state changes
func (rom *RiskObservabilityManager) TrackCircuitBreakerEvent(
	previousState, newState CircuitBreakerState,
	dailyDD, weeklyDD float64,
	reason string,
	responseTime time.Duration,
	correlationID string,
) {
	rom.metrics.CircuitBreakerState = rom.stateToFloat(newState)
	rom.metrics.StateTransitions++
	rom.metrics.TimeInCurrentState = 0 // Reset time in state
	
	// Calculate drawdown slope (% per minute)
	drawdownSlope := rom.calculateDrawdownSlope(dailyDD, weeklyDD)
	rom.metrics.DrawdownSlope = drawdownSlope
	
	context := map[string]interface{}{
		"previous_state": string(previousState),
		"new_state":      string(newState),
		"daily_drawdown": dailyDD,
		"weekly_drawdown": weeklyDD,
		"reason":         reason,
		"response_time":  responseTime.Milliseconds(),
	}
	
	metrics := map[string]float64{
		"daily_drawdown_pct":  dailyDD,
		"weekly_drawdown_pct": weeklyDD,
		"drawdown_slope":      drawdownSlope,
		"response_time_ms":    float64(responseTime.Milliseconds()),
		"state_numeric":       rom.stateToFloat(newState),
	}
	
	severity := SeverityWarning
	if newState == StateHalted || newState == StateEmergency {
		severity = SeverityCritical
	}
	
	rom.LogStructuredEvent(
		EventCircuitBreakerTriggered,
		severity,
		"circuit_breaker",
		fmt.Sprintf("Circuit breaker transitioned to %s", newState),
		context,
		metrics,
		correlationID,
	)
	
	// Update Prometheus metrics
	observ.SetGauge("circuit_breaker_state", rom.stateToFloat(newState), nil)
	observ.SetGauge("drawdown_slope_pct_per_minute", drawdownSlope, nil)
	observ.Observe("circuit_breaker_response_time_ms", float64(responseTime.Milliseconds()), nil)
}

// TrackDecisionLatency tracks decision engine performance
func (rom *RiskObservabilityManager) TrackDecisionLatency(
	latency time.Duration,
	ordersSuppressed int,
	correlationID string,
) {
	rom.updateLatencyMetric(rom.metrics.DecisionLatency, latency)
	rom.metrics.OrderSuppressions += int64(ordersSuppressed)
	
	severity := SeverityInfo
	if latency > 100*time.Millisecond {
		severity = SeverityWarning
	}
	
	context := map[string]interface{}{
		"orders_suppressed": ordersSuppressed,
		"latency_ms":        latency.Milliseconds(),
	}
	
	metrics := map[string]float64{
		"latency_ms":        float64(latency.Milliseconds()),
		"orders_suppressed": float64(ordersSuppressed),
	}
	
	rom.LogStructuredEvent(
		"decision_processing",
		severity,
		"decision_engine",
		"Decision processing completed",
		context,
		metrics,
		correlationID,
	)
	
	observ.Observe("decision_processing_latency_ms", float64(latency.Milliseconds()), nil)
	observ.IncCounter("orders_suppressed_total", map[string]string{"count": fmt.Sprintf("%d", ordersSuppressed)})
}

// TrackAlertLatency tracks alerting system performance
func (rom *RiskObservabilityManager) TrackAlertLatency(
	alertType string,
	latency time.Duration,
	success bool,
	correlationID string,
) {
	rom.updateLatencyMetric(rom.metrics.AlertLatency, latency)
	
	severity := SeverityInfo
	if !success {
		severity = SeverityError
	} else if latency > 5*time.Second {
		severity = SeverityWarning
	}
	
	context := map[string]interface{}{
		"alert_type": alertType,
		"success":    success,
		"latency_ms": latency.Milliseconds(),
	}
	
	metrics := map[string]float64{
		"latency_ms": float64(latency.Milliseconds()),
	}
	
	rom.LogStructuredEvent(
		"alert_sent",
		severity,
		"alerting",
		fmt.Sprintf("Alert %s processed", alertType),
		context,
		metrics,
		correlationID,
	)
	
	observ.Observe("alert_latency_ms", float64(latency.Milliseconds()), map[string]string{
		"alert_type": alertType,
		"success":    fmt.Sprintf("%t", success),
	})
}

// TrackComponentHealth updates component health status
func (rom *RiskObservabilityManager) TrackComponentHealth(
	component string,
	status string,
	responseTime time.Duration,
	errorCount int64,
	details map[string]interface{},
) {
	rom.healthMonitor.mu.Lock()
	defer rom.healthMonitor.mu.Unlock()
	
	health := ComponentHealth{
		Status:       status,
		LastCheck:    time.Now(),
		ResponseTime: responseTime,
		ErrorCount:   errorCount,
		Details:      details,
	}
	
	// Calculate uptime based on previous status
	if prevHealth, exists := rom.healthMonitor.componentStatus[component]; exists {
		if prevHealth.Status == "healthy" && status == "healthy" {
			health.Uptime = prevHealth.Uptime + time.Since(prevHealth.LastCheck)
		} else {
			health.Uptime = 0 // Reset uptime on status change
		}
	}
	
	rom.healthMonitor.componentStatus[component] = health
	
	// Update health score
	healthScore := rom.calculateHealthScore(status)
	rom.metrics.ComponentHealth[component] = healthScore
	
	// Log health event if degraded
	if status != "healthy" {
		severity := SeverityWarning
		if status == "unhealthy" {
			severity = SeverityCritical
		}
		
		context := map[string]interface{}{
			"component":      component,
			"status":         status,
			"response_time":  responseTime.Milliseconds(),
			"error_count":    errorCount,
			"details":        details,
		}
		
		metrics := map[string]float64{
			"response_time_ms": float64(responseTime.Milliseconds()),
			"error_count":      float64(errorCount),
			"health_score":     healthScore,
		}
		
		rom.LogStructuredEvent(
			"component_health_degraded",
			severity,
			component,
			fmt.Sprintf("Component %s is %s", component, status),
			context,
			metrics,
			"",
		)
	}
	
	// Update Prometheus metrics
	observ.SetGauge("component_health_score", healthScore, map[string]string{
		"component": component,
	})
}

// GetRiskMetrics returns current risk system metrics
func (rom *RiskObservabilityManager) GetRiskMetrics() RiskMetrics {
	rom.mu.RLock()
	defer rom.mu.RUnlock()
	return *rom.metrics
}

// GetRecentEvents returns recent structured events for analysis
func (rom *RiskObservabilityManager) GetRecentEvents(maxEvents int, eventTypes []string) []StructuredEvent {
	return rom.eventLogger.GetRecentEvents(maxEvents, eventTypes)
}

// GetSystemHealth returns overall system health status
func (rom *RiskObservabilityManager) GetSystemHealth() map[string]interface{} {
	rom.healthMonitor.mu.RLock()
	defer rom.healthMonitor.mu.RUnlock()
	
	overallHealth := "healthy"
	unhealthyComponents := 0
	degradedComponents := 0
	
	for _, health := range rom.healthMonitor.componentStatus {
		switch health.Status {
		case "unhealthy":
			unhealthyComponents++
			overallHealth = "unhealthy"
		case "degraded":
			degradedComponents++
			if overallHealth == "healthy" {
				overallHealth = "degraded"
			}
		}
	}
	
	return map[string]interface{}{
		"overall_status":        overallHealth,
		"total_components":      len(rom.healthMonitor.componentStatus),
		"healthy_components":    len(rom.healthMonitor.componentStatus) - unhealthyComponents - degradedComponents,
		"degraded_components":   degradedComponents,
		"unhealthy_components":  unhealthyComponents,
		"last_health_check":     rom.healthMonitor.lastHealthCheck,
		"component_details":     rom.healthMonitor.componentStatus,
	}
}

// Helper methods

func (rom *RiskObservabilityManager) updateLatencyMetric(metric *LatencyMetric, latency time.Duration) {
	// This is a simplified implementation - in production, you'd use a proper
	// quantile tracker like t-digest or HDRHistogram
	metric.Count++
	if latency > metric.Max {
		metric.Max = latency
	}
	
	// Simple approximation for percentiles
	metric.P50 = latency // Simplified
	metric.P90 = latency
	metric.P95 = latency
	metric.P99 = latency
}

func (rom *RiskObservabilityManager) calculateDataQualityScore(quality NAVDataQuality) float64 {
	totalSymbols := len(quality.StaleQuotes) + len(quality.MissingQuotes) + 
				   len(quality.UsingMidPrice) + len(quality.UsingLastTrade)
	if totalSymbols == 0 {
		return 1.0
	}
	
	// Penalty for missing and stale quotes
	missingPenalty := float64(len(quality.MissingQuotes)) * 0.8
	stalePenalty := float64(len(quality.StaleQuotes)) * 0.3
	
	score := 1.0 - (missingPenalty+stalePenalty)/float64(totalSymbols)
	if score < 0 {
		score = 0
	}
	return score
}

func (rom *RiskObservabilityManager) calculateDrawdownSlope(dailyDD, weeklyDD float64) float64 {
	// Simplified slope calculation - would need historical data for accurate slope
	// For now, use daily drawdown as proxy for recent rate
	return dailyDD // Represents recent drawdown rate
}

func (rom *RiskObservabilityManager) stateToFloat(state CircuitBreakerState) float64 {
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

func (rom *RiskObservabilityManager) calculateHealthScore(status string) float64 {
	switch status {
	case "healthy":
		return 1.0
	case "degraded":
		return 0.5
	case "unhealthy":
		return 0.0
	default:
		return 0.0
	}
}

func (rom *RiskObservabilityManager) checkAlertThresholds(event StructuredEvent) {
	// Check various alert thresholds and trigger operational alerts
	if event.Type == EventNAVUpdate {
		if latencyMs, ok := event.Metrics["latency_ms"]; ok {
			if latencyMs > rom.alertThresholds.NAVUpdateLatencyMs {
				rom.triggerOperationalAlert("nav_latency_high", event)
			}
		}
		
		if qualityScore, ok := event.Metrics["quality_score"]; ok {
			if qualityScore < rom.alertThresholds.DataQualityScore {
				rom.triggerOperationalAlert("data_quality_low", event)
			}
		}
	}
	
	if event.Type == EventCircuitBreakerTriggered {
		if drawdownSlope, ok := event.Metrics["drawdown_slope"]; ok {
			if drawdownSlope > rom.alertThresholds.DrawdownSlopePerMinute {
				rom.triggerOperationalAlert("drawdown_acceleration", event)
			}
		}
	}
}

func (rom *RiskObservabilityManager) triggerOperationalAlert(alertType string, event StructuredEvent) {
	// This would integrate with the alerting system (Slack, PagerDuty, etc.)
	observ.IncCounter("operational_alerts_total", map[string]string{
		"alert_type":  alertType,
		"event_type":  event.Type,
		"severity":    event.Severity,
		"component":   event.Component,
	})
}

// StructuredEventLogger methods

func (sel *StructuredEventLogger) LogEvent(event StructuredEvent) {
	sel.mu.Lock()
	defer sel.mu.Unlock()
	
	// Add to buffer
	sel.buffer = append(sel.buffer, event)
	
	// Trim buffer if too large
	if len(sel.buffer) > sel.maxBuffer {
		sel.buffer = sel.buffer[len(sel.buffer)-sel.maxBuffer:]
	}
	
	// Write to file asynchronously
	go sel.persistEvent(event)
}

func (sel *StructuredEventLogger) persistEvent(event StructuredEvent) {
	// Create directory if needed
	if err := os.MkdirAll("data/risk_events", 0755); err != nil {
		return
	}
	
	// Open log file
	file, err := os.OpenFile(sel.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer file.Close()
	
	// Serialize and write event
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return
	}
	
	fmt.Fprintf(file, "%s\n", eventJSON)
}

func (sel *StructuredEventLogger) GetRecentEvents(maxEvents int, eventTypes []string) []StructuredEvent {
	sel.mu.RLock()
	defer sel.mu.RUnlock()
	
	// Filter by event types if specified
	filtered := sel.buffer
	if len(eventTypes) > 0 {
		typeMap := make(map[string]bool)
		for _, t := range eventTypes {
			typeMap[t] = true
		}
		
		filtered = make([]StructuredEvent, 0)
		for _, event := range sel.buffer {
			if typeMap[event.Type] {
				filtered = append(filtered, event)
			}
		}
	}
	
	// Return most recent events
	if maxEvents <= 0 || maxEvents >= len(filtered) {
		result := make([]StructuredEvent, len(filtered))
		copy(result, filtered)
		return result
	}
	
	start := len(filtered) - maxEvents
	result := make([]StructuredEvent, maxEvents)
	copy(result, filtered[start:])
	return result
}