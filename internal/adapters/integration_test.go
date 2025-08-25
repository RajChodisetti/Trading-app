package adapters

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"
)

// TestLiveDataIntegration validates that all risk gates work with live data feeds
func TestLiveDataIntegration(t *testing.T) {
	// Skip in CI unless explicitly enabled
	if os.Getenv("LIVE_INTEGRATION_TESTS") != "true" {
		t.Skip("Live integration tests disabled (set LIVE_INTEGRATION_TESTS=true to enable)")
	}
	
	// Require API keys for live testing
	apiKey := os.Getenv("ALPHAVANTAGE_API_KEY")
	if apiKey == "" {
		t.Skip("ALPHAVANTAGE_API_KEY not set - skipping live integration tests")
	}
	
	logger := log.New(os.Stdout, "[INTEGRATION] ", log.LstdFlags)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	
	t.Run("quotes_live_integration", func(t *testing.T) {
		testQuotesLiveIntegration(t, ctx, apiKey, logger)
	})
	
	t.Run("cache_performance", func(t *testing.T) {
		testCachePerformance(t, ctx, logger)
	})
	
	t.Run("provider_health_monitoring", func(t *testing.T) {
		testProviderHealthMonitoring(t, ctx, logger)
	})
	
	t.Run("fallback_behavior", func(t *testing.T) {
		testFallbackBehavior(t, ctx, logger)
	})
	
	t.Run("shadow_mode_parity", func(t *testing.T) {
		testShadowModeParity(t, ctx, logger)
	})
}

// testQuotesLiveIntegration tests live quotes with real API
func testQuotesLiveIntegration(t *testing.T, ctx context.Context, apiKey string, logger *log.Logger) {
	// Create live Alpha Vantage adapter
	config := AlphaVantageConfig{
		APIKey:              apiKey,
		RateLimitPerMinute:  5,
		DailyCap:            50, // Conservative for testing
		CacheTTLSeconds:     60,
		StaleCeilingSeconds: 300,
		TimeoutSeconds:      10,
		MaxRetries:          2,
		BackoffBaseMs:       1000,
	}
	
	adapter, err := NewAlphaVantageAdapter(config)
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}
	defer adapter.Close()
	
	// Test symbols
	testSymbols := []string{"AAPL", "NVDA", "SPY"}
	
	t.Run("single_quote_fetch", func(t *testing.T) {
		start := time.Now()
		quote, err := adapter.GetQuote(ctx, "AAPL")
		latency := time.Since(start)
		
		if err != nil {
			t.Fatalf("Failed to get quote: %v", err)
		}
		
		// Validate quote structure
		if quote.Symbol != "AAPL" {
			t.Errorf("Expected symbol AAPL, got %s", quote.Symbol)
		}
		
		if quote.Bid <= 0 || quote.Ask <= 0 || quote.Last <= 0 {
			t.Errorf("Invalid prices: bid=%.2f ask=%.2f last=%.2f", quote.Bid, quote.Ask, quote.Last)
		}
		
		if quote.Bid >= quote.Ask {
			t.Errorf("Bid >= Ask: bid=%.2f ask=%.2f", quote.Bid, quote.Ask)
		}
		
		// Check latency is reasonable
		if latency > 5*time.Second {
			t.Errorf("Quote fetch too slow: %v", latency)
		}
		
		// Check freshness
		staleness := time.Since(quote.Timestamp)
		if staleness > 10*time.Minute {
			t.Errorf("Quote too stale: %v", staleness)
		}
		
		logger.Printf("✅ Live quote AAPL: bid=%.2f ask=%.2f last=%.2f latency=%v staleness=%v", 
			quote.Bid, quote.Ask, quote.Last, latency, staleness)
	})
	
	t.Run("batch_quote_fetch", func(t *testing.T) {
		start := time.Now()
		quotes, err := adapter.GetQuotes(ctx, testSymbols)
		latency := time.Since(start)
		
		if err != nil {
			t.Fatalf("Failed to get quotes: %v", err)
		}
		
		// Should get at least one quote (rate limits might prevent all)
		if len(quotes) == 0 {
			t.Fatalf("No quotes returned")
		}
		
		for symbol, quote := range quotes {
			if quote.Symbol != symbol {
				t.Errorf("Symbol mismatch: expected %s, got %s", symbol, quote.Symbol)
			}
			
			if quote.Bid <= 0 || quote.Ask <= 0 || quote.Last <= 0 {
				t.Errorf("%s: Invalid prices: bid=%.2f ask=%.2f last=%.2f", 
					symbol, quote.Bid, quote.Ask, quote.Last)
			}
		}
		
		logger.Printf("✅ Live batch quotes: %d symbols, latency=%v", len(quotes), latency)
	})
	
	t.Run("rate_limit_handling", func(t *testing.T) {
		// Make rapid requests to test rate limiting
		errors := 0
		successes := 0
		
		for i := 0; i < 10; i++ {
			_, err := adapter.GetQuote(ctx, "AAPL")
			if err != nil {
				errors++
				if fmt.Sprintf("%v", err) == "rate limit exceeded" {
					logger.Printf("Rate limit correctly enforced")
					break
				}
			} else {
				successes++
			}
			
			time.Sleep(100 * time.Millisecond)
		}
		
		logger.Printf("Rate limit test: %d successes, %d errors", successes, errors)
	})
}

// testCachePerformance tests quote cache behavior
func testCachePerformance(t *testing.T, ctx context.Context, logger *log.Logger) {
	cache := NewQuoteCache(30*time.Second, logger)
	
	testQuote := Quote{
		Symbol:    "TEST",
		Bid:       100.0,
		Ask:       100.1,
		Last:      100.05,
		Timestamp: time.Now(),
	}
	
	t.Run("cache_operations", func(t *testing.T) {
		// Test cache miss
		_, found, _ := cache.Get("TEST")
		if found {
			t.Error("Expected cache miss")
		}
		
		// Test cache set and hit
		cache.Set("TEST", testQuote, "test")
		
		retrievedQuote, found, stale := cache.Get("TEST")
		if !found {
			t.Error("Expected cache hit")
		}
		
		if stale {
			t.Error("Quote should not be stale immediately")
		}
		
		if retrievedQuote.Symbol != "TEST" {
			t.Errorf("Retrieved wrong quote: %+v", retrievedQuote)
		}
		
		// Test staleness detection
		staleQuote := testQuote
		staleQuote.Timestamp = time.Now().Add(-10 * time.Minute)
		cache.Set("STALE", staleQuote, "test")
		
		_, found, stale = cache.Get("STALE")
		if !found {
			t.Error("Expected cache hit for stale quote")
		}
		
		if !stale {
			t.Error("Quote should be marked as stale")
		}
		
		logger.Printf("✅ Cache operations working correctly")
	})
	
	t.Run("cache_metrics", func(t *testing.T) {
		metrics := cache.GetMetrics()
		
		if metrics.Hits < 2 { // Should have at least 2 hits from previous test
			t.Errorf("Expected >= 2 cache hits, got %d", metrics.Hits)
		}
		
		if metrics.Misses < 1 { // Should have at least 1 miss
			t.Errorf("Expected >= 1 cache miss, got %d", metrics.Misses)
		}
		
		logger.Printf("✅ Cache metrics: hits=%d misses=%d", metrics.Hits, metrics.Misses)
	})
	
	t.Run("cache_cleanup", func(t *testing.T) {
		// Add expired entry
		expiredQuote := testQuote
		cache.Set("EXPIRED", expiredQuote, "test")
		
		// Manually trigger cleanup (normally done by background goroutine)
		cache.Cleanup()
		
		logger.Printf("✅ Cache cleanup completed")
	})
}

// testProviderHealthMonitoring tests health monitoring functionality
func testProviderHealthMonitoring(t *testing.T, ctx context.Context, logger *log.Logger) {
	health := NewProviderHealth("test_provider", logger)
	
	t.Run("health_state_transitions", func(t *testing.T) {
		// Start healthy
		if health.GetStatus() != ProviderStatusHealthy {
			t.Errorf("Expected healthy status initially, got %v", health.GetStatus())
		}
		
		// Record some successes
		for i := 0; i < 5; i++ {
			health.RecordSuccess(100 * time.Millisecond)
		}
		
		if health.GetStatus() != ProviderStatusHealthy {
			t.Errorf("Should remain healthy after successes")
		}
		
		// Record errors to trigger degraded state
		for i := 0; i < 3; i++ {
			health.RecordError(fmt.Errorf("test error %d", i))
		}
		
		// Should still be healthy due to success rate
		status := health.GetStatus()
		logger.Printf("Status after mixed results: %v", status)
		
		// Record consecutive errors
		for i := 0; i < 6; i++ {
			health.RecordError(fmt.Errorf("consecutive error %d", i))
		}
		
		// Should now be failed
		if health.GetStatus() != ProviderStatusFailed {
			t.Errorf("Expected failed status after consecutive errors, got %v", health.GetStatus())
		}
		
		logger.Printf("✅ Health state transitions working")
	})
	
	t.Run("health_metrics", func(t *testing.T) {
		metrics := health.GetMetrics()
		
		expectedFields := []string{"status", "error_rate", "consecutive_errors", "success_count", "error_count"}
		for _, field := range expectedFields {
			if _, exists := metrics[field]; !exists {
				t.Errorf("Missing health metric: %s", field)
			}
		}
		
		logger.Printf("✅ Health metrics: %+v", metrics)
	})
}

// testFallbackBehavior tests adapter fallback mechanisms
func testFallbackBehavior(t *testing.T, ctx context.Context, logger *log.Logger) {
	// Create a failing primary adapter and a working fallback  
	primaryAdapter := NewMockQuotesAdapter()
	fallbackAdapter := NewMockQuotesAdapter()
	
	// Configure mock data
	fallbackAdapter.AddQuote(&Quote{
		Symbol: "AAPL",
		Bid:    150.0,
		Ask:    150.1,
		Last:   150.05,
		Timestamp: time.Now(),
		Source: "mock_fallback",
	})
	
	cache := NewQuoteCache(30*time.Second, logger)
	refresher := NewQuoteRefresher(cache, primaryAdapter, fallbackAdapter, []string{"AAPL"}, 1*time.Second, logger)
	
	t.Run("fallback_activation", func(t *testing.T) {
		// Start refresher
		refresher.Start()
		defer refresher.Stop()
		
		// Wait for refresh cycles
		time.Sleep(3 * time.Second)
		
		// Check if fallback was used
		quote, found, _ := cache.Get("AAPL")
		if !found {
			t.Error("Expected quote in cache after fallback")
		}
		
		if quote.Source != "mock_fallback" && quote.Source != "fallback" {
			t.Errorf("Expected fallback source, got %s", quote.Source)
		}
		
		logger.Printf("✅ Fallback behavior working: source=%s", quote.Source)
	})
}

// testShadowModeParity tests shadow mode functionality
func testShadowModeParity(t *testing.T, ctx context.Context, logger *log.Logger) {
	// Create mock providers for shadow testing
	liveProvider := NewMockHaltsProvider()
	shadowAdapter := NewShadowHaltsAdapter(liveProvider, logger)
	
	t.Run("shadow_mode_startup", func(t *testing.T) {
		err := shadowAdapter.StartShadowMode(ctx)
		if err != nil {
			t.Fatalf("Failed to start shadow mode: %v", err)
		}
		defer shadowAdapter.StopShadowMode()
		
		// Wait for shadow mode to initialize
		time.Sleep(1 * time.Second)
		
		// Check that shadow mode is running
		metrics := shadowAdapter.GetShadowMetrics()
		if !metrics["running"].(bool) {
			t.Error("Shadow mode should be running")
		}
		
		logger.Printf("✅ Shadow mode started successfully")
	})
	
	t.Run("parity_checking", func(t *testing.T) {
		err := shadowAdapter.StartShadowMode(ctx)
		if err != nil {
			t.Fatalf("Failed to start shadow mode: %v", err)
		}
		defer shadowAdapter.StopShadowMode()
		
		// Add some test data
		shadowAdapter.UpdateCurrentHalt("AAPL", &HaltInfo{
			Symbol:      "AAPL",
			Halted:      true,
			Reason:      "news pending",
			HaltTime:    time.Now(),
			LastUpdated: time.Now(),
			Source:      "current",
		})
		
		liveProvider.SetHalt("AAPL", true, "news pending")
		
		// Wait for parity check
		time.Sleep(2 * time.Second)
		
		metrics := shadowAdapter.GetShadowMetrics()
		logger.Printf("✅ Parity metrics: %+v", metrics)
	})
}

// Benchmark tests for performance validation
func BenchmarkQuoteValidation(b *testing.B) {
	cache := NewQuoteCache(30*time.Second, log.New(os.Stdout, "", 0))
	
	validQuote := Quote{
		Symbol:    "BENCH",
		Bid:       100.0,
		Ask:       100.1,
		Last:      100.05,
		Timestamp: time.Now(),
	}
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		// This would call the validation function from the refresher
		refresher := &QuoteRefresher{cache: cache}
		refresher.validateQuote(validQuote)
	}
}

func BenchmarkCacheOperations(b *testing.B) {
	cache := NewQuoteCache(30*time.Second, log.New(os.Stdout, "", 0))
	
	quote := Quote{
		Symbol:    "BENCH",
		Bid:       100.0,
		Ask:       100.1,
		Last:      100.05,
		Timestamp: time.Now(),
	}
	
	b.ResetTimer()
	
	b.Run("cache_set", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			symbol := fmt.Sprintf("SYMBOL_%d", i%100) // Cycle through 100 symbols
			cache.Set(symbol, quote, "bench")
		}
	})
	
	b.Run("cache_get", func(b *testing.B) {
		// Pre-populate cache
		for i := 0; i < 100; i++ {
			cache.Set(fmt.Sprintf("SYMBOL_%d", i), quote, "bench")
		}
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			symbol := fmt.Sprintf("SYMBOL_%d", i%100)
			cache.Get(symbol)
		}
	})
}

func BenchmarkProviderHealth(b *testing.B) {
	health := NewProviderHealth("bench", log.New(os.Stdout, "", 0))
	
	b.Run("record_success", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			health.RecordSuccess(100 * time.Millisecond)
		}
	})
	
	b.Run("record_error", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			health.RecordError(fmt.Errorf("bench error"))
		}
	})
	
	b.Run("get_status", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			health.GetStatus()
		}
	})
}