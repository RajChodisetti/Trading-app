package adapters

import (
	"context"
	"time"
)

// HotpathProtectedQuotesAdapter wraps a quotes adapter with hotpath protection
type HotpathProtectedQuotesAdapter struct {
	adapter      QuotesAdapter
	guard        *HotpathGuard
	provider     string
	isLive       bool  // Whether this adapter makes live calls
}

// NewHotpathProtectedQuotesAdapter creates a hotpath-protected wrapper
func NewHotpathProtectedQuotesAdapter(provider string, adapter QuotesAdapter, guard *HotpathGuard, isLive bool) *HotpathProtectedQuotesAdapter {
	return &HotpathProtectedQuotesAdapter{
		adapter:  adapter,
		guard:    guard,
		provider: provider,
		isLive:   isLive,
	}
}

// GetQuote gets a quote with hotpath protection
func (hpa *HotpathProtectedQuotesAdapter) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	startTime := time.Now()
	
	// Pre-request validation
	if err := hpa.guard.CheckPreRequest(ctx, hpa.provider, symbol, hpa.isLive); err != nil {
		return nil, NewRateLimitError(symbol, err.Error())
	}
	
	// Make the request
	quote, err := hpa.adapter.GetQuote(ctx, symbol)
	
	// Calculate timing and success
	latencyMs := time.Since(startTime).Milliseconds()
	success := err == nil
	fromCache := false
	
	// Detect if quote came from cache (based on staleness and speed)
	if quote != nil && latencyMs < 50 && quote.StalenessMs < 1000 {
		fromCache = true
	}
	
	// Post-request validation
	if checkErr := hpa.guard.CheckPostRequest(ctx, hpa.provider, latencyMs, success, fromCache); checkErr != nil {
		// Log the violation but don't fail the request unless in strict mode
		// The guard has already handled the violation internally
	}
	
	return quote, err
}

// GetQuotes gets multiple quotes with hotpath protection
func (hpa *HotpathProtectedQuotesAdapter) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	startTime := time.Now()
	
	// Check each symbol individually for hotpath compliance
	allowedSymbols := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		if err := hpa.guard.CheckPreRequest(ctx, hpa.provider, symbol, hpa.isLive); err != nil {
			// Skip this symbol but continue with others
			continue
		}
		allowedSymbols = append(allowedSymbols, symbol)
	}
	
	// Make the request with allowed symbols
	quotes, err := hpa.adapter.GetQuotes(ctx, allowedSymbols)
	
	// Calculate metrics
	latencyMs := time.Since(startTime).Milliseconds()
	success := err == nil
	fromCache := false
	
	// Estimate cache usage (rough heuristic)
	if len(quotes) > 0 {
		avgStaleness := int64(0)
		for _, quote := range quotes {
			avgStaleness += quote.StalenessMs
		}
		avgStaleness /= int64(len(quotes))
		
		if latencyMs < 100 && avgStaleness < 2000 {
			fromCache = true
		}
	}
	
	// Post-request validation
	if checkErr := hpa.guard.CheckPostRequest(ctx, hpa.provider, latencyMs, success, fromCache); checkErr != nil {
		// Log but don't fail unless critical
	}
	
	return quotes, err
}

// HealthCheck passes through with protection
func (hpa *HotpathProtectedQuotesAdapter) HealthCheck(ctx context.Context) error {
	// Health checks are not subject to hotpath limits
	return hpa.adapter.HealthCheck(ctx)
}

// Close passes through
func (hpa *HotpathProtectedQuotesAdapter) Close() error {
	return hpa.adapter.Close()
}

// Enhanced Provider Manager with Hotpath Protection
type HotpathProtectedProviderManager struct {
	*ProviderManager
	guard *HotpathGuard
}

// NewHotpathProtectedProviderManager creates a provider manager with hotpath protection
func NewHotpathProtectedProviderManager(config ProviderManagerConfig, guard *HotpathGuard) *HotpathProtectedProviderManager {
	return &HotpathProtectedProviderManager{
		ProviderManager: NewProviderManager(config),
		guard:           guard,
	}
}

// RegisterProvider registers a provider with hotpath protection
func (hppm *HotpathProtectedProviderManager) RegisterProvider(name string, provider QuotesAdapter, isLive bool) {
	// Wrap provider with hotpath protection
	protectedProvider := NewHotpathProtectedQuotesAdapter(name, provider, hppm.guard, isLive)
	
	// Register the protected provider
	hppm.ProviderManager.RegisterProvider(name, protectedProvider)
	
	// Set up health monitoring
	go hppm.monitorProviderHealth(name, provider)
}

// monitorProviderHealth monitors provider health and updates hotpath guard
func (hppm *HotpathProtectedProviderManager) monitorProviderHealth(name string, provider QuotesAdapter) {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := provider.HealthCheck(ctx)
			cancel()
			
			// Update hotpath guard with health status
			hppm.guard.SetProviderHealth(name, err == nil)
			
		}
	}
}

// GetQuote gets a quote with full hotpath protection
func (hppm *HotpathProtectedProviderManager) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	// Check system-wide hotpath status
	metrics := hppm.guard.GetMetrics()
	if metrics.EmergencyStop {
		return nil, NewProviderError(symbol, "system in emergency stop", nil)
	}
	
	// Use underlying provider manager
	return hppm.ProviderManager.GetQuote(ctx, symbol)
}

// HotpathAwareFactory creates adapters with hotpath protection
type HotpathAwareFactory struct {
	guard  *HotpathGuard
	config HotpathConfig
}

// NewHotpathAwareFactory creates a factory for hotpath-protected adapters
func NewHotpathAwareFactory(config HotpathConfig) *HotpathAwareFactory {
	guard := NewHotpathGuard(config)
	return &HotpathAwareFactory{
		guard:  guard,
		config: config,
	}
}

// CreateAlphaVantageAdapter creates Alpha Vantage adapter with hotpath protection
func (haf *HotpathAwareFactory) CreateAlphaVantageAdapter(config AlphaVantageConfig) (*HotpathProtectedQuotesAdapter, error) {
	baseAdapter, err := NewAlphaVantageAdapter(config)
	if err != nil {
		return nil, err
	}
	
	return NewHotpathProtectedQuotesAdapter("alphavantage", baseAdapter, haf.guard, true), nil
}

// CreatePolygonAdapter creates Polygon adapter with hotpath protection
func (haf *HotpathAwareFactory) CreatePolygonAdapter(config PolygonConfig) (*HotpathProtectedQuotesAdapter, error) {
	baseAdapter, err := NewPolygonAdapter(config)
	if err != nil {
		return nil, err
	}
	
	return NewHotpathProtectedQuotesAdapter("polygon", baseAdapter, haf.guard, true), nil
}

// CreateMockAdapter creates mock adapter (no hotpath protection needed)
func (haf *HotpathAwareFactory) CreateMockAdapter() *HotpathProtectedQuotesAdapter {
	baseAdapter := NewMockQuotesAdapter()
	return NewHotpathProtectedQuotesAdapter("mock", baseAdapter, haf.guard, false)
}

// GetGuard returns the hotpath guard for external monitoring
func (haf *HotpathAwareFactory) GetGuard() *HotpathGuard {
	return haf.guard
}

// HotpathMonitor provides monitoring and alerting for hotpath violations
type HotpathMonitor struct {
	guard    *HotpathGuard
	alerting func(violation HotpathViolation) // Alert callback
}

// NewHotpathMonitor creates a hotpath monitor
func NewHotpathMonitor(guard *HotpathGuard) *HotpathMonitor {
	monitor := &HotpathMonitor{
		guard: guard,
	}
	
	// Set up violation handling
	guard.SetViolationHandler(monitor.handleViolation)
	guard.SetEmergencyStopHandler(monitor.handleEmergencyStop)
	
	return monitor
}

// handleViolation handles hotpath violations with monitoring and alerting
func (hm *HotpathMonitor) handleViolation(violation HotpathViolation) {
	// Log violation (in real implementation would use observ.Log)
	
	// Send alerts for critical violations
	if violation.Severity == SeverityCritical || violation.Severity == SeverityFatal {
		if hm.alerting != nil {
			hm.alerting(violation)
		}
	}
	
	// Update metrics (in real implementation would use observ.IncCounter)
	// observ.IncCounter("hotpath_violations_total", map[string]string{
	//     "type": string(violation.Type),
	//     "severity": string(violation.Severity),
	// })
}

// handleEmergencyStop handles emergency stop events
func (hm *HotpathMonitor) handleEmergencyStop(reason string) {
	// Critical alert for emergency stops
	if hm.alerting != nil {
		violation := HotpathViolation{
			Type:      ViolationConsecutiveBreach,
			Message:   "Emergency stop triggered: " + reason,
			Severity:  SeverityFatal,
			Timestamp: time.Now(),
			Context:   map[string]interface{}{"reason": reason},
		}
		hm.alerting(violation)
	}
	
	// Log emergency stop
	// observ.Log("hotpath_emergency_stop", map[string]any{
	//     "reason": reason,
	//     "timestamp": time.Now().UTC().Format(time.RFC3339),
	// })
}

// SetAlertingCallback sets callback for violation alerts
func (hm *HotpathMonitor) SetAlertingCallback(callback func(violation HotpathViolation)) {
	hm.alerting = callback
}

// GetStatus returns current hotpath status
func (hm *HotpathMonitor) GetStatus() HotpathStatus {
	metrics := hm.guard.GetMetrics()
	
	status := HotpathStatus{
		Enabled:      hm.guard.enabled,
		Healthy:      !metrics.EmergencyStop && metrics.ConsecutiveBreaches == 0,
		Metrics:      metrics,
		LastCheck:    time.Now(),
	}
	
	// Determine overall health
	if metrics.EmergencyStop {
		status.Status = "emergency_stop"
	} else if metrics.ConsecutiveBreaches > 2 {
		status.Status = "degraded"
	} else if metrics.TotalViolations > 0 {
		status.Status = "warning"
	} else {
		status.Status = "healthy"
	}
	
	return status
}

// HotpathStatus represents current hotpath system status
type HotpathStatus struct {
	Enabled   bool           `json:"enabled"`
	Healthy   bool           `json:"healthy"`
	Status    string         `json:"status"`  // "healthy", "warning", "degraded", "emergency_stop"
	Metrics   HotpathMetrics `json:"metrics"`
	LastCheck time.Time      `json:"last_check"`
}

// Default hotpath configuration for different environments

// GetDefaultHotpathConfig returns default configuration
func GetDefaultHotpathConfig() HotpathConfig {
	return HotpathConfig{
		MaxLiveCallsPerSecond:     10,    // Conservative limit
		MaxDecisionLatencyMs:      200,   // 200ms max decision time
		MinCacheHitRate:           0.80,  // 80% cache hit rate minimum
		MinSuccessRate:            0.95,  // 95% success rate minimum
		MaxConsecutiveBreaches:    3,     // Max 3 consecutive breaches
		BreachCooldownSeconds:     60,    // 1 minute cooldown
		EmergencyStopDurationMin:  5,     // 5 minute emergency stop
		LatencyBufferSize:         100,   // Track last 100 latencies
		QualityWindowSeconds:      60,    // 60 second quality window
		BudgetWarningThreshold:    0.80,  // Warn at 80% budget
		BudgetCriticalThreshold:   0.95,  // Critical at 95% budget
		DefaultToMockOnViolation:  true,  // Fail to mock
		StrictMode:                false, // Allow some flexibility
		AllowGracefulDegradation:  true,  // Allow gradual degradation
	}
}

// GetProductionHotpathConfig returns production-safe configuration
func GetProductionHotpathConfig() HotpathConfig {
	config := GetDefaultHotpathConfig()
	config.MaxLiveCallsPerSecond = 5     // Even more conservative
	config.MaxDecisionLatencyMs = 150    // Stricter latency
	config.MinSuccessRate = 0.98         // Higher success rate
	config.StrictMode = true             // Strict enforcement
	config.MaxConsecutiveBreaches = 2    // Lower breach tolerance
	return config
}

// GetTestingHotpathConfig returns testing-friendly configuration
func GetTestingHotpathConfig() HotpathConfig {
	config := GetDefaultHotpathConfig()
	config.MaxLiveCallsPerSecond = 100   // Higher for testing
	config.MaxDecisionLatencyMs = 1000   // More lenient
	config.MinCacheHitRate = 0.50        // Lower for testing
	config.MinSuccessRate = 0.80         // Lower for testing
	config.StrictMode = false            // Not strict for testing
	config.MaxConsecutiveBreaches = 10   // More tolerance
	return config
}

// Note: MockQuotesAdapter is implemented in mock.go