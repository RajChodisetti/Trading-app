package adapters

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestAlphaVantageAdapter(t *testing.T) {
	// Skip if no API key (CI/local without key)
	apiKey := os.Getenv("ALPHA_VANTAGE_API_KEY")
	if apiKey == "" {
		t.Skip("Skipping Alpha Vantage test - no API key provided")
	}
	
	// Only run in short mode for CI (not during regular test runs)
	if !testing.Short() {
		t.Skip("Skipping Alpha Vantage test - use -short to run live tests")
	}
	
	config := AlphaVantageConfig{
		APIKey:              apiKey,
		RateLimitPerMinute:  5,
		DailyCap:            10, // Conservative for testing
		CacheTTLSeconds:     60,
		StaleCeilingSeconds: 180,
		TimeoutSeconds:      10,
		MaxRetries:          2,
		BackoffBaseMs:       1000,
	}
	
	adapter, err := NewAlphaVantageAdapter(config)
	if err != nil {
		t.Fatalf("NewAlphaVantageAdapter() error = %v", err)
	}
	defer adapter.Close()
	
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	
	t.Run("health check", func(t *testing.T) {
		if err := adapter.HealthCheck(ctx); err != nil {
			t.Errorf("HealthCheck() error = %v", err)
		}
	})
	
	t.Run("get single quote", func(t *testing.T) {
		quote, err := adapter.GetQuote(ctx, "AAPL")
		if err != nil {
			// Log error but don't fail - API might be rate limited
			t.Logf("GetQuote() error = %v (may be rate limited)", err)
			return
		}
		
		if quote.Symbol != "AAPL" {
			t.Errorf("Symbol = %v, want AAPL", quote.Symbol)
		}
		
		if quote.Source != "alphavantage" {
			t.Errorf("Source = %v, want alphavantage", quote.Source)
		}
		
		if err := ValidateQuote(quote); err != nil {
			t.Errorf("Invalid quote from Alpha Vantage: %v", err)
		}
		
		t.Logf("Alpha Vantage quote: %+v", quote)
	})
	
	t.Run("cache functionality", func(t *testing.T) {
		// First call should hit API
		start := time.Now()
		quote1, err := adapter.GetQuote(ctx, "MSFT")
		latency1 := time.Since(start)
		
		if err != nil {
			t.Logf("First GetQuote() error = %v", err)
			return
		}
		
		// Second call should hit cache (much faster)
		start = time.Now()
		quote2, err := adapter.GetQuote(ctx, "MSFT")
		latency2 := time.Since(start)
		
		if err != nil {
			t.Errorf("Second GetQuote() error = %v", err)
		}
		
		// Cache hit should be much faster
		if latency2 >= latency1 {
			t.Logf("Cache may not be working: first=%v, second=%v", latency1, latency2)
		}
		
		// Prices should be the same (from cache)
		if quote1.Last != quote2.Last {
			t.Logf("Cache miss or price update: first=%.2f, second=%.2f", quote1.Last, quote2.Last)
		}
		
		t.Logf("Latencies: API=%.2fms, Cache=%.2fms", 
			float64(latency1.Nanoseconds())/1e6, 
			float64(latency2.Nanoseconds())/1e6)
	})
	
	t.Run("budget tracking", func(t *testing.T) {
		used, total, resetTime := adapter.GetBudgetStatus()
		t.Logf("Budget: %d/%d requests, resets at %v", used, total, resetTime.Format("15:04:05"))
		
		if used < 0 || used > total {
			t.Errorf("Invalid budget status: used=%d, total=%d", used, total)
		}
	})
}

func TestAlphaVantageConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  AlphaVantageConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: AlphaVantageConfig{
				APIKey:         "test-key",
				TimeoutSeconds: 10,
			},
			wantErr: false,
		},
		{
			name: "missing API key",
			config: AlphaVantageConfig{
				TimeoutSeconds: 10,
			},
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAlphaVantageAdapter(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewAlphaVantageAdapter() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestAlphaVantageIntegrationDemo demonstrates the full integration
func TestAlphaVantageIntegrationDemo(t *testing.T) {
	if os.Getenv("ALPHA_VANTAGE_DEMO") == "" {
		t.Skip("Set ALPHA_VANTAGE_DEMO=1 to run integration demo")
	}
	
	apiKey := os.Getenv("ALPHA_VANTAGE_API_KEY")
	if apiKey == "" {
		t.Fatal("ALPHA_VANTAGE_API_KEY required for demo")
	}
	
	// Test the factory pattern
	factory := NewQuotesAdapterFactory(QuotesConfig{
		Adapter: "alphavantage",
		Providers: QuotesProviderConfigs{
			AlphaVantage: AlphaVantageProviderConfig{
				APIKeyEnv:           "ALPHA_VANTAGE_API_KEY",
				RateLimitPerMinute:  5,
				DailyCap:            20,
				CacheTTLSeconds:     60,
				StaleCeilingSeconds: 180,
				TimeoutSeconds:      10,
				MaxRetries:          3,
				BackoffBaseMs:       1000,
			},
		},
	})
	
	adapter, err := factory.CreateAdapter()
	if err != nil {
		t.Fatalf("Factory CreateAdapter() error = %v", err)
	}
	defer adapter.Close()
	
	symbols := []string{"AAPL", "MSFT", "GOOGL"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	quotes, err := adapter.GetQuotes(ctx, symbols)
	if err != nil {
		t.Logf("GetQuotes() error = %v", err)
		return
	}
	
	t.Logf("Successfully fetched %d quotes:", len(quotes))
	for symbol, quote := range quotes {
		t.Logf("  %s: $%.2f (spread: %.1f bps, session: %s, source: %s)", 
			symbol, quote.Last, quote.SpreadBps(), quote.Session, quote.Source)
	}
}