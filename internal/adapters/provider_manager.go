package adapters

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// ProviderManager orchestrates multiple quote providers with failover and health management
type ProviderManager struct {
	mu            sync.RWMutex
	providers     map[string]QuotesAdapter
	healthRegistry *HealthRegistry
	config        ProviderManagerConfig
	
	// Current provider state
	activeProvider string
	warmProvider   string
	
	// Live activation state
	liveSymbolsAllowlist []string
	canaryStartTime      time.Time
	priorityStartTime    time.Time
	expansionState       ExpansionState
	
	// Metrics
	providerSwitchCount map[string]int64
	lastSwitch          time.Time
}

// ProviderManagerConfig holds configuration for provider management
type ProviderManagerConfig struct {
	ActiveProvider    string   `yaml:"provider_active"`
	WarmProvider      string   `yaml:"provider_warm"`
	LiveSymbolsAllowlist []string `yaml:"live_symbols_allowlist"`
	CanarySymbols     []string `yaml:"canary_symbols"`
	PrioritySymbols   []string `yaml:"priority_symbols"`
	
	// Timing configuration
	CanaryDurationMinutes   int `yaml:"canary_duration_minutes"`
	PriorityDurationMinutes int `yaml:"priority_duration_minutes"`
	
	// Circuit breaker configuration
	CircuitBreakerConfig CircuitBreakerConfig `yaml:"circuit_breaker"`
	
	// Cost and budget
	DailyCostLimitUSD float64 `yaml:"daily_cost_limit_usd"`
}

// CircuitBreakerConfig holds circuit breaker settings
type CircuitBreakerConfig struct {
	ErrorThreshold       float64 `yaml:"error_threshold"`
	ConsecutiveFailures  int     `yaml:"consecutive_failures"`
	CooldownMinutes      int     `yaml:"cooldown_minutes"`
	ProbeIntervalSeconds int     `yaml:"probe_interval_seconds"`
}

// ExpansionState tracks the gradual live expansion
type ExpansionState string

const (
	ExpansionCanary   ExpansionState = "canary"   // Only canary symbols live
	ExpansionPriority ExpansionState = "priority" // Priority symbols live
	ExpansionFull     ExpansionState = "full"     // All symbols live
)

// HealthRegistry manages provider health state with circuit breakers
type HealthRegistry struct {
	mu                sync.RWMutex
	states            map[string]*ProviderHealthState
	circuitBreakers   map[string]*CircuitBreaker
}

// ProviderHealthState tracks health with history
type ProviderHealthState struct {
	Provider            string          `json:"provider"`
	Status              HealthState     `json:"status"`
	LastCheck           time.Time       `json:"last_check"`
	ConsecutiveErrors   int             `json:"consecutive_errors"`
	ConsecutiveSuccess  int             `json:"consecutive_success"`
	TotalRequests       int64           `json:"total_requests"`
	TotalErrors         int64           `json:"total_errors"`
	LastError           string          `json:"last_error,omitempty"`
	CooldownUntil       time.Time       `json:"cooldown_until,omitempty"`
}

// CircuitBreaker implements circuit breaker pattern for providers
type CircuitBreaker struct {
	mu                  sync.RWMutex
	provider            string
	config              CircuitBreakerConfig
	state               CircuitBreakerState
	failures            int
	lastFailure         time.Time
	nextProbe          time.Time
}

// CircuitBreakerState represents the circuit breaker state
type CircuitBreakerState string

const (
	CircuitClosed    CircuitBreakerState = "closed"     // Normal operation
	CircuitOpen      CircuitBreakerState = "open"       // Failing, reject requests
	CircuitHalfOpen  CircuitBreakerState = "half-open"  // Probing for recovery
)

// NewProviderManager creates a new provider manager
func NewProviderManager(config ProviderManagerConfig) *ProviderManager {
	pm := &ProviderManager{
		providers:         make(map[string]QuotesAdapter),
		config:           config,
		activeProvider:   config.ActiveProvider,
		warmProvider:     config.WarmProvider,
		liveSymbolsAllowlist: config.LiveSymbolsAllowlist,
		canaryStartTime:  time.Now(),
		expansionState:   ExpansionCanary,
		providerSwitchCount: make(map[string]int64),
		
		healthRegistry: &HealthRegistry{
			states:          make(map[string]*ProviderHealthState),
			circuitBreakers: make(map[string]*CircuitBreaker),
		},
	}
	
	observ.Log("provider_manager_created", map[string]any{
		"active_provider":    config.ActiveProvider,
		"warm_provider":      config.WarmProvider,
		"canary_symbols":     config.CanarySymbols,
		"priority_symbols":   config.PrioritySymbols,
		"expansion_state":    string(pm.expansionState),
	})
	
	return pm
}

// RegisterProvider registers a provider with the manager
func (pm *ProviderManager) RegisterProvider(name string, provider QuotesAdapter) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	pm.providers[name] = provider
	
	// Initialize health state
	pm.healthRegistry.states[name] = &ProviderHealthState{
		Provider:  name,
		Status:    HealthHealthy,
		LastCheck: time.Now(),
	}
	
	// Initialize circuit breaker
	pm.healthRegistry.circuitBreakers[name] = &CircuitBreaker{
		provider: name,
		config:   pm.config.CircuitBreakerConfig,
		state:    CircuitClosed,
	}
	
	observ.Log("provider_registered", map[string]any{
		"provider": name,
		"total_providers": len(pm.providers),
	})
}

// GetQuote gets a quote using the provider selection strategy
func (pm *ProviderManager) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	// Check if symbol is allowed for live mode
	if !pm.isSymbolLiveAllowed(symbol) {
		// Return mock quote for non-live symbols
		if mockProvider, exists := pm.providers["mock"]; exists {
			return mockProvider.GetQuote(ctx, symbol)
		}
		return nil, fmt.Errorf("symbol %s not allowed for live mode and no mock provider", symbol)
	}
	
	// Select provider based on health and strategy
	providerName := pm.selectProvider(symbol)
	
	provider, exists := pm.providers[providerName]
	if !exists {
		return nil, fmt.Errorf("selected provider %s not found", providerName)
	}
	
	// Check circuit breaker
	breaker := pm.healthRegistry.circuitBreakers[providerName]
	if !breaker.allowRequest() {
		// Try warm provider if available
		if pm.warmProvider != "" && pm.warmProvider != providerName {
			if warmProvider, exists := pm.providers[pm.warmProvider]; exists {
				observ.Log("provider_circuit_breaker_fallback", map[string]any{
					"from_provider": providerName,
					"to_provider":   pm.warmProvider,
					"symbol":        symbol,
				})
				return warmProvider.GetQuote(ctx, symbol)
			}
		}
		return nil, fmt.Errorf("provider %s circuit breaker open", providerName)
	}
	
	// Make the request
	startTime := time.Now()
	quote, err := provider.GetQuote(ctx, symbol)
	latency := time.Since(startTime)
	
	// Record metrics
	pm.recordProviderMetrics(providerName, err == nil, latency)
	
	// Update health state
	if err != nil {
		pm.recordProviderError(providerName, err)
		breaker.recordFailure()
		
		// Try failover if this was the active provider
		if providerName == pm.activeProvider {
			return pm.tryFailover(ctx, symbol, err)
		}
		
		return nil, err
	}
	
	pm.recordProviderSuccess(providerName)
	breaker.recordSuccess()
	
	return quote, nil
}

// GetQuotes gets multiple quotes
func (pm *ProviderManager) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	results := make(map[string]*Quote)
	
	for _, symbol := range symbols {
		quote, err := pm.GetQuote(ctx, symbol)
		if err != nil {
			observ.Log("provider_manager_quote_error", map[string]any{
				"symbol": symbol,
				"error":  err.Error(),
			})
			continue
		}
		results[symbol] = quote
	}
	
	return results, nil
}

// HealthCheck performs comprehensive health check
func (pm *ProviderManager) HealthCheck(ctx context.Context) error {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	var errors []string
	
	// Check each provider
	for name, provider := range pm.providers {
		if err := provider.HealthCheck(ctx); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", name, err))
			pm.recordProviderError(name, err)
		} else {
			pm.recordProviderSuccess(name)
		}
	}
	
	if len(errors) > 0 {
		return fmt.Errorf("provider health check failures: %v", errors)
	}
	
	return nil
}

// Close closes all providers
func (pm *ProviderManager) Close() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	
	var errors []string
	
	for name, provider := range pm.providers {
		if err := provider.Close(); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", name, err))
		}
	}
	
	if len(errors) > 0 {
		return fmt.Errorf("provider close errors: %v", errors)
	}
	
	return nil
}

// selectProvider selects the best provider for a symbol
func (pm *ProviderManager) selectProvider(symbol string) string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	// Check if active provider is healthy
	activeHealth := pm.healthRegistry.states[pm.activeProvider]
	if activeHealth != nil && activeHealth.Status != HealthFailed {
		return pm.activeProvider
	}
	
	// If active provider is failed, try warm provider
	if pm.warmProvider != "" {
		warmHealth := pm.healthRegistry.states[pm.warmProvider]
		if warmHealth != nil && warmHealth.Status != HealthFailed {
			// Switch to warm provider
			pm.switchProvider(pm.warmProvider, "active_failed")
			return pm.warmProvider
		}
	}
	
	// If all else fails, return active provider and let circuit breaker handle it
	return pm.activeProvider
}

// isSymbolLiveAllowed checks if symbol is allowed for live mode based on expansion state
func (pm *ProviderManager) isSymbolLiveAllowed(symbol string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	// Update expansion state based on time elapsed
	pm.updateExpansionState()
	
	switch pm.expansionState {
	case ExpansionCanary:
		// Only canary symbols allowed
		for _, canarySymbol := range pm.config.CanarySymbols {
			if canarySymbol == symbol {
				return true
			}
		}
		return false
		
	case ExpansionPriority:
		// Priority symbols allowed
		for _, prioritySymbol := range pm.config.PrioritySymbols {
			if prioritySymbol == symbol {
				return true
			}
		}
		return false
		
	case ExpansionFull:
		// All symbols allowed
		return true
		
	default:
		return false
	}
}

// updateExpansionState updates the expansion state based on elapsed time
func (pm *ProviderManager) updateExpansionState() {
	now := time.Now()
	
	switch pm.expansionState {
	case ExpansionCanary:
		if now.Sub(pm.canaryStartTime) > time.Duration(pm.config.CanaryDurationMinutes)*time.Minute {
			pm.expansionState = ExpansionPriority
			pm.priorityStartTime = now
			
			observ.Log("expansion_state_changed", map[string]any{
				"from":                "canary",
				"to":                  "priority",
				"canary_duration_min": pm.config.CanaryDurationMinutes,
				"symbols_allowed":     pm.config.PrioritySymbols,
			})
		}
		
	case ExpansionPriority:
		if now.Sub(pm.priorityStartTime) > time.Duration(pm.config.PriorityDurationMinutes)*time.Minute {
			pm.expansionState = ExpansionFull
			
			observ.Log("expansion_state_changed", map[string]any{
				"from":                   "priority",
				"to":                     "full",
				"priority_duration_min":  pm.config.PriorityDurationMinutes,
				"symbols_allowed":        "all",
			})
		}
	}
}

// switchProvider switches the active provider
func (pm *ProviderManager) switchProvider(newProvider, reason string) {
	if pm.activeProvider == newProvider {
		return
	}
	
	oldProvider := pm.activeProvider
	pm.activeProvider = newProvider
	pm.providerSwitchCount[newProvider]++
	pm.lastSwitch = time.Now()
	
	observ.Log("provider_switch", map[string]any{
		"from":    oldProvider,
		"to":      newProvider,
		"reason":  reason,
		"symbol":  "", // Will be filled by caller if relevant
	})
}

// tryFailover attempts to failover to another provider
func (pm *ProviderManager) tryFailover(ctx context.Context, symbol string, originalErr error) (*Quote, error) {
	if pm.warmProvider == "" {
		return nil, originalErr
	}
	
	warmProvider, exists := pm.providers[pm.warmProvider]
	if !exists {
		return nil, originalErr
	}
	
	// Try warm provider
	quote, err := warmProvider.GetQuote(ctx, symbol)
	if err != nil {
		return nil, originalErr // Return original error if failover also fails
	}
	
	// Successful failover
	pm.switchProvider(pm.warmProvider, "failover_success")
	
	observ.Log("provider_failover_success", map[string]any{
		"symbol":          symbol,
		"failed_provider": pm.activeProvider,
		"warm_provider":   pm.warmProvider,
		"original_error":  originalErr.Error(),
	})
	
	return quote, nil
}

// recordProviderMetrics records metrics for a provider
func (pm *ProviderManager) recordProviderMetrics(provider string, success bool, latency time.Duration) {
	labels := map[string]string{"provider": provider}
	
	observ.IncCounter("provider_requests_total", labels)
	
	if success {
		observ.IncCounter("provider_successes_total", labels)
	} else {
		observ.IncCounter("provider_errors_total", labels)
	}
	
	observ.RecordDuration("provider_latency", latency, labels)
}

// recordProviderError records an error for a provider
func (pm *ProviderManager) recordProviderError(provider string, err error) {
	pm.healthRegistry.mu.Lock()
	defer pm.healthRegistry.mu.Unlock()
	
	state := pm.healthRegistry.states[provider]
	if state == nil {
		return
	}
	
	state.ConsecutiveErrors++
	state.ConsecutiveSuccess = 0
	state.TotalErrors++
	state.LastError = err.Error()
	state.LastCheck = time.Now()
	
	// Update health status based on consecutive errors
	if state.ConsecutiveErrors >= pm.config.CircuitBreakerConfig.ConsecutiveFailures {
		state.Status = HealthFailed
		state.CooldownUntil = time.Now().Add(time.Duration(pm.config.CircuitBreakerConfig.CooldownMinutes) * time.Minute)
	} else if state.ConsecutiveErrors >= 2 {
		state.Status = HealthDegraded
	}
}

// recordProviderSuccess records a success for a provider
func (pm *ProviderManager) recordProviderSuccess(provider string) {
	pm.healthRegistry.mu.Lock()
	defer pm.healthRegistry.mu.Unlock()
	
	state := pm.healthRegistry.states[provider]
	if state == nil {
		return
	}
	
	state.ConsecutiveSuccess++
	state.ConsecutiveErrors = 0
	state.TotalRequests++
	state.LastCheck = time.Now()
	
	// Recovery logic
	if state.Status == HealthFailed && state.ConsecutiveSuccess >= 5 {
		state.Status = HealthDegraded
	} else if state.Status == HealthDegraded && state.ConsecutiveSuccess >= 3 {
		state.Status = HealthHealthy
	}
}

// GetProviderHealth returns health information for all providers
func (pm *ProviderManager) GetProviderHealth() map[string]*ProviderHealthState {
	pm.healthRegistry.mu.RLock()
	defer pm.healthRegistry.mu.RUnlock()
	
	result := make(map[string]*ProviderHealthState)
	for name, state := range pm.healthRegistry.states {
		// Return a copy to prevent mutations
		result[name] = &ProviderHealthState{
			Provider:           state.Provider,
			Status:             state.Status,
			LastCheck:          state.LastCheck,
			ConsecutiveErrors:  state.ConsecutiveErrors,
			ConsecutiveSuccess: state.ConsecutiveSuccess,
			TotalRequests:      state.TotalRequests,
			TotalErrors:        state.TotalErrors,
			LastError:          state.LastError,
			CooldownUntil:      state.CooldownUntil,
		}
	}
	
	return result
}

// GetManagerStatus returns current manager status
func (pm *ProviderManager) GetManagerStatus() map[string]any {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	return map[string]any{
		"active_provider":        pm.activeProvider,
		"warm_provider":          pm.warmProvider,
		"expansion_state":        string(pm.expansionState),
		"canary_start_time":      pm.canaryStartTime.Format(time.RFC3339),
		"priority_start_time":    pm.priorityStartTime.Format(time.RFC3339),
		"live_symbols_allowlist": pm.liveSymbolsAllowlist,
		"provider_switch_count":  pm.providerSwitchCount,
		"last_switch":            pm.lastSwitch.Format(time.RFC3339),
		"total_providers":        len(pm.providers),
	}
}

// Circuit breaker implementation

// allowRequest checks if the circuit breaker allows a request
func (cb *CircuitBreaker) allowRequest() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	
	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		// Check if it's time to probe
		if time.Now().After(cb.nextProbe) {
			cb.mu.RUnlock()
			cb.mu.Lock()
			cb.state = CircuitHalfOpen
			cb.mu.Unlock()
			cb.mu.RLock()
			return true
		}
		return false
	case CircuitHalfOpen:
		return true
	default:
		return false
	}
}

// recordFailure records a failure for the circuit breaker
func (cb *CircuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.failures++
	cb.lastFailure = time.Now()
	
	if cb.failures >= cb.config.ConsecutiveFailures {
		cb.state = CircuitOpen
		cb.nextProbe = time.Now().Add(time.Duration(cb.config.CooldownMinutes) * time.Minute)
		
		observ.Log("circuit_breaker_opened", map[string]any{
			"provider":     cb.provider,
			"failures":     cb.failures,
			"next_probe":   cb.nextProbe.Format(time.RFC3339),
		})
	}
}

// recordSuccess records a success for the circuit breaker
func (cb *CircuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	if cb.state == CircuitHalfOpen {
		cb.state = CircuitClosed
		cb.failures = 0
		
		observ.Log("circuit_breaker_closed", map[string]any{
			"provider": cb.provider,
			"reason":   "successful_probe",
		})
	} else if cb.state == CircuitClosed {
		cb.failures = 0
	}
}

// ProviderManagerStatus represents the current state of the provider manager
type ProviderManagerStatus struct {
	ActiveProvider    string          `json:"active_provider"`
	WarmProvider      string          `json:"warm_provider"`
	LiveModeEnabled   bool            `json:"live_mode_enabled"`
	ShadowModeEnabled bool            `json:"shadow_mode_enabled"`
	ExpansionState    ExpansionState  `json:"expansion_state"`
	LiveSymbols       []string        `json:"live_symbols"`
	ProviderCount     int             `json:"provider_count"`
}

// GetStatus returns the current status of the provider manager
func (pm *ProviderManager) GetStatus() ProviderManagerStatus {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	
	return ProviderManagerStatus{
		ActiveProvider:    pm.activeProvider,
		WarmProvider:      pm.warmProvider,
		LiveModeEnabled:   len(pm.liveSymbolsAllowlist) > 0,
		ShadowModeEnabled: true, // Default to shadow mode for Session 18
		ExpansionState:    pm.expansionState,
		LiveSymbols:       pm.liveSymbolsAllowlist,
		ProviderCount:     len(pm.providers),
	}
}