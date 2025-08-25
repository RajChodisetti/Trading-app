package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// VCRMode defines VCR recording/replay behavior
type VCRMode string

const (
	VCRModeRecord VCRMode = "record"   // Record interactions with live APIs
	VCRModeReplay VCRMode = "replay"   // Replay recorded interactions
	VCRModeOff    VCRMode = "off"      // Bypass VCR entirely
)

// VCRConfig configures VCR behavior
type VCRConfig struct {
	Mode        VCRMode `json:"mode"`
	FixturePath string  `json:"fixture_path"`
	Enabled     bool    `json:"enabled"`
}

// VCRInteraction represents a recorded API interaction
type VCRInteraction struct {
	Request  VCRRequest  `json:"request"`
	Response VCRResponse `json:"response"`
	Latency  int64       `json:"latency_ms"`
}

// VCRRequest captures request details
type VCRRequest struct {
	Method   string            `json:"method"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Symbol   string            `json:"symbol,omitempty"`   // For easy identification
}

// VCRResponse captures response details  
type VCRResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body"`
	Error      string            `json:"error,omitempty"`
}

// VCRCassette contains recorded interactions
type VCRCassette struct {
	Name         string           `json:"name"`
	Interactions []VCRInteraction `json:"interactions"`
	RecordedAt   time.Time        `json:"recorded_at"`
}

// VCRAdapter wraps any adapter to provide record/replay functionality
type VCRAdapter struct {
	underlying QuotesAdapter
	config     VCRConfig
	cassette   *VCRCassette
	mu         sync.RWMutex
	logger     *log.Logger
}

// NewVCRAdapter creates a VCR-enabled adapter
func NewVCRAdapter(underlying QuotesAdapter, config VCRConfig, cassetteName string, logger *log.Logger) (*VCRAdapter, error) {
	adapter := &VCRAdapter{
		underlying: underlying,
		config:     config,
		logger:     logger,
	}
	
	if !config.Enabled {
		return adapter, nil
	}
	
	cassettePath := filepath.Join(config.FixturePath, cassetteName+".json")
	
	switch config.Mode {
	case VCRModeRecord:
		adapter.cassette = &VCRCassette{
			Name:         cassetteName,
			Interactions: make([]VCRInteraction, 0),
			RecordedAt:   time.Now(),
		}
		
	case VCRModeReplay:
		cassette, err := adapter.loadCassette(cassettePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load VCR cassette: %w", err)
		}
		adapter.cassette = cassette
		
	case VCRModeOff:
		// VCR disabled, pass through to underlying adapter
	}
	
	return adapter, nil
}

// GetQuote implements QuotesAdapter with VCR functionality
func (v *VCRAdapter) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	if !v.config.Enabled || v.config.Mode == VCRModeOff {
		return v.underlying.GetQuote(ctx, symbol)
	}
	
	if v.config.Mode == VCRModeReplay {
		return v.replayQuote(symbol)
	}
	
	// Record mode
	start := time.Now()
	quote, err := v.underlying.GetQuote(ctx, symbol)
	latency := time.Since(start)
	
	v.recordInteraction(symbol, quote, err, latency)
	
	return quote, err
}

// GetQuotes implements QuotesAdapter with VCR functionality
func (v *VCRAdapter) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	if !v.config.Enabled || v.config.Mode == VCRModeOff {
		return v.underlying.GetQuotes(ctx, symbols)
	}
	
	if v.config.Mode == VCRModeReplay {
		return v.replayQuotes(symbols)
	}
	
	// Record mode
	start := time.Now()
	quotes, err := v.underlying.GetQuotes(ctx, symbols)
	latency := time.Since(start)
	
	v.recordBatchInteraction(symbols, quotes, err, latency)
	
	return quotes, err
}

// replayQuote replays a recorded quote
func (v *VCRAdapter) replayQuote(symbol string) (*Quote, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	
	for _, interaction := range v.cassette.Interactions {
		if interaction.Request.Symbol == symbol {
			if interaction.Response.Error != "" {
				return nil, fmt.Errorf("replayed error: %s", interaction.Response.Error)
			}
			
			var quote Quote
			if err := json.Unmarshal([]byte(interaction.Response.Body), &quote); err != nil {
				return nil, fmt.Errorf("failed to unmarshal replayed quote: %w", err)
			}
			
			// Simulate original latency
			time.Sleep(time.Duration(interaction.Latency) * time.Millisecond)
			
			return &quote, nil
		}
	}
	
	return nil, fmt.Errorf("no recorded interaction for symbol %s", symbol)
}

// replayQuotes replays recorded quotes for multiple symbols
func (v *VCRAdapter) replayQuotes(symbols []string) (map[string]*Quote, error) {
	quotes := make(map[string]*Quote)
	
	for _, symbol := range symbols {
		quote, err := v.replayQuote(symbol)
		if err != nil {
			continue // Skip missing symbols
		}
		quotes[symbol] = quote
	}
	
	if len(quotes) == 0 {
		return nil, fmt.Errorf("no recorded quotes found for symbols: %v", symbols)
	}
	
	return quotes, nil
}

// recordInteraction records an API interaction
func (v *VCRAdapter) recordInteraction(symbol string, quote *Quote, err error, latency time.Duration) {
	v.mu.Lock()
	defer v.mu.Unlock()
	
	interaction := VCRInteraction{
		Request: VCRRequest{
			Method: "GET",
			URL:    fmt.Sprintf("/quote/%s", symbol),
			Symbol: symbol,
		},
		Latency: latency.Milliseconds(),
	}
	
	if err != nil {
		interaction.Response.Error = err.Error()
	} else {
		body, _ := json.Marshal(quote)
		interaction.Response.Body = string(body)
		interaction.Response.StatusCode = 200
	}
	
	v.cassette.Interactions = append(v.cassette.Interactions, interaction)
}

// recordBatchInteraction records a batch API interaction
func (v *VCRAdapter) recordBatchInteraction(symbols []string, quotes map[string]*Quote, err error, latency time.Duration) {
	v.mu.Lock()
	defer v.mu.Unlock()
	
	interaction := VCRInteraction{
		Request: VCRRequest{
			Method: "GET",
			URL:    "/quotes/batch",
			Symbol: fmt.Sprintf("batch[%d]", len(symbols)),
		},
		Latency: latency.Milliseconds(),
	}
	
	if err != nil {
		interaction.Response.Error = err.Error()
	} else {
		body, _ := json.Marshal(quotes)
		interaction.Response.Body = string(body)
		interaction.Response.StatusCode = 200
	}
	
	v.cassette.Interactions = append(v.cassette.Interactions, interaction)
}

// SaveCassette saves recorded interactions to disk
func (v *VCRAdapter) SaveCassette() error {
	if !v.config.Enabled || v.config.Mode != VCRModeRecord || v.cassette == nil {
		return nil
	}
	
	v.mu.RLock()
	defer v.mu.RUnlock()
	
	cassettePath := filepath.Join(v.config.FixturePath, v.cassette.Name+".json")
	
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(cassettePath), 0755); err != nil {
		return fmt.Errorf("failed to create VCR directory: %w", err)
	}
	
	data, err := json.MarshalIndent(v.cassette, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cassette: %w", err)
	}
	
	if err := os.WriteFile(cassettePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write cassette: %w", err)
	}
	
	v.logger.Printf("VCR: Saved %d interactions to %s", len(v.cassette.Interactions), cassettePath)
	
	return nil
}

// loadCassette loads recorded interactions from disk
func (v *VCRAdapter) loadCassette(path string) (*VCRCassette, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read cassette file: %w", err)
	}
	
	var cassette VCRCassette
	if err := json.Unmarshal(data, &cassette); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cassette: %w", err)
	}
	
	v.logger.Printf("VCR: Loaded %d interactions from %s", len(cassette.Interactions), path)
	
	return &cassette, nil
}

// HealthCheck implements QuotesAdapter
func (v *VCRAdapter) HealthCheck(ctx context.Context) error {
	if !v.config.Enabled || v.config.Mode == VCRModeReplay {
		return nil // VCR replay mode is always healthy
	}
	
	return v.underlying.HealthCheck(ctx)
}

// Close implements QuotesAdapter
func (v *VCRAdapter) Close() error {
	if err := v.SaveCassette(); err != nil {
		v.logger.Printf("Failed to save VCR cassette: %v", err)
	}
	
	if v.underlying != nil {
		return v.underlying.Close()
	}
	
	return nil
}

// ChaosConfig configures chaos testing behavior
type ChaosConfig struct {
	Enabled           bool    `json:"enabled"`
	ErrorRate         float64 `json:"error_rate"`         // 0.1 = 10% error injection
	TimeoutRate       float64 `json:"timeout_rate"`       // 0.05 = 5% timeout injection
	LatencyMultiplier float64 `json:"latency_multiplier"` // 2.0 = 2x normal latency
	NetworkErrorRate  float64 `json:"network_error_rate"` // Network-specific errors
	ParseErrorRate    float64 `json:"parse_error_rate"`   // JSON parsing errors
}

// ChaosAdapter wraps any adapter to inject failures for testing
type ChaosAdapter struct {
	underlying QuotesAdapter
	config     ChaosConfig
	rand       *rand.Rand
	logger     *log.Logger
}

// NewChaosAdapter creates a chaos-enabled adapter
func NewChaosAdapter(underlying QuotesAdapter, config ChaosConfig, logger *log.Logger) *ChaosAdapter {
	return &ChaosAdapter{
		underlying: underlying,
		config:     config,
		rand:       rand.New(rand.NewSource(time.Now().UnixNano())),
		logger:     logger,
	}
}

// GetQuote implements QuotesAdapter with chaos injection
func (c *ChaosAdapter) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	if !c.config.Enabled {
		return c.underlying.GetQuote(ctx, symbol)
	}
	
	// Inject chaos
	if chaos := c.injectChaos(symbol); chaos != nil {
		return nil, chaos
	}
	
	// Add latency if configured
	if c.config.LatencyMultiplier > 1.0 {
		delay := time.Duration(float64(100*time.Millisecond) * c.config.LatencyMultiplier)
		time.Sleep(delay)
	}
	
	return c.underlying.GetQuote(ctx, symbol)
}

// GetQuotes implements QuotesAdapter with chaos injection
func (c *ChaosAdapter) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	if !c.config.Enabled {
		return c.underlying.GetQuotes(ctx, symbols)
	}
	
	// Inject chaos for batch requests
	if chaos := c.injectChaos("batch"); chaos != nil {
		return nil, chaos
	}
	
	// Add latency if configured
	if c.config.LatencyMultiplier > 1.0 {
		delay := time.Duration(float64(200*time.Millisecond) * c.config.LatencyMultiplier)
		time.Sleep(delay)
	}
	
	return c.underlying.GetQuotes(ctx, symbols)
}

// injectChaos randomly injects various types of failures
func (c *ChaosAdapter) injectChaos(context string) error {
	// Timeout errors
	if c.rand.Float64() < c.config.TimeoutRate {
		c.logger.Printf("CHAOS: Injecting timeout error for %s", context)
		return fmt.Errorf("chaos timeout: simulated request timeout")
	}
	
	// Network errors
	if c.rand.Float64() < c.config.NetworkErrorRate {
		c.logger.Printf("CHAOS: Injecting network error for %s", context)
		return fmt.Errorf("chaos network: connection refused")
	}
	
	// Parse errors
	if c.rand.Float64() < c.config.ParseErrorRate {
		c.logger.Printf("CHAOS: Injecting parse error for %s", context)
		return fmt.Errorf("chaos parse: invalid JSON response")
	}
	
	// General errors
	if c.rand.Float64() < c.config.ErrorRate {
		c.logger.Printf("CHAOS: Injecting general error for %s", context)
		return fmt.Errorf("chaos error: simulated provider failure")
	}
	
	return nil // No chaos injected
}

// HealthCheck implements QuotesAdapter
func (c *ChaosAdapter) HealthCheck(ctx context.Context) error {
	if !c.config.Enabled {
		return c.underlying.HealthCheck(ctx)
	}
	
	// Inject chaos into health checks too
	if chaos := c.injectChaos("healthcheck"); chaos != nil {
		return chaos
	}
	
	return c.underlying.HealthCheck(ctx)
}

// Close implements QuotesAdapter
func (c *ChaosAdapter) Close() error {
	if c.underlying != nil {
		return c.underlying.Close()
	}
	return nil
}

// TestingAdapterFactory creates adapters configured for testing
type TestingAdapterFactory struct {
	vcrConfig   VCRConfig
	chaosConfig ChaosConfig
	logger      *log.Logger
}

// NewTestingAdapterFactory creates a factory for test adapters
func NewTestingAdapterFactory(vcrConfig VCRConfig, chaosConfig ChaosConfig, logger *log.Logger) *TestingAdapterFactory {
	return &TestingAdapterFactory{
		vcrConfig:   vcrConfig,
		chaosConfig: chaosConfig,
		logger:      logger,
	}
}

// CreateQuotesAdapter creates a testing-enabled quotes adapter
func (tf *TestingAdapterFactory) CreateQuotesAdapter(baseAdapter QuotesAdapter, testName string) (QuotesAdapter, error) {
	adapter := baseAdapter
	
	// Wrap with VCR if enabled
	if tf.vcrConfig.Enabled {
		vcrAdapter, err := NewVCRAdapter(adapter, tf.vcrConfig, testName, tf.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create VCR adapter: %w", err)
		}
		adapter = vcrAdapter
	}
	
	// Wrap with chaos if enabled
	if tf.chaosConfig.Enabled {
		adapter = NewChaosAdapter(adapter, tf.chaosConfig, tf.logger)
	}
	
	return adapter, nil
}

// PropertyTestValidateQuote runs property-based tests on quote validation
func PropertyTestValidateQuote(t interface{}, validate func(Quote) bool, iterations int) {
	type TestRunner interface {
		Errorf(format string, args ...interface{})
	}
	
	runner, ok := t.(TestRunner)
	if !ok {
		panic("PropertyTestValidateQuote requires a testing.T or similar interface")
	}
	
	for i := 0; i < iterations; i++ {
		quote := generateRandomQuote()
		
		// Test the validation function
		isValid := validate(quote)
		
		// Check invariants
		if quote.Bid <= 0 || quote.Ask <= 0 || quote.Last <= 0 {
			if isValid {
				runner.Errorf("Invalid quote passed validation: bid=%.2f ask=%.2f last=%.2f", 
					quote.Bid, quote.Ask, quote.Last)
			}
		}
		
		if quote.Bid >= quote.Ask && quote.Bid > 0 && quote.Ask > 0 {
			if isValid {
				runner.Errorf("Quote with bid >= ask passed validation: bid=%.2f ask=%.2f", 
					quote.Bid, quote.Ask)
			}
		}
		
		spread := (quote.Ask - quote.Bid) / quote.Last
		if spread > 0.05 && quote.Last > 5.0 { // 5% spread on stock > $5
			// Should be rejected unless flagged as micro-cap
			if isValid && quote.Volume > 100000 { // High volume = not micro-cap
				runner.Errorf("High spread quote passed validation: spread=%.2f%% price=%.2f volume=%d", 
					spread*100, quote.Last, quote.Volume)
			}
		}
		
		staleness := time.Since(quote.Timestamp)
		if staleness > 5*time.Minute {
			if isValid {
				runner.Errorf("Stale quote passed validation: age=%v", staleness)
			}
		}
	}
}

// generateRandomQuote creates a random quote for property testing
func generateRandomQuote() Quote {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	
	// Generate base price
	basePrice := 10.0 + r.Float64()*200.0 // $10-$210
	
	// Generate bid/ask around base price
	spread := 0.01 + r.Float64()*0.04 // 1-5% spread
	halfSpread := spread * basePrice / 2
	
	quote := Quote{
		Symbol:    fmt.Sprintf("TEST%d", r.Intn(1000)),
		Bid:       basePrice - halfSpread,
		Ask:       basePrice + halfSpread,
		Last:      basePrice + (r.Float64()-0.5)*halfSpread, // Within bid/ask
		Volume:    int64(1000 + r.Intn(1000000)),
		Timestamp: time.Now().Add(-time.Duration(r.Intn(300))*time.Second), // 0-5 min old
		Session:   "RTH",
		Halted:    false,
		Source:    "test",
	}
	
	// Occasionally generate invalid data for edge case testing
	if r.Float64() < 0.1 {
		switch r.Intn(4) {
		case 0:
			quote.Bid = 0 // Invalid bid
		case 1:
			quote.Ask = quote.Bid - 1 // Invalid ask < bid
		case 2:
			quote.Last = 0 // Invalid last
		case 3:
			quote.Timestamp = time.Now().Add(-10 * time.Minute) // Stale
		}
	}
	
	return quote
}