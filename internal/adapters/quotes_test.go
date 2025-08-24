package adapters

import (
	"context"
	"testing"
	"time"
)

func TestValidateQuote(t *testing.T) {
	now := time.Now()
	
	tests := []struct {
		name    string
		quote   *Quote
		wantErr bool
	}{
		{
			name: "valid quote",
			quote: &Quote{
				Symbol:      "AAPL",
				Bid:         100.50,
				Ask:         100.55,
				Last:        100.52,
				Volume:      1000000,
				Timestamp:   now.Add(-30 * time.Second),
				Session:     "RTH",
				Source:      "mock",
				StalenessMs: 30000,
			},
			wantErr: false,
		},
		{
			name:    "nil quote",
			quote:   nil,
			wantErr: true,
		},
		{
			name: "empty symbol",
			quote: &Quote{
				Symbol: "",
				Bid:    100.50,
				Ask:    100.55,
			},
			wantErr: true,
		},
		{
			name: "invalid prices",
			quote: &Quote{
				Symbol: "AAPL",
				Bid:    -1.0,
				Ask:    100.55,
			},
			wantErr: true,
		},
		{
			name: "ask less than bid",
			quote: &Quote{
				Symbol: "AAPL",
				Bid:    100.55,
				Ask:    100.50, // Invalid: ask < bid
			},
			wantErr: true,
		},
		{
			name: "negative volume",
			quote: &Quote{
				Symbol: "AAPL",
				Bid:    100.50,
				Ask:    100.55,
				Last:   100.52,
				Volume: -1000,
			},
			wantErr: true,
		},
		{
			name: "future timestamp",
			quote: &Quote{
				Symbol:    "AAPL",
				Bid:       100.50,
				Ask:       100.55,
				Last:      100.52,
				Volume:    1000,
				Timestamp: now.Add(10 * time.Minute), // Too far in future
			},
			wantErr: true,
		},
		{
			name: "invalid session",
			quote: &Quote{
				Symbol:  "AAPL",
				Bid:     100.50,
				Ask:     100.55,
				Last:    100.52,
				Volume:  1000,
				Session: "INVALID",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateQuote(tt.quote)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateQuote() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestQuoteSpreadBps(t *testing.T) {
	quote := &Quote{
		Bid: 100.00,
		Ask: 100.10,
	}
	
	expectedSpread := 10.0 // 0.10/100.00 * 10000 = 10 bps
	actualSpread := quote.SpreadBps()
	
	// Allow small floating point precision differences
	if abs(actualSpread-expectedSpread) > 0.001 {
		t.Errorf("SpreadBps() = %v, want %v", actualSpread, expectedSpread)
	}
}

func TestMockQuotesAdapter(t *testing.T) {
	adapter := NewMockQuotesAdapter()
	ctx := context.Background()
	
	t.Run("get valid quote", func(t *testing.T) {
		quote, err := adapter.GetQuote(ctx, "AAPL")
		if err != nil {
			t.Fatalf("GetQuote() error = %v", err)
		}
		
		if quote.Symbol != "AAPL" {
			t.Errorf("Symbol = %v, want AAPL", quote.Symbol)
		}
		
		if quote.Source != "mock" {
			t.Errorf("Source = %v, want mock", quote.Source)
		}
		
		if err := ValidateQuote(quote); err != nil {
			t.Errorf("Invalid quote: %v", err)
		}
	})
	
	t.Run("get invalid symbol", func(t *testing.T) {
		_, err := adapter.GetQuote(ctx, "NONEXISTENT")
		if err == nil {
			t.Error("Expected error for non-existent symbol")
		}
	})
	
	t.Run("get multiple quotes", func(t *testing.T) {
		symbols := []string{"AAPL", "NVDA", "BIOX"}
		quotes, err := adapter.GetQuotes(ctx, symbols)
		if err != nil {
			t.Fatalf("GetQuotes() error = %v", err)
		}
		
		if len(quotes) != 3 {
			t.Errorf("Got %d quotes, want 3", len(quotes))
		}
		
		for _, symbol := range symbols {
			if _, exists := quotes[symbol]; !exists {
				t.Errorf("Missing quote for %s", symbol)
			}
		}
	})
	
	t.Run("health check", func(t *testing.T) {
		if err := adapter.HealthCheck(ctx); err != nil {
			t.Errorf("HealthCheck() error = %v", err)
		}
		
		adapter.SetHealth(false)
		if err := adapter.HealthCheck(ctx); err == nil {
			t.Error("Expected health check to fail")
		}
	})
}

func TestSimQuotesAdapter(t *testing.T) {
	adapter := NewSimQuotesAdapter()
	ctx := context.Background()
	
	t.Run("get simulated quote", func(t *testing.T) {
		quote, err := adapter.GetQuote(ctx, "AAPL")
		if err != nil {
			t.Fatalf("GetQuote() error = %v", err)
		}
		
		if quote.Symbol != "AAPL" {
			t.Errorf("Symbol = %v, want AAPL", quote.Symbol)
		}
		
		if quote.Source != "sim" {
			t.Errorf("Source = %v, want sim", quote.Source)
		}
		
		if err := ValidateQuote(quote); err != nil {
			t.Errorf("Invalid simulated quote: %v", err)
		}
		
		// Check realistic price movement
		if quote.Bid <= 0 || quote.Ask <= 0 || quote.Last <= 0 {
			t.Error("Simulated quote has invalid prices")
		}
		
		if quote.Ask <= quote.Bid {
			t.Error("Simulated quote has invalid spread")
		}
	})
	
	t.Run("consistent pricing", func(t *testing.T) {
		// Get multiple quotes for same symbol
		quote1, _ := adapter.GetQuote(ctx, "AAPL")
		quote2, _ := adapter.GetQuote(ctx, "AAPL")
		
		// Prices should be different due to random walk
		priceDiff := abs(quote1.Last - quote2.Last)
		if priceDiff == 0 {
			t.Error("Expected price movement between quotes")
		}
		
		// But not wildly different (within reasonable bounds)
		maxExpectedMove := quote1.Last * 0.1 // 10% max move
		if priceDiff > maxExpectedMove {
			t.Errorf("Price moved too much: %.2f -> %.2f", quote1.Last, quote2.Last)
		}
	})
	
	t.Run("halt functionality", func(t *testing.T) {
		adapter.SetHalted("AAPL", true)
		quote, _ := adapter.GetQuote(ctx, "AAPL")
		
		if !quote.Halted {
			t.Error("Expected quote to be halted")
		}
		
		adapter.SetHalted("AAPL", false)
		quote, _ = adapter.GetQuote(ctx, "AAPL")
		
		if quote.Halted {
			t.Error("Expected quote to not be halted")
		}
	})
}

func TestGetCurrentSession(t *testing.T) {
	session := GetCurrentSession()
	
	validSessions := map[SessionType]bool{
		SessionPremarket:  true,
		SessionRegular:    true,
		SessionPostmarket: true,
		SessionClosed:     true,
		SessionUnknown:    true,
	}
	
	if !validSessions[session] {
		t.Errorf("GetCurrentSession() returned invalid session: %v", session)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func BenchmarkMockQuoteAdapter(b *testing.B) {
	adapter := NewMockQuotesAdapter()
	ctx := context.Background()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := adapter.GetQuote(ctx, "AAPL")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSimQuoteAdapter(b *testing.B) {
	adapter := NewSimQuotesAdapter()
	ctx := context.Background()
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := adapter.GetQuote(ctx, "AAPL")
		if err != nil {
			b.Fatal(err)
		}
	}
}