package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// PolygonAdapter implements QuotesAdapter for Polygon.io API
type PolygonAdapter struct {
	apiKey      string
	httpClient  *http.Client
	rateLimiter *rate.Limiter
	cache       *quoteCache
	config      PolygonConfig
	
	// Budget tracking
	mu              sync.RWMutex
	requestsToday   int
	budgetResetTime time.Time
	
	// Health tracking
	consecutiveErrors int
	lastHealthCheck   time.Time
	healthy          bool
	
	// Real-time capabilities
	supportsRealtime bool
}

// PolygonConfig holds configuration for Polygon adapter
type PolygonConfig struct {
	APIKey              string
	RateLimitPerMinute  int
	DailyRequestCap     int
	CacheTTLSeconds     int
	StaleCeilingSeconds int
	TimeoutSeconds      int
	MaxRetries          int
	BackoffBaseMs       int
	CostPerRequestUSD   float64
	RealTimeData        bool
}

// NewPolygonAdapter creates a new Polygon.io adapter
func NewPolygonAdapter(config PolygonConfig) (*PolygonAdapter, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("Polygon API key is required")
	}
	
	// Set defaults optimized for Polygon
	if config.RateLimitPerMinute <= 0 {
		config.RateLimitPerMinute = 100 // Higher rate limits than Alpha Vantage
	}
	if config.DailyRequestCap <= 0 {
		config.DailyRequestCap = 50000 // Much higher daily cap
	}
	if config.CacheTTLSeconds <= 0 {
		config.CacheTTLSeconds = 10 // Shorter TTL for real-time data
	}
	if config.StaleCeilingSeconds <= 0 {
		config.StaleCeilingSeconds = 60 // Allow longer staleness
	}
	if config.TimeoutSeconds <= 0 {
		config.TimeoutSeconds = 5 // Faster timeout
	}
	if config.MaxRetries <= 0 {
		config.MaxRetries = 3
	}
	if config.BackoffBaseMs <= 0 {
		config.BackoffBaseMs = 500 // Faster backoff
	}
	
	return &PolygonAdapter{
		apiKey: config.APIKey,
		httpClient: &http.Client{
			Timeout: time.Duration(config.TimeoutSeconds) * time.Second,
		},
		rateLimiter: rate.NewLimiter(rate.Limit(float64(config.RateLimitPerMinute)/60), 2),
		cache: &quoteCache{
			entries: make(map[string]*cacheEntry),
			maxSize: 2000,
		},
		config:            config,
		budgetResetTime:   time.Now().Add(24 * time.Hour),
		healthy:           true,
		consecutiveErrors: 0,
		supportsRealtime:  config.RealTimeData,
	}, nil
}

// GetQuote fetches a single quote from Polygon.io
func (p *PolygonAdapter) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	symbol = normalizeSymbol(symbol)
	if symbol == "" {
		return nil, NewBadSymbolError(symbol, "empty symbol")
	}
	
	// Check cache first
	if cachedEntry := p.cache.getEntry(symbol); cachedEntry != nil {
		staleness := time.Since(cachedEntry.fetchedAt)
		cached := *cachedEntry.quote // Copy quote
		cached.StalenessMs = staleness.Milliseconds()
		
		// Return cached if within TTL
		if staleness <= cachedEntry.ttl {
			return &cached, nil
		}
		
		// Check stale ceiling - fail if too old
		if staleness.Seconds() > float64(p.config.StaleCeilingSeconds) {
			return nil, NewStaleError(symbol, staleness)
		}
		
		// Return stale quote if fresh fetch would exceed budget/rate
		if !p.canMakeRequest() {
			return &cached, nil
		}
	}
	
	// Fetch fresh quote
	quote, err := p.fetchQuote(ctx, symbol)
	if err != nil {
		p.recordError()
		
		// Return cached quote if available, even if stale
		if cachedEntry := p.cache.getEntry(symbol); cachedEntry != nil {
			staleness := time.Since(cachedEntry.fetchedAt)
			if staleness.Seconds() <= float64(p.config.StaleCeilingSeconds) {
				cached := *cachedEntry.quote
				cached.StalenessMs = staleness.Milliseconds()
				return &cached, nil
			}
		}
		
		return nil, err
	}
	
	// Cache successful fetch
	p.cache.put(symbol, quote, time.Duration(p.config.CacheTTLSeconds)*time.Second)
	p.recordSuccess()
	
	return quote, nil
}

// GetQuotes fetches multiple quotes with potential batch optimization
func (p *PolygonAdapter) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	results := make(map[string]*Quote)
	
	// Polygon supports some batch operations, but for now fetch individually
	// TODO: Implement batch API once we have sufficient volume
	for _, symbol := range symbols {
		quote, err := p.GetQuote(ctx, symbol)
		if err != nil {
			// Log error but continue with other symbols
			observ.Log("polygon_quote_error", map[string]any{
				"symbol": symbol,
				"error":  err.Error(),
			})
			continue
		}
		results[symbol] = quote
	}
	
	return results, nil
}

// HealthCheck verifies adapter health
func (p *PolygonAdapter) HealthCheck(ctx context.Context) error {
	p.mu.RLock()
	healthy := p.healthy
	lastCheck := p.lastHealthCheck
	p.mu.RUnlock()
	
	// Use cached health status if recent
	if time.Since(lastCheck) < 30*time.Second {
		if !healthy {
			return fmt.Errorf("Polygon adapter unhealthy (consecutive errors: %d)", p.consecutiveErrors)
		}
		return nil
	}
	
	// Perform health check with a test symbol
	_, err := p.GetQuote(ctx, "AAPL")
	
	p.mu.Lock()
	p.lastHealthCheck = time.Now()
	if err != nil {
		p.healthy = false
		p.mu.Unlock()
		return fmt.Errorf("Polygon health check failed: %v", err)
	}
	p.healthy = true
	p.mu.Unlock()
	
	return nil
}

// Close performs cleanup
func (p *PolygonAdapter) Close() error {
	// No persistent connections to close for HTTP client
	return nil
}

// fetchQuote makes the actual API request to Polygon.io
func (p *PolygonAdapter) fetchQuote(ctx context.Context, symbol string) (*Quote, error) {
	// Check budget and rate limits
	if !p.canMakeRequest() {
		return nil, NewRateLimitError(symbol, "rate limit or daily budget exceeded")
	}
	
	// Wait for rate limiter
	if err := p.rateLimiter.Wait(ctx); err != nil {
		return nil, NewNetworkError(symbol, "rate limit wait cancelled", err)
	}
	
	// Increment request counter
	p.incrementRequestCount()
	
	// Build request URL - using Polygon's last quote API
	baseURL := "https://api.polygon.io/v2/last/nbbo"
	params := url.Values{
		"ticker": {symbol},
		"apikey": {p.apiKey},
	}
	
	requestURL := baseURL + "/" + symbol + "?" + params.Encode()
	
	// Make request with retries
	var lastErr error
	for attempt := 0; attempt < p.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff with jitter
			backoff := time.Duration(p.config.BackoffBaseMs*(1<<attempt)) * time.Millisecond
			jitter := time.Duration(100+p.rateLimiter.Tokens()*50) * time.Millisecond
			time.Sleep(backoff + jitter)
		}
		
		req, err := http.NewRequestWithContext(ctx, "GET", requestURL, nil)
		if err != nil {
			return nil, NewNetworkError(symbol, "failed to create request", err)
		}
		
		resp, err := p.httpClient.Do(req)
		if err != nil {
			lastErr = NewNetworkError(symbol, "request failed", err)
			continue
		}
		defer resp.Body.Close()
		
		// Check for rate limiting
		if resp.StatusCode == 429 {
			lastErr = NewRateLimitError(symbol, "API rate limit exceeded")
			continue
		}
		
		// Check for other HTTP errors
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			lastErr = NewProviderError(symbol, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)), nil)
			continue
		}
		
		// Parse response
		quote, err := p.parsePolygonResponse(resp.Body, symbol)
		if err != nil {
			lastErr = err
			continue
		}
		
		return quote, nil
	}
	
	return nil, lastErr
}

// parsePolygonResponse parses Polygon.io last quote response
func (p *PolygonAdapter) parsePolygonResponse(body io.Reader, symbol string) (*Quote, error) {
	var response struct {
		Status string `json:"status"`
		Results struct {
			T  string  `json:"T"`  // Ticker
			P  float64 `json:"p"`  // Bid price
			S  int     `json:"s"`  // Bid size
			P1 float64 `json:"P"`  // Ask price  
			S1 int     `json:"S"`  // Ask size
			C  []int   `json:"c"`  // Conditions
			X  int     `json:"x"`  // Exchange
			Q  int64   `json:"q"`  // Sequence number
			T1 int64   `json:"t"`  // Timestamp (nanoseconds)
		} `json:"results"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return nil, NewProviderError(symbol, "failed to parse response", err)
	}
	
	// Check for API errors
	if response.Status != "OK" {
		if response.Error != "" {
			return nil, NewProviderError(symbol, response.Error, nil)
		}
		if response.Message != "" {
			return nil, NewProviderError(symbol, response.Message, nil)
		}
		return nil, NewProviderError(symbol, "non-OK status: "+response.Status, nil)
	}
	
	// Parse quote data
	results := response.Results
	if results.T != symbol {
		return nil, NewBadSymbolError(symbol, "symbol mismatch in response")
	}
	
	// Calculate last price (mid-point for simplicity)
	last := (results.P + results.P1) / 2
	if last <= 0 {
		return nil, NewProviderError(symbol, "invalid price data", nil)
	}
	
	// Convert timestamp from nanoseconds to time
	timestamp := time.Unix(0, results.T1)
	
	// Calculate staleness
	staleness := time.Since(timestamp)
	
	return &Quote{
		Symbol:      symbol,
		Bid:         results.P,
		Ask:         results.P1,
		Last:        last,
		Volume:      int64(results.S + results.S1), // Combine bid/ask sizes as approximate volume
		Timestamp:   timestamp,
		Session:     string(GetCurrentSession()),
		Halted:      false, // TODO: Parse from conditions array
		Source:      "polygon",
		StalenessMs: staleness.Milliseconds(),
	}, nil
}

// canMakeRequest checks budget and rate limits
func (p *PolygonAdapter) canMakeRequest() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	// Reset daily budget if needed
	if time.Now().After(p.budgetResetTime) {
		p.mu.RUnlock()
		p.mu.Lock()
		p.requestsToday = 0
		p.budgetResetTime = time.Now().Add(24 * time.Hour)
		p.mu.Unlock()
		p.mu.RLock()
	}
	
	return p.requestsToday < p.config.DailyRequestCap
}

// incrementRequestCount tracks API usage
func (p *PolygonAdapter) incrementRequestCount() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requestsToday++
}

// recordError tracks consecutive errors for health monitoring
func (p *PolygonAdapter) recordError() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.consecutiveErrors++
	if p.consecutiveErrors >= 3 {
		p.healthy = false
	}
}

// recordSuccess resets error tracking
func (p *PolygonAdapter) recordSuccess() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.consecutiveErrors = 0
	p.healthy = true
}

// GetBudgetStatus returns current budget usage
func (p *PolygonAdapter) GetBudgetStatus() (used, total int, resetTime time.Time) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.requestsToday, p.config.DailyRequestCap, p.budgetResetTime
}

// GetCostEstimate returns estimated cost for budget tracking
func (p *PolygonAdapter) GetCostEstimate() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return float64(p.requestsToday) * p.config.CostPerRequestUSD
}

// SupportsRealtime returns whether this adapter provides real-time data
func (p *PolygonAdapter) SupportsRealtime() bool {
	return p.supportsRealtime
}

// normalizeSymbol normalizes symbol format for Polygon.io
func normalizeSymbol(symbol string) string {
	if symbol == "" {
		return ""
	}
	
	// Polygon uses standard format: AAPL, BRK.A, BRK.B
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	
	// Handle common variations
	switch {
	case strings.Contains(symbol, "BRK-A"):
		return "BRK.A"
	case strings.Contains(symbol, "BRK-B"):
		return "BRK.B"
	case strings.HasSuffix(symbol, ".US"):
		return strings.TrimSuffix(symbol, ".US")
	default:
		return symbol
	}
}

// GetProviderInfo returns metadata about this provider
func (p *PolygonAdapter) GetProviderInfo() map[string]any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	return map[string]any{
		"name":               "polygon",
		"supports_realtime":  p.supportsRealtime,
		"rate_limit_pm":      p.config.RateLimitPerMinute,
		"daily_cap":          p.config.DailyRequestCap,
		"requests_today":     p.requestsToday,
		"cost_per_request":   p.config.CostPerRequestUSD,
		"estimated_cost_usd": p.GetCostEstimate(),
		"healthy":            p.healthy,
		"consecutive_errors": p.consecutiveErrors,
	}
}