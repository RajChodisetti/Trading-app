package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MockQuotesAdapter provides deterministic quotes for testing
type MockQuotesAdapter struct {
	quotes    map[string]*Quote
	healthOk  bool
	latencyMs int
}

// NewMockQuotesAdapter creates a mock adapter with predefined quotes
func NewMockQuotesAdapter() *MockQuotesAdapter {
	now := time.Now()
	
	return &MockQuotesAdapter{
		quotes: map[string]*Quote{
			"AAPL": {
				Symbol:      "AAPL",
				Bid:         206.70,
				Ask:         206.90,
				Last:        206.80,
				Volume:      12500000,
				Timestamp:   now.Add(-30 * time.Second), // Recent quote
				Session:     string(GetCurrentSession()),
				Halted:      false,
				Source:      "mock",
				StalenessMs: 30000, // 30 seconds old
			},
			"NVDA": {
				Symbol:      "NVDA",
				Bid:         449.90,
				Ask:         450.10,
				Last:        450.00,
				Volume:      8200000,
				Timestamp:   now.Add(-45 * time.Second),
				Session:     string(GetCurrentSession()),
				Halted:      true, // Halted for testing
				Source:      "mock",
				StalenessMs: 45000,
			},
			"BIOX": {
				Symbol:      "BIOX",
				Bid:         12.45,
				Ask:         12.55,
				Last:        12.50,
				Volume:      125000,
				Timestamp:   now.Add(-2 * time.Minute),
				Session:     string(GetCurrentSession()),
				Halted:      false,
				Source:      "mock",
				StalenessMs: 120000, // 2 minutes old
			},
			"STALE": {
				Symbol:      "STALE",
				Bid:         100.00,
				Ask:         100.10,
				Last:        100.05,
				Volume:      1000,
				Timestamp:   now.Add(-10 * time.Minute), // Very stale for testing
				Session:     string(GetCurrentSession()),
				Halted:      false,
				Source:      "mock",
				StalenessMs: 600000, // 10 minutes old
			},
			"INVALID": {
				Symbol:      "INVALID",
				Bid:         50.00,
				Ask:         49.90, // Invalid: ask < bid
				Last:        49.95,
				Volume:      1000,
				Timestamp:   now,
				Session:     string(GetCurrentSession()),
				Halted:      false,
				Source:      "mock",
				StalenessMs: 0,
			},
		},
		healthOk:  true,
		latencyMs: 50, // Simulate 50ms latency
	}
}

// GetQuote returns a mock quote for the given symbol
func (m *MockQuotesAdapter) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	// Simulate latency
	if m.latencyMs > 0 {
		time.Sleep(time.Duration(m.latencyMs) * time.Millisecond)
	}
	
	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	
	quote, exists := m.quotes[symbol]
	if !exists {
		return nil, NewBadSymbolError(symbol, "symbol not found in mock data")
	}
	
	// Create a copy with updated staleness
	now := time.Now()
	quoteCopy := *quote
	quoteCopy.StalenessMs = now.Sub(quote.Timestamp).Milliseconds()
	
	return &quoteCopy, nil
}

// GetQuotes returns mock quotes for multiple symbols
func (m *MockQuotesAdapter) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	results := make(map[string]*Quote)
	
	for _, symbol := range symbols {
		quote, err := m.GetQuote(ctx, symbol)
		if err != nil {
			// Log error but continue with other symbols
			continue
		}
		results[symbol] = quote
	}
	
	return results, nil
}

// HealthCheck returns the mock health status
func (m *MockQuotesAdapter) HealthCheck(ctx context.Context) error {
	if !m.healthOk {
		return fmt.Errorf("mock adapter unhealthy")
	}
	return nil
}

// Close performs cleanup (no-op for mock)
func (m *MockQuotesAdapter) Close() error {
	return nil
}

// SetHealth allows tests to control health status
func (m *MockQuotesAdapter) SetHealth(healthy bool) {
	m.healthOk = healthy
}

// SetLatency allows tests to control simulated latency
func (m *MockQuotesAdapter) SetLatency(ms int) {
	m.latencyMs = ms
}

// AddQuote allows tests to add custom quotes
func (m *MockQuotesAdapter) AddQuote(quote *Quote) {
	if quote != nil {
		m.quotes[quote.Symbol] = quote
	}
}

// RemoveQuote allows tests to remove quotes
func (m *MockQuotesAdapter) RemoveQuote(symbol string) {
	delete(m.quotes, strings.ToUpper(strings.TrimSpace(symbol)))
}

// GetAvailableSymbols returns all symbols available in mock data
func (m *MockQuotesAdapter) GetAvailableSymbols() []string {
	symbols := make([]string, 0, len(m.quotes))
	for symbol := range m.quotes {
		symbols = append(symbols, symbol)
	}
	return symbols
}