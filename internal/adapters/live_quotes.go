package adapters

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// LiveQuoteAdapter wraps a provider adapter with shadow mode, caching, and health monitoring
type LiveQuoteAdapter struct {
	provider     QuotesAdapter
	mockAdapter  QuotesAdapter
	cache        *BoundedQuoteCache
	config       LiveQuoteConfig
	
	// Shadow mode state
	mu                    sync.RWMutex
	shadowMode            bool
	shadowSamples         int64
	shadowMismatches      int64
	lastShadowComparison  time.Time
	
	// Health state with hysteresis  
	healthState           HealthState
	consecutiveBreaches   int
	consecutiveOks        int
	lastHealthTransition  time.Time
	
	// Canary rollout state
	canaryStartTime       time.Time
	canaryExpanded        bool
	
	// Metrics tracking
	metrics               *QuoteMetrics
	
	// Budget and rate limiting
	budgetTracker         *BudgetTracker
	
	// Hotpath protection
	hotpathCalls          int64
}

// LiveQuoteConfig holds configuration for live quote adapter
type LiveQuoteConfig struct {
	// Feature flags
	LiveEnabled           bool     `yaml:"live_enabled"`
	ShadowMode           bool     `yaml:"shadow_mode"`
	
	// Canary rollout
	CanarySymbols        []string `yaml:"canary_symbols"`
	PrioritySymbols      []string `yaml:"priority_symbols"`
	CanaryDurationMinutes int     `yaml:"canary_duration_minutes"`
	
	// Adaptive refresh tiers (ms)
	PositionsRefreshMs   int `yaml:"positions_ms"`
	WatchlistRefreshMs   int `yaml:"watchlist_ms"`
	OthersRefreshMs      int `yaml:"others_ms"`
	
	// Freshness and hysteresis
	FreshnessCeilingSeconds     int `yaml:"freshness_ceiling_seconds"`
	FreshnessCeilingAHSeconds   int `yaml:"freshness_ceiling_ah_seconds"`
	HysteresisSeconds          int `yaml:"hysteresis_seconds"`
	ConsecutiveBreachToDegrade int `yaml:"consecutive_breach_to_degrade"`
	ConsecutiveOkToRecover     int `yaml:"consecutive_ok_to_recover"`
	
	// Cache bounds
	CacheMaxEntries           int `yaml:"cache_max_entries"`
	CacheTTLSeconds          int `yaml:"cache_ttl_seconds"`
	CacheMaxAgeExtendSeconds int `yaml:"cache_max_age_extend_seconds"`
	
	// Budget management
	DailyRequestCap    int     `yaml:"daily_request_cap"`
	BudgetWarningPct   float64 `yaml:"budget_warning_pct"`
	ShadowSampleRate   float64 `yaml:"shadow_sample_rate"`
	
	// Health thresholds
	DegradedErrorRate      float64 `yaml:"degraded_error_rate"`
	FailedErrorRate        float64 `yaml:"failed_error_rate"`
	MaxConsecutiveErrors   int     `yaml:"max_consecutive_errors"`
	FreshnessP95ThresholdMs int64  `yaml:"freshness_p95_threshold_ms"`
	SuccessRateThreshold   float64 `yaml:"success_rate_threshold"`
	
	// Fallback behavior
	FallbackToCache bool `yaml:"fallback_to_cache"`
	FallbackToMock  bool `yaml:"fallback_to_mock"`
}

// HealthState represents the health status with hysteresis
type HealthState string

const (
	HealthHealthy  HealthState = "healthy"
	HealthDegraded HealthState = "degraded"
	HealthFailed   HealthState = "failed"
)

// BoundedQuoteCache is a thread-safe cache with size and TTL bounds
type BoundedQuoteCache struct {
	mu          sync.RWMutex
	entries     map[string]*BoundedCacheEntry
	maxEntries  int
	defaultTTL  time.Duration
	evictions   int64
	hits        int64
	misses      int64
}

// BoundedCacheEntry represents a cached quote with metadata
type BoundedCacheEntry struct {
	quote     *Quote
	fetchedAt time.Time
	ttl       time.Duration
	tier      string  // "positions", "watchlist", "others"
	priority  int     // Higher number = higher priority for eviction decisions
}

// QuoteMetrics tracks performance metrics
type QuoteMetrics struct {
	mu                sync.RWMutex
	requests          int64
	successes         int64
	errors            int64
	freshnessSamples  []int64  // Ring buffer for P95 calculation
	shadowSamples     int64
	shadowMismatches  int64
	hotpathCalls      int64
}

// BudgetTracker manages API budget with daily reset
type BudgetTracker struct {
	mu              sync.RWMutex
	requestsToday   int
	dailyCap        int
	resetTime       time.Time
	warningPct      float64
	lastWarning     time.Time
}

// NewLiveQuoteAdapter creates a new live quote adapter with all features
func NewLiveQuoteAdapter(provider QuotesAdapter, config LiveQuoteConfig) (*LiveQuoteAdapter, error) {
	// Validate config
	if len(config.CanarySymbols) == 0 {
		return nil, fmt.Errorf("canary symbols cannot be empty")
	}
	if len(config.PrioritySymbols) == 0 {
		return nil, fmt.Errorf("priority symbols cannot be empty")
	}
	
	// Create bounded cache
	cache := &BoundedQuoteCache{
		entries:     make(map[string]*BoundedCacheEntry),
		maxEntries:  config.CacheMaxEntries,
		defaultTTL:  time.Duration(config.CacheTTLSeconds) * time.Second,
	}
	
	// Create budget tracker
	budgetTracker := &BudgetTracker{
		dailyCap:    config.DailyRequestCap,
		resetTime:   time.Now().Add(24 * time.Hour),
		warningPct:  config.BudgetWarningPct,
	}
	
	// Create metrics
	metrics := &QuoteMetrics{
		freshnessSamples: make([]int64, 0, 1000), // Ring buffer for P95
	}
	
	adapter := &LiveQuoteAdapter{
		provider:         provider,
		mockAdapter:      NewMockQuotesAdapter(),
		cache:           cache,
		config:          config,
		shadowMode:      config.ShadowMode,
		healthState:     HealthHealthy,
		canaryStartTime: time.Now(),
		metrics:         metrics,
		budgetTracker:   budgetTracker,
	}
	
	observ.Log("live_quote_adapter_created", map[string]any{
		"shadow_mode":        config.ShadowMode,
		"live_enabled":       config.LiveEnabled,
		"canary_symbols":     config.CanarySymbols,
		"priority_symbols":   config.PrioritySymbols,
		"cache_max_entries":  config.CacheMaxEntries,
		"daily_request_cap":  config.DailyRequestCap,
	})
	
	return adapter, nil
}

// GetQuote implements QuotesAdapter with full feature set
func (lq *LiveQuoteAdapter) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	startTime := time.Now()
	
	// Increment request counter
	lq.metrics.incrementRequest()
	
	// Check if symbol is in canary rollout phase
	if !lq.isSymbolAllowed(symbol) {
		// Use mock for non-canary symbols during rollout
		return lq.mockAdapter.GetQuote(ctx, symbol)
	}
	
	// Try cache first
	if cachedEntry := lq.cache.getEntry(symbol); cachedEntry != nil {
		staleness := time.Since(cachedEntry.fetchedAt)
		cached := *cachedEntry.quote // Copy the quote
		cached.StalenessMs = staleness.Milliseconds()
		
		// Check if cache is fresh enough
		if lq.isCacheFresh(&cached, staleness) {
			lq.cache.recordHit()
			return &cached, nil
		}
		
		// Check if we should extend cache due to budget constraints
		if lq.shouldExtendCache(staleness) {
			lq.cache.recordHit()
			observ.Log("quote_cache_extended", map[string]any{
				"symbol":        symbol,
				"staleness_ms":  staleness.Milliseconds(),
				"reason":        "budget_preservation",
			})
			return &cached, nil
		}
	}
	
	lq.cache.recordMiss()
	
	// Determine which adapter to use based on live mode and health
	var quote *Quote
	var err error
	var fromLive bool
	
	if lq.config.LiveEnabled && lq.canUseLive() {
		// HOTPATH PROTECTION: Track live calls
		lq.metrics.incrementHotpathCall()
		
		quote, err = lq.provider.GetQuote(ctx, symbol)
		fromLive = true
		
		if err != nil {
			lq.recordError()
			
			// Fallback to cache if available
			if lq.config.FallbackToCache {
				if cachedEntry := lq.cache.getEntry(symbol); cachedEntry != nil {
					staleness := time.Since(cachedEntry.fetchedAt)
					if staleness.Seconds() <= float64(lq.config.FreshnessCeilingSeconds*3) { // Allow staler cache on error
						cached := *cachedEntry.quote // Copy the quote
						cached.StalenessMs = staleness.Milliseconds()
						observ.Log("quote_fallback_to_cache", map[string]any{
							"symbol":        symbol,
							"staleness_ms":  staleness.Milliseconds(),
							"error":         err.Error(),
						})
						return &cached, nil
					}
				}
			}
			
			// Final fallback to mock
			if lq.config.FallbackToMock {
				observ.Log("quote_fallback_to_mock", map[string]any{
					"symbol": symbol,
					"error":  err.Error(),
				})
				return lq.mockAdapter.GetQuote(ctx, symbol)
			}
			
			return nil, err
		}
	} else {
		// Use mock adapter
		quote, err = lq.mockAdapter.GetQuote(ctx, symbol)
		fromLive = false
		
		if err != nil {
			return nil, err
		}
	}
	
	// Record metrics
	latency := time.Since(startTime)
	lq.metrics.recordFreshness(quote.StalenessMs)
	lq.recordSuccess()
	
	// Cache the quote with appropriate tier
	tier := lq.getSymbolTier(symbol)
	lq.cache.putWithTier(symbol, quote, lq.cache.defaultTTL, tier)
	
	// Shadow mode comparison
	if lq.shadowMode && fromLive && lq.shouldSampleForShadow() {
		go lq.compareShadow(ctx, symbol, quote)
	}
	
	observ.Log("quote_fetched", map[string]any{
		"symbol":      symbol,
		"source":      quote.Source,
		"from_live":   fromLive,
		"latency_ms":  latency.Milliseconds(),
		"staleness_ms": quote.StalenessMs,
		"tier":        tier,
	})
	
	return quote, nil
}

// GetQuotes implements QuotesAdapter for batch requests
func (lq *LiveQuoteAdapter) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	results := make(map[string]*Quote)
	
	// Process each symbol individually
	for _, symbol := range symbols {
		if quote, err := lq.GetQuote(ctx, symbol); err == nil {
			results[symbol] = quote
		}
	}
	
	return results, nil
}

// HealthCheck implements QuotesAdapter with comprehensive health assessment
func (lq *LiveQuoteAdapter) HealthCheck(ctx context.Context) error {
	lq.mu.RLock()
	state := lq.healthState
	lq.mu.RUnlock()
	
	if state == HealthFailed {
		return fmt.Errorf("live quote adapter in failed state")
	}
	
	// Check metrics against thresholds
	metrics := lq.getMetricsSummary()
	
	// Check success rate
	if metrics.SuccessRate < lq.config.SuccessRateThreshold {
		return fmt.Errorf("success rate (%.2f) below threshold (%.2f)", 
			metrics.SuccessRate, lq.config.SuccessRateThreshold)
	}
	
	// Check P95 freshness
	if metrics.FreshnessP95 > lq.config.FreshnessP95ThresholdMs {
		return fmt.Errorf("P95 freshness (%dms) above threshold (%dms)",
			metrics.FreshnessP95, lq.config.FreshnessP95ThresholdMs)
	}
	
	// Check budget remaining
	budgetUsed, budgetTotal, _ := lq.budgetTracker.getBudgetStatus()
	budgetRemaining := float64(budgetTotal-budgetUsed) / float64(budgetTotal)
	if budgetRemaining < lq.config.BudgetWarningPct {
		return fmt.Errorf("budget remaining (%.1f%%) below warning threshold (%.1f%%)",
			budgetRemaining*100, lq.config.BudgetWarningPct*100)
	}
	
	return nil
}

// Close implements QuotesAdapter
func (lq *LiveQuoteAdapter) Close() error {
	if err := lq.provider.Close(); err != nil {
		return err
	}
	return lq.mockAdapter.Close()
}

// Helper methods

// isSymbolAllowed checks if symbol is allowed in current canary phase
func (lq *LiveQuoteAdapter) isSymbolAllowed(symbol string) bool {
	lq.mu.RLock()
	defer lq.mu.RUnlock()
	
	// If canary phase hasn't been expanded yet
	if !lq.canaryExpanded {
		// Check if canary duration has passed
		if time.Since(lq.canaryStartTime) > time.Duration(lq.config.CanaryDurationMinutes)*time.Minute {
			lq.mu.RUnlock()
			lq.mu.Lock()
			lq.canaryExpanded = true
			lq.mu.Unlock()
			lq.mu.RLock()
			
			observ.Log("canary_rollout_expanded", map[string]any{
				"duration_minutes": lq.config.CanaryDurationMinutes,
				"from_symbols":     lq.config.CanarySymbols,
				"to_symbols":       lq.config.PrioritySymbols,
			})
		} else {
			// Still in canary phase - only allow canary symbols
			for _, canarySymbol := range lq.config.CanarySymbols {
				if canarySymbol == symbol {
					return true
				}
			}
			return false
		}
	}
	
	// Canary expanded - check priority symbols
	for _, prioritySymbol := range lq.config.PrioritySymbols {
		if prioritySymbol == symbol {
			return true
		}
	}
	
	return false
}

// canUseLive determines if live adapter should be used based on health
func (lq *LiveQuoteAdapter) canUseLive() bool {
	lq.mu.RLock()
	defer lq.mu.RUnlock()
	
	// Check health state
	if lq.healthState == HealthFailed {
		return false
	}
	
	// Check budget
	return lq.budgetTracker.canMakeRequest()
}

// isCacheFresh checks if cached quote is fresh enough
func (lq *LiveQuoteAdapter) isCacheFresh(cached *Quote, staleness time.Duration) bool {
	session := GetCurrentSession()
	
	var maxAge time.Duration
	if session == SessionRegular {
		maxAge = time.Duration(lq.config.FreshnessCeilingSeconds) * time.Second
	} else {
		maxAge = time.Duration(lq.config.FreshnessCeilingAHSeconds) * time.Second
	}
	
	return staleness <= maxAge
}

// shouldExtendCache checks if cache should be extended due to budget constraints
func (lq *LiveQuoteAdapter) shouldExtendCache(staleness time.Duration) bool {
	if !lq.config.FallbackToCache {
		return false
	}
	
	// Extend cache if budget is low
	budgetUsed, budgetTotal, _ := lq.budgetTracker.getBudgetStatus()
	budgetRemaining := float64(budgetTotal-budgetUsed) / float64(budgetTotal)
	
	if budgetRemaining < lq.config.BudgetWarningPct {
		maxExtension := time.Duration(lq.config.CacheMaxAgeExtendSeconds) * time.Second
		return staleness <= maxExtension
	}
	
	return false
}

// getSymbolTier determines the refresh tier for a symbol
func (lq *LiveQuoteAdapter) getSymbolTier(symbol string) string {
	// This would typically integrate with portfolio state
	// For now, use a simple heuristic based on priority symbols
	for _, prioritySymbol := range lq.config.PrioritySymbols {
		if prioritySymbol == symbol {
			return "watchlist"
		}
	}
	return "others"
}

// shouldSampleForShadow determines if this quote should be used for shadow comparison
func (lq *LiveQuoteAdapter) shouldSampleForShadow() bool {
	return rand.Float64() < lq.config.ShadowSampleRate
}

// compareShadow performs shadow mode comparison asynchronously
func (lq *LiveQuoteAdapter) compareShadow(ctx context.Context, symbol string, liveQuote *Quote) {
	mockQuote, err := lq.mockAdapter.GetQuote(ctx, symbol)
	if err != nil {
		return // Skip comparison if mock fails
	}
	
	lq.metrics.incrementShadowSample()
	
	// Compare key fields
	spreadDiff := math.Abs(liveQuote.SpreadBps() - mockQuote.SpreadBps())
	midDiff := math.Abs((liveQuote.Bid+liveQuote.Ask)/2 - (mockQuote.Bid+mockQuote.Ask)/2)
	midPct := midDiff / ((liveQuote.Bid + liveQuote.Ask) / 2)
	
	// Consider it a mismatch if spread or mid differs significantly
	isMismatch := spreadDiff > 50 || midPct > 0.02 // 50bps spread diff or 2% mid diff
	
	if isMismatch {
		lq.metrics.incrementShadowMismatch()
		
		observ.Log("shadow_mismatch", map[string]any{
			"symbol":           symbol,
			"live_bid":         liveQuote.Bid,
			"live_ask":         liveQuote.Ask,
			"mock_bid":         mockQuote.Bid,
			"mock_ask":         mockQuote.Ask,
			"spread_diff_bps":  spreadDiff,
			"mid_diff_pct":     midPct * 100,
		})
	}
}

// recordError handles error tracking with hysteresis
func (lq *LiveQuoteAdapter) recordError() {
	lq.mu.Lock()
	defer lq.mu.Unlock()
	
	lq.metrics.incrementError()
	lq.consecutiveOks = 0
	lq.consecutiveBreaches++
	
	// Check for health state transitions
	oldState := lq.healthState
	var newState HealthState
	
	if lq.consecutiveBreaches >= lq.config.ConsecutiveBreachToDegrade {
		if lq.healthState == HealthHealthy {
			newState = HealthDegraded
		} else if lq.consecutiveBreaches >= lq.config.MaxConsecutiveErrors {
			newState = HealthFailed
		} else {
			newState = lq.healthState
		}
	} else {
		newState = lq.healthState
	}
	
	if newState != oldState {
		lq.healthState = newState
		lq.lastHealthTransition = time.Now()
		
		observ.Log("health_state_transition", map[string]any{
			"from":                oldState,
			"to":                  newState,
			"consecutive_breaches": lq.consecutiveBreaches,
			"consecutive_oks":      lq.consecutiveOks,
		})
	}
}

// recordSuccess handles success tracking with hysteresis
func (lq *LiveQuoteAdapter) recordSuccess() {
	lq.mu.Lock()
	defer lq.mu.Unlock()
	
	lq.metrics.incrementSuccess()
	lq.consecutiveBreaches = 0
	lq.consecutiveOks++
	
	// Check for recovery
	oldState := lq.healthState
	var newState HealthState
	
	if lq.consecutiveOks >= lq.config.ConsecutiveOkToRecover {
		if lq.healthState == HealthDegraded {
			newState = HealthHealthy
		} else if lq.healthState == HealthFailed {
			newState = HealthDegraded // Failed -> Degraded -> Healthy (gradual recovery)
		} else {
			newState = lq.healthState
		}
	} else {
		newState = lq.healthState
	}
	
	if newState != oldState {
		lq.healthState = newState
		lq.lastHealthTransition = time.Now()
		
		observ.Log("health_state_transition", map[string]any{
			"from":                oldState,
			"to":                  newState,
			"consecutive_breaches": lq.consecutiveBreaches,
			"consecutive_oks":      lq.consecutiveOks,
		})
	}
}

// MetricsSummary holds aggregated metrics
type MetricsSummary struct {
	Requests      int64   `json:"requests"`
	Successes     int64   `json:"successes"`
	Errors        int64   `json:"errors"`
	SuccessRate   float64 `json:"success_rate"`
	FreshnessP95  int64   `json:"freshness_p95_ms"`
	ShadowSamples int64   `json:"shadow_samples"`
	ShadowMismatches int64 `json:"shadow_mismatches"`
	ShadowMismatchRate float64 `json:"shadow_mismatch_rate"`
	HotpathCalls  int64   `json:"hotpath_calls"`
}

// getMetricsSummary returns current metrics summary
func (lq *LiveQuoteAdapter) getMetricsSummary() MetricsSummary {
	lq.metrics.mu.RLock()
	defer lq.metrics.mu.RUnlock()
	
	summary := MetricsSummary{
		Requests:        lq.metrics.requests,
		Successes:       lq.metrics.successes,
		Errors:          lq.metrics.errors,
		ShadowSamples:   lq.metrics.shadowSamples,
		ShadowMismatches: lq.metrics.shadowMismatches,
		HotpathCalls:    lq.metrics.hotpathCalls,
	}
	
	if summary.Requests > 0 {
		summary.SuccessRate = float64(summary.Successes) / float64(summary.Requests)
	}
	
	if summary.ShadowSamples > 0 {
		summary.ShadowMismatchRate = float64(summary.ShadowMismatches) / float64(summary.ShadowSamples)
	}
	
	// Calculate P95 freshness
	if len(lq.metrics.freshnessSamples) > 0 {
		samples := make([]int64, len(lq.metrics.freshnessSamples))
		copy(samples, lq.metrics.freshnessSamples)
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		
		p95Index := int(float64(len(samples)) * 0.95)
		if p95Index >= len(samples) {
			p95Index = len(samples) - 1
		}
		summary.FreshnessP95 = samples[p95Index]
	}
	
	return summary
}

// BoundedQuoteCache implementation

func (c *BoundedQuoteCache) get(symbol string) *Quote {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	entry, exists := c.entries[symbol]
	if !exists {
		return nil
	}
	
	// Check TTL
	if time.Since(entry.fetchedAt) > entry.ttl {
		return nil
	}
	
	// Return copy to prevent mutations
	quoteCopy := *entry.quote
	return &quoteCopy
}

func (c *BoundedQuoteCache) getEntry(symbol string) *BoundedCacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	entry, exists := c.entries[symbol]
	if !exists {
		return nil
	}
	
	// Check TTL
	if time.Since(entry.fetchedAt) > entry.ttl {
		return nil
	}
	
	return entry
}

func (c *BoundedQuoteCache) putWithTier(symbol string, quote *Quote, ttl time.Duration, tier string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// Evict if cache is full
	if len(c.entries) >= c.maxEntries {
		c.evictLRU()
	}
	
	priority := c.getTierPriority(tier)
	
	c.entries[symbol] = &BoundedCacheEntry{
		quote:     quote,
		fetchedAt: time.Now(),
		ttl:       ttl,
		tier:      tier,
		priority:  priority,
	}
}

func (c *BoundedQuoteCache) evictLRU() {
	var oldestSymbol string
	var oldestTime time.Time = time.Now()
	var lowestPriority int = math.MaxInt32
	
	// Find oldest entry with lowest priority
	for symbol, entry := range c.entries {
		if entry.priority < lowestPriority || 
		   (entry.priority == lowestPriority && entry.fetchedAt.Before(oldestTime)) {
			oldestTime = entry.fetchedAt
			oldestSymbol = symbol
			lowestPriority = entry.priority
		}
	}
	
	if oldestSymbol != "" {
		delete(c.entries, oldestSymbol)
		c.evictions++
	}
}

func (c *BoundedQuoteCache) getTierPriority(tier string) int {
	switch tier {
	case "positions":
		return 100 // Highest priority
	case "watchlist":
		return 50
	case "others":
		return 10  // Lowest priority
	default:
		return 10
	}
}

func (c *BoundedQuoteCache) recordHit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hits++
}

func (c *BoundedQuoteCache) recordMiss() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.misses++
}

// BudgetTracker implementation

func (bt *BudgetTracker) canMakeRequest() bool {
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	
	// Reset budget if needed
	if time.Now().After(bt.resetTime) {
		bt.mu.RUnlock()
		bt.mu.Lock()
		bt.requestsToday = 0
		bt.resetTime = time.Now().Add(24 * time.Hour)
		bt.mu.Unlock()
		bt.mu.RLock()
	}
	
	return bt.requestsToday < bt.dailyCap
}

func (bt *BudgetTracker) incrementRequest() {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.requestsToday++
	
	// Check for warning threshold
	remaining := float64(bt.dailyCap-bt.requestsToday) / float64(bt.dailyCap)
	if remaining < bt.warningPct && time.Since(bt.lastWarning) > time.Hour {
		bt.lastWarning = time.Now()
		
		observ.Log("budget_warning", map[string]any{
			"requests_used":  bt.requestsToday,
			"daily_cap":      bt.dailyCap,
			"remaining_pct":  remaining * 100,
		})
	}
}

func (bt *BudgetTracker) getBudgetStatus() (used, total int, resetTime time.Time) {
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	return bt.requestsToday, bt.dailyCap, bt.resetTime
}

// QuoteMetrics implementation

func (qm *QuoteMetrics) incrementRequest() {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.requests++
}

func (qm *QuoteMetrics) incrementSuccess() {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.successes++
}

func (qm *QuoteMetrics) incrementError() {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.errors++
}

func (qm *QuoteMetrics) recordFreshness(staleness int64) {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	
	// Add to ring buffer
	qm.freshnessSamples = append(qm.freshnessSamples, staleness)
	
	// Limit buffer size
	if len(qm.freshnessSamples) > 1000 {
		qm.freshnessSamples = qm.freshnessSamples[1:]
	}
}

func (qm *QuoteMetrics) incrementShadowSample() {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.shadowSamples++
}

func (qm *QuoteMetrics) incrementShadowMismatch() {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.shadowMismatches++
}

func (qm *QuoteMetrics) incrementHotpathCall() {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.hotpathCalls++
}

// GetHotpathCalls returns the number of live calls made (for testing hotpath protection)
func (lq *LiveQuoteAdapter) GetHotpathCalls() int64 {
	lq.metrics.mu.RLock()
	defer lq.metrics.mu.RUnlock()
	return lq.metrics.hotpathCalls
}