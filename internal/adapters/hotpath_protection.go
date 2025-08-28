package adapters

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// HotpathGuard enforces invariants and safety rails for the critical trading path
type HotpathGuard struct {
	mu                    sync.RWMutex
	enabled               bool
	config                HotpathConfig
	
	// Invariant tracking
	liveCalls             int64         // Atomic counter for live provider calls
	decisionLatency       []int64       // Recent decision latencies (circular buffer)
	latencyIndex          int           // Current position in latency buffer
	
	// Safety rails
	emergencyStop         bool          // Emergency stop flag
	budgetExhausted       bool          // Budget exhaustion flag
	providerDegraded      map[string]bool // Provider degradation status
	
	// Rate limiting
	callsThisSecond       int64         // Calls in current second
	currentSecond         int64         // Current second timestamp
	
	// Quality gates
	successRate           *RingBuffer   // Success rate tracking
	cacheHitRate          *RingBuffer   // Cache hit rate tracking
	
	// Breach tracking
	consecutiveBreaches   int           // Consecutive invariant breaches
	lastBreach            time.Time     // Last breach timestamp
	breachCooldown        time.Duration // Cooldown after breach
	
	// Metrics
	totalChecks           int64         // Total invariant checks
	totalViolations       int64         // Total violations
	emergencyStops        int64         // Emergency stop count
	
	// Callbacks
	onViolation          func(violation HotpathViolation) // Violation callback
	onEmergencyStop      func(reason string)             // Emergency stop callback
}

// HotpathConfig holds configuration for hotpath protection
type HotpathConfig struct {
	// Core invariants
	MaxLiveCallsPerSecond     int           `yaml:"max_live_calls_per_second"`     // Max live provider calls/sec
	MaxDecisionLatencyMs      int64         `yaml:"max_decision_latency_ms"`       // Max decision latency
	MinCacheHitRate           float64       `yaml:"min_cache_hit_rate"`            // Minimum cache hit rate
	MinSuccessRate            float64       `yaml:"min_success_rate"`              // Minimum provider success rate
	
	// Safety thresholds
	MaxConsecutiveBreaches    int           `yaml:"max_consecutive_breaches"`      // Max breaches before emergency stop
	BreachCooldownSeconds     int           `yaml:"breach_cooldown_seconds"`       // Cooldown after breach
	EmergencyStopDurationMin  int           `yaml:"emergency_stop_duration_min"`   // Emergency stop duration
	
	// Quality monitoring
	LatencyBufferSize         int           `yaml:"latency_buffer_size"`           // Latency tracking buffer size
	QualityWindowSeconds      int           `yaml:"quality_window_seconds"`        // Quality tracking window
	
	// Budget protection
	BudgetWarningThreshold    float64       `yaml:"budget_warning_threshold"`      // Budget warning threshold
	BudgetCriticalThreshold   float64       `yaml:"budget_critical_threshold"`     // Budget critical threshold
	
	// Fail-safe settings
	DefaultToMockOnViolation  bool          `yaml:"default_to_mock_on_violation"`  // Use mock on violation
	StrictMode                bool          `yaml:"strict_mode"`                   // Strict invariant enforcement
	AllowGracefulDegradation  bool          `yaml:"allow_graceful_degradation"`    // Allow gradual degradation
}

// HotpathViolation represents a violation of hotpath invariants
type HotpathViolation struct {
	Type        ViolationType `json:"type"`
	Message     string        `json:"message"`
	Value       float64       `json:"value"`
	Threshold   float64       `json:"threshold"`
	Severity    Severity      `json:"severity"`
	Timestamp   time.Time     `json:"timestamp"`
	Context     map[string]interface{} `json:"context"`
}

// ViolationType defines types of hotpath violations
type ViolationType string

const (
	ViolationLiveCalls      ViolationType = "live_calls"       // Excessive live calls
	ViolationLatency        ViolationType = "latency"          // High decision latency
	ViolationCacheHitRate   ViolationType = "cache_hit_rate"   // Low cache hit rate
	ViolationSuccessRate    ViolationType = "success_rate"     // Low provider success rate
	ViolationBudgetExhausted ViolationType = "budget_exhausted" // Budget exhausted
	ViolationProviderHealth ViolationType = "provider_health"  // Provider health degraded
	ViolationConsecutiveBreach ViolationType = "consecutive_breach" // Too many breaches
)

// Severity levels for violations
type Severity string

const (
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
	SeverityFatal    Severity = "fatal"
)

// RingBuffer implements a circular buffer for quality metrics
type RingBuffer struct {
	data    []float64
	size    int
	index   int
	count   int
	mu      sync.Mutex
}

// NewRingBuffer creates a new ring buffer
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		data: make([]float64, size),
		size: size,
	}
}

// Add adds a value to the ring buffer
func (rb *RingBuffer) Add(value float64) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	
	rb.data[rb.index] = value
	rb.index = (rb.index + 1) % rb.size
	if rb.count < rb.size {
		rb.count++
	}
}

// Average returns the average of values in the buffer
func (rb *RingBuffer) Average() float64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	
	if rb.count == 0 {
		return 0
	}
	
	sum := 0.0
	for i := 0; i < rb.count; i++ {
		sum += rb.data[i]
	}
	
	return sum / float64(rb.count)
}

// NewHotpathGuard creates a new hotpath guard
func NewHotpathGuard(config HotpathConfig) *HotpathGuard {
	guard := &HotpathGuard{
		enabled:             true,
		config:             config,
		providerDegraded:   make(map[string]bool),
		decisionLatency:    make([]int64, config.LatencyBufferSize),
		breachCooldown:     time.Duration(config.BreachCooldownSeconds) * time.Second,
		successRate:        NewRingBuffer(60), // 60-second window
		cacheHitRate:       NewRingBuffer(60), // 60-second window
	}
	
	// Set default callback handlers
	guard.onViolation = guard.defaultViolationHandler
	guard.onEmergencyStop = guard.defaultEmergencyStopHandler
	
	return guard
}

// CheckPreRequest validates invariants before making a provider request
func (hg *HotpathGuard) CheckPreRequest(ctx context.Context, provider string, symbol string, isLive bool) error {
	if !hg.enabled {
		return nil
	}
	
	atomic.AddInt64(&hg.totalChecks, 1)
	
	// Check if we're in emergency stop
	if hg.isEmergencyStop() {
		return fmt.Errorf("system in emergency stop mode")
	}
	
	// Check rate limits
	if err := hg.checkRateLimit(isLive); err != nil {
		violation := HotpathViolation{
			Type:      ViolationLiveCalls,
			Message:   err.Error(),
			Severity:  SeverityCritical,
			Timestamp: time.Now(),
		}
		hg.handleViolation(violation)
		return err
	}
	
	// Check provider health
	if hg.isProviderDegraded(provider) {
		violation := HotpathViolation{
			Type:      ViolationProviderHealth,
			Message:   fmt.Sprintf("provider %s is degraded", provider),
			Severity:  SeverityWarning,
			Timestamp: time.Now(),
			Context:   map[string]interface{}{"provider": provider},
		}
		hg.handleViolation(violation)
		
		if hg.config.StrictMode {
			return fmt.Errorf("provider %s is degraded", provider)
		}
	}
	
	// Check budget status
	if hg.budgetExhausted {
		violation := HotpathViolation{
			Type:      ViolationBudgetExhausted,
			Message:   "budget exhausted",
			Severity:  SeverityFatal,
			Timestamp: time.Now(),
		}
		hg.handleViolation(violation)
		return fmt.Errorf("budget exhausted")
	}
	
	// Track live calls
	if isLive {
		atomic.AddInt64(&hg.liveCalls, 1)
	}
	
	return nil
}

// CheckPostRequest validates invariants after making a provider request
func (hg *HotpathGuard) CheckPostRequest(ctx context.Context, provider string, latencyMs int64, success bool, fromCache bool) error {
	if !hg.enabled {
		return nil
	}
	
	// Record decision latency
	hg.recordDecisionLatency(latencyMs)
	
	// Record success rate
	var successValue float64
	if success {
		successValue = 1.0
	}
	hg.successRate.Add(successValue)
	
	// Record cache hit rate
	var cacheValue float64
	if fromCache {
		cacheValue = 1.0
	}
	hg.cacheHitRate.Add(cacheValue)
	
	// Check latency invariant
	if latencyMs > hg.config.MaxDecisionLatencyMs {
		violation := HotpathViolation{
			Type:      ViolationLatency,
			Message:   fmt.Sprintf("decision latency %dms exceeds limit %dms", latencyMs, hg.config.MaxDecisionLatencyMs),
			Value:     float64(latencyMs),
			Threshold: float64(hg.config.MaxDecisionLatencyMs),
			Severity:  SeverityWarning,
			Timestamp: time.Now(),
			Context:   map[string]interface{}{"provider": provider},
		}
		hg.handleViolation(violation)
	}
	
	// Check success rate invariant
	currentSuccessRate := hg.successRate.Average()
	if currentSuccessRate < hg.config.MinSuccessRate {
		violation := HotpathViolation{
			Type:      ViolationSuccessRate,
			Message:   fmt.Sprintf("success rate %.2f%% below threshold %.2f%%", currentSuccessRate*100, hg.config.MinSuccessRate*100),
			Value:     currentSuccessRate,
			Threshold: hg.config.MinSuccessRate,
			Severity:  SeverityCritical,
			Timestamp: time.Now(),
		}
		hg.handleViolation(violation)
	}
	
	// Check cache hit rate invariant
	currentCacheHitRate := hg.cacheHitRate.Average()
	if currentCacheHitRate < hg.config.MinCacheHitRate {
		violation := HotpathViolation{
			Type:      ViolationCacheHitRate,
			Message:   fmt.Sprintf("cache hit rate %.2f%% below threshold %.2f%%", currentCacheHitRate*100, hg.config.MinCacheHitRate*100),
			Value:     currentCacheHitRate,
			Threshold: hg.config.MinCacheHitRate,
			Severity:  SeverityWarning,
			Timestamp: time.Now(),
		}
		hg.handleViolation(violation)
	}
	
	return nil
}

// checkRateLimit checks if rate limits are being respected
func (hg *HotpathGuard) checkRateLimit(isLive bool) error {
	if !isLive {
		return nil // No rate limit for cache/mock calls
	}
	
	now := time.Now().Unix()
	
	// Reset counter if we've moved to a new second
	if atomic.LoadInt64(&hg.currentSecond) != now {
		atomic.StoreInt64(&hg.currentSecond, now)
		atomic.StoreInt64(&hg.callsThisSecond, 0)
	}
	
	calls := atomic.AddInt64(&hg.callsThisSecond, 1)
	if calls > int64(hg.config.MaxLiveCallsPerSecond) {
		return fmt.Errorf("rate limit exceeded: %d calls/sec > %d limit", calls, hg.config.MaxLiveCallsPerSecond)
	}
	
	return nil
}

// recordDecisionLatency records decision latency in circular buffer
func (hg *HotpathGuard) recordDecisionLatency(latencyMs int64) {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	
	hg.decisionLatency[hg.latencyIndex] = latencyMs
	hg.latencyIndex = (hg.latencyIndex + 1) % len(hg.decisionLatency)
}

// handleViolation handles a hotpath violation
func (hg *HotpathGuard) handleViolation(violation HotpathViolation) {
	atomic.AddInt64(&hg.totalViolations, 1)
	
	hg.mu.Lock()
	defer hg.mu.Unlock()
	
	// Track consecutive breaches
	if time.Since(hg.lastBreach) < hg.breachCooldown {
		hg.consecutiveBreaches++
	} else {
		hg.consecutiveBreaches = 1
	}
	hg.lastBreach = time.Now()
	
	// Check if we should trigger emergency stop
	if hg.consecutiveBreaches >= hg.config.MaxConsecutiveBreaches {
		hg.triggerEmergencyStop(fmt.Sprintf("too many consecutive breaches: %d", hg.consecutiveBreaches))
		return
	}
	
	// Handle based on severity
	switch violation.Severity {
	case SeverityFatal:
		hg.triggerEmergencyStop(violation.Message)
	case SeverityCritical:
		if hg.config.StrictMode {
			hg.triggerEmergencyStop(violation.Message)
		}
	}
	
	// Call violation handler
	if hg.onViolation != nil {
		go hg.onViolation(violation)
	}
}

// triggerEmergencyStop triggers emergency stop
func (hg *HotpathGuard) triggerEmergencyStop(reason string) {
	hg.emergencyStop = true
	atomic.AddInt64(&hg.emergencyStops, 1)
	
	// Schedule emergency stop reset
	go func() {
		duration := time.Duration(hg.config.EmergencyStopDurationMin) * time.Minute
		time.Sleep(duration)
		
		hg.mu.Lock()
		hg.emergencyStop = false
		hg.consecutiveBreaches = 0
		hg.mu.Unlock()
	}()
	
	if hg.onEmergencyStop != nil {
		go hg.onEmergencyStop(reason)
	}
}

// isEmergencyStop checks if system is in emergency stop
func (hg *HotpathGuard) isEmergencyStop() bool {
	hg.mu.RLock()
	defer hg.mu.RUnlock()
	return hg.emergencyStop
}

// isProviderDegraded checks if a provider is marked as degraded
func (hg *HotpathGuard) isProviderDegraded(provider string) bool {
	hg.mu.RLock()
	defer hg.mu.RUnlock()
	return hg.providerDegraded[provider]
}

// SetProviderHealth updates provider health status
func (hg *HotpathGuard) SetProviderHealth(provider string, healthy bool) {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	hg.providerDegraded[provider] = !healthy
}

// SetBudgetStatus updates budget exhaustion status
func (hg *HotpathGuard) SetBudgetStatus(exhausted bool) {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	hg.budgetExhausted = exhausted
}

// GetMetrics returns current hotpath metrics
func (hg *HotpathGuard) GetMetrics() HotpathMetrics {
	hg.mu.RLock()
	defer hg.mu.RUnlock()
	
	return HotpathMetrics{
		LiveCalls:           atomic.LoadInt64(&hg.liveCalls),
		TotalChecks:         atomic.LoadInt64(&hg.totalChecks),
		TotalViolations:     atomic.LoadInt64(&hg.totalViolations),
		EmergencyStops:      atomic.LoadInt64(&hg.emergencyStops),
		ConsecutiveBreaches: hg.consecutiveBreaches,
		EmergencyStop:       hg.emergencyStop,
		BudgetExhausted:     hg.budgetExhausted,
		SuccessRate:         hg.successRate.Average(),
		CacheHitRate:        hg.cacheHitRate.Average(),
		ProvidersDegraded:   len(hg.providerDegraded),
	}
}

// HotpathMetrics holds current hotpath metrics
type HotpathMetrics struct {
	LiveCalls           int64   `json:"live_calls"`
	TotalChecks         int64   `json:"total_checks"`
	TotalViolations     int64   `json:"total_violations"`
	EmergencyStops      int64   `json:"emergency_stops"`
	ConsecutiveBreaches int     `json:"consecutive_breaches"`
	EmergencyStop       bool    `json:"emergency_stop"`
	BudgetExhausted     bool    `json:"budget_exhausted"`
	SuccessRate         float64 `json:"success_rate"`
	CacheHitRate        float64 `json:"cache_hit_rate"`
	ProvidersDegraded   int     `json:"providers_degraded"`
}

// SetViolationHandler sets custom violation handler
func (hg *HotpathGuard) SetViolationHandler(handler func(violation HotpathViolation)) {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	hg.onViolation = handler
}

// SetEmergencyStopHandler sets custom emergency stop handler
func (hg *HotpathGuard) SetEmergencyStopHandler(handler func(reason string)) {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	hg.onEmergencyStop = handler
}

// Enable enables hotpath protection
func (hg *HotpathGuard) Enable() {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	hg.enabled = true
}

// Disable disables hotpath protection (for testing)
func (hg *HotpathGuard) Disable() {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	hg.enabled = false
}

// ForceEmergencyStop manually triggers emergency stop
func (hg *HotpathGuard) ForceEmergencyStop(reason string) {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	hg.triggerEmergencyStop(reason)
}

// ClearEmergencyStop manually clears emergency stop
func (hg *HotpathGuard) ClearEmergencyStop() {
	hg.mu.Lock()
	defer hg.mu.Unlock()
	hg.emergencyStop = false
	hg.consecutiveBreaches = 0
}

// Default handlers

// defaultViolationHandler provides default violation handling
func (hg *HotpathGuard) defaultViolationHandler(violation HotpathViolation) {
	// In real implementation, this would call observ.Log
	// observ.Log("hotpath_violation", map[string]any{
	//     "type":      string(violation.Type),
	//     "message":   violation.Message,
	//     "severity":  string(violation.Severity),
	//     "value":     violation.Value,
	//     "threshold": violation.Threshold,
	//     "context":   violation.Context,
	// })
}

// defaultEmergencyStopHandler provides default emergency stop handling
func (hg *HotpathGuard) defaultEmergencyStopHandler(reason string) {
	// In real implementation, this would call observ.Log and send alerts
	// observ.Log("hotpath_emergency_stop", map[string]any{
	//     "reason": reason,
	//     "timestamp": time.Now().UTC().Format(time.RFC3339),
	// })
}

// ResetCounters resets all counters (for testing)
func (hg *HotpathGuard) ResetCounters() {
	atomic.StoreInt64(&hg.liveCalls, 0)
	atomic.StoreInt64(&hg.totalChecks, 0)
	atomic.StoreInt64(&hg.totalViolations, 0)
	atomic.StoreInt64(&hg.emergencyStops, 0)
	
	hg.mu.Lock()
	defer hg.mu.Unlock()
	hg.consecutiveBreaches = 0
	hg.emergencyStop = false
	hg.budgetExhausted = false
	hg.providerDegraded = make(map[string]bool)
}