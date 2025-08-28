package adapters

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// LiveQuoteIntegration wraps the quote adapter with live feed configuration and metrics
type LiveQuoteIntegration struct {
	liveAdapter *LiveQuoteAdapter
	baseAdapter QuotesAdapter
	config      LiveFeedsConfig
}

// LiveFeedsConfig represents the live feeds configuration
type LiveFeedsConfig struct {
	Quotes LiveQuoteFeedConfig `yaml:"quotes"`
}

// LiveQuoteFeedConfig represents the quotes section of live feeds config
type LiveQuoteFeedConfig struct {
	LiveEnabled           bool     `yaml:"live_enabled"`
	ShadowMode           bool     `yaml:"shadow_mode"`
	Provider             string   `yaml:"provider"`
	CanarySymbols        []string `yaml:"canary_symbols"`
	PrioritySymbols      []string `yaml:"priority_symbols"`
	CanaryDurationMinutes int     `yaml:"canary_duration_minutes"`
	
	Tiers struct {
		PositionsMs  int `yaml:"positions_ms"`
		WatchlistMs  int `yaml:"watchlist_ms"`
		OthersMs     int `yaml:"others_ms"`
	} `yaml:"tiers"`
	
	FreshnessCeilingSeconds     int `yaml:"freshness_ceiling_seconds"`
	FreshnessCeilingAHSeconds   int `yaml:"freshness_ceiling_ah_seconds"`
	HysteresisSeconds          int `yaml:"hysteresis_seconds"`
	ConsecutiveBreachToDegrade int `yaml:"consecutive_breach_to_degrade"`
	ConsecutiveOkToRecover     int `yaml:"consecutive_ok_to_recover"`
	
	Cache struct {
		MaxEntries           int `yaml:"max_entries"`
		TTLSeconds          int `yaml:"ttl_seconds"`
		MaxAgeExtendSeconds int `yaml:"max_age_extend_seconds"`
	} `yaml:"cache"`
	
	RequestsPerMinute      int     `yaml:"requests_per_minute"`
	DailyRequestCap       int     `yaml:"daily_request_cap"`
	BudgetWarningPct      float64 `yaml:"budget_warning_pct"`
	ShadowSampleRate      float64 `yaml:"shadow_sample_rate"`
	
	Health struct {
		DegradedErrorRate      float64 `yaml:"degraded_error_rate"`
		FailedErrorRate        float64 `yaml:"failed_error_rate"`
		MaxConsecutiveErrors   int     `yaml:"max_consecutive_errors"`
		FreshnessP95ThresholdMs int64  `yaml:"freshness_p95_threshold_ms"`
		SuccessRateThreshold   float64 `yaml:"success_rate_threshold"`
	} `yaml:"health"`
	
	FallbackToCache bool `yaml:"fallback_to_cache"`
	FallbackToMock  bool `yaml:"fallback_to_mock"`
}

// NewLiveQuoteIntegration creates a live quote integration with configuration
func NewLiveQuoteIntegration(baseAdapter QuotesAdapter, config LiveFeedsConfig) (*LiveQuoteIntegration, error) {
	// Check environment variable overrides
	liveEnabled := config.Quotes.LiveEnabled
	if envLive := os.Getenv("LIVE_QUOTES_ENABLED"); envLive != "" {
		if parsed, err := strconv.ParseBool(envLive); err == nil {
			liveEnabled = parsed
		}
	}
	
	shadowMode := config.Quotes.ShadowMode
	if envShadow := os.Getenv("SHADOW_MODE_ENABLED"); envShadow != "" {
		if parsed, err := strconv.ParseBool(envShadow); err == nil {
			shadowMode = parsed
		}
	}
	
	// Apply kill switches
	if os.Getenv("DISABLE_LIVE_QUOTES") == "true" {
		liveEnabled = false
	}
	if os.Getenv("FORCE_MOCK_MODE") == "true" {
		liveEnabled = false
		shadowMode = false
	}
	
	// Create live adapter configuration
	liveConfig := LiveQuoteConfig{
		LiveEnabled:           liveEnabled,
		ShadowMode:           shadowMode,
		CanarySymbols:        config.Quotes.CanarySymbols,
		PrioritySymbols:      config.Quotes.PrioritySymbols,
		CanaryDurationMinutes: config.Quotes.CanaryDurationMinutes,
		PositionsRefreshMs:   config.Quotes.Tiers.PositionsMs,
		WatchlistRefreshMs:   config.Quotes.Tiers.WatchlistMs,
		OthersRefreshMs:      config.Quotes.Tiers.OthersMs,
		FreshnessCeilingSeconds:     config.Quotes.FreshnessCeilingSeconds,
		FreshnessCeilingAHSeconds:   config.Quotes.FreshnessCeilingAHSeconds,
		HysteresisSeconds:          config.Quotes.HysteresisSeconds,
		ConsecutiveBreachToDegrade: config.Quotes.ConsecutiveBreachToDegrade,
		ConsecutiveOkToRecover:     config.Quotes.ConsecutiveOkToRecover,
		CacheMaxEntries:           config.Quotes.Cache.MaxEntries,
		CacheTTLSeconds:          config.Quotes.Cache.TTLSeconds,
		CacheMaxAgeExtendSeconds: config.Quotes.Cache.MaxAgeExtendSeconds,
		DailyRequestCap:    config.Quotes.DailyRequestCap,
		BudgetWarningPct:   config.Quotes.BudgetWarningPct,
		ShadowSampleRate:   config.Quotes.ShadowSampleRate,
		DegradedErrorRate:      config.Quotes.Health.DegradedErrorRate,
		FailedErrorRate:        config.Quotes.Health.FailedErrorRate,
		MaxConsecutiveErrors:   config.Quotes.Health.MaxConsecutiveErrors,
		FreshnessP95ThresholdMs: config.Quotes.Health.FreshnessP95ThresholdMs,
		SuccessRateThreshold:   config.Quotes.Health.SuccessRateThreshold,
		FallbackToCache: config.Quotes.FallbackToCache,
		FallbackToMock:  config.Quotes.FallbackToMock,
	}
	
	// Create live adapter
	liveAdapter, err := NewLiveQuoteAdapter(baseAdapter, liveConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create live quote adapter: %w", err)
	}
	
	integration := &LiveQuoteIntegration{
		liveAdapter: liveAdapter,
		baseAdapter: baseAdapter,
		config:     config,
	}
	
	// Set up metrics reporting
	integration.setupMetricsReporting()
	
	observ.Log("live_quote_integration_created", map[string]any{
		"live_enabled":     liveEnabled,
		"shadow_mode":      shadowMode,
		"provider":         config.Quotes.Provider,
		"canary_symbols":   config.Quotes.CanarySymbols,
		"priority_symbols": config.Quotes.PrioritySymbols,
		"daily_cap":        config.Quotes.DailyRequestCap,
	})
	
	return integration, nil
}

// GetQuote implements QuotesAdapter interface
func (li *LiveQuoteIntegration) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	startTime := time.Now()
	
	// Record the request
	observ.IncCounter("quote_requests_total", map[string]string{
		"symbol":   symbol,
		"provider": li.config.Quotes.Provider,
	})
	
	quote, err := li.liveAdapter.GetQuote(ctx, symbol)
	
	// Record metrics
	latency := time.Since(startTime)
	observ.RecordDuration("decision_latency", latency, map[string]string{
		"component": "quote_fetch",
		"symbol":    symbol,
	})
	
	if err != nil {
		observ.IncCounter("quote_errors_total", map[string]string{
			"symbol":     symbol,
			"provider":   li.config.Quotes.Provider,
			"error_type": classifyError(err),
		})
		return nil, err
	}
	
	// Record success
	observ.IncCounter("quote_successes_total", map[string]string{
		"symbol":   symbol,
		"provider": li.config.Quotes.Provider,
		"source":   quote.Source,
	})
	
	// Record freshness
	observ.RecordHistogram("quote_freshness", float64(quote.StalenessMs), map[string]string{
		"symbol": symbol,
		"source": quote.Source,
	})
	
	return quote, nil
}

// GetQuotes implements QuotesAdapter interface
func (li *LiveQuoteIntegration) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	return li.liveAdapter.GetQuotes(ctx, symbols)
}

// HealthCheck implements QuotesAdapter interface
func (li *LiveQuoteIntegration) HealthCheck(ctx context.Context) error {
	return li.liveAdapter.HealthCheck(ctx)
}

// Close implements QuotesAdapter interface
func (li *LiveQuoteIntegration) Close() error {
	return li.liveAdapter.Close()
}

// GetPromotionMetrics returns metrics needed for promotion gate evaluation
func (li *LiveQuoteIntegration) GetPromotionMetrics() map[string]any {
	summary := li.liveAdapter.getMetricsSummary()
	
	return map[string]any{
		"requests":              summary.Requests,
		"successes":             summary.Successes,
		"errors":                summary.Errors,
		"success_rate":          summary.SuccessRate,
		"freshness_p95_ms":      summary.FreshnessP95,
		"shadow_samples":        summary.ShadowSamples,
		"shadow_mismatches":     summary.ShadowMismatches,
		"shadow_mismatch_rate":  summary.ShadowMismatchRate,
		"hotpath_calls":         summary.HotpathCalls,
	}
}

// GetCanaryStatus returns the current canary rollout status
func (li *LiveQuoteIntegration) GetCanaryStatus() map[string]any {
	li.liveAdapter.mu.RLock()
	defer li.liveAdapter.mu.RUnlock()
	
	return map[string]any{
		"canary_started":  li.liveAdapter.canaryStartTime.Format(time.RFC3339),
		"canary_expanded": li.liveAdapter.canaryExpanded,
		"health_state":    string(li.liveAdapter.healthState),
		"consecutive_breaches": li.liveAdapter.consecutiveBreaches,
		"consecutive_oks": li.liveAdapter.consecutiveOks,
	}
}

// setupMetricsReporting sets up periodic metrics reporting
func (li *LiveQuoteIntegration) setupMetricsReporting() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		
		for range ticker.C {
			li.updateMetricsGauges()
		}
	}()
	
	// Update metrics immediately
	li.updateMetricsGauges()
}

// updateMetricsGauges updates gauge metrics from the live adapter
func (li *LiveQuoteIntegration) updateMetricsGauges() {
	// Get current metrics
	summary := li.liveAdapter.getMetricsSummary()
	
	// Update gauges for healthz endpoint
	observ.SetGauge("quote_success_rate", summary.SuccessRate, map[string]string{"provider": li.config.Quotes.Provider})
	observ.SetGauge("quote_freshness_p95_ms", float64(summary.FreshnessP95), map[string]string{"provider": li.config.Quotes.Provider})
	observ.SetGauge("shadow_mismatch_rate", summary.ShadowMismatchRate, nil)
	observ.SetGauge("hotpath_live_calls_total", float64(summary.HotpathCalls), nil)
	
	// Cache metrics
	cacheHits, cacheTotal := li.liveAdapter.cache.hits, li.liveAdapter.cache.hits + li.liveAdapter.cache.misses
	cacheHitRate := 0.0
	if cacheTotal > 0 {
		cacheHitRate = float64(cacheHits) / float64(cacheTotal)
	}
	observ.SetGauge("quote_cache_hit_rate", cacheHitRate, nil)
	observ.SetGauge("quote_cache_size", float64(len(li.liveAdapter.cache.entries)), nil)
	observ.SetGauge("quote_cache_evictions_total", float64(li.liveAdapter.cache.evictions), nil)
	
	// Budget tracking
	budgetUsed, budgetTotal, _ := li.liveAdapter.budgetTracker.getBudgetStatus()
	observ.SetGauge("provider_budget_used", float64(budgetUsed), map[string]string{"provider": li.config.Quotes.Provider})
	observ.SetGauge("provider_budget_total", float64(budgetTotal), map[string]string{"provider": li.config.Quotes.Provider})
	
	// Health status (0=failed, 1=degraded, 2=healthy)
	var healthValue float64
	switch li.liveAdapter.healthState {
	case HealthFailed:
		healthValue = 0
	case HealthDegraded:
		healthValue = 1
	case HealthHealthy:
		healthValue = 2
	}
	observ.SetGauge("provider_health_status", healthValue, map[string]string{"provider": li.config.Quotes.Provider})
	
	// Feature flags
	liveEnabledValue := 0.0
	if li.liveAdapter.config.LiveEnabled {
		liveEnabledValue = 1.0
	}
	shadowModeValue := 0.0
	if li.liveAdapter.config.ShadowMode {
		shadowModeValue = 1.0
	}
	observ.SetGauge("live_quotes_enabled", liveEnabledValue, nil)
	observ.SetGauge("shadow_mode_enabled", shadowModeValue, nil)
	observ.SetGauge("provider_active", 1, map[string]string{"provider": li.config.Quotes.Provider})
	
	// Shadow mode counters
	observ.SetGauge("shadow_samples_total", float64(summary.ShadowSamples), nil)
	observ.SetGauge("shadow_mismatches_total", float64(summary.ShadowMismatches), nil)
}

// classifyError classifies errors for metrics
func classifyError(err error) string {
	if quoteErr, ok := err.(*QuoteError); ok {
		return quoteErr.Type
	}
	
	errMsg := strings.ToLower(err.Error())
	if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
		return "timeout"
	}
	if strings.Contains(errMsg, "network") || strings.Contains(errMsg, "connection") {
		return "network"
	}
	if strings.Contains(errMsg, "rate") || strings.Contains(errMsg, "limit") {
		return "rate_limit"
	}
	if strings.Contains(errMsg, "context canceled") {
		return "canceled"
	}
	
	return "unknown"
}

// GetDefaultLiveFeedsConfig returns sensible defaults for live feeds configuration
func GetDefaultLiveFeedsConfig() LiveFeedsConfig {
	config := LiveFeedsConfig{}
	
	// Quotes configuration with canary approach
	config.Quotes.LiveEnabled = false
	config.Quotes.ShadowMode = true
	config.Quotes.Provider = "alphavantage"
	config.Quotes.CanarySymbols = []string{"AAPL", "SPY"}
	config.Quotes.PrioritySymbols = []string{"AAPL", "NVDA", "SPY", "TSLA", "QQQ"}
	config.Quotes.CanaryDurationMinutes = 15
	
	// Adaptive refresh tiers
	config.Quotes.Tiers.PositionsMs = 800
	config.Quotes.Tiers.WatchlistMs = 2500
	config.Quotes.Tiers.OthersMs = 6000
	
	// Freshness and hysteresis
	config.Quotes.FreshnessCeilingSeconds = 5
	config.Quotes.FreshnessCeilingAHSeconds = 60
	config.Quotes.HysteresisSeconds = 3
	config.Quotes.ConsecutiveBreachToDegrade = 3
	config.Quotes.ConsecutiveOkToRecover = 5
	
	// Cache bounds
	config.Quotes.Cache.MaxEntries = 2000
	config.Quotes.Cache.TTLSeconds = 60
	config.Quotes.Cache.MaxAgeExtendSeconds = 300
	
	// Budget management
	config.Quotes.RequestsPerMinute = 5
	config.Quotes.DailyRequestCap = 300
	config.Quotes.BudgetWarningPct = 0.15
	config.Quotes.ShadowSampleRate = 0.2
	
	// Health thresholds
	config.Quotes.Health.DegradedErrorRate = 0.01
	config.Quotes.Health.FailedErrorRate = 0.05
	config.Quotes.Health.MaxConsecutiveErrors = 3
	config.Quotes.Health.FreshnessP95ThresholdMs = 5000
	config.Quotes.Health.SuccessRateThreshold = 0.99
	
	// Fallback behavior
	config.Quotes.FallbackToCache = true
	config.Quotes.FallbackToMock = true
	
	return config
}