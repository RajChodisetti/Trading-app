package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// AlphaVantageAdapter implements QuotesAdapter for Alpha Vantage API
type AlphaVantageAdapter struct {
	apiKey      string
	httpClient  *http.Client
	rateLimiter *rate.Limiter
	cache       *quoteCache
	config      AlphaVantageConfig
	
	// Budget tracking
	mu              sync.RWMutex
	requestsToday   int
	budgetResetTime time.Time
	
	// Health tracking
	consecutiveErrors int
	lastHealthCheck   time.Time
	healthy          bool
}

// AlphaVantageConfig holds configuration for Alpha Vantage adapter
type AlphaVantageConfig struct {
	APIKey              string
	RateLimitPerMinute  int
	DailyCap            int
	CacheTTLSeconds     int
	StaleCeilingSeconds int
	TimeoutSeconds      int
	MaxRetries          int
	BackoffBaseMs       int
}

// quoteCache provides thread-safe LRU cache with TTL
type quoteCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	maxSize int
}

type cacheEntry struct {
	quote     *Quote
	fetchedAt time.Time
	ttl       time.Duration
}

// NewAlphaVantageAdapter creates a new Alpha Vantage adapter
func NewAlphaVantageAdapter(config AlphaVantageConfig) (*AlphaVantageAdapter, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("Alpha Vantage API key is required")
	}
	
	// Set defaults
	if config.RateLimitPerMinute <= 0 {
		config.RateLimitPerMinute = 5 // Free tier limit
	}
	if config.DailyCap <= 0 {
		config.DailyCap = 300 // Conservative daily limit
	}
	if config.CacheTTLSeconds <= 0 {
		config.CacheTTLSeconds = 60
	}
	if config.StaleCeilingSeconds <= 0 {
		config.StaleCeilingSeconds = 180 // 3 minutes max staleness
	}
	if config.TimeoutSeconds <= 0 {
		config.TimeoutSeconds = 10
	}
	if config.MaxRetries <= 0 {
		config.MaxRetries = 3
	}
	if config.BackoffBaseMs <= 0 {
		config.BackoffBaseMs = 1000
	}
	
	return &AlphaVantageAdapter{
		apiKey: config.APIKey,
		httpClient: &http.Client{
			Timeout: time.Duration(config.TimeoutSeconds) * time.Second,
		},
		rateLimiter: rate.NewLimiter(rate.Limit(float64(config.RateLimitPerMinute)/60), 1),
		cache: &quoteCache{
			entries: make(map[string]*cacheEntry),
			maxSize: 1000, // Max cached symbols
		},
		config:            config,
		budgetResetTime:   time.Now().Add(24 * time.Hour),
		healthy:           true,
		consecutiveErrors: 0,
	}, nil
}

// GetQuote fetches a single quote with caching and rate limiting
func (av *AlphaVantageAdapter) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return nil, NewBadSymbolError(symbol, "empty symbol")
	}
	
	// Check cache first
	if cachedEntry := av.cache.getEntry(symbol); cachedEntry != nil {
		staleness := time.Since(cachedEntry.fetchedAt)
		cachedQuote := *cachedEntry.quote // Copy quote
		cachedQuote.StalenessMs = staleness.Milliseconds()
		
		// Return cached if within TTL
		if staleness <= cachedEntry.ttl {
			return &cachedQuote, nil
		}
		
		// Check stale ceiling - fail if too old
		if staleness.Seconds() > float64(av.config.StaleCeilingSeconds) {
			return nil, NewStaleError(symbol, staleness)
		}
		
		// Return stale quote if fresh fetch would exceed budget/rate
		if !av.canMakeRequest() {
			return &cachedQuote, nil
		}
	}
	
	// Fetch fresh quote
	quote, err := av.fetchQuote(ctx, symbol)
	if err != nil {
		av.recordError()
		
		// Return cached quote if available, even if stale
		if cachedEntry := av.cache.getEntry(symbol); cachedEntry != nil {
			staleness := time.Since(cachedEntry.fetchedAt)
			if staleness.Seconds() <= float64(av.config.StaleCeilingSeconds) {
				cachedQuote := *cachedEntry.quote
				cachedQuote.StalenessMs = staleness.Milliseconds()
				return &cachedQuote, nil
			}
		}
		
		return nil, err
	}
	
	// Cache successful fetch
	av.cache.put(symbol, quote, time.Duration(av.config.CacheTTLSeconds)*time.Second)
	av.recordSuccess()
	
	return quote, nil
}

// GetQuotes fetches multiple quotes with batch optimization
func (av *AlphaVantageAdapter) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	results := make(map[string]*Quote)
	
	// Alpha Vantage doesn't support batch requests, so fetch individually
	for _, symbol := range symbols {
		quote, err := av.GetQuote(ctx, symbol)
		if err != nil {
			// Log error but continue with other symbols
			continue
		}
		results[symbol] = quote
	}
	
	return results, nil
}

// HealthCheck verifies adapter health
func (av *AlphaVantageAdapter) HealthCheck(ctx context.Context) error {
	av.mu.RLock()
	healthy := av.healthy
	lastCheck := av.lastHealthCheck
	av.mu.RUnlock()
	
	// Use cached health status if recent
	if time.Since(lastCheck) < 30*time.Second {
		if !healthy {
			return fmt.Errorf("Alpha Vantage adapter unhealthy (consecutive errors: %d)", av.consecutiveErrors)
		}
		return nil
	}
	
	// Perform health check with a test symbol
	_, err := av.GetQuote(ctx, "AAPL")
	
	av.mu.Lock()
	av.lastHealthCheck = time.Now()
	if err != nil {
		av.healthy = false
		av.mu.Unlock()
		return fmt.Errorf("Alpha Vantage health check failed: %v", err)
	}
	av.healthy = true
	av.mu.Unlock()
	
	return nil
}

// Close performs cleanup
func (av *AlphaVantageAdapter) Close() error {
	// No persistent connections to close for HTTP client
	return nil
}

// fetchQuote makes the actual API request to Alpha Vantage
func (av *AlphaVantageAdapter) fetchQuote(ctx context.Context, symbol string) (*Quote, error) {
	// Check budget and rate limits
	if !av.canMakeRequest() {
		return nil, NewRateLimitError(symbol, "rate limit or daily budget exceeded")
	}
	
	// Wait for rate limiter
	if err := av.rateLimiter.Wait(ctx); err != nil {
		return nil, NewNetworkError(symbol, "rate limit wait cancelled", err)
	}
	
	// Increment request counter
	av.incrementRequestCount()
	
	// Build request URL
	baseURL := "https://www.alphavantage.co/query"
	params := url.Values{
		"function": {"GLOBAL_QUOTE"},
		"symbol":   {symbol},
		"apikey":   {av.apiKey},
	}
	
	requestURL := baseURL + "?" + params.Encode()
	
	// Make request with retries
	var lastErr error
	for attempt := 0; attempt < av.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff with jitter
			backoff := time.Duration(av.config.BackoffBaseMs*(1<<attempt)) * time.Millisecond
			jitter := time.Duration(200+av.rateLimiter.Tokens()*100) * time.Millisecond
			time.Sleep(backoff + jitter)
		}
		
		req, err := http.NewRequestWithContext(ctx, "GET", requestURL, nil)
		if err != nil {
			return nil, NewNetworkError(symbol, "failed to create request", err)
		}
		
		resp, err := av.httpClient.Do(req)
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
		quote, err := av.parseGlobalQuoteResponse(resp.Body, symbol)
		if err != nil {
			lastErr = err
			continue
		}
		
		return quote, nil
	}
	
	return nil, lastErr
}

// parseGlobalQuoteResponse parses Alpha Vantage GLOBAL_QUOTE response
func (av *AlphaVantageAdapter) parseGlobalQuoteResponse(body io.Reader, symbol string) (*Quote, error) {
	var response struct {
		GlobalQuote map[string]string `json:"Global Quote"`
		ErrorMessage string           `json:"Error Message"`
		Information  string           `json:"Information"`
	}
	
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return nil, NewProviderError(symbol, "failed to parse response", err)
	}
	
	// Check for API errors
	if response.ErrorMessage != "" {
		return nil, NewProviderError(symbol, response.ErrorMessage, nil)
	}
	if response.Information != "" {
		// Usually rate limit or API call frequency message
		return nil, NewRateLimitError(symbol, response.Information)
	}
	
	quote := response.GlobalQuote
	if len(quote) == 0 {
		return nil, NewBadSymbolError(symbol, "no quote data returned")
	}
	
	// Parse quote fields (Alpha Vantage uses numbered keys)
	bid, _ := strconv.ParseFloat(quote["05. price"], 64)
	ask, _ := strconv.ParseFloat(quote["05. price"], 64) // AV doesn't provide bid/ask, use price
	last, _ := strconv.ParseFloat(quote["05. price"], 64)
	volume, _ := strconv.ParseInt(quote["06. volume"], 10, 64)
	
	// Add typical spread for simulation (0.02-0.05%)
	spread := last * 0.0003
	bid = last - spread/2
	ask = last + spread/2
	
	// Parse timestamp
	timestamp := time.Now() // AV doesn't provide real-time timestamp in free tier
	
	return &Quote{
		Symbol:      symbol,
		Bid:         bid,
		Ask:         ask,
		Last:        last,
		Volume:      volume,
		Timestamp:   timestamp,
		Session:     string(GetCurrentSession()),
		Halted:      false, // AV doesn't provide halt status in GLOBAL_QUOTE
		Source:      "alphavantage",
		StalenessMs: 0,
	}, nil
}

// canMakeRequest checks budget and rate limits
func (av *AlphaVantageAdapter) canMakeRequest() bool {
	av.mu.RLock()
	defer av.mu.RUnlock()
	
	// Reset daily budget if needed
	if time.Now().After(av.budgetResetTime) {
		av.mu.RUnlock()
		av.mu.Lock()
		av.requestsToday = 0
		av.budgetResetTime = time.Now().Add(24 * time.Hour)
		av.mu.Unlock()
		av.mu.RLock()
	}
	
	return av.requestsToday < av.config.DailyCap
}

// incrementRequestCount tracks API usage
func (av *AlphaVantageAdapter) incrementRequestCount() {
	av.mu.Lock()
	defer av.mu.Unlock()
	av.requestsToday++
}

// recordError tracks consecutive errors for health monitoring
func (av *AlphaVantageAdapter) recordError() {
	av.mu.Lock()
	defer av.mu.Unlock()
	av.consecutiveErrors++
	if av.consecutiveErrors >= 3 {
		av.healthy = false
	}
}

// recordSuccess resets error tracking
func (av *AlphaVantageAdapter) recordSuccess() {
	av.mu.Lock()
	defer av.mu.Unlock()
	av.consecutiveErrors = 0
	av.healthy = true
}

// Cache methods
func (c *quoteCache) get(symbol string) *Quote {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	entry, exists := c.entries[symbol]
	if !exists {
		return nil
	}
	
	// Create copy to avoid race conditions
	quoteCopy := *entry.quote
	return &quoteCopy
}

func (c *quoteCache) getEntry(symbol string) *cacheEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	entry, exists := c.entries[symbol]
	if !exists {
		return nil
	}
	
	return entry
}

func (c *quoteCache) put(symbol string, quote *Quote, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// Simple LRU eviction if cache is full
	if len(c.entries) >= c.maxSize {
		// Remove oldest entry (simplified LRU)
		var oldestSymbol string
		var oldestTime time.Time = time.Now()
		for sym, entry := range c.entries {
			if entry.fetchedAt.Before(oldestTime) {
				oldestTime = entry.fetchedAt
				oldestSymbol = sym
			}
		}
		delete(c.entries, oldestSymbol)
	}
	
	c.entries[symbol] = &cacheEntry{
		quote:     quote,
		fetchedAt: time.Now(),
		ttl:       ttl,
	}
}

// GetBudgetStatus returns current budget usage
func (av *AlphaVantageAdapter) GetBudgetStatus() (used, total int, resetTime time.Time) {
	av.mu.RLock()
	defer av.mu.RUnlock()
	return av.requestsToday, av.config.DailyCap, av.budgetResetTime
}