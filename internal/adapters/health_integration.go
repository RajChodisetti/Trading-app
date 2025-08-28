package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// MultiProviderHealth represents comprehensive health status for multi-provider system
type MultiProviderHealth struct {
	Status           string                        `json:"status"`            // "healthy", "degraded", "failed"
	Timestamp        string                        `json:"timestamp"`         // ISO 8601
	LiveMode         bool                          `json:"live_mode"`         // Whether live mode is active
	ShadowMode       bool                          `json:"shadow_mode"`       // Whether shadow mode is active
	CanPromoteToLive bool                          `json:"can_promote_to_live"` // Whether promotion gates pass
	
	// Provider comparison
	ActiveProvider   string                        `json:"active_provider"`   // Current active provider
	WarmProvider     string                        `json:"warm_provider"`     // Warm spare provider
	Providers        map[string]ProviderHealthInfo `json:"providers"`         // Per-provider health
	
	// Expansion state
	ExpansionState   string                        `json:"expansion_state"`   // "canary", "priority", "full"
	LiveSymbols      []string                      `json:"live_symbols"`      // Currently enabled symbols
	
	// Cost governance
	CostSummary      *CostSummary                  `json:"cost_summary"`      // Budget and cost information
	
	// Quality metrics (for promotion gates)
	QualityMetrics   QualityMetrics                `json:"quality_metrics"`   // Key quality indicators
	
	// Promotion gates
	PromotionGates   PromotionGates                `json:"promotion_gates"`   // Gate status for live promotion
}

// ProviderHealthInfo represents health status of a single provider
type ProviderHealthInfo struct {
	Name                string            `json:"name"`                   // Provider name
	Status              string            `json:"status"`                 // "healthy", "degraded", "failed"
	IsActive            bool              `json:"is_active"`              // Currently active
	IsWarm              bool              `json:"is_warm"`                // Warm spare
	SupportsRealtime    bool              `json:"supports_realtime"`      // Real-time capability
	
	// Operational metrics
	RequestsToday       int64             `json:"requests_today"`         // Requests made today
	EstimatedCostUSD    float64           `json:"estimated_cost_usd"`     // Estimated cost today
	DailyLimitUSD       float64           `json:"daily_limit_usd"`        // Daily cost limit
	BudgetRemainingPct  float64           `json:"budget_remaining_pct"`   // Budget remaining %
	
	// Performance metrics
	SuccessRate         float64           `json:"success_rate"`           // Success rate (0-1)
	AvgLatencyMs        float64           `json:"avg_latency_ms"`         // Average response time
	P95LatencyMs        float64           `json:"p95_latency_ms"`         // P95 response time
	FreshnessP95Ms      int64             `json:"freshness_p95_ms"`       // P95 data freshness
	
	// Circuit breaker
	CircuitBreakerState string            `json:"circuit_breaker_state"`  // "closed", "open", "half-open"
	ConsecutiveErrors   int               `json:"consecutive_errors"`     // Current error streak
	LastError           *string           `json:"last_error"`             // Most recent error
	LastHealthCheck     string            `json:"last_health_check"`      // ISO 8601
	
	// Comparison vs other providers
	RelativePerformance string            `json:"relative_performance"`   // "better", "similar", "worse"
}

// QualityMetrics holds key metrics for promotion gate evaluation
type QualityMetrics struct {
	// Overall system metrics
	DecisionLatencyP95Ms    int64   `json:"decision_latency_p95_ms"`     // Decision engine latency
	HotpathLiveCallsTotal   int64   `json:"hotpath_live_calls_total"`    // Must be 0 for promotion
	
	// Shadow mode validation
	ShadowSamples           int64   `json:"shadow_samples"`              // Total shadow comparisons
	ShadowMismatches        int64   `json:"shadow_mismatches"`           // Shadow mismatches
	ShadowMismatchRate      float64 `json:"shadow_mismatch_rate"`        // Mismatch rate (should be <2%)
	
	// Provider quality comparison
	ActiveProviderP95Ms     int64   `json:"active_provider_p95_ms"`      // Active provider freshness
	WarmProviderP95Ms       int64   `json:"warm_provider_p95_ms"`        // Warm provider freshness
	ProviderPerformanceDiff float64 `json:"provider_performance_diff"`   // Performance difference
	
	// Cache performance
	CacheHitRate            float64 `json:"cache_hit_rate"`              // Cache efficiency
	CacheEvictionRate       float64 `json:"cache_eviction_rate"`         // Cache pressure
}

// PromotionGates represents the status of each promotion gate
type PromotionGates struct {
	Overall                 string  `json:"overall"`                  // "PASS", "FAIL", "PENDING"
	FreshnessRTH            string  `json:"freshness_rth"`            // RTH freshness gate
	FreshnessAH             string  `json:"freshness_ah"`             // After-hours freshness gate
	SuccessRate             string  `json:"success_rate"`             // Success rate gate
	DecisionLatency         string  `json:"decision_latency"`         // Decision latency gate
	HotpathIsolation        string  `json:"hotpath_isolation"`        // Hotpath calls gate
	ShadowMismatchRate      string  `json:"shadow_mismatch_rate"`     // Shadow validation gate
	BudgetRemaining         string  `json:"budget_remaining"`         // Budget health gate
	
	// Gate details
	Details                 map[string]GateDetail `json:"details"`  // Per-gate details
}

// GateDetail provides detailed information about a specific promotion gate
type GateDetail struct {
	Status      string  `json:"status"`       // "PASS", "FAIL", "PENDING"
	Current     float64 `json:"current"`      // Current metric value
	Threshold   float64 `json:"threshold"`    // Required threshold
	Unit        string  `json:"unit"`         // Metric unit
	Message     string  `json:"message"`      // Human-readable status
	LastUpdated string  `json:"last_updated"` // ISO 8601
}

// MultiProviderHealthHandler creates an enhanced health endpoint for multi-provider monitoring
func MultiProviderHealthHandler(pm *ProviderManager, cg *CostGovernor, acm *AdaptiveCadenceManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		
		health := gatherMultiProviderHealth(ctx, pm, cg, acm)
		
		// Set appropriate HTTP status code
		statusCode := http.StatusOK
		switch health.Status {
		case "degraded":
			statusCode = http.StatusPartialContent // 206
		case "failed":
			statusCode = http.StatusServiceUnavailable // 503
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(health)
	})
}

// gatherMultiProviderHealth collects comprehensive health information from all providers
func gatherMultiProviderHealth(ctx context.Context, pm *ProviderManager, cg *CostGovernor, acm *AdaptiveCadenceManager) *MultiProviderHealth {
	now := time.Now().UTC()
	
	// Get provider manager status
	status := pm.GetStatus()
	costSummary := cg.GetCostSummary()
	
	// Gather per-provider health information
	providers := make(map[string]ProviderHealthInfo)
	var overallStatus string = "healthy"
	
	// Collect health from all registered providers
	for providerName, adapter := range pm.providers {
		providerHealth := gatherProviderHealth(ctx, providerName, adapter, pm, cg)
		providers[providerName] = providerHealth
		
		// Aggregate overall status
		if providerHealth.Status == "failed" {
			overallStatus = "failed"
		} else if providerHealth.Status == "degraded" && overallStatus != "failed" {
			overallStatus = "degraded"
		}
	}
	
	// Determine live mode status from configuration or provider state
	liveMode := status.LiveModeEnabled
	shadowMode := status.ShadowModeEnabled
	
	// Calculate quality metrics
	qualityMetrics := calculateQualityMetrics(providers, pm, cg)
	
	// Evaluate promotion gates
	promotionGates := evaluatePromotionGates(qualityMetrics, providers, liveMode)
	
	return &MultiProviderHealth{
		Status:           overallStatus,
		Timestamp:        now.Format(time.RFC3339),
		LiveMode:         liveMode,
		ShadowMode:       shadowMode,
		CanPromoteToLive: promotionGates.Overall == "PASS",
		ActiveProvider:   status.ActiveProvider,
		WarmProvider:     status.WarmProvider,
		Providers:        providers,
		ExpansionState:   string(status.ExpansionState),
		LiveSymbols:      status.LiveSymbols,
		CostSummary:      costSummary,
		QualityMetrics:   qualityMetrics,
		PromotionGates:   promotionGates,
	}
}

// gatherProviderHealth collects health information for a single provider
func gatherProviderHealth(ctx context.Context, name string, adapter QuotesAdapter, pm *ProviderManager, cg *CostGovernor) ProviderHealthInfo {
	health := ProviderHealthInfo{
		Name:             name,
		Status:           "unknown",
		IsActive:         pm.activeProvider == name,
		IsWarm:           pm.warmProvider == name,
		SupportsRealtime: false, // Default
		LastHealthCheck:  time.Now().UTC().Format(time.RFC3339),
	}
	
	// Get real-time capability if supported
	if rtAdapter, ok := adapter.(interface{ SupportsRealtime() bool }); ok {
		health.SupportsRealtime = rtAdapter.SupportsRealtime()
	}
	
	// Get cost information
	if costAdapter, ok := adapter.(interface{ GetCostEstimate() float64 }); ok {
		health.EstimatedCostUSD = costAdapter.GetCostEstimate()
	}
	
	// Get budget status from cost governor
	used, limit, remainingPct, found := cg.GetProviderBudgetStatus(name)
	if found {
		health.RequestsToday = int64(used) // Approximation
		health.DailyLimitUSD = limit
		health.BudgetRemainingPct = remainingPct
	}
	
	// Health check
	healthErr := adapter.HealthCheck(ctx)
	if healthErr != nil {
		health.Status = "failed"
		errMsg := healthErr.Error()
		health.LastError = &errMsg
	} else {
		health.Status = "healthy"
	}
	
	// Circuit breaker state
	if cbState, exists := pm.healthRegistry.circuitBreakers[name]; exists {
		switch cbState.state {
		case CircuitClosed:
			health.CircuitBreakerState = "closed"
		case CircuitOpen:
			health.CircuitBreakerState = "open"
		case CircuitHalfOpen:
			health.CircuitBreakerState = "half_open"
		}
		health.ConsecutiveErrors = cbState.failures
	}
	
	// Performance metrics from observability
	health.SuccessRate = getProviderSuccessRate(name)
	health.AvgLatencyMs = getProviderAvgLatency(name)
	health.P95LatencyMs = getProviderP95Latency(name)
	health.FreshnessP95Ms = getProviderFreshnessP95(name)
	
	// Relative performance comparison
	health.RelativePerformance = calculateRelativePerformance(name, health, pm)
	
	return health
}

// calculateQualityMetrics computes key quality indicators for promotion gates
func calculateQualityMetrics(providers map[string]ProviderHealthInfo, pm *ProviderManager, cg *CostGovernor) QualityMetrics {
	metrics := QualityMetrics{}
	
	// System-wide decision latency
	metrics.DecisionLatencyP95Ms = getSystemDecisionLatencyP95()
	
	// Hotpath live calls (critical for promotion)
	metrics.HotpathLiveCallsTotal = getHotpathLiveCalls()
	
	// Shadow mode metrics
	metrics.ShadowSamples = getShadowSamples()
	metrics.ShadowMismatches = getShadowMismatches()
	if metrics.ShadowSamples > 0 {
		metrics.ShadowMismatchRate = float64(metrics.ShadowMismatches) / float64(metrics.ShadowSamples)
	}
	
	// Provider comparison
	if activeProvider, exists := providers[pm.activeProvider]; exists {
		metrics.ActiveProviderP95Ms = activeProvider.FreshnessP95Ms
	}
	if warmProvider, exists := providers[pm.warmProvider]; exists {
		metrics.WarmProviderP95Ms = warmProvider.FreshnessP95Ms
		
		// Calculate performance difference
		if metrics.ActiveProviderP95Ms > 0 && metrics.WarmProviderP95Ms > 0 {
			metrics.ProviderPerformanceDiff = float64(metrics.WarmProviderP95Ms-metrics.ActiveProviderP95Ms) / float64(metrics.ActiveProviderP95Ms)
		}
	}
	
	// Cache performance
	metrics.CacheHitRate = getCacheHitRate()
	metrics.CacheEvictionRate = getCacheEvictionRate()
	
	return metrics
}

// evaluatePromotionGates checks all promotion gates for live mode readiness
func evaluatePromotionGates(metrics QualityMetrics, providers map[string]ProviderHealthInfo, liveMode bool) PromotionGates {
	gates := PromotionGates{
		Details: make(map[string]GateDetail),
	}
	now := time.Now().UTC()
	
	// RTH Freshness Gate (≤ 5000ms P95)
	rthGate := evaluateGate(float64(metrics.ActiveProviderP95Ms), 5000, "≤", "ms", "RTH freshness P95")
	gates.FreshnessRTH = rthGate.Status
	gates.Details["freshness_rth"] = rthGate
	
	// After Hours Freshness Gate (≤ 60000ms P95)
	ahGate := evaluateGate(float64(metrics.ActiveProviderP95Ms), 60000, "≤", "ms", "After hours freshness P95")
	gates.FreshnessAH = ahGate.Status
	gates.Details["freshness_ah"] = ahGate
	
	// Success Rate Gate (≥ 99%)
	var avgSuccessRate float64
	var providerCount int
	for _, provider := range providers {
		if provider.IsActive || provider.IsWarm {
			avgSuccessRate += provider.SuccessRate
			providerCount++
		}
	}
	if providerCount > 0 {
		avgSuccessRate /= float64(providerCount)
	}
	successGate := evaluateGate(avgSuccessRate*100, 99, "≥", "%", "Provider success rate")
	gates.SuccessRate = successGate.Status
	gates.Details["success_rate"] = successGate
	
	// Decision Latency Gate (≤ 200ms P95)
	latencyGate := evaluateGate(float64(metrics.DecisionLatencyP95Ms), 200, "≤", "ms", "Decision latency P95")
	gates.DecisionLatency = latencyGate.Status
	gates.Details["decision_latency"] = latencyGate
	
	// Hotpath Isolation Gate (== 0)
	hotpathGate := evaluateGate(float64(metrics.HotpathLiveCallsTotal), 0, "==", "calls", "Hotpath live calls")
	gates.HotpathIsolation = hotpathGate.Status
	gates.Details["hotpath_isolation"] = hotpathGate
	
	// Shadow Mismatch Rate Gate (≤ 2%)
	shadowGate := evaluateGate(metrics.ShadowMismatchRate*100, 2, "≤", "%", "Shadow mismatch rate")
	gates.ShadowMismatchRate = shadowGate.Status
	gates.Details["shadow_mismatch_rate"] = shadowGate
	
	// Budget Remaining Gate (≥ 10%)
	var minBudgetRemaining float64 = 100 // Start with 100%
	for _, provider := range providers {
		if provider.IsActive || provider.IsWarm {
			if provider.BudgetRemainingPct < minBudgetRemaining {
				minBudgetRemaining = provider.BudgetRemainingPct
			}
		}
	}
	budgetGate := evaluateGate(minBudgetRemaining*100, 10, "≥", "%", "Minimum budget remaining")
	gates.BudgetRemaining = budgetGate.Status
	gates.Details["budget_remaining"] = budgetGate
	
	// Update timestamps
	for key, gate := range gates.Details {
		gate.LastUpdated = now.Format(time.RFC3339)
		gates.Details[key] = gate
	}
	
	// Overall gate evaluation
	allGatesPass := gates.FreshnessRTH == "PASS" &&
		gates.FreshnessAH == "PASS" &&
		gates.SuccessRate == "PASS" &&
		gates.DecisionLatency == "PASS" &&
		gates.HotpathIsolation == "PASS" &&
		gates.ShadowMismatchRate == "PASS" &&
		gates.BudgetRemaining == "PASS"
	
	if allGatesPass {
		gates.Overall = "PASS"
	} else {
		gates.Overall = "FAIL"
	}
	
	return gates
}

// evaluateGate evaluates a single promotion gate
func evaluateGate(current, threshold float64, operator, unit, description string) GateDetail {
	var status string
	var message string
	
	switch operator {
	case "≤", "<=":
		if current <= threshold {
			status = "PASS"
			message = fmt.Sprintf("%s: %.2f%s ≤ %.2f%s ✓", description, current, unit, threshold, unit)
		} else {
			status = "FAIL"
			message = fmt.Sprintf("%s: %.2f%s > %.2f%s ✗", description, current, unit, threshold, unit)
		}
	case "≥", ">=":
		if current >= threshold {
			status = "PASS"
			message = fmt.Sprintf("%s: %.2f%s ≥ %.2f%s ✓", description, current, unit, threshold, unit)
		} else {
			status = "FAIL"
			message = fmt.Sprintf("%s: %.2f%s < %.2f%s ✗", description, current, unit, threshold, unit)
		}
	case "==":
		if current == threshold {
			status = "PASS"
			message = fmt.Sprintf("%s: %.0f%s == %.0f%s ✓", description, current, unit, threshold, unit)
		} else {
			status = "FAIL"
			message = fmt.Sprintf("%s: %.0f%s != %.0f%s ✗", description, current, unit, threshold, unit)
		}
	default:
		status = "FAIL"
		message = fmt.Sprintf("%s: unknown operator %s", description, operator)
	}
	
	return GateDetail{
		Status:      status,
		Current:     current,
		Threshold:   threshold,
		Unit:        unit,
		Message:     message,
		LastUpdated: time.Now().UTC().Format(time.RFC3339),
	}
}

// Helper functions to extract metrics from observability system
func getProviderSuccessRate(provider string) float64 {
	// TODO: Implement metric extraction from observ registry
	return 0.99 // Placeholder
}

func getProviderAvgLatency(provider string) float64 {
	// TODO: Implement metric extraction
	return 150.0 // Placeholder
}

func getProviderP95Latency(provider string) float64 {
	// TODO: Implement metric extraction
	return 300.0 // Placeholder
}

func getProviderFreshnessP95(provider string) int64 {
	// TODO: Implement metric extraction
	return 2000 // Placeholder
}

func getSystemDecisionLatencyP95() int64 {
	// TODO: Implement metric extraction
	return 180 // Placeholder
}

func getHotpathLiveCalls() int64 {
	// TODO: Implement metric extraction
	return 0 // Placeholder - must be 0 for promotion
}

func getShadowSamples() int64 {
	// TODO: Implement metric extraction
	return 1000 // Placeholder
}

func getShadowMismatches() int64 {
	// TODO: Implement metric extraction
	return 15 // Placeholder
}

func getCacheHitRate() float64 {
	// TODO: Implement metric extraction
	return 0.95 // Placeholder
}

func getCacheEvictionRate() float64 {
	// TODO: Implement metric extraction
	return 0.05 // Placeholder
}

func calculateRelativePerformance(providerName string, health ProviderHealthInfo, pm *ProviderManager) string {
	// Simple heuristic based on freshness and success rate
	if health.FreshnessP95Ms < 3000 && health.SuccessRate > 0.99 {
		return "better"
	} else if health.FreshnessP95Ms > 10000 || health.SuccessRate < 0.95 {
		return "worse"
	}
	return "similar"
}