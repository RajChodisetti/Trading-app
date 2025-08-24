# Session 12 Plan: Real Adapter Integrations (Start with Quotes)

## Overview

Replace the "sim" quotes adapter with a real market data provider integration while maintaining safety rails and deterministic testing. This session establishes the adapter pattern for future provider swaps and begins the transition from simulated to live market data.

## What's Changed vs. Previous Sessions

- **Real Market Data**: Move from simulated quotes to actual market data feeds
- **Adapter Pattern**: Establish clean abstraction for swapping data providers
- **Provider Selection**: Start with a reliable, cost-effective quotes provider (Polygon, Alpha Vantage, or Twelve Data)
- **Safety First**: Maintain paper trading mode and all existing safety gates
- **Testing Strategy**: Keep deterministic tests with quote mocks while enabling live data option

## Acceptance Criteria

- **Real Quotes Integration**: Successfully fetch live quotes from chosen provider with proper error handling
- **Adapter Abstraction**: Clean interface allows easy swapping between sim/real/mock quote providers  
- **Configuration Control**: Environment variables control which adapter to use (`QUOTES=sim|polygon|mock`)
- **Market Hours**: Proper handling of pre-market, regular hours, after-hours, and market holidays
- **Error Resilience**: Graceful degradation when quotes unavailable, with fallback to cached/stale data
- **Testing**: All existing tests pass; new tests validate quote adapter behavior
- **Metrics**: Quote fetch latency, error rates, cache hit rates, and provider-specific metrics

## Implementation Plan

### 1) Provider Research & Selection (15 min)

**Provider Options:**
- **Polygon.io**: $99/month, real-time, excellent API, WebSocket support
- **Alpha Vantage**: Free tier available, good for development, rate limited
- **Twelve Data**: $8/month basic, good balance, multiple asset classes
- **IEX Cloud**: Developer-friendly, reasonable pricing, good documentation

**Selection Criteria:**
- Cost for development/testing
- API reliability and documentation quality  
- Rate limits and real-time capabilities
- Market coverage (US equities minimum)

**Decision**: Start with **Alpha Vantage** for free development, upgrade path to Polygon for production

### 2) Adapter Interface Design (20 min)

**Core Interface:**
```go
type QuotesAdapter interface {
    GetQuote(symbol string) (*Quote, error)
    GetQuotes(symbols []string) (map[string]*Quote, error)
    HealthCheck() error
    Close() error
}

type Quote struct {
    Symbol    string    `json:"symbol"`
    Bid       float64   `json:"bid"`
    Ask       float64   `json:"ask"`  
    Last      float64   `json:"last"`
    Volume    int64     `json:"volume"`
    Timestamp time.Time `json:"timestamp"`
    Source    string    `json:"source"`
}
```

**Implementations:**
- `SimQuotesAdapter` (existing sim behavior)
- `AlphaVantageAdapter` (real market data)
- `MockQuotesAdapter` (for deterministic testing)

### 3) Alpha Vantage Integration (25 min)

**API Integration:**
- Real-time quote endpoint: `GLOBAL_QUOTE` function
- Batch quotes: Multiple symbol support
- Rate limiting: 5 calls/minute free tier
- Error handling: API errors, network timeouts, invalid symbols

**Key Features:**
```go
type AlphaVantageAdapter struct {
    apiKey     string
    httpClient *http.Client
    rateLimiter *rate.Limiter
    cache      map[string]*CachedQuote
}

type CachedQuote struct {
    Quote     *Quote
    FetchedAt time.Time
    TTL       time.Duration
}
```

**Market Hours Logic:**
- Check if market is open (9:30 AM - 4:00 PM ET, weekdays)
- Handle pre-market (4:00-9:30 AM) and after-hours (4:00-8:00 PM) sessions
- Use cached quotes when market closed
- Respect provider rate limits with exponential backoff

### 4) Configuration & Factory Pattern (15 min)

**Enhanced config/config.yaml:**
```yaml
adapters:
  QUOTES: "sim"  # sim | alphavantage | polygon | mock

quote_providers:
  alphavantage:
    api_key_env: "ALPHA_VANTAGE_API_KEY"
    rate_limit_per_minute: 5
    cache_ttl_seconds: 60
    timeout_seconds: 10
  
  polygon:
    api_key_env: "POLYGON_API_KEY" 
    rate_limit_per_minute: 100
    cache_ttl_seconds: 5
    timeout_seconds: 5
```

**Factory Pattern:**
```go
func NewQuotesAdapter(config Config) (QuotesAdapter, error) {
    switch config.Adapters.Quotes {
    case "sim":
        return NewSimQuotesAdapter(), nil
    case "alphavantage":
        return NewAlphaVantageAdapter(config.QuoteProviders.AlphaVantage)
    case "mock":
        return NewMockQuotesAdapter(fixtures), nil
    default:
        return nil, fmt.Errorf("unknown quotes adapter: %s", config.Adapters.Quotes)
    }
}
```

### 5) Integration with Decision Engine (10 min)

**Update cmd/decision/main.go:**
- Replace direct fixture loading with adapter pattern
- Add quote adapter initialization with proper error handling
- Maintain existing decision flow with enhanced quote data
- Add quote freshness validation (reject stale quotes)

**Quote Validation:**
```go
func validateQuote(quote *Quote) error {
    if quote.Bid <= 0 || quote.Ask <= 0 {
        return fmt.Errorf("invalid quote prices: bid=%.2f ask=%.2f", quote.Bid, quote.Ask)
    }
    if quote.Ask <= quote.Bid {
        return fmt.Errorf("invalid spread: ask(%.2f) <= bid(%.2f)", quote.Ask, quote.Bid)
    }
    if time.Since(quote.Timestamp) > 5*time.Minute {
        return fmt.Errorf("stale quote: %v old", time.Since(quote.Timestamp))
    }
    return nil
}
```

### 6) Testing & Metrics (10 min)

**Enhanced Testing:**
- Mock adapter for deterministic tests
- Real adapter integration test (requires API key)
- Quote validation test cases
- Market hours logic testing

**New Metrics:**
```go
observ.IncCounter("quotes_fetched_total", map[string]string{
    "provider": "alphavantage",
    "symbol": symbol,
})
observ.Observe("quote_fetch_latency_ms", latency, map[string]string{
    "provider": "alphavantage",
})
observ.IncCounter("quote_errors_total", map[string]string{
    "provider": "alphavantage", 
    "error_type": "rate_limit",
})
```

**Test Cases:**
- Valid quote fetching and validation
- Rate limit handling and backoff
- Market hours and cache behavior
- Network error resilience
- Invalid symbol handling

## Success Metrics

- **Quote Integration**: Real quotes successfully fetched from Alpha Vantage API
- **Performance**: Quote fetch latency <500ms p95, cache hit rate >80%  
- **Reliability**: <1% quote fetch error rate during market hours
- **Testing**: All existing tests pass, new adapter tests achieve >90% coverage
- **Safety**: Paper trading mode maintained, no real money at risk

## Dependencies

- **API Access**: Alpha Vantage free API key (5 calls/minute limit)
- **Network**: Reliable internet connection for API calls
- **Configuration**: Environment variable support for API keys
- **Testing**: Mock server capability for deterministic testing

## Risk Mitigation

- **Rate Limits**: Implement proper rate limiting and caching
- **API Failures**: Graceful degradation with cached/stale quotes
- **Cost Control**: Start with free tier, monitor usage carefully
- **Safety Gates**: All existing gates remain active, paper mode enforced
- **Rollback Plan**: Easy config switch back to sim adapter

## Evidence Required

- Real quotes displaying in decision logs with provider attribution
- Quote fetch metrics showing <500ms latency and >95% success rate
- All integration tests passing with both sim and real adapters
- Market hours logic working correctly (cache during closed hours)
- Error handling demonstrated (network failures, invalid symbols, rate limits)

## Next Session Preview

Session 13 will focus on **Drawdown Monitoring and Circuit Breakers**, implementing real-time portfolio drawdown tracking with automatic trading halts to protect against significant losses.