package adapters

import (
	"log"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// ProviderStatus represents the health state of a data provider
type ProviderStatus string

const (
	ProviderStatusHealthy  ProviderStatus = "healthy"
	ProviderStatusDegraded ProviderStatus = "degraded"
	ProviderStatusFailed   ProviderStatus = "failed"
)

// ProviderHealth tracks provider reliability and performance
type ProviderHealth struct {
	mu             sync.RWMutex
	name           string
	status         ProviderStatus
	lastSuccessful time.Time
	lastError      time.Time
	errorCount     int64
	successCount   int64
	consecutiveErrors int
	latencyP95     time.Duration
	logger         *log.Logger
	
	// Health thresholds
	degradedErrorRate    float64 // 0.01 = 1%
	failedErrorRate      float64 // 0.10 = 10%
	maxConsecutiveErrors int     // 5
	recoveryWindow       time.Duration // 5 minutes
}

// NewProviderHealth creates a new provider health monitor
func NewProviderHealth(name string, logger *log.Logger) *ProviderHealth {
	return &ProviderHealth{
		name:                 name,
		status:               ProviderStatusHealthy,
		degradedErrorRate:    0.01, // 1%
		failedErrorRate:      0.10, // 10%
		maxConsecutiveErrors: 5,
		recoveryWindow:       5 * time.Minute,
		logger:               logger,
	}
}

// RecordSuccess records a successful operation
func (ph *ProviderHealth) RecordSuccess(latency time.Duration) {
	ph.mu.Lock()
	defer ph.mu.Unlock()

	ph.lastSuccessful = time.Now()
	ph.successCount++
	ph.consecutiveErrors = 0
	ph.updateLatency(latency)

	// Check if we should recover from failed/degraded state
	if ph.status != ProviderStatusHealthy {
		if ph.shouldRecover() {
			oldStatus := ph.status
			ph.status = ProviderStatusHealthy
			ph.logger.Printf("Provider %s recovered to healthy (was %s)", ph.name, oldStatus)
			
			observ.IncCounter("provider_status_change_total", map[string]string{
				"provider":   ph.name,
				"from":       string(oldStatus),
				"to":         string(ProviderStatusHealthy),
			})
		}
	}

	observ.IncCounter("provider_operations_total", map[string]string{
		"provider": ph.name,
		"result":   "success",
	})

	observ.SetGauge("provider_status", ph.statusToFloat(), map[string]string{
		"provider": ph.name,
		"status":   string(ph.status),
	})
}

// RecordError records a failed operation
func (ph *ProviderHealth) RecordError(err error) {
	ph.mu.Lock()
	defer ph.mu.Unlock()

	ph.lastError = time.Now()
	ph.errorCount++
	ph.consecutiveErrors++

	oldStatus := ph.status
	ph.updateStatus()

	if oldStatus != ph.status {
		ph.logger.Printf("Provider %s status changed: %s -> %s (consecutive errors: %d)", 
			ph.name, oldStatus, ph.status, ph.consecutiveErrors)
		
		observ.IncCounter("provider_status_change_total", map[string]string{
			"provider": ph.name,
			"from":     string(oldStatus),
			"to":       string(ph.status),
		})
	}

	observ.IncCounter("provider_operations_total", map[string]string{
		"provider": ph.name,
		"result":   "error",
	})

	observ.SetGauge("provider_status", ph.statusToFloat(), map[string]string{
		"provider": ph.name,
		"status":   string(ph.status),
	})

	ph.logger.Printf("Provider %s error (consecutive: %d): %v", ph.name, ph.consecutiveErrors, err)
}

// GetStatus returns the current provider status
func (ph *ProviderHealth) GetStatus() ProviderStatus {
	ph.mu.RLock()
	defer ph.mu.RUnlock()
	return ph.status
}

// GetMetrics returns current health metrics
func (ph *ProviderHealth) GetMetrics() map[string]interface{} {
	ph.mu.RLock()
	defer ph.mu.RUnlock()

	total := ph.successCount + ph.errorCount
	errorRate := 0.0
	if total > 0 {
		errorRate = float64(ph.errorCount) / float64(total)
	}

	return map[string]interface{}{
		"status":             string(ph.status),
		"error_rate":         errorRate,
		"consecutive_errors": ph.consecutiveErrors,
		"last_successful":    ph.lastSuccessful,
		"last_error":         ph.lastError,
		"latency_p95_ms":     ph.latencyP95.Milliseconds(),
		"success_count":      ph.successCount,
		"error_count":        ph.errorCount,
	}
}

// updateStatus calculates new status based on error patterns
func (ph *ProviderHealth) updateStatus() {
	total := ph.successCount + ph.errorCount
	if total == 0 {
		return
	}

	errorRate := float64(ph.errorCount) / float64(total)

	// Check for immediate failure conditions
	if ph.consecutiveErrors >= ph.maxConsecutiveErrors {
		ph.status = ProviderStatusFailed
		return
	}

	// Check error rate thresholds
	if errorRate >= ph.failedErrorRate {
		ph.status = ProviderStatusFailed
	} else if errorRate >= ph.degradedErrorRate {
		ph.status = ProviderStatusDegraded
	}
}

// shouldRecover determines if provider should recover to healthy state
func (ph *ProviderHealth) shouldRecover() bool {
	// Must have had recent success and be in recovery window
	if time.Since(ph.lastSuccessful) > ph.recoveryWindow {
		return false
	}

	// Must not have recent errors
	if time.Since(ph.lastError) < ph.recoveryWindow {
		return false
	}

	return ph.consecutiveErrors == 0
}

// updateLatency maintains a simple P95 approximation
func (ph *ProviderHealth) updateLatency(latency time.Duration) {
	// Simple exponential moving average for P95 approximation
	if ph.latencyP95 == 0 {
		ph.latencyP95 = latency
	} else {
		// Weight new samples more heavily
		alpha := 0.1
		ph.latencyP95 = time.Duration(float64(ph.latencyP95)*(1-alpha) + float64(latency)*alpha)
	}

	observ.RecordDuration("provider_latency", latency, map[string]string{
		"provider": ph.name,
	})
}

// statusToFloat converts status to numeric value for metrics
func (ph *ProviderHealth) statusToFloat() float64 {
	switch ph.status {
	case ProviderStatusHealthy:
		return 1.0
	case ProviderStatusDegraded:
		return 0.5
	case ProviderStatusFailed:
		return 0.0
	default:
		return -1.0
	}
}

// RateBudget manages API request budgets with token bucket algorithm
type RateBudget struct {
	mu           sync.Mutex
	maxRequests  int           // Maximum requests in time window
	timeWindow   time.Duration // Time window for budget
	requests     []time.Time   // Request timestamps
	dailyUsed    int           // Requests used today
	lastReset    time.Time     // Last daily reset
	logger       *log.Logger
}

// NewRateBudget creates a new rate budget manager
func NewRateBudget(maxRequests int, timeWindow time.Duration, logger *log.Logger) *RateBudget {
	return &RateBudget{
		maxRequests: maxRequests,
		timeWindow:  timeWindow,
		requests:    make([]time.Time, 0),
		lastReset:   time.Now(),
		logger:      logger,
	}
}

// CanMakeRequest checks if a request is allowed within budget
func (rb *RateBudget) CanMakeRequest() bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	now := time.Now()
	
	// Reset daily counter if new day
	if now.Sub(rb.lastReset) >= 24*time.Hour {
		rb.dailyUsed = 0
		rb.lastReset = now
		rb.logger.Printf("Rate budget reset: daily usage reset to 0")
	}

	// Check daily budget
	if rb.dailyUsed >= rb.maxRequests {
		observ.IncCounter("rate_budget_exhausted_total", map[string]string{
			"window": "daily",
		})
		return false
	}

	// Clean old requests outside time window
	cutoff := now.Add(-rb.timeWindow)
	validRequests := make([]time.Time, 0)
	
	for _, reqTime := range rb.requests {
		if reqTime.After(cutoff) {
			validRequests = append(validRequests, reqTime)
		}
	}
	rb.requests = validRequests

	observ.SetGauge("rate_budget_remaining", float64(rb.maxRequests-rb.dailyUsed), map[string]string{})
	observ.SetGauge("rate_budget_window_usage", float64(len(rb.requests)), map[string]string{})

	return len(rb.requests) < rb.maxRequests
}

// RecordRequest records a successful request
func (rb *RateBudget) RecordRequest() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	now := time.Now()
	rb.requests = append(rb.requests, now)
	rb.dailyUsed++

	observ.IncCounter("rate_budget_used_total", map[string]string{})
	
	if rb.dailyUsed%50 == 0 { // Log every 50 requests
		remaining := rb.maxRequests - rb.dailyUsed
		rb.logger.Printf("Rate budget: %d/%d used today (%d remaining)", 
			rb.dailyUsed, rb.maxRequests, remaining)
	}
}

// GetUsage returns current usage statistics
func (rb *RateBudget) GetUsage() map[string]interface{} {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	return map[string]interface{}{
		"daily_used":     rb.dailyUsed,
		"daily_limit":    rb.maxRequests,
		"daily_remaining": rb.maxRequests - rb.dailyUsed,
		"window_usage":   len(rb.requests),
		"last_reset":     rb.lastReset,
	}
}