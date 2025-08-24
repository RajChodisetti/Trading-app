# Session 15 Plan: Real Adapter Integration

## Overview

Replace remaining mock adapters with live market data feeds, implementing robust error handling, rate limiting, and graceful degradation. Build upon Sessions 13-14's risk management foundation to ensure all existing circuit breaker and position control protections remain active while transitioning to real-time data sources.

## What's Changed vs. Previous Sessions

- **Data Sources**: Transition from mock/stub data to live market data feeds (Alpha Vantage, Polygon, etc.)
- **Error Resilience**: Implement robust fallback mechanisms when live feeds fail or rate-limit
- **Performance Optimization**: Add caching, connection pooling, and efficient data parsing for real-time operations
- **Risk Preservation**: Maintain all existing risk controls (circuit breakers, caps, cooldowns) with live data
- **Operational Monitoring**: Enhanced observability for feed health, latency, and error rates

## Acceptance Criteria

- **Live Quotes Integration**: Real-time bid/ask/last prices from Alpha Vantage or Polygon with <100ms latency
- **News Feed Integration**: Live news ingestion from Reuters or Bloomberg with proper filtering and deduplication
- **Halts Data**: Real-time trading halt notifications with immediate position freeze capability
- **Error Handling**: Graceful degradation to cached data when live feeds fail, with clear operator alerts
- **Rate Limit Management**: Respect API rate limits with intelligent queuing and backoff strategies
- **Data Quality Validation**: Reject stale, malformed, or suspicious data with comprehensive logging
- **Backwards Compatibility**: All existing functionality (caps, cooldowns, circuit breakers) works with live data
- **Performance SLA**: Decision latency remains <200ms p95 even with live data integration

## Implementation Plan

### 1) Live Quotes Adapter Enhancement (30 min)

**Alpha Vantage Integration:**
```go
type AlphaVantageAdapter struct {
    client      *http.Client
    apiKey      string
    rateLimiter *rate.Limiter
    cache       *QuoteCache
    config      AlphaVantageConfig
    logger      *log.Logger
}

type AlphaVantageConfig struct {
    APIKey           string        `json:"api_key"`
    BaseURL          string        `json:"base_url"`
    RequestsPerMin   int           `json:"requests_per_min"`
    TimeoutSeconds   int           `json:"timeout_seconds"`
    CacheExpiryMs    int           `json:"cache_expiry_ms"`
    RetryAttempts    int           `json:"retry_attempts"`
    FallbackToCache  bool          `json:"fallback_to_cache"`
}

func (av *AlphaVantageAdapter) GetQuotes(symbols []string) (map[string]Quote, error) {
    quotes := make(map[string]Quote)
    
    for _, symbol := range symbols {
        // Check cache first
        if cached, found := av.cache.Get(symbol); found && !cached.IsStale() {
            quotes[symbol] = cached
            continue
        }
        
        // Rate limit check
        if err := av.rateLimiter.Wait(context.Background()); err != nil {
            return av.fallbackToCache(symbols, err)
        }
        
        // Make API request
        quote, err := av.fetchQuote(symbol)
        if err != nil {
            av.logger.Printf("Failed to fetch %s from AlphaVantage: %v", symbol, err)
            // Try cache fallback
            if cached, found := av.cache.Get(symbol); found {
                quote = cached
            } else {
                continue // Skip this symbol
            }
        }
        
        // Validate quote quality
        if av.isQuoteValid(quote) {
            av.cache.Set(symbol, quote)
            quotes[symbol] = quote
        }
    }
    
    return quotes, nil
}
```

**Quote Caching and Validation:**
```go
type QuoteCache struct {
    mu      sync.RWMutex
    quotes  map[string]CachedQuote
    expiry  time.Duration
}

type CachedQuote struct {
    Quote     Quote     `json:"quote"`
    Timestamp time.Time `json:"timestamp"`
}

func (qc *QuoteCache) Get(symbol string) (Quote, bool) {
    qc.mu.RLock()
    defer qc.mu.RUnlock()
    
    cached, exists := qc.quotes[symbol]
    if !exists {
        return Quote{}, false
    }
    
    if time.Since(cached.Timestamp) > qc.expiry {
        return Quote{}, false // Expired
    }
    
    return cached.Quote, true
}

func (av *AlphaVantageAdapter) isQuoteValid(quote Quote) bool {
    // Data quality checks
    if quote.Bid <= 0 || quote.Ask <= 0 || quote.Last <= 0 {
        return false
    }
    
    // Spread sanity check
    spread := (quote.Ask - quote.Bid) / quote.Last
    if spread > 0.05 { // 5% spread seems suspicious
        return false
    }
    
    // Timestamp recency check
    if time.Since(quote.Timestamp) > 5*time.Minute {
        return false // Too stale
    }
    
    return true
}
```

### 2) Live News Feed Integration (25 min)

**Reuters/Bloomberg Adapter:**
```go
type NewsAdapter struct {
    client      *http.Client
    config      NewsConfig
    deduplicator *NewsDeduplicator
    processor   *NewsProcessor
}

type NewsConfig struct {
    Provider        string   `json:"provider"` // "reuters" or "bloomberg"
    APIKey         string   `json:"api_key"`
    WebhookURL     string   `json:"webhook_url"`
    Categories     []string `json:"categories"`
    MinConfidence  float64  `json:"min_confidence"`
    MaxArticlesPerMin int   `json:"max_articles_per_min"`
}

func (na *NewsAdapter) StreamNews(ctx context.Context) (<-chan NewsItem, error) {
    newsChan := make(chan NewsItem, 100)
    
    go func() {
        defer close(newsChan)
        
        for {
            select {
            case <-ctx.Done():
                return
            default:
                articles, err := na.fetchLatestNews()
                if err != nil {
                    na.logError("Failed to fetch news", err)
                    time.Sleep(10 * time.Second)
                    continue
                }
                
                for _, article := range articles {
                    // Deduplication check
                    if na.deduplicator.IsDuplicate(article) {
                        continue
                    }
                    
                    // Process and extract signals
                    newsItem, err := na.processor.Process(article)
                    if err != nil {
                        continue
                    }
                    
                    // Quality filter
                    if newsItem.Confidence >= na.config.MinConfidence {
                        newsChan <- newsItem
                    }
                }
                
                time.Sleep(30 * time.Second) // Poll every 30 seconds
            }
        }
    }()
    
    return newsChan, nil
}
```

**News Deduplication:**
```go
type NewsDeduplicator struct {
    seen      map[string]time.Time
    mu        sync.RWMutex
    retention time.Duration
}

func (nd *NewsDeduplicator) IsDuplicate(article Article) bool {
    nd.mu.Lock()
    defer nd.mu.Unlock()
    
    // Clean old entries
    nd.cleanup()
    
    // Generate content hash
    hash := nd.generateHash(article.Title, article.Content)
    
    if _, exists := nd.seen[hash]; exists {
        return true
    }
    
    nd.seen[hash] = time.Now()
    return false
}

func (nd *NewsDeduplicator) generateHash(title, content string) string {
    // Simple content-based hash
    h := sha256.Sum256([]byte(title + content))
    return fmt.Sprintf("%x", h)[:16] // First 16 chars
}
```

### 3) Trading Halts Real-Time Integration (20 min)

**NYSE/NASDAQ Halts Feed:**
```go
type HaltsAdapter struct {
    wsConn      *websocket.Conn
    config      HaltsConfig
    haltStatus  map[string]HaltInfo
    mu          sync.RWMutex
    alertChan   chan HaltAlert
}

type HaltInfo struct {
    Symbol       string    `json:"symbol"`
    Halted       bool      `json:"halted"`
    Reason       string    `json:"reason"`
    HaltTime     time.Time `json:"halt_time"`
    ResumeTime   *time.Time `json:"resume_time,omitempty"`
    LastUpdated  time.Time `json:"last_updated"`
}

func (ha *HaltsAdapter) ConnectStream(ctx context.Context) error {
    conn, _, err := websocket.DefaultDialer.Dial(ha.config.WebSocketURL, nil)
    if err != nil {
        return fmt.Errorf("failed to connect to halts stream: %w", err)
    }
    
    ha.wsConn = conn
    
    go func() {
        defer conn.Close()
        
        for {
            select {
            case <-ctx.Done():
                return
            default:
                var haltMsg HaltMessage
                if err := conn.ReadJSON(&haltMsg); err != nil {
                    ha.logError("Failed to read halt message", err)
                    continue
                }
                
                ha.processHaltMessage(haltMsg)
            }
        }
    }()
    
    return nil
}

func (ha *HaltsAdapter) processHaltMessage(msg HaltMessage) {
    ha.mu.Lock()
    defer ha.mu.Unlock()
    
    haltInfo := HaltInfo{
        Symbol:      msg.Symbol,
        Halted:      msg.Action == "halt",
        Reason:      msg.Reason,
        HaltTime:    msg.Timestamp,
        LastUpdated: time.Now(),
    }
    
    // Update local state
    ha.haltStatus[msg.Symbol] = haltInfo
    
    // Send alert for immediate action
    if msg.Action == "halt" {
        ha.alertChan <- HaltAlert{
            Symbol:    msg.Symbol,
            Halted:    true,
            Reason:    msg.Reason,
            Timestamp: msg.Timestamp,
        }
    }
}
```

### 4) Error Handling and Circuit Breakers (15 min)

**Feed Health Monitoring:**
```go
type FeedHealthMonitor struct {
    feeds       map[string]*FeedHealth
    mu          sync.RWMutex
    alertMgr    *AlertManager
}

type FeedHealth struct {
    Name            string        `json:"name"`
    LastSuccessful  time.Time     `json:"last_successful"`
    LastError       time.Time     `json:"last_error"`
    ErrorCount      int           `json:"error_count"`
    SuccessCount    int           `json:"success_count"`
    LatencyP95      time.Duration `json:"latency_p95"`
    Status          string        `json:"status"` // "healthy", "degraded", "failed"
}

func (fhm *FeedHealthMonitor) RecordSuccess(feedName string, latency time.Duration) {
    fhm.mu.Lock()
    defer fhm.mu.Unlock()
    
    health, exists := fhm.feeds[feedName]
    if !exists {
        health = &FeedHealth{Name: feedName}
        fhm.feeds[feedName] = health
    }
    
    health.LastSuccessful = time.Now()
    health.SuccessCount++
    health.LatencyP95 = fhm.updateLatencyMetric(health.LatencyP95, latency)
    
    // Update status
    if health.ErrorCount > 0 && time.Since(health.LastError) > 5*time.Minute {
        health.Status = "healthy"
    }
}

func (fhm *FeedHealthMonitor) RecordError(feedName string, err error) {
    fhm.mu.Lock()
    defer fhm.mu.Unlock()
    
    health := fhm.feeds[feedName]
    health.LastError = time.Now()
    health.ErrorCount++
    
    // Update status based on error rate
    errorRate := float64(health.ErrorCount) / float64(health.SuccessCount + health.ErrorCount)
    if errorRate > 0.1 {
        health.Status = "degraded"
    }
    if errorRate > 0.5 {
        health.Status = "failed"
        fhm.alertMgr.SendAlert(fmt.Sprintf("Feed %s has failed (error rate %.1f%%)", feedName, errorRate*100))
    }
}
```

### 5) Configuration and Deployment (10 min)

**Live Adapter Configuration:**
```yaml
# config/live_adapters.yaml
adapters:
  quotes:
    provider: "alphavantage"
    config:
      api_key: "${ALPHAVANTAGE_API_KEY}"
      requests_per_min: 60
      cache_expiry_ms: 1000
      fallback_to_cache: true
      
  news:
    provider: "reuters"
    config:
      api_key: "${REUTERS_API_KEY}"
      webhook_url: "${NEWS_WEBHOOK_URL}"
      min_confidence: 0.7
      max_articles_per_min: 30
      
  halts:
    provider: "nasdaq"
    config:
      websocket_url: "wss://api.nasdaq.com/halts"
      reconnect_attempts: 5
      heartbeat_interval: 30
      
# Fallback behavior
fallback:
  quotes_cache_duration: "5m"
  news_buffer_size: 1000  
  halt_cache_duration: "1h"
  emergency_mode_threshold: 0.5 # 50% failure rate triggers emergency mode
```

**Environment Variable Management:**
```bash
# Required API keys
export ALPHAVANTAGE_API_KEY="your_key_here"
export REUTERS_API_KEY="your_key_here"
export POLYGON_API_KEY="your_key_here"

# Feature flags
export LIVE_QUOTES_ENABLED=true
export LIVE_NEWS_ENABLED=true
export LIVE_HALTS_ENABLED=true
export FALLBACK_TO_MOCK_ON_ERROR=true
```

## Integration Testing Plan

### Adapter Switch Testing:
```go
func TestAdapterSwitching(t *testing.T) {
    testCases := []struct {
        name           string
        adapterConfig  string
        expectedType   string
        shouldFallback bool
    }{
        {
            name: "alphavantage_quotes",
            adapterConfig: "alphavantage",
            expectedType: "*adapters.AlphaVantageAdapter",
        },
        {
            name: "fallback_on_failure",
            adapterConfig: "invalid_provider",
            expectedType: "*adapters.MockAdapter",
            shouldFallback: true,
        },
    }
}
```

## Success Metrics

- **Data Quality**: >99% valid quotes with <100ms median latency
- **Error Resilience**: <30s recovery time when primary feeds fail
- **Risk Preservation**: All existing risk controls (caps, cooldowns, circuit breakers) maintain 100% effectiveness
- **Performance**: Decision latency <200ms p95 with live data vs <50ms with mocks
- **Uptime**: >99.9% system availability despite external feed issues

## Risk Mitigation

- **Graceful Degradation**: System automatically falls back to cached/mock data when live feeds fail
- **Rate Limit Protection**: Built-in throttling prevents API quota exhaustion
- **Data Validation**: Multi-layer validation prevents bad data from affecting decisions
- **Circuit Breakers**: Automatic feed isolation when error rates exceed thresholds
- **Emergency Mode**: Manual override to disable live feeds and revert to mock data

## Evidence Required

- Live quotes showing real bid/ask spreads with proper validation
- News ingestion processing 100+ articles/hour with deduplication working
- Trading halts triggering immediate position freezes
- Fallback mechanisms working smoothly when APIs fail
- All existing risk controls (Session 13-14) operating correctly with live data

## Next Session Preview

**Session 16: Production Hardening** - Implement comprehensive monitoring, alerting, automated failover, performance optimization, security hardening, and deployment automation to prepare the system for live trading operations.

This session transitions the trading system from development/testing with mock data to production-ready operation with live market data feeds, while preserving all the risk management and safety controls built in previous sessions.