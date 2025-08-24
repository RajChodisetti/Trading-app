package adapters

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// QuotesAdapterFactory creates quotes adapters based on configuration
type QuotesAdapterFactory struct {
	config QuotesConfig
}

// QuotesConfig holds configuration for all quote providers
type QuotesConfig struct {
	Adapter   string                     `yaml:"adapter"`   // "mock" | "sim" | "alphavantage"  
	Providers QuotesProviderConfigs      `yaml:"providers"`
}

// QuotesProviderConfigs holds provider-specific configurations
type QuotesProviderConfigs struct {
	AlphaVantage AlphaVantageProviderConfig `yaml:"alphavantage"`
	Polygon      PolygonProviderConfig      `yaml:"polygon"`
}

// AlphaVantageProviderConfig holds Alpha Vantage specific config
type AlphaVantageProviderConfig struct {
	APIKeyEnv           string `yaml:"api_key_env"`
	RateLimitPerMinute  int    `yaml:"rate_limit_per_minute"`
	DailyCap            int    `yaml:"daily_cap"`
	CacheTTLSeconds     int    `yaml:"cache_ttl_seconds"`
	StaleCeilingSeconds int    `yaml:"stale_ceiling_seconds"`
	TimeoutSeconds      int    `yaml:"timeout_seconds"`
	MaxRetries          int    `yaml:"max_retries"`
	BackoffBaseMs       int    `yaml:"backoff_base_ms"`
}

// PolygonProviderConfig holds Polygon.io specific config (for future use)
type PolygonProviderConfig struct {
	APIKeyEnv          string `yaml:"api_key_env"`
	RateLimitPerMinute int    `yaml:"rate_limit_per_minute"`
	TimeoutSeconds     int    `yaml:"timeout_seconds"`
}

// NewQuotesAdapterFactory creates a new factory
func NewQuotesAdapterFactory(config QuotesConfig) *QuotesAdapterFactory {
	return &QuotesAdapterFactory{config: config}
}

// CreateAdapter creates the appropriate quotes adapter based on configuration
func (f *QuotesAdapterFactory) CreateAdapter() (QuotesAdapter, error) {
	adapter := strings.ToLower(strings.TrimSpace(f.config.Adapter))
	
	// Check for environment variable override
	if envAdapter := os.Getenv("QUOTES"); envAdapter != "" {
		adapter = strings.ToLower(strings.TrimSpace(envAdapter))
		observ.Log("quotes_adapter_override", map[string]any{
			"config_adapter": f.config.Adapter,
			"env_override":   adapter,
		})
	}
	
	switch adapter {
	case "mock":
		observ.Log("quotes_adapter_created", map[string]any{
			"type":   "mock",
			"reason": "deterministic testing",
		})
		return NewMockQuotesAdapter(), nil
		
	case "sim":
		observ.Log("quotes_adapter_created", map[string]any{
			"type":   "sim", 
			"reason": "simulation mode",
		})
		return NewSimQuotesAdapter(), nil
		
	case "alphavantage":
		return f.createAlphaVantageAdapter()
		
	case "polygon":
		return nil, fmt.Errorf("Polygon adapter not yet implemented")
		
	default:
		// Unknown adapter - fall back to mock with warning
		observ.Log("quotes_adapter_fallback", map[string]any{
			"requested_adapter": adapter,
			"fallback_to":       "mock",
			"reason":            "unknown adapter type",
		})
		return NewMockQuotesAdapter(), nil
	}
}

// createAlphaVantageAdapter creates an Alpha Vantage adapter with safety checks
func (f *QuotesAdapterFactory) createAlphaVantageAdapter() (QuotesAdapter, error) {
	config := f.config.Providers.AlphaVantage
	
	// Get API key from environment
	apiKey := ""
	if config.APIKeyEnv != "" {
		apiKey = os.Getenv(config.APIKeyEnv)
	}
	
	// If no API key, fall back to mock with warning
	if apiKey == "" {
		observ.Log("quotes_adapter_fallback", map[string]any{
			"requested_adapter": "alphavantage",
			"fallback_to":       "mock", 
			"reason":            "missing API key",
			"api_key_env":       config.APIKeyEnv,
		})
		return NewMockQuotesAdapter(), nil
	}
	
	// Create Alpha Vantage config
	avConfig := AlphaVantageConfig{
		APIKey:              apiKey,
		RateLimitPerMinute:  config.RateLimitPerMinute,
		DailyCap:            config.DailyCap,
		CacheTTLSeconds:     config.CacheTTLSeconds,
		StaleCeilingSeconds: config.StaleCeilingSeconds,
		TimeoutSeconds:      config.TimeoutSeconds,
		MaxRetries:          config.MaxRetries,
		BackoffBaseMs:       config.BackoffBaseMs,
	}
	
	adapter, err := NewAlphaVantageAdapter(avConfig)
	if err != nil {
		observ.Log("quotes_adapter_fallback", map[string]any{
			"requested_adapter": "alphavantage",
			"fallback_to":       "mock",
			"reason":            "adapter creation failed",
			"error":             err.Error(),
		})
		return NewMockQuotesAdapter(), nil
	}
	
	observ.Log("quotes_adapter_created", map[string]any{
		"type":            "alphavantage",
		"rate_limit_pm":   config.RateLimitPerMinute,
		"daily_cap":       config.DailyCap,
		"cache_ttl_sec":   config.CacheTTLSeconds,
		"stale_ceiling":   config.StaleCeilingSeconds,
		"api_key_masked":  maskAPIKey(apiKey),
	})
	
	return adapter, nil
}

// HealthMonitor wraps an adapter with health monitoring and auto-fallback
type HealthMonitor struct {
	primary     QuotesAdapter
	fallback    QuotesAdapter  
	config      HealthMonitorConfig
	
	// Health tracking
	consecutive_errors int
	last_health_check  time.Time
	healthy           bool
	using_fallback    bool
}

// HealthMonitorConfig controls health monitoring behavior
type HealthMonitorConfig struct {
	MaxConsecutiveErrors int           `yaml:"max_consecutive_errors"`
	HealthCheckInterval  time.Duration `yaml:"health_check_interval"`
	FallbackThreshold    int           `yaml:"fallback_threshold"`
	RecoveryThreshold    int           `yaml:"recovery_threshold"`
}

// NewHealthMonitor creates a health-monitored adapter with fallback
func NewHealthMonitor(primary, fallback QuotesAdapter, config HealthMonitorConfig) *HealthMonitor {
	// Set defaults
	if config.MaxConsecutiveErrors <= 0 {
		config.MaxConsecutiveErrors = 3
	}
	if config.HealthCheckInterval <= 0 {
		config.HealthCheckInterval = 60 * time.Second
	}
	if config.FallbackThreshold <= 0 {
		config.FallbackThreshold = 3
	}
	if config.RecoveryThreshold <= 0 {
		config.RecoveryThreshold = 3
	}
	
	return &HealthMonitor{
		primary:   primary,
		fallback:  fallback,
		config:    config,
		healthy:   true,
	}
}

// GetQuote implements QuotesAdapter with health monitoring
func (hm *HealthMonitor) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	adapter := hm.getActiveAdapter()
	
	quote, err := adapter.GetQuote(ctx, symbol)
	if err != nil {
		hm.recordError()
		
		// Try fallback if primary failed and we're not already using fallback
		if !hm.using_fallback && hm.shouldUseFallback() {
			hm.switchToFallback()
			if quote, fallbackErr := hm.fallback.GetQuote(ctx, symbol); fallbackErr == nil {
				return quote, nil
			}
		}
		
		return nil, err
	}
	
	hm.recordSuccess()
	return quote, nil
}

// GetQuotes implements QuotesAdapter with health monitoring  
func (hm *HealthMonitor) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	adapter := hm.getActiveAdapter()
	return adapter.GetQuotes(ctx, symbols)
}

// HealthCheck implements QuotesAdapter
func (hm *HealthMonitor) HealthCheck(ctx context.Context) error {
	return hm.getActiveAdapter().HealthCheck(ctx)
}

// Close implements QuotesAdapter
func (hm *HealthMonitor) Close() error {
	if err := hm.primary.Close(); err != nil {
		return err
	}
	return hm.fallback.Close()
}

// Helper methods
func (hm *HealthMonitor) getActiveAdapter() QuotesAdapter {
	if hm.using_fallback {
		return hm.fallback
	}
	return hm.primary
}

func (hm *HealthMonitor) recordError() {
	hm.consecutive_errors++
	if hm.consecutive_errors >= hm.config.MaxConsecutiveErrors {
		hm.healthy = false
	}
}

func (hm *HealthMonitor) recordSuccess() {
	hm.consecutive_errors = 0
	hm.healthy = true
}

func (hm *HealthMonitor) shouldUseFallback() bool {
	return hm.consecutive_errors >= hm.config.FallbackThreshold
}

func (hm *HealthMonitor) switchToFallback() {
	if !hm.using_fallback {
		hm.using_fallback = true
		observ.Log("quotes_adapter_fallback_activated", map[string]any{
			"consecutive_errors": hm.consecutive_errors,
			"threshold":         hm.config.FallbackThreshold,
		})
	}
}

// maskAPIKey masks sensitive API key for logging
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "***" + key[len(key)-4:]
}

// GetDefaultQuotesConfig returns sensible defaults for quotes configuration
func GetDefaultQuotesConfig() QuotesConfig {
	return QuotesConfig{
		Adapter: "mock", // Safe default for development
		Providers: QuotesProviderConfigs{
			AlphaVantage: AlphaVantageProviderConfig{
				APIKeyEnv:           "ALPHA_VANTAGE_API_KEY",
				RateLimitPerMinute:  5,
				DailyCap:            300,
				CacheTTLSeconds:     60,
				StaleCeilingSeconds: 180,
				TimeoutSeconds:      10,
				MaxRetries:          3,
				BackoffBaseMs:       1000,
			},
			Polygon: PolygonProviderConfig{
				APIKeyEnv:          "POLYGON_API_KEY", 
				RateLimitPerMinute: 100,
				TimeoutSeconds:     5,
			},
		},
	}
}