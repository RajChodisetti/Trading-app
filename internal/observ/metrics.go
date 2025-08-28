package observ

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type registry struct {
	mu       sync.Mutex
	counters map[string]map[string]int64 // name -> labelsKey -> count
	gauges   map[string]map[string]float64 // name -> labelsKey -> value
	hist     map[string]map[string][]float64
}

var reg = &registry{
	counters: map[string]map[string]int64{},
	gauges:   map[string]map[string]float64{},
	hist:     map[string]map[string][]float64{},
}

// canonicalize label map so key order is stable
func canonLabels(lbl map[string]string) string {
	if len(lbl) == 0 {
		return ""
	}
	keys := make([]string, 0, len(lbl))
	for k := range lbl {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(lbl[k])
	}
	return b.String()
}

func IncCounter(name string, labels map[string]string) {
	IncCounterBy(name, labels, 1.0)
}

func IncCounterBy(name string, labels map[string]string, value float64) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	m, ok := reg.counters[name]
	if !ok {
		m = map[string]int64{}
		reg.counters[name] = m
	}
	k := canonLabels(labels)
	m[k] += int64(value)
}

func SetGauge(name string, value float64, labels map[string]string) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	m, ok := reg.gauges[name]
	if !ok {
		m = map[string]float64{}
		reg.gauges[name] = m
	}
	k := canonLabels(labels)
	m[k] = value
}

func Observe(name string, value float64, labels map[string]string) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	m, ok := reg.hist[name]
	if !ok {
		m = map[string][]float64{}
		reg.hist[name] = m
	}
	k := canonLabels(labels)
	m[k] = append(m[k], value)
}

// RecordHistogram records a histogram observation
func RecordHistogram(name string, value float64, labels map[string]string) {
	Observe(name, value, labels)
}

// RecordGauge records a gauge value
func RecordGauge(name string, value float64, labels map[string]string) {
	SetGauge(name, value, labels)
}

// RecordDuration records a duration metric
func RecordDuration(name string, duration time.Duration, labels map[string]string) {
	Observe(name+"_ms", float64(duration.Milliseconds()), labels)
}

// Basic text/JSON dump for quick checks (not Prometheus format on purpose)
func Handler() http.Handler {
	type dump struct {
		Counters map[string]map[string]int64     `json:"counters"`
		Gauges   map[string]map[string]float64   `json:"gauges"`
		Hist     map[string]map[string][]float64 `json:"histograms"`
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.mu.Lock()
		defer reg.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dump{Counters: reg.counters, Gauges: reg.gauges, Hist: reg.hist})
	})
}

// HealthStatus represents overall system health status
type HealthStatus struct {
	Status    string                 `json:"status"`    // "healthy", "degraded", "failed"
	Timestamp string                 `json:"timestamp"` // ISO 8601
	Uptime    string                 `json:"uptime"`    // Duration since start
	Version   string                 `json:"version"`   // Build version
	Metrics   HealthMetrics          `json:"metrics"`   // Key metrics for promotion gates
	Details   map[string]interface{} `json:"details"`   // Additional health details
}

// HealthMetrics holds key metrics for promotion gate evaluation
type HealthMetrics struct {
	// Quote metrics
	FreshnessP95Ms        int64   `json:"freshness_p95_ms"`        // P95 quote freshness in ms
	SuccessRate           float64 `json:"success_rate"`            // Quote fetch success rate
	CacheHitRate          float64 `json:"cache_hit_rate"`          // Cache hit rate
	HotpathCalls          int64   `json:"hotpath_calls"`           // Live calls on hotpath (must be 0)
	
	// Decision engine metrics
	DecisionLatencyP95Ms  int64   `json:"decision_latency_p95_ms"` // P95 decision latency in ms
	DecisionSuccessRate   float64 `json:"decision_success_rate"`   // Decision success rate
	
	// Shadow mode metrics
	ShadowSamples         int64   `json:"shadow_samples"`          // Total shadow comparisons
	ShadowMismatches      int64   `json:"shadow_mismatches"`       // Shadow mode mismatches
	ShadowMismatchRate    float64 `json:"shadow_mismatch_rate"`    // Shadow mismatch rate
	
	// Budget and rate limiting
	BudgetUsed            int     `json:"budget_used"`             // API requests used today
	BudgetTotal           int     `json:"budget_total"`            // Daily API budget
	BudgetRemainingPct    float64 `json:"budget_remaining_pct"`    // Budget remaining %
}

var (
	startTime = time.Now()
	version   = "dev" // Set via build flags
)

// SetVersion sets the version string for health reports
func SetVersion(v string) {
	version = v
}

// HealthHandler returns a comprehensive health endpoint for promotion gate monitoring
func HealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.mu.Lock()
		defer reg.mu.Unlock()
		
		health := HealthStatus{
			Status:    calculateOverallHealthStatus(),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Uptime:    time.Since(startTime).String(),
			Version:   version,
			Metrics:   calculateHealthMetrics(),
			Details:   gatherHealthDetails(),
		}
		
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

// calculateOverallHealthStatus determines the overall health status
func calculateOverallHealthStatus() string {
	// Check for critical failures
	if hasFailedComponents() {
		return "failed"
	}
	
	// Check for degraded performance
	if hasDegradedComponents() {
		return "degraded"
	}
	
	return "healthy"
}

// calculateHealthMetrics computes key metrics from raw telemetry
func calculateHealthMetrics() HealthMetrics {
	metrics := HealthMetrics{}
	
	// Quote freshness P95
	if freshnessSamples, exists := reg.hist["quote_freshness_ms"]; exists {
		for _, samples := range freshnessSamples {
			if len(samples) > 0 {
				sorted := make([]float64, len(samples))
				copy(sorted, samples)
				sort.Float64s(sorted)
				p95Index := int(float64(len(sorted)) * 0.95)
				if p95Index >= len(sorted) {
					p95Index = len(sorted) - 1
				}
				metrics.FreshnessP95Ms = int64(sorted[p95Index])
				break
			}
		}
	}
	
	// Success rate (quote fetches)
	var totalRequests, totalSuccesses int64
	if requests, exists := reg.counters["quote_requests_total"]; exists {
		for _, count := range requests {
			totalRequests += count
		}
	}
	if successes, exists := reg.counters["quote_successes_total"]; exists {
		for _, count := range successes {
			totalSuccesses += count
		}
	}
	if totalRequests > 0 {
		metrics.SuccessRate = float64(totalSuccesses) / float64(totalRequests)
	}
	
	// Cache hit rate
	var totalCacheHits, totalCacheMisses int64
	if hits, exists := reg.counters["quote_cache_hits_total"]; exists {
		for _, count := range hits {
			totalCacheHits += count
		}
	}
	if misses, exists := reg.counters["quote_cache_misses_total"]; exists {
		for _, count := range misses {
			totalCacheMisses += count
		}
	}
	if totalCacheHits+totalCacheMisses > 0 {
		metrics.CacheHitRate = float64(totalCacheHits) / float64(totalCacheHits+totalCacheMisses)
	}
	
	// Hotpath calls (critical for promotion)
	if hotpathCalls, exists := reg.counters["hotpath_live_calls_total"]; exists {
		for _, count := range hotpathCalls {
			metrics.HotpathCalls += count
		}
	}
	
	// Decision latency P95
	if decisionSamples, exists := reg.hist["decision_latency_ms"]; exists {
		for _, samples := range decisionSamples {
			if len(samples) > 0 {
				sorted := make([]float64, len(samples))
				copy(sorted, samples)
				sort.Float64s(sorted)
				p95Index := int(float64(len(sorted)) * 0.95)
				if p95Index >= len(sorted) {
					p95Index = len(sorted) - 1
				}
				metrics.DecisionLatencyP95Ms = int64(sorted[p95Index])
				break
			}
		}
	}
	
	// Shadow mode metrics
	if shadowSamples, exists := reg.counters["shadow_samples_total"]; exists {
		for _, count := range shadowSamples {
			metrics.ShadowSamples += count
		}
	}
	if shadowMismatches, exists := reg.counters["shadow_mismatches_total"]; exists {
		for _, count := range shadowMismatches {
			metrics.ShadowMismatches += count
		}
	}
	if metrics.ShadowSamples > 0 {
		metrics.ShadowMismatchRate = float64(metrics.ShadowMismatches) / float64(metrics.ShadowSamples)
	}
	
	// Budget tracking (from gauges)
	if budgetUsed, exists := reg.gauges["provider_budget_used"]; exists {
		for _, value := range budgetUsed {
			metrics.BudgetUsed = int(value)
			break
		}
	}
	if budgetTotal, exists := reg.gauges["provider_budget_total"]; exists {
		for _, value := range budgetTotal {
			metrics.BudgetTotal = int(value)
			break
		}
	}
	if metrics.BudgetTotal > 0 {
		metrics.BudgetRemainingPct = float64(metrics.BudgetTotal-metrics.BudgetUsed) / float64(metrics.BudgetTotal)
	}
	
	return metrics
}

// hasFailedComponents checks for critical failures
func hasFailedComponents() bool {
	// Check if any provider is marked as failed
	if providerHealth, exists := reg.gauges["provider_health_status"]; exists {
		for _, status := range providerHealth {
			if status == 0 { // 0 = failed, 1 = degraded, 2 = healthy
				return true
			}
		}
	}
	
	// Check for excessive error rates
	var totalErrors, totalRequests int64
	if errors, exists := reg.counters["quote_errors_total"]; exists {
		for _, count := range errors {
			totalErrors += count
		}
	}
	if requests, exists := reg.counters["quote_requests_total"]; exists {
		for _, count := range requests {
			totalRequests += count
		}
	}
	if totalRequests > 100 && float64(totalErrors)/float64(totalRequests) > 0.1 {
		return true // > 10% error rate = failed
	}
	
	return false
}

// hasDegradedComponents checks for performance degradation
func hasDegradedComponents() bool {
	// Check if any provider is marked as degraded
	if providerHealth, exists := reg.gauges["provider_health_status"]; exists {
		for _, status := range providerHealth {
			if status == 1 { // 1 = degraded
				return true
			}
		}
	}
	
	// Check decision latency
	if decisionSamples, exists := reg.hist["decision_latency_ms"]; exists {
		for _, samples := range decisionSamples {
			if len(samples) > 10 { // Need sufficient samples
				sorted := make([]float64, len(samples))
				copy(sorted, samples)
				sort.Float64s(sorted)
				p95Index := int(float64(len(sorted)) * 0.95)
				if p95Index >= len(sorted) {
					p95Index = len(sorted) - 1
				}
				if sorted[p95Index] > 200 { // > 200ms P95 = degraded
					return true
				}
			}
		}
	}
	
	return false
}

// gatherHealthDetails collects additional health information
func gatherHealthDetails() map[string]interface{} {
	details := make(map[string]interface{})
	
	// Active providers
	activeProviders := []string{}
	if providerStatus, exists := reg.gauges["provider_active"]; exists {
		for labelKey, isActive := range providerStatus {
			if isActive == 1 {
				activeProviders = append(activeProviders, labelKey)
			}
		}
	}
	details["active_providers"] = activeProviders
	
	// Cache statistics
	cacheStats := map[string]interface{}{}
	if cacheSize, exists := reg.gauges["quote_cache_size"]; exists {
		for _, size := range cacheSize {
			cacheStats["size"] = int(size)
			break
		}
	}
	if cacheEvictions, exists := reg.counters["quote_cache_evictions_total"]; exists {
		for _, count := range cacheEvictions {
			cacheStats["evictions"] = count
			break
		}
	}
	details["cache"] = cacheStats
	
	// Feature flags
	featureFlags := map[string]interface{}{}
	if liveEnabled, exists := reg.gauges["live_quotes_enabled"]; exists {
		for _, value := range liveEnabled {
			featureFlags["live_quotes_enabled"] = value == 1
			break
		}
	}
	if shadowMode, exists := reg.gauges["shadow_mode_enabled"]; exists {
		for _, value := range shadowMode {
			featureFlags["shadow_mode_enabled"] = value == 1
			break
		}
	}
	details["feature_flags"] = featureFlags
	
	// Recent errors (top 5)
	if errorTypes, exists := reg.counters["quote_errors_by_type"]; exists {
		type errorCount struct {
			Type  string
			Count int64
		}
		var errors []errorCount
		for errorType, count := range errorTypes {
			errors = append(errors, errorCount{Type: errorType, Count: count})
		}
		sort.Slice(errors, func(i, j int) bool {
			return errors[i].Count > errors[j].Count
		})
		if len(errors) > 5 {
			errors = errors[:5]
		}
		details["top_errors"] = errors
	}
	
	return details
}

// Simple health handler (legacy)
func Health() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}
