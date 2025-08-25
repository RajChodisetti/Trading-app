package adapters

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// NewsAdapter interface for news data providers
type NewsAdapter interface {
	StreamNews(ctx context.Context) (<-chan NewsItem, error)
	GetLatestNews(ctx context.Context, symbols []string) ([]*NewsItem, error)
	HealthCheck(ctx context.Context) error
	Close() error
}

// NewsItem represents a news article with compliance controls
type NewsItem struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Summary     string            `json:"summary,omitempty"`     // Stored only if ToS allows
	Content     string            `json:"-"`                     // Never stored - compliance
	URL         string            `json:"url,omitempty"`
	Source      string            `json:"source"`
	Author      string            `json:"author,omitempty"`
	Symbols     []string          `json:"symbols"`
	Confidence  float64           `json:"confidence"`
	Sentiment   string            `json:"sentiment"`             // "positive", "negative", "neutral"
	Category    string            `json:"category"`              // "earnings", "merger", "guidance", etc.
	Timestamp   time.Time         `json:"timestamp"`
	Provider    string            `json:"provider"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	
	// Compliance fields
	ContentHash string    `json:"content_hash"`          // For deduplication without storing content
	ProcessedAt time.Time `json:"processed_at"`
	Retained    bool      `json:"retained"`              // Whether we can store this
}

// ShadowNewsAdapter runs news feeds in shadow mode for validation
type ShadowNewsAdapter struct {
	mu              sync.RWMutex
	liveProvider    NewsAdapter
	deduplicator    *NewsDeduplicator
	processor       *NewsProcessor
	running         bool
	logger          *log.Logger
	config          NewsConfig
	
	// Shadow mode state
	shadowSignals   []NewsSignal
	maxSignalAge    time.Duration
	
	// Metrics
	metrics         ShadowNewsMetrics
}

// NewsConfig configures news processing and compliance
type NewsConfig struct {
	Provider          string   `json:"provider"`           // "benzinga", "finnhub", "polygon"
	MinConfidence     float64  `json:"min_confidence"`     // 0.7
	MaxArticlesPerMin int      `json:"max_articles_per_min"` // 30
	Categories        []string `json:"categories"`         // ["earnings", "guidance", "merger"]
	
	// Compliance settings
	StoreContent      bool     `json:"store_content"`      // false for compliance
	StoreSummary      bool     `json:"store_summary"`      // true if ToS allows
	RetentionDays     int      `json:"retention_days"`     // 30
	AllowedProviders  []string `json:"allowed_providers"`  // Whitelisted providers
	
	// Processing settings
	DedupeWindowHours int      `json:"dedupe_window_hours"` // 24
	SignalExpiryHours int      `json:"signal_expiry_hours"` // 4
}

// NewsSignal represents extracted trading signals from news
type NewsSignal struct {
	Symbol      string            `json:"symbol"`
	Sentiment   string            `json:"sentiment"`
	Confidence  float64           `json:"confidence"`
	Category    string            `json:"category"`
	Timestamp   time.Time         `json:"timestamp"`
	SourceID    string            `json:"source_id"`
	Metadata    map[string]string `json:"metadata"`
	ExpiresAt   time.Time         `json:"expires_at"`
}

// ShadowNewsMetrics tracks shadow mode performance
type ShadowNewsMetrics struct {
	ArticlesProcessed   int64                `json:"articles_processed"`
	SignalsExtracted    int64                `json:"signals_extracted"`
	DuplicatesFiltered  int64                `json:"duplicates_filtered"`
	ComplianceBlocked   int64                `json:"compliance_blocked"`
	QualityRejected     int64                `json:"quality_rejected"`
	LastProcessed       time.Time            `json:"last_processed"`
	ProviderBreakdown   map[string]int64     `json:"provider_breakdown"`
	SentimentBreakdown  map[string]int64     `json:"sentiment_breakdown"`
}

// NewsDeduplicator handles content deduplication with compliance
type NewsDeduplicator struct {
	mu        sync.RWMutex
	seen      map[string]time.Time // content hash -> first seen time
	retention time.Duration
	logger    *log.Logger
}

// NewsProcessor extracts trading signals from news
type NewsProcessor struct {
	config NewsConfig
	logger *log.Logger
}

// NewShadowNewsAdapter creates a new shadow news adapter
func NewShadowNewsAdapter(liveProvider NewsAdapter, config NewsConfig, logger *log.Logger) *ShadowNewsAdapter {
	return &ShadowNewsAdapter{
		liveProvider:  liveProvider,
		deduplicator:  NewNewsDeduplicator(time.Duration(config.DedupeWindowHours)*time.Hour, logger),
		processor:     NewNewsProcessor(config, logger),
		config:        config,
		logger:        logger,
		maxSignalAge:  time.Duration(config.SignalExpiryHours) * time.Hour,
		metrics: ShadowNewsMetrics{
			ProviderBreakdown:  make(map[string]int64),
			SentimentBreakdown: make(map[string]int64),
		},
	}
}

// NewNewsDeduplicator creates a new news deduplicator
func NewNewsDeduplicator(retention time.Duration, logger *log.Logger) *NewsDeduplicator {
	return &NewsDeduplicator{
		seen:      make(map[string]time.Time),
		retention: retention,
		logger:    logger,
	}
}

// NewNewsProcessor creates a new news processor
func NewNewsProcessor(config NewsConfig, logger *log.Logger) *NewsProcessor {
	return &NewsProcessor{
		config: config,
		logger: logger,
	}
}

// StartShadowMode begins shadow mode news processing
func (sna *ShadowNewsAdapter) StartShadowMode(ctx context.Context) error {
	sna.mu.Lock()
	if sna.running {
		sna.mu.Unlock()
		return fmt.Errorf("shadow mode already running")
	}
	sna.running = true
	sna.mu.Unlock()
	
	sna.logger.Printf("Starting news shadow mode with provider: %s", sna.config.Provider)
	
	// Start news streaming
	go sna.streamLoop(ctx)
	
	// Start cleanup loop
	go sna.cleanupLoop(ctx)
	
	observ.IncCounter("news_shadow_mode_started_total", map[string]string{
		"provider": sna.config.Provider,
	})
	
	return nil
}

// StopShadowMode stops shadow mode operation
func (sna *ShadowNewsAdapter) StopShadowMode() error {
	sna.mu.Lock()
	defer sna.mu.Unlock()
	
	if !sna.running {
		return fmt.Errorf("shadow mode not running")
	}
	
	sna.running = false
	sna.logger.Printf("Stopped news shadow mode")
	
	observ.IncCounter("news_shadow_mode_stopped_total", map[string]string{})
	
	return nil
}

// streamLoop processes the news stream
func (sna *ShadowNewsAdapter) streamLoop(ctx context.Context) {
	newsChan, err := sna.liveProvider.StreamNews(ctx)
	if err != nil {
		sna.logger.Printf("Failed to start news stream: %v", err)
		return
	}
	
	for {
		select {
		case <-ctx.Done():
			return
		case newsItem, ok := <-newsChan:
			if !ok {
				sna.logger.Printf("News stream closed")
				return
			}
			
			if !sna.isRunning() {
				return
			}
			
			sna.processNewsItem(newsItem)
		}
	}
}

// processNewsItem processes a single news item in shadow mode
func (sna *ShadowNewsAdapter) processNewsItem(item NewsItem) {
	start := time.Now()
	
	// Compliance check - never store full content
	if sna.config.StoreContent {
		sna.logger.Printf("WARNING: Content storage enabled - compliance risk")
	}
	
	// Generate content hash for deduplication (without storing content)
	item.ContentHash = sna.generateContentHash(item.Title, item.Content)
	item.ProcessedAt = time.Now()
	
	// Check for duplicates
	if sna.deduplicator.IsDuplicate(item.ContentHash) {
		sna.mu.Lock()
		sna.metrics.DuplicatesFiltered++
		sna.mu.Unlock()
		
		observ.IncCounter("news_shadow_duplicate_filtered_total", map[string]string{
			"provider": item.Provider,
		})
		return
	}
	
	// Compliance filtering
	if !sna.isProviderAllowed(item.Provider) {
		sna.mu.Lock()
		sna.metrics.ComplianceBlocked++
		sna.mu.Unlock()
		
		observ.IncCounter("news_shadow_compliance_blocked_total", map[string]string{
			"provider": item.Provider,
			"reason":   "provider_not_allowed",
		})
		return
	}
	
	// Quality filtering
	if item.Confidence < sna.config.MinConfidence {
		sna.mu.Lock()
		sna.metrics.QualityRejected++
		sna.mu.Unlock()
		
		observ.IncCounter("news_shadow_quality_rejected_total", map[string]string{
			"reason": "low_confidence",
		})
		return
	}
	
	// Extract trading signals
	signals, err := sna.processor.ExtractSignals(item)
	if err != nil {
		sna.logger.Printf("Failed to extract signals from news: %v", err)
		return
	}
	
	// Store signals in shadow mode (log-only)
	sna.mu.Lock()
	sna.metrics.ArticlesProcessed++
	sna.metrics.SignalsExtracted += int64(len(signals))
	sna.metrics.LastProcessed = time.Now()
	sna.metrics.ProviderBreakdown[item.Provider]++
	sna.metrics.SentimentBreakdown[item.Sentiment]++
	
	// Add signals to shadow state (with expiry)
	for _, signal := range signals {
		signal.ExpiresAt = time.Now().Add(sna.maxSignalAge)
		sna.shadowSignals = append(sna.shadowSignals, signal)
	}
	sna.mu.Unlock()
	
	// Record processing metrics
	latency := time.Since(start)
	observ.RecordDuration("news_shadow_processing_latency", latency, map[string]string{
		"provider": item.Provider,
	})
	
	observ.IncCounter("news_shadow_processed_total", map[string]string{
		"provider":  item.Provider,
		"sentiment": item.Sentiment,
		"category":  item.Category,
	})
	
	// Log shadow signals (would influence decisions in live mode)
	for _, signal := range signals {
		sna.logger.Printf("SHADOW SIGNAL: %s %s confidence=%.2f category=%s", 
			signal.Symbol, signal.Sentiment, signal.Confidence, signal.Category)
	}
}

// cleanupLoop periodically cleans expired signals and deduplication data
func (sna *ShadowNewsAdapter) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !sna.isRunning() {
				return
			}
			sna.cleanup()
		}
	}
}

// cleanup removes expired signals and deduplication data
func (sna *ShadowNewsAdapter) cleanup() {
	sna.mu.Lock()
	defer sna.mu.Unlock()
	
	now := time.Now()
	validSignals := make([]NewsSignal, 0)
	
	for _, signal := range sna.shadowSignals {
		if signal.ExpiresAt.After(now) {
			validSignals = append(validSignals, signal)
		}
	}
	
	expiredCount := len(sna.shadowSignals) - len(validSignals)
	sna.shadowSignals = validSignals
	
	if expiredCount > 0 {
		observ.IncCounterBy("news_shadow_signals_expired_total", map[string]string{}, float64(expiredCount))
		sna.logger.Printf("Cleaned up %d expired shadow signals", expiredCount)
	}
	
	// Cleanup deduplication data
	sna.deduplicator.Cleanup()
}

// generateContentHash creates a hash for deduplication without storing content
func (sna *ShadowNewsAdapter) generateContentHash(title, content string) string {
	h := sha256.Sum256([]byte(title + content))
	return fmt.Sprintf("%x", h)[:16] // First 16 chars
}

// isProviderAllowed checks if provider is on whitelist
func (sna *ShadowNewsAdapter) isProviderAllowed(provider string) bool {
	if len(sna.config.AllowedProviders) == 0 {
		return true // No restrictions
	}
	
	for _, allowed := range sna.config.AllowedProviders {
		if provider == allowed {
			return true
		}
	}
	
	return false
}

// GetShadowSignals returns current shadow signals (for analysis)
func (sna *ShadowNewsAdapter) GetShadowSignals() []NewsSignal {
	sna.mu.RLock()
	defer sna.mu.RUnlock()
	
	// Copy to avoid race conditions
	signals := make([]NewsSignal, len(sna.shadowSignals))
	copy(signals, sna.shadowSignals)
	
	return signals
}

// GetShadowMetrics returns shadow mode metrics
func (sna *ShadowNewsAdapter) GetShadowMetrics() map[string]interface{} {
	sna.mu.RLock()
	defer sna.mu.RUnlock()
	
	return map[string]interface{}{
		"articles_processed":   sna.metrics.ArticlesProcessed,
		"signals_extracted":    sna.metrics.SignalsExtracted,
		"duplicates_filtered":  sna.metrics.DuplicatesFiltered,
		"compliance_blocked":   sna.metrics.ComplianceBlocked,
		"quality_rejected":     sna.metrics.QualityRejected,
		"last_processed":       sna.metrics.LastProcessed,
		"provider_breakdown":   sna.metrics.ProviderBreakdown,
		"sentiment_breakdown":  sna.metrics.SentimentBreakdown,
		"active_signals":       len(sna.shadowSignals),
		"running":              sna.running,
	}
}

// isRunning safely checks if shadow mode is running
func (sna *ShadowNewsAdapter) isRunning() bool {
	sna.mu.RLock()
	defer sna.mu.RUnlock()
	return sna.running
}

// IsDuplicate checks if content has been seen before
func (nd *NewsDeduplicator) IsDuplicate(contentHash string) bool {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	
	// Clean old entries first
	nd.cleanupUnsafe()
	
	if _, exists := nd.seen[contentHash]; exists {
		return true
	}
	
	nd.seen[contentHash] = time.Now()
	return false
}

// Cleanup removes old deduplication entries
func (nd *NewsDeduplicator) Cleanup() {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	nd.cleanupUnsafe()
}

// cleanupUnsafe removes old entries (must be called with lock held)
func (nd *NewsDeduplicator) cleanupUnsafe() {
	cutoff := time.Now().Add(-nd.retention)
	
	for hash, timestamp := range nd.seen {
		if timestamp.Before(cutoff) {
			delete(nd.seen, hash)
		}
	}
}

// ExtractSignals extracts trading signals from news items
func (np *NewsProcessor) ExtractSignals(item NewsItem) ([]NewsSignal, error) {
	signals := make([]NewsSignal, 0)
	
	// Extract signal for each symbol mentioned
	for _, symbol := range item.Symbols {
		signal := NewsSignal{
			Symbol:     symbol,
			Sentiment:  item.Sentiment,
			Confidence: item.Confidence,
			Category:   item.Category,
			Timestamp:  item.Timestamp,
			SourceID:   item.ID,
			Metadata: map[string]string{
				"provider": item.Provider,
				"author":   item.Author,
			},
		}
		
		signals = append(signals, signal)
	}
	
	return signals, nil
}

// MockNewsProvider provides a mock news provider for testing
type MockNewsProvider struct {
	mu       sync.RWMutex
	articles []*NewsItem
	closed   bool
}

// NewMockNewsProvider creates a mock news provider
func NewMockNewsProvider() *MockNewsProvider {
	return &MockNewsProvider{
		articles: make([]*NewsItem, 0),
	}
}

// StreamNews returns a channel of news items
func (mnp *MockNewsProvider) StreamNews(ctx context.Context) (<-chan NewsItem, error) {
	newsChan := make(chan NewsItem, 10)
	
	go func() {
		defer close(newsChan)
		
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mnp.mu.RLock()
				if mnp.closed {
					mnp.mu.RUnlock()
					return
				}
				
				// Send a mock news item
				newsItem := NewsItem{
					ID:         fmt.Sprintf("mock_%d", time.Now().Unix()),
					Title:      "Mock News Item",
					Source:     "mock_provider",
					Symbols:    []string{"AAPL", "NVDA"},
					Confidence: 0.8,
					Sentiment:  "positive",
					Category:   "earnings",
					Timestamp:  time.Now(),
					Provider:   "mock",
				}
				mnp.mu.RUnlock()
				
				select {
				case newsChan <- newsItem:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	
	return newsChan, nil
}

// GetLatestNews returns recent news items
func (mnp *MockNewsProvider) GetLatestNews(ctx context.Context, symbols []string) ([]*NewsItem, error) {
	mnp.mu.RLock()
	defer mnp.mu.RUnlock()
	
	return mnp.articles, nil
}

// AddNews adds a news item for testing
func (mnp *MockNewsProvider) AddNews(item *NewsItem) {
	mnp.mu.Lock()
	defer mnp.mu.Unlock()
	
	mnp.articles = append(mnp.articles, item)
}

// HealthCheck validates the mock provider
func (mnp *MockNewsProvider) HealthCheck(ctx context.Context) error {
	mnp.mu.RLock()
	defer mnp.mu.RUnlock()
	
	if mnp.closed {
		return fmt.Errorf("provider is closed")
	}
	
	return nil
}

// Close stops the mock provider
func (mnp *MockNewsProvider) Close() error {
	mnp.mu.Lock()
	defer mnp.mu.Unlock()
	
	mnp.closed = true
	return nil
}