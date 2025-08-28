package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// TestPromotionGatesIntegration tests all promotion gate criteria end-to-end
func TestPromotionGatesIntegration(t *testing.T) {
	// Set up test environment
	observ.SetVersion("test-1.0.0")
	
	// Create test health server
	healthServer := httptest.NewServer(observ.HealthHandler())
	defer healthServer.Close()
	
	// Create reliable test provider
	testProvider := &reliableTestProvider{
		quotes: map[string]*Quote{
			"AAPL": {Symbol: "AAPL", Bid: 150.0, Ask: 150.1, Last: 150.05, Source: "alphavantage", StalenessMs: 1000},
			"SPY":  {Symbol: "SPY", Bid: 400.0, Ask: 400.1, Last: 400.05, Source: "alphavantage", StalenessMs: 800},
		},
	}
	
	config := LiveQuoteConfig{
		LiveEnabled:             true,
		ShadowMode:             true,
		CanarySymbols:          []string{"AAPL", "SPY"},
		PrioritySymbols:        []string{"AAPL", "SPY"},
		CanaryDurationMinutes:  1,
		FreshnessP95ThresholdMs: 5000,
		SuccessRateThreshold:   0.99,
		ShadowSampleRate:       1.0,
		CacheMaxEntries:        1000,
		CacheTTLSeconds:       30,
		DailyRequestCap:       1000,
		BudgetWarningPct:      0.1,
		FallbackToMock:        true,
		ConsecutiveBreachToDegrade: 3,
		ConsecutiveOkToRecover: 3,
	}
	
	// Create live adapter with integration
	liveAdapter, err := NewLiveQuoteAdapter(testProvider, config)
	require.NoError(t, err)
	
	liveFeedsConfig := LiveFeedsConfig{
		Quotes: LiveQuoteFeedConfig{
			LiveEnabled:           config.LiveEnabled,
			ShadowMode:           config.ShadowMode,
			Provider:             "alphavantage",
			CanarySymbols:        config.CanarySymbols,
			PrioritySymbols:      config.PrioritySymbols,
			CanaryDurationMinutes: config.CanaryDurationMinutes,
			DailyRequestCap:      config.DailyRequestCap,
			BudgetWarningPct:     config.BudgetWarningPct,
			ShadowSampleRate:     config.ShadowSampleRate,
			FallbackToCache:      config.FallbackToCache,
			FallbackToMock:       config.FallbackToMock,
		},
	}
	
	liveFeedsConfig.Quotes.Tiers.PositionsMs = 800
	liveFeedsConfig.Quotes.Tiers.WatchlistMs = 2500
	liveFeedsConfig.Quotes.Tiers.OthersMs = 6000
	liveFeedsConfig.Quotes.FreshnessCeilingSeconds = 5
	liveFeedsConfig.Quotes.FreshnessCeilingAHSeconds = 60
	liveFeedsConfig.Quotes.HysteresisSeconds = 3
	liveFeedsConfig.Quotes.ConsecutiveBreachToDegrade = 3
	liveFeedsConfig.Quotes.ConsecutiveOkToRecover = 3
	liveFeedsConfig.Quotes.Cache.MaxEntries = 1000
	liveFeedsConfig.Quotes.Cache.TTLSeconds = 30
	liveFeedsConfig.Quotes.Health.FreshnessP95ThresholdMs = 5000
	liveFeedsConfig.Quotes.Health.SuccessRateThreshold = 0.99
	
	integration, err := NewLiveQuoteIntegration(liveAdapter, liveFeedsConfig)
	require.NoError(t, err)
	
	ctx := context.Background()
	
	// Test Phase 1: Generate sufficient data for promotion gates
	t.Run("generate_promotion_data", func(t *testing.T) {
		// Generate at least 50 requests (minimum samples for promotion)
		for i := 0; i < 60; i++ {
			symbol := "AAPL"
			if i%3 == 0 {
				symbol = "SPY"
			}
			
			quote, err := integration.GetQuote(ctx, symbol)
			require.NoError(t, err)
			assert.NotNil(t, quote)
			assert.Equal(t, "alphavantage", quote.Source)
			
			// Vary cache behavior to generate different patterns
			if i%10 == 0 {
				// Clear cache occasionally to force fresh requests
				liveAdapter.cache.mu.Lock()
				liveAdapter.cache.entries = make(map[string]*BoundedCacheEntry)
				liveAdapter.cache.mu.Unlock()
			}
			
			// Small delay to simulate realistic timing
			time.Sleep(10 * time.Millisecond)
		}
		
		// Wait for shadow comparisons to complete
		time.Sleep(500 * time.Millisecond)
	})
	
	// Test Phase 2: Validate promotion gate criteria
	t.Run("validate_promotion_gates", func(t *testing.T) {
		// Get promotion metrics
		promotionMetrics := integration.GetPromotionMetrics()
		
		// Gate 1: Minimum samples (≥50)
		requests := promotionMetrics["requests"].(int64)
		assert.GreaterOrEqual(t, requests, int64(50), "Gate 1: Minimum samples")
		
		// Gate 2: Success rate (≥99%)
		successRate := promotionMetrics["success_rate"].(float64)
		assert.GreaterOrEqual(t, successRate, 0.99, "Gate 2: Success rate ≥99%%")
		
		// Gate 3: Freshness P95 (≤5000ms)
		freshnessP95 := promotionMetrics["freshness_p95_ms"].(int64)
		assert.LessOrEqual(t, freshnessP95, int64(5000), "Gate 3: Freshness P95 ≤5000ms")
		
		// Gate 4: Hotpath calls (tracked properly)
		hotpathCalls := promotionMetrics["hotpath_calls"].(int64)
		assert.GreaterOrEqual(t, hotpathCalls, int64(1), "Gate 4: Hotpath calls tracked")
		
		// Gate 5: Shadow mode metrics (valid)
		shadowSamples := promotionMetrics["shadow_samples"].(int64)
		assert.GreaterOrEqual(t, shadowSamples, int64(1), "Gate 5: Shadow samples generated")
		
		shadowMismatchRate := promotionMetrics["shadow_mismatch_rate"].(float64)
		assert.LessOrEqual(t, shadowMismatchRate, 0.05, "Gate 5: Shadow mismatch rate ≤5%")
		
		t.Logf("Promotion Gates Summary:")
		t.Logf("  Requests: %d", requests)
		t.Logf("  Success Rate: %.2f%%", successRate*100)
		t.Logf("  Freshness P95: %dms", freshnessP95)
		t.Logf("  Hotpath Calls: %d", hotpathCalls)
		t.Logf("  Shadow Samples: %d", shadowSamples)
		t.Logf("  Shadow Mismatch Rate: %.2f%%", shadowMismatchRate*100)
	})
	
	// Test Phase 3: Health endpoint validation
	t.Run("validate_health_endpoint", func(t *testing.T) {
		// Make request to health endpoint
		resp, err := http.Get(healthServer.URL)
		require.NoError(t, err)
		defer resp.Body.Close()
		
		// Should return 200 OK for healthy system
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		
		// Parse health response
		var health observ.HealthStatus
		err = json.NewDecoder(resp.Body).Decode(&health)
		require.NoError(t, err)
		
		// Validate health structure
		assert.Equal(t, "healthy", health.Status)
		assert.NotEmpty(t, health.Timestamp)
		assert.NotEmpty(t, health.Uptime)
		assert.Equal(t, "test-1.0.0", health.Version)
		
		// Validate promotion gate metrics in health response
		assert.Greater(t, health.Metrics.SuccessRate, 0.99)
		assert.LessOrEqual(t, health.Metrics.FreshnessP95Ms, int64(5000))
		assert.GreaterOrEqual(t, health.Metrics.HotpathCalls, int64(0))
		assert.GreaterOrEqual(t, health.Metrics.ShadowSamples, int64(0))
		
		t.Logf("Health Status: %s", health.Status)
		t.Logf("Health Metrics: %+v", health.Metrics)
	})
	
	// Test Phase 4: Canary rollout validation
	t.Run("validate_canary_rollout", func(t *testing.T) {
		canaryStatus := integration.GetCanaryStatus()
		
		// Should have started canary
		assert.NotNil(t, canaryStatus["canary_started"])
		
		// May or may not be expanded depending on timing
		expanded := canaryStatus["canary_expanded"].(bool)
		t.Logf("Canary Expanded: %t", expanded)
		
		// Health state should be healthy
		healthState := canaryStatus["health_state"].(string)
		assert.Equal(t, "healthy", healthState)
	})
	
	// Test Phase 5: Cache performance validation
	t.Run("validate_cache_performance", func(t *testing.T) {
		liveAdapter.cache.mu.RLock()
		cacheSize := len(liveAdapter.cache.entries)
		cacheHits := liveAdapter.cache.hits
		cacheMisses := liveAdapter.cache.misses
		cacheEvictions := liveAdapter.cache.evictions
		liveAdapter.cache.mu.RUnlock()
		
		// Cache should have reasonable hit rate
		totalCacheRequests := cacheHits + cacheMisses
		if totalCacheRequests > 0 {
			hitRate := float64(cacheHits) / float64(totalCacheRequests)
			assert.Greater(t, hitRate, 0.1, "Cache hit rate should be > 10%")
			t.Logf("Cache Hit Rate: %.2f%%", hitRate*100)
		}
		
		// Cache should not exceed bounds
		assert.LessOrEqual(t, cacheSize, config.CacheMaxEntries)
		
		t.Logf("Cache Metrics:")
		t.Logf("  Size: %d/%d", cacheSize, config.CacheMaxEntries)
		t.Logf("  Hits: %d", cacheHits)
		t.Logf("  Misses: %d", cacheMisses)
		t.Logf("  Evictions: %d", cacheEvictions)
	})
	
	// Test Phase 6: Budget tracking validation
	t.Run("validate_budget_tracking", func(t *testing.T) {
		budgetUsed, budgetTotal, resetTime := liveAdapter.budgetTracker.getBudgetStatus()
		
		// Budget should be tracked properly
		assert.Greater(t, budgetUsed, 0, "Budget usage should be tracked")
		assert.Equal(t, config.DailyRequestCap, budgetTotal)
		assert.True(t, resetTime.After(time.Now()), "Reset time should be in future")
		
		budgetRemainingPct := float64(budgetTotal-budgetUsed) / float64(budgetTotal)
		
		t.Logf("Budget Status:")
		t.Logf("  Used: %d/%d", budgetUsed, budgetTotal)
		t.Logf("  Remaining: %.1f%%", budgetRemainingPct*100)
		t.Logf("  Reset Time: %s", resetTime.Format(time.RFC3339))
	})
}

// TestPromotionGatesFailureScenarios tests scenarios where promotion gates should fail
func TestPromotionGatesFailureScenarios(t *testing.T) {
	t.Run("high_error_rate_fails_promotion", func(t *testing.T) {
		// Create provider that fails 20% of the time
		flakyProvider := &flakyTestProvider{
			errorRate: 0.2,
			quotes: map[string]*Quote{
				"AAPL": {Symbol: "AAPL", Bid: 150.0, Ask: 150.1, Last: 150.05, Source: "alphavantage"},
			},
		}
		
		config := LiveQuoteConfig{
			LiveEnabled:             true,
			ShadowMode:             false,
			CanarySymbols:          []string{"AAPL"},
			PrioritySymbols:        []string{"AAPL"},
			SuccessRateThreshold:   0.99, // Requires 99% success rate
			CacheMaxEntries:        100,
			CacheTTLSeconds:       1, // Short TTL to force fresh requests
			DailyRequestCap:       1000,
			ConsecutiveBreachToDegrade: 3,
			ConsecutiveOkToRecover: 3,
			FallbackToMock:        false, // Don't fallback to test error rate
		}
		
		liveAdapter, err := NewLiveQuoteAdapter(flakyProvider, config)
		require.NoError(t, err)
		
		ctx := context.Background()
		
		// Generate requests to accumulate error rate
		for i := 0; i < 50; i++ {
			_, _ = liveAdapter.GetQuote(ctx, "AAPL") // Ignore errors
			time.Sleep(5 * time.Millisecond)
		}
		
		// Check that health check fails due to success rate
		err = liveAdapter.HealthCheck(ctx)
		assert.Error(t, err, "Health check should fail with high error rate")
		assert.Contains(t, err.Error(), "success rate", "Error should mention success rate")
	})
	
	t.Run("stale_quotes_fail_promotion", func(t *testing.T) {
		// Create provider that returns very stale quotes
		staleProvider := &testQuoteProvider{
			quotes: map[string]*Quote{
				"AAPL": {Symbol: "AAPL", Bid: 150.0, Ask: 150.1, Last: 150.05, Source: "alphavantage", StalenessMs: 10000}, // 10s stale
			},
		}
		
		config := LiveQuoteConfig{
			LiveEnabled:             true,
			ShadowMode:             false,
			CanarySymbols:          []string{"AAPL"},
			PrioritySymbols:        []string{"AAPL"},
			FreshnessP95ThresholdMs: 5000, // Max 5s freshness
			SuccessRateThreshold:   0.99,
			CacheMaxEntries:        100,
			CacheTTLSeconds:       1, // Short TTL to force fresh requests
			DailyRequestCap:       1000,
			FallbackToMock:        true,
		}
		
		liveAdapter, err := NewLiveQuoteAdapter(staleProvider, config)
		require.NoError(t, err)
		
		ctx := context.Background()
		
		// Generate requests with stale data
		for i := 0; i < 30; i++ {
			_, _ = liveAdapter.GetQuote(ctx, "AAPL")
			time.Sleep(5 * time.Millisecond)
		}
		
		// Check that health check fails due to freshness
		err = liveAdapter.HealthCheck(ctx)
		assert.Error(t, err, "Health check should fail with stale quotes")
		assert.Contains(t, err.Error(), "freshness", "Error should mention freshness")
	})
}

// TestGracefulShutdown tests graceful shutdown functionality
func TestGracefulShutdown(t *testing.T) {
	// Create test adapter
	testProvider := &testQuoteProvider{
		quotes: map[string]*Quote{
			"AAPL": {Symbol: "AAPL", Bid: 150.0, Ask: 150.1, Last: 150.05, Source: "test"},
		},
	}
	
	config := LiveQuoteConfig{
		LiveEnabled:     true,
		CanarySymbols:   []string{"AAPL"},
		PrioritySymbols: []string{"AAPL"},
		CacheMaxEntries: 100,
		CacheTTLSeconds: 60,
		DailyRequestCap: 1000,
	}
	
	adapter, err := NewLiveQuoteAdapter(testProvider, config)
	require.NoError(t, err)
	
	// Create graceful shutdown manager
	persistence := NewStatePersistenceManager("/tmp/test_adapter_state.json", 5*time.Second)
	shutdown := NewGracefulShutdownManager(persistence, 10*time.Second)
	
	// Register adapter
	shutdown.RegisterAdapter(adapter)
	
	// Start persistence
	persistence.Start()
	
	// Make some requests to generate state
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		quote, err := adapter.GetQuote(ctx, "AAPL")
		require.NoError(t, err)
		assert.NotNil(t, quote)
		time.Sleep(10 * time.Millisecond)
	}
	
	// Perform graceful shutdown
	start := time.Now()
	err = shutdown.Shutdown()
	shutdownDuration := time.Since(start)
	
	// Shutdown should succeed within timeout
	assert.NoError(t, err)
	assert.Less(t, shutdownDuration, 10*time.Second)
	
	t.Logf("Graceful shutdown completed in %v", shutdownDuration)
}

// Helper test providers

type reliableTestProvider struct {
	quotes map[string]*Quote
}

func (p *reliableTestProvider) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	if quote, exists := p.quotes[symbol]; exists {
		result := *quote
		result.Timestamp = time.Now()
		// Add small random staleness for realism
		result.StalenessMs = int64(500 + (len(symbol) * 100)) // Deterministic but varied
		return &result, nil
	}
	return nil, NewBadSymbolError(symbol, "symbol not found")
}

func (p *reliableTestProvider) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	results := make(map[string]*Quote)
	for _, symbol := range symbols {
		if quote, err := p.GetQuote(ctx, symbol); err == nil {
			results[symbol] = quote
		}
	}
	return results, nil
}

func (p *reliableTestProvider) HealthCheck(ctx context.Context) error {
	return nil
}

func (p *reliableTestProvider) Close() error {
	return nil
}

type flakyTestProvider struct {
	errorRate float64
	quotes    map[string]*Quote
	callCount int
}

func (p *flakyTestProvider) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	p.callCount++
	
	// Deterministic failure based on error rate
	if float64(p.callCount%int(1/p.errorRate)) == 0 {
		return nil, NewProviderError(symbol, "simulated flaky error", nil)
	}
	
	if quote, exists := p.quotes[symbol]; exists {
		result := *quote
		result.Timestamp = time.Now()
		return &result, nil
	}
	return nil, NewBadSymbolError(symbol, "symbol not found")
}

func (p *flakyTestProvider) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	results := make(map[string]*Quote)
	for _, symbol := range symbols {
		if quote, err := p.GetQuote(ctx, symbol); err == nil {
			results[symbol] = quote
		}
	}
	return results, nil
}

func (p *flakyTestProvider) HealthCheck(ctx context.Context) error {
	return nil
}

func (p *flakyTestProvider) Close() error {
	return nil
}