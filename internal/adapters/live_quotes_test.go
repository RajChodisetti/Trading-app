package adapters

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveQuoteAdapter_CanaryRollout tests the canary rollout functionality
func TestLiveQuoteAdapter_CanaryRollout(t *testing.T) {
	// Create a test provider that always succeeds
	testProvider := &testQuoteProvider{
		quotes: map[string]*Quote{
			"AAPL": {Symbol: "AAPL", Bid: 150.0, Ask: 150.1, Last: 150.05, Source: "test"},
			"SPY":  {Symbol: "SPY", Bid: 400.0, Ask: 400.1, Last: 400.05, Source: "test"},
			"NVDA": {Symbol: "NVDA", Bid: 800.0, Ask: 800.1, Last: 800.05, Source: "test"},
		},
	}
	
	config := LiveQuoteConfig{
		LiveEnabled:           true,
		ShadowMode:           false,
		CanarySymbols:        []string{"AAPL", "SPY"},
		PrioritySymbols:      []string{"AAPL", "SPY", "NVDA"},
		CanaryDurationMinutes: 1, // One minute delay for testing phases
		CacheMaxEntries:      100,
		CacheTTLSeconds:     60,
		DailyRequestCap:     1000,
		BudgetWarningPct:    0.1,
		FallbackToMock:      true,
	}
	
	adapter, err := NewLiveQuoteAdapter(testProvider, config)
	require.NoError(t, err)
	
	ctx := context.Background()
	
	// Test Phase 1: Only canary symbols should be allowed
	t.Run("canary_phase_only_canary_symbols", func(t *testing.T) {
		// AAPL should work (canary symbol)
		quote, err := adapter.GetQuote(ctx, "AAPL")
		require.NoError(t, err)
		assert.Equal(t, "test", quote.Source) // Should come from test provider
		
		// NVDA should be served by mock (not in canary)
		quote, err = adapter.GetQuote(ctx, "NVDA")
		require.NoError(t, err)
		assert.Equal(t, "mock", quote.Source) // Should come from mock
	})
	
	// Test Phase 2: Wait for canary expansion and test priority symbols
	t.Run("canary_expansion_priority_symbols", func(t *testing.T) {
		// Simulate canary expansion by manually triggering it
		// In a real test, we would wait, but for unit tests we simulate the passage of time
		adapter.simulateCanaryExpansion() // Force expansion for testing
		
		// Now NVDA should work (priority symbol after expansion)
		quote, err := adapter.GetQuote(ctx, "NVDA")
		require.NoError(t, err)
		assert.Equal(t, "test", quote.Source) // Should come from test provider
		
		// Verify expansion status
		status := adapter.getCanaryStatus()
		assert.True(t, status["canary_expanded"].(bool))
	})
}

// TestLiveQuoteAdapter_ShadowMode tests shadow mode comparison functionality
func TestLiveQuoteAdapter_ShadowMode(t *testing.T) {
	testProvider := &testQuoteProvider{
		quotes: map[string]*Quote{
			"AAPL": {Symbol: "AAPL", Bid: 150.0, Ask: 150.1, Last: 150.05, Source: "test"},
		},
	}
	
	config := LiveQuoteConfig{
		LiveEnabled:         true,
		ShadowMode:         true,
		CanarySymbols:      []string{"AAPL"},
		PrioritySymbols:    []string{"AAPL"},
		ShadowSampleRate:   1.0, // Sample all quotes for testing
		CacheMaxEntries:    100,
		CacheTTLSeconds:   60,
		DailyRequestCap:   1000,
		BudgetWarningPct:  0.1,
		FallbackToMock:    true,
	}
	
	adapter, err := NewLiveQuoteAdapter(testProvider, config)
	require.NoError(t, err)
	
	ctx := context.Background()
	
	// Fetch a quote - should trigger shadow comparison
	quote, err := adapter.GetQuote(ctx, "AAPL")
	require.NoError(t, err)
	assert.Equal(t, "test", quote.Source)
	
	// Give time for async shadow comparison
	time.Sleep(100 * time.Millisecond)
	
	// Check shadow metrics
	metrics := adapter.getMetricsSummary()
	assert.Greater(t, metrics.ShadowSamples, int64(0))
}

// TestLiveQuoteAdapter_CacheWithBounds tests cache behavior with size limits
func TestLiveQuoteAdapter_CacheWithBounds(t *testing.T) {
	testProvider := &testQuoteProvider{
		quotes: make(map[string]*Quote),
	}
	
	// Create many test quotes
	for i := 0; i < 10; i++ {
		symbol := fmt.Sprintf("TEST%d", i)
		testProvider.quotes[symbol] = &Quote{
			Symbol: symbol,
			Bid:    float64(100 + i),
			Ask:    100.0 + float64(i) + 0.1,
			Last:   100.0 + float64(i) + 0.05,
			Source: "test",
		}
	}
	
	config := LiveQuoteConfig{
		LiveEnabled:         true,
		ShadowMode:         false,
		CanarySymbols:      []string{"TEST0", "TEST1"},
		PrioritySymbols:    []string{"TEST0", "TEST1", "TEST2", "TEST3", "TEST4"},
		CacheMaxEntries:    3, // Small cache to trigger eviction
		CacheTTLSeconds:   60,
		DailyRequestCap:   1000,
		BudgetWarningPct:  0.1,
		FallbackToMock:    true,
	}
	
	adapter, err := NewLiveQuoteAdapter(testProvider, config)
	require.NoError(t, err)
	
	ctx := context.Background()
	
	// Fill cache beyond capacity
	for i := 0; i < 5; i++ {
		symbol := fmt.Sprintf("TEST%d", i)
		quote, err := adapter.GetQuote(ctx, symbol)
		require.NoError(t, err)
		assert.Equal(t, symbol, quote.Symbol)
	}
	
	// Check that evictions occurred
	adapter.cache.mu.RLock()
	cacheSize := len(adapter.cache.entries)
	evictions := adapter.cache.evictions
	adapter.cache.mu.RUnlock()
	
	assert.LessOrEqual(t, cacheSize, config.CacheMaxEntries)
	assert.Greater(t, evictions, int64(0))
}

// TestLiveQuoteAdapter_HealthHysteresis tests health state transitions with hysteresis
func TestLiveQuoteAdapter_HealthHysteresis(t *testing.T) {
	// Create a provider that can be toggled to fail
	failingProvider := &toggleableQuoteProvider{
		shouldFail: false,
		quotes: map[string]*Quote{
			"AAPL": {Symbol: "AAPL", Bid: 150.0, Ask: 150.1, Last: 150.05, Source: "test"},
		},
	}
	
	config := LiveQuoteConfig{
		LiveEnabled:                true,
		ShadowMode:                false,
		CanarySymbols:             []string{"AAPL"},
		PrioritySymbols:           []string{"AAPL"},
		ConsecutiveBreachToDegrade: 2, // Small values for testing
		ConsecutiveOkToRecover:     2,
		MaxConsecutiveErrors:       3,
		CacheMaxEntries:           100,
		CacheTTLSeconds:          60,
		DailyRequestCap:          1000,
		BudgetWarningPct:         0.1,
		FallbackToMock:           true,
	}
	
	adapter, err := NewLiveQuoteAdapter(failingProvider, config)
	require.NoError(t, err)
	
	ctx := context.Background()
	
	// Initially healthy
	assert.Equal(t, HealthHealthy, adapter.healthState)
	
	// Cause consecutive failures
	failingProvider.shouldFail = true
	
	// First failure - still healthy
	_, err = adapter.GetQuote(ctx, "AAPL")
	assert.Error(t, err)
	assert.Equal(t, HealthHealthy, adapter.healthState)
	
	// Second failure - should become degraded
	_, err = adapter.GetQuote(ctx, "AAPL")
	assert.Error(t, err)
	assert.Equal(t, HealthDegraded, adapter.healthState)
	
	// Third failure - should become failed
	_, err = adapter.GetQuote(ctx, "AAPL")
	assert.Error(t, err)
	assert.Equal(t, HealthFailed, adapter.healthState)
	
	// Start recovery
	failingProvider.shouldFail = false
	
	// First success - still failed
	_, err = adapter.GetQuote(ctx, "AAPL")
	require.NoError(t, err)
	assert.Equal(t, HealthFailed, adapter.healthState)
	
	// Second success - should become degraded (gradual recovery)
	_, err = adapter.GetQuote(ctx, "AAPL")
	require.NoError(t, err)
	assert.Equal(t, HealthDegraded, adapter.healthState)
}

// TestLiveQuoteAdapter_BudgetTracking tests budget tracking and warnings
func TestLiveQuoteAdapter_BudgetTracking(t *testing.T) {
	testProvider := &testQuoteProvider{
		quotes: map[string]*Quote{
			"AAPL": {Symbol: "AAPL", Bid: 150.0, Ask: 150.1, Last: 150.05, Source: "test"},
		},
	}
	
	config := LiveQuoteConfig{
		LiveEnabled:      true,
		ShadowMode:      false,
		CanarySymbols:   []string{"AAPL"},
		PrioritySymbols: []string{"AAPL"},
		DailyRequestCap: 5, // Small cap for testing
		BudgetWarningPct: 0.6, // Warn at 60%
		CacheMaxEntries: 100,
		CacheTTLSeconds: 60,
		FallbackToMock:  true,
	}
	
	adapter, err := NewLiveQuoteAdapter(testProvider, config)
	require.NoError(t, err)
	
	ctx := context.Background()
	
	// Use up most of the budget
	for i := 0; i < 4; i++ {
		quote, err := adapter.GetQuote(ctx, "AAPL")
		require.NoError(t, err)
		assert.Equal(t, "test", quote.Source)
		
		// Clear cache to force new requests
		adapter.cache.mu.Lock()
		adapter.cache.entries = make(map[string]*BoundedCacheEntry)
		adapter.cache.mu.Unlock()
	}
	
	// Check budget status
	used, total, _ := adapter.budgetTracker.getBudgetStatus()
	assert.Equal(t, 4, used)
	assert.Equal(t, 5, total)
	
	// One more request should still work
	quote, err := adapter.GetQuote(ctx, "AAPL")
	require.NoError(t, err)
	assert.Equal(t, "test", quote.Source)
	
	// Clear cache again
	adapter.cache.mu.Lock()
	adapter.cache.entries = make(map[string]*BoundedCacheEntry)
	adapter.cache.mu.Unlock()
	
	// Next request should be served from mock (budget exceeded)
	quote, err = adapter.GetQuote(ctx, "AAPL")
	require.NoError(t, err)
	assert.Equal(t, "mock", quote.Source) // Should fallback to mock
}

// TestLiveQuoteAdapter_HotpathProtection tests that hotpath calls are tracked
func TestLiveQuoteAdapter_HotpathProtection(t *testing.T) {
	testProvider := &testQuoteProvider{
		quotes: map[string]*Quote{
			"AAPL": {Symbol: "AAPL", Bid: 150.0, Ask: 150.1, Last: 150.05, Source: "test"},
		},
	}
	
	config := LiveQuoteConfig{
		LiveEnabled:      true,
		ShadowMode:      false,
		CanarySymbols:   []string{"AAPL"},
		PrioritySymbols: []string{"AAPL"},
		CacheMaxEntries: 100,
		CacheTTLSeconds: 60,
		DailyRequestCap: 1000,
		BudgetWarningPct: 0.1,
		FallbackToMock:  true,
	}
	
	adapter, err := NewLiveQuoteAdapter(testProvider, config)
	require.NoError(t, err)
	
	ctx := context.Background()
	
	// Initially, no hotpath calls
	assert.Equal(t, int64(0), adapter.GetHotpathCalls())
	
	// Make a live call
	quote, err := adapter.GetQuote(ctx, "AAPL")
	require.NoError(t, err)
	assert.Equal(t, "test", quote.Source)
	
	// Should have recorded hotpath call
	assert.Equal(t, int64(1), adapter.GetHotpathCalls())
	
	// Second call should hit cache, no additional hotpath call
	quote, err = adapter.GetQuote(ctx, "AAPL")
	require.NoError(t, err)
	assert.Equal(t, "test", quote.Source)
	assert.Equal(t, int64(1), adapter.GetHotpathCalls()) // Still 1
}

// TestPromotionGatesChecker tests the promotion gates evaluation
func TestPromotionGatesChecker(t *testing.T) {
	testProvider := &testQuoteProvider{
		quotes: map[string]*Quote{
			"AAPL": {Symbol: "AAPL", Bid: 150.0, Ask: 150.1, Last: 150.05, Source: "test", StalenessMs: 100},
			"SPY":  {Symbol: "SPY", Bid: 400.0, Ask: 400.1, Last: 400.05, Source: "test", StalenessMs: 200},
		},
	}
	
	config := LiveQuoteConfig{
		LiveEnabled:             true,
		ShadowMode:             true,
		CanarySymbols:          []string{"AAPL", "SPY"},
		PrioritySymbols:        []string{"AAPL", "SPY"},
		FreshnessP95ThresholdMs: 5000,
		SuccessRateThreshold:   0.99,
		ShadowSampleRate:       1.0,
		CacheMaxEntries:        100,
		CacheTTLSeconds:       60,
		DailyRequestCap:       1000,
		BudgetWarningPct:      0.1,
		FallbackToMock:        true,
	}
	
	adapter, err := NewLiveQuoteAdapter(testProvider, config)
	require.NoError(t, err)
	
	ctx := context.Background()
	
	// Generate some successful requests
	for i := 0; i < 10; i++ {
		symbol := "AAPL"
		if i%2 == 0 {
			symbol = "SPY"
		}
		
		quote, err := adapter.GetQuote(ctx, symbol)
		require.NoError(t, err)
		assert.NotNil(t, quote)
		
		// Clear cache periodically to generate fresh requests
		if i%3 == 0 {
			adapter.cache.mu.Lock()
			adapter.cache.entries = make(map[string]*BoundedCacheEntry)
			adapter.cache.mu.Unlock()
		}
	}
	
	// Wait for shadow comparisons
	time.Sleep(200 * time.Millisecond)
	
	// Check metrics for promotion gates
	metrics := adapter.getMetricsSummary()
	
	// Should have good success rate
	assert.Greater(t, metrics.SuccessRate, 0.9)
	
	// Should have reasonable freshness
	assert.Less(t, metrics.FreshnessP95, int64(5000))
	
	// Should have shadow samples in shadow mode
	assert.Greater(t, metrics.ShadowSamples, int64(0))
	
	// Hotpath calls should be tracked
	assert.Greater(t, metrics.HotpathCalls, int64(0))
	
	// Health check should pass
	err = adapter.HealthCheck(ctx)
	assert.NoError(t, err)
}

// Helper types for testing

type testQuoteProvider struct {
	quotes map[string]*Quote
}

func (p *testQuoteProvider) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	if quote, exists := p.quotes[symbol]; exists {
		// Return a copy with current timestamp
		result := *quote
		result.Timestamp = time.Now()
		return &result, nil
	}
	return nil, NewBadSymbolError(symbol, "symbol not found")
}

func (p *testQuoteProvider) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	results := make(map[string]*Quote)
	for _, symbol := range symbols {
		if quote, err := p.GetQuote(ctx, symbol); err == nil {
			results[symbol] = quote
		}
	}
	return results, nil
}

func (p *testQuoteProvider) HealthCheck(ctx context.Context) error {
	return nil
}

func (p *testQuoteProvider) Close() error {
	return nil
}

type toggleableQuoteProvider struct {
	shouldFail bool
	quotes     map[string]*Quote
}

func (p *toggleableQuoteProvider) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	if p.shouldFail {
		return nil, NewProviderError(symbol, "simulated failure", nil)
	}
	
	if quote, exists := p.quotes[symbol]; exists {
		result := *quote
		result.Timestamp = time.Now()
		return &result, nil
	}
	return nil, NewBadSymbolError(symbol, "symbol not found")
}

func (p *toggleableQuoteProvider) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	results := make(map[string]*Quote)
	for _, symbol := range symbols {
		if quote, err := p.GetQuote(ctx, symbol); err == nil {
			results[symbol] = quote
		}
	}
	return results, nil
}

func (p *toggleableQuoteProvider) HealthCheck(ctx context.Context) error {
	if p.shouldFail {
		return fmt.Errorf("simulated health check failure")
	}
	return nil
}

func (p *toggleableQuoteProvider) Close() error {
	return nil
}

// Helper method to get canary status (would be exposed in real implementation)
func (lq *LiveQuoteAdapter) getCanaryStatus() map[string]interface{} {
	lq.mu.RLock()
	defer lq.mu.RUnlock()
	
	return map[string]interface{}{
		"canary_started":  lq.canaryStartTime,
		"canary_expanded": lq.canaryExpanded,
		"health_state":    string(lq.healthState),
	}
}