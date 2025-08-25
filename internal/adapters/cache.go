package adapters

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// QuoteCache provides thread-safe caching with TTL and freshness tracking
type QuoteCache struct {
	mu         sync.RWMutex
	quotes     map[string]CachedQuote
	maxAge     time.Duration
	metrics    *CacheMetrics
	logger     *log.Logger
}

// CachedQuote represents a quote with caching metadata
type CachedQuote struct {
	Quote      Quote     `json:"quote"`
	CachedAt   time.Time `json:"cached_at"`
	Source     string    `json:"source"`     // "live", "fallback", "mock"
	IsStale    bool      `json:"is_stale"`
	Freshness  time.Duration `json:"freshness"` // Time since quote timestamp
}

// CacheMetrics tracks cache performance
type CacheMetrics struct {
	Hits          int64 `json:"hits"`
	Misses        int64 `json:"misses"`
	Evictions     int64 `json:"evictions"`
	StaleReads    int64 `json:"stale_reads"`
	LastUpdated   time.Time `json:"last_updated"`
}

// NewQuoteCache creates a new quote cache
func NewQuoteCache(maxAge time.Duration, logger *log.Logger) *QuoteCache {
	return &QuoteCache{
		quotes:  make(map[string]CachedQuote),
		maxAge:  maxAge,
		metrics: &CacheMetrics{},
		logger:  logger,
	}
}

// Get retrieves a quote from cache, checking freshness
func (qc *QuoteCache) Get(symbol string) (Quote, bool, bool) {
	qc.mu.RLock()
	defer qc.mu.RUnlock()

	cached, exists := qc.quotes[symbol]
	if !exists {
		qc.metrics.Misses++
		observ.IncCounter("quote_cache_miss_total", map[string]string{
			"symbol": symbol,
		})
		return Quote{}, false, false
	}

	qc.metrics.Hits++
	
	// Check if cache entry is expired
	isExpired := time.Since(cached.CachedAt) > qc.maxAge
	
	// Check if quote itself is stale based on freshness ceiling
	freshnessCeiling := 5 * time.Second // RTH freshness requirement
	if time.Now().Hour() < 9 || time.Now().Hour() >= 16 { // After hours
		freshnessCeiling = 60 * time.Second
	}
	
	isStale := time.Since(cached.Quote.Timestamp) > freshnessCeiling
	if isStale {
		qc.metrics.StaleReads++
		observ.IncCounter("quote_cache_stale_read_total", map[string]string{
			"symbol": symbol,
			"source": cached.Source,
		})
	}

	observ.IncCounter("quote_cache_hit_total", map[string]string{
		"symbol": symbol,
		"source": cached.Source,
		"stale":  fmt.Sprintf("%t", isStale),
	})

	// Record freshness metric
	freshness := time.Since(cached.Quote.Timestamp)
	observ.RecordHistogram("quote_freshness_seconds", freshness.Seconds(), map[string]string{
		"symbol": symbol,
		"source": cached.Source,
	})

	return cached.Quote, true, isStale || isExpired
}

// Set stores a quote in cache with metadata
func (qc *QuoteCache) Set(symbol string, quote Quote, source string) {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	freshness := time.Since(quote.Timestamp)
	
	cached := CachedQuote{
		Quote:     quote,
		CachedAt:  time.Now(),
		Source:    source,
		IsStale:   freshness > 5*time.Second, // Mark as stale if > 5s old
		Freshness: freshness,
	}

	qc.quotes[symbol] = cached
	qc.metrics.LastUpdated = time.Now()

	observ.IncCounter("quote_cache_set_total", map[string]string{
		"symbol": symbol,
		"source": source,
	})

	qc.logger.Printf("Cached quote for %s from %s (freshness: %.2fs)", 
		symbol, source, freshness.Seconds())
}

// GetMetrics returns current cache metrics
func (qc *QuoteCache) GetMetrics() CacheMetrics {
	qc.mu.RLock()
	defer qc.mu.RUnlock()
	
	total := qc.metrics.Hits + qc.metrics.Misses
	hitRatio := 0.0
	if total > 0 {
		hitRatio = float64(qc.metrics.Hits) / float64(total)
	}

	observ.RecordGauge("quote_cache_hit_ratio", hitRatio, map[string]string{})
	observ.RecordGauge("quote_cache_size", float64(len(qc.quotes)), map[string]string{})

	return *qc.metrics
}

// Cleanup removes expired entries
func (qc *QuoteCache) Cleanup() {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	now := time.Now()
	evicted := 0
	
	for symbol, cached := range qc.quotes {
		if now.Sub(cached.CachedAt) > qc.maxAge {
			delete(qc.quotes, symbol)
			evicted++
		}
	}

	qc.metrics.Evictions += int64(evicted)
	
	if evicted > 0 {
		observ.IncCounterBy("quote_cache_evictions_total", map[string]string{}, float64(evicted))
		qc.logger.Printf("Evicted %d expired quotes from cache", evicted)
	}
}

// QuoteRefresher runs background quote refresh operations
type QuoteRefresher struct {
	cache           *QuoteCache
	provider        QuotesAdapter
	fallbackProvider QuotesAdapter
	watchlist       []string
	refreshInterval time.Duration
	health          *ProviderHealth
	budget          *RateBudget
	logger          *log.Logger
	ctx             context.Context
	cancel          context.CancelFunc
}

// NewQuoteRefresher creates a background quote refresher
func NewQuoteRefresher(cache *QuoteCache, provider QuotesAdapter, fallback QuotesAdapter, 
	watchlist []string, refreshInterval time.Duration, logger *log.Logger) *QuoteRefresher {
	
	ctx, cancel := context.WithCancel(context.Background())
	
	return &QuoteRefresher{
		cache:            cache,
		provider:         provider,
		fallbackProvider: fallback,
		watchlist:        watchlist,
		refreshInterval:  refreshInterval,
		health:           NewProviderHealth("quotes", logger),
		budget:           NewRateBudget(300, 24*time.Hour, logger), // 300 requests per day
		logger:           logger,
		ctx:              ctx,
		cancel:           cancel,
	}
}

// Start begins the background refresh loop
func (qr *QuoteRefresher) Start() {
	go qr.refreshLoop()
	go qr.cleanupLoop()
	qr.logger.Printf("Quote refresher started for %d symbols, interval: %v", 
		len(qr.watchlist), qr.refreshInterval)
}

// Stop halts the background refresh
func (qr *QuoteRefresher) Stop() {
	qr.cancel()
	qr.logger.Printf("Quote refresher stopped")
}

// refreshLoop runs the main refresh cycle
func (qr *QuoteRefresher) refreshLoop() {
	ticker := time.NewTicker(qr.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-qr.ctx.Done():
			return
		case <-ticker.C:
			qr.refreshQuotes()
		}
	}
}

// cleanupLoop periodically cleans expired cache entries
func (qr *QuoteRefresher) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-qr.ctx.Done():
			return
		case <-ticker.C:
			qr.cache.Cleanup()
		}
	}
}

// refreshQuotes fetches fresh quotes for watchlist symbols
func (qr *QuoteRefresher) refreshQuotes() {
	start := time.Now()
	
	// Check budget before making requests
	if !qr.budget.CanMakeRequest() {
		qr.logger.Printf("Skipping quote refresh - daily budget exhausted")
		observ.IncCounter("quote_refresh_budget_exhausted_total", map[string]string{})
		return
	}

	// Determine which provider to use based on health
	currentProvider := qr.provider
	source := "live"
	
	if qr.health.GetStatus() == ProviderStatusFailed {
		currentProvider = qr.fallbackProvider
		source = "fallback"
		observ.IncCounter("quote_refresh_fallback_activation_total", map[string]string{
			"reason": "provider_failed",
		})
	}

	// Fetch quotes
	quotes, err := currentProvider.GetQuotes(context.Background(), qr.watchlist)
	latency := time.Since(start)
	
	observ.RecordHistogram("quote_refresh_latency_ms", latency.Seconds()*1000, map[string]string{
		"source": source,
	})

	if err != nil {
		qr.health.RecordError(err)
		qr.logger.Printf("Quote refresh failed: %v", err)
		
		// Try fallback if primary failed
		if source == "live" && qr.fallbackProvider != nil {
			qr.logger.Printf("Trying fallback provider...")
			quotes, err = qr.fallbackProvider.GetQuotes(context.Background(), qr.watchlist)
			if err == nil {
				source = "fallback"
				observ.IncCounter("quote_refresh_fallback_success_total", map[string]string{})
			}
		}
		
		if err != nil {
			observ.IncCounter("quote_refresh_error_total", map[string]string{
				"source": source,
				"error":  "fetch_failed",
			})
			return
		}
	}

	// Record successful fetch
	qr.health.RecordSuccess(latency)
	qr.budget.RecordRequest()

	// Validate and cache quotes
	validQuotes := 0
	for symbol, quote := range quotes {
		if qr.validateQuote(*quote) {
			qr.cache.Set(symbol, *quote, source)
			validQuotes++
		} else {
			observ.IncCounter("quote_rejected_total", map[string]string{
				"symbol": symbol,
				"source": source,
				"reason": "validation_failed",
			})
		}
	}

	qr.logger.Printf("Refreshed %d/%d valid quotes from %s (latency: %v)", 
		validQuotes, len(qr.watchlist), source, latency)
	
	observ.IncCounter("quote_refresh_success_total", map[string]string{
		"source": source,
	})
	observ.RecordGauge("quote_refresh_valid_count", float64(validQuotes), map[string]string{
		"source": source,
	})
}

// validateQuote performs strict data validation
func (qr *QuoteRefresher) validateQuote(quote Quote) bool {
	// Basic price validation
	if quote.Bid <= 0 || quote.Ask <= 0 || quote.Last <= 0 {
		return false
	}

	// Bid must be less than ask
	if quote.Bid >= quote.Ask {
		return false
	}

	// Last price should be reasonable relative to bid/ask
	if quote.Last < quote.Bid*0.95 || quote.Last > quote.Ask*1.05 {
		return false
	}

	// Spread sanity check (< 3% for most symbols)
	spread := (quote.Ask - quote.Bid) / quote.Last
	if spread > 0.03 {
		// Allow wider spreads for micro-caps or after hours
		if quote.Last > 5.0 && (time.Now().Hour() >= 9 && time.Now().Hour() < 16) {
			return false
		}
	}

	// Timestamp recency check
	staleness := time.Since(quote.Timestamp)
	maxStaleness := 5 * time.Minute
	if time.Now().Hour() >= 9 && time.Now().Hour() < 16 { // RTH
		maxStaleness = 30 * time.Second
	}
	
	if staleness > maxStaleness {
		return false
	}

	return true
}