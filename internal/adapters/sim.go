package adapters

import (
	"context"
	"math"
	"math/rand"
	"strings"
	"time"
)

// SimQuotesAdapter provides simulated quotes with realistic behavior
type SimQuotesAdapter struct {
	baseQuotes map[string]*baseQuote
	random     *rand.Rand
}

type baseQuote struct {
	Symbol    string
	BasePrice float64
	Volatility float64 // Daily volatility as decimal (e.g., 0.02 for 2%)
	Volume    int64
	Halted    bool
}

// NewSimQuotesAdapter creates a sim adapter with realistic quote simulation
func NewSimQuotesAdapter() *SimQuotesAdapter {
	return &SimQuotesAdapter{
		baseQuotes: map[string]*baseQuote{
			"AAPL": {
				Symbol:     "AAPL",
				BasePrice:  206.80,
				Volatility: 0.025, // 2.5% daily volatility
				Volume:     15000000,
				Halted:     false,
			},
			"NVDA": {
				Symbol:     "NVDA", 
				BasePrice:  450.00,
				Volatility: 0.035, // 3.5% daily volatility
				Volume:     10000000,
				Halted:     false, // Can be dynamically set
			},
			"BIOX": {
				Symbol:     "BIOX",
				BasePrice:  12.50,
				Volatility: 0.055, // 5.5% daily volatility (smaller stock)
				Volume:     200000,
				Halted:     false,
			},
			"MSFT": {
				Symbol:     "MSFT",
				BasePrice:  415.75,
				Volatility: 0.022,
				Volume:     12000000,
				Halted:     false,
			},
			"GOOGL": {
				Symbol:     "GOOGL", 
				BasePrice:  172.50,
				Volatility: 0.028,
				Volume:     8000000,
				Halted:     false,
			},
		},
		random: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// GetQuote generates a simulated quote with realistic price movement
func (s *SimQuotesAdapter) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	// Small latency simulation (10-50ms)
	latency := time.Duration(10+s.random.Intn(40)) * time.Millisecond
	time.Sleep(latency)
	
	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	
	base, exists := s.baseQuotes[symbol]
	if !exists {
		return nil, NewBadSymbolError(symbol, "symbol not supported by sim adapter")
	}
	
	now := time.Now()
	
	// Generate realistic price with random walk
	priceChange := s.generatePriceMovement(base.Volatility)
	currentPrice := base.BasePrice * (1 + priceChange)
	
	// Generate bid/ask spread (typically 0.01-0.05% for liquid stocks)
	spreadPct := 0.0001 + s.random.Float64()*0.0004 // 0.01-0.05%
	if base.BasePrice < 50 { // Wider spreads for cheaper stocks
		spreadPct *= 2
	}
	
	spread := currentPrice * spreadPct
	halfSpread := spread / 2
	
	bid := currentPrice - halfSpread
	ask := currentPrice + halfSpread
	last := currentPrice + (s.random.Float64()-0.5)*spread // Last within spread
	
	// Round to reasonable precision
	bid = roundToTick(bid, getTickSize(bid))
	ask = roundToTick(ask, getTickSize(ask))
	last = roundToTick(last, getTickSize(last))
	
	// Generate volume with some randomness
	volumeVariation := 0.7 + s.random.Float64()*0.6 // 70%-130% of base
	volume := int64(float64(base.Volume) * volumeVariation)
	
	quote := &Quote{
		Symbol:      symbol,
		Bid:         bid,
		Ask:         ask,
		Last:        last,
		Volume:      volume,
		Timestamp:   now,
		Session:     string(GetCurrentSession()),
		Halted:      base.Halted,
		Source:      "sim",
		StalenessMs: 0, // Fresh simulated quote
	}
	
	return quote, nil
}

// GetQuotes returns simulated quotes for multiple symbols
func (s *SimQuotesAdapter) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	results := make(map[string]*Quote)
	
	for _, symbol := range symbols {
		quote, err := s.GetQuote(ctx, symbol)
		if err != nil {
			// Continue with other symbols, log error in real implementation
			continue
		}
		results[symbol] = quote
	}
	
	return results, nil
}

// HealthCheck always returns healthy for sim adapter
func (s *SimQuotesAdapter) HealthCheck(ctx context.Context) error {
	return nil
}

// Close performs cleanup (no-op for sim)
func (s *SimQuotesAdapter) Close() error {
	return nil
}

// SetHalted allows external control of halt status (for testing)
func (s *SimQuotesAdapter) SetHalted(symbol string, halted bool) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if base, exists := s.baseQuotes[symbol]; exists {
		base.Halted = halted
	}
}

// generatePriceMovement creates realistic intraday price movement
func (s *SimQuotesAdapter) generatePriceMovement(dailyVol float64) float64 {
	// Convert daily volatility to per-minute volatility
	// Assuming 6.5 trading hours = 390 minutes
	minuteVol := dailyVol / math.Sqrt(390)
	
	// Generate random normal movement
	return s.random.NormFloat64() * minuteVol
}

// getTickSize returns appropriate tick size for price level
func getTickSize(price float64) float64 {
	if price >= 1.00 {
		return 0.01 // $0.01 ticks for stocks $1+
	}
	return 0.0001 // $0.0001 ticks for sub-dollar stocks
}

// roundToTick rounds price to appropriate tick size
func roundToTick(price, tickSize float64) float64 {
	return math.Round(price/tickSize) * tickSize
}

// AddSymbol allows adding new symbols to simulation
func (s *SimQuotesAdapter) AddSymbol(symbol string, basePrice, volatility float64, volume int64) {
	s.baseQuotes[strings.ToUpper(symbol)] = &baseQuote{
		Symbol:     strings.ToUpper(symbol),
		BasePrice:  basePrice,
		Volatility: volatility,
		Volume:     volume,
		Halted:     false,
	}
}

// GetSupportedSymbols returns all symbols supported by sim adapter
func (s *SimQuotesAdapter) GetSupportedSymbols() []string {
	symbols := make([]string, 0, len(s.baseQuotes))
	for symbol := range s.baseQuotes {
		symbols = append(symbols, symbol)
	}
	return symbols
}