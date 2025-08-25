package adapters

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// HaltsAdapter interface for trading halt data providers
type HaltsAdapter interface {
	GetHaltStatus(ctx context.Context, symbol string) (*HaltInfo, error)
	GetAllHalts(ctx context.Context) (map[string]*HaltInfo, error)
	StartShadowMode(ctx context.Context) error
	StopShadowMode() error
	GetShadowMetrics() map[string]interface{}
	HealthCheck(ctx context.Context) error
	Close() error
}

// HaltInfo represents trading halt information
type HaltInfo struct {
	Symbol       string    `json:"symbol"`
	Halted       bool      `json:"halted"`
	Reason       string    `json:"reason"`
	HaltTime     time.Time `json:"halt_time"`
	ResumeTime   *time.Time `json:"resume_time,omitempty"`
	LastUpdated  time.Time `json:"last_updated"`
	Source       string    `json:"source"`
}

// HaltEvent represents a halt state change event
type HaltEvent struct {
	Symbol    string    `json:"symbol"`
	Action    string    `json:"action"` // "halt" or "resume"
	Reason    string    `json:"reason"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`
}

// ShadowHaltsAdapter runs halt feeds in shadow mode for validation
type ShadowHaltsAdapter struct {
	mu             sync.RWMutex
	liveProvider   HaltsAdapter
	currentHalts   map[string]*HaltInfo // Current halt state from existing system
	shadowHalts    map[string]*HaltInfo // Halt state from live provider
	running        bool
	logger         *log.Logger
	
	// Shadow mode metrics
	metrics        ShadowMetrics
}

// ShadowMetrics tracks shadow mode performance
type ShadowMetrics struct {
	EventsReceived    int64                  `json:"events_received"`
	ParityMatches     int64                  `json:"parity_matches"`
	ParityMismatches  int64                  `json:"parity_mismatches"`
	ShadowOnlyEvents  int64                  `json:"shadow_only_events"`
	CurrentOnlyEvents int64                  `json:"current_only_events"`
	LastParity        time.Time              `json:"last_parity_check"`
	MismatchReasons   map[string]int64       `json:"mismatch_reasons"`
}

// NewShadowHaltsAdapter creates a new shadow mode halts adapter
func NewShadowHaltsAdapter(liveProvider HaltsAdapter, logger *log.Logger) *ShadowHaltsAdapter {
	return &ShadowHaltsAdapter{
		liveProvider: liveProvider,
		currentHalts: make(map[string]*HaltInfo),
		shadowHalts:  make(map[string]*HaltInfo),
		logger:       logger,
		metrics: ShadowMetrics{
			MismatchReasons: make(map[string]int64),
		},
	}
}

// GetHaltStatus returns halt status from current system (not shadow)
func (sha *ShadowHaltsAdapter) GetHaltStatus(ctx context.Context, symbol string) (*HaltInfo, error) {
	sha.mu.RLock()
	defer sha.mu.RUnlock()
	
	halt, exists := sha.currentHalts[symbol]
	if !exists {
		return &HaltInfo{
			Symbol:      symbol,
			Halted:      false,
			LastUpdated: time.Now(),
			Source:      "current",
		}, nil
	}
	
	return halt, nil
}

// GetAllHalts returns all current halts (not shadow)
func (sha *ShadowHaltsAdapter) GetAllHalts(ctx context.Context) (map[string]*HaltInfo, error) {
	sha.mu.RLock()
	defer sha.mu.RUnlock()
	
	result := make(map[string]*HaltInfo)
	for symbol, halt := range sha.currentHalts {
		result[symbol] = halt
	}
	
	return result, nil
}

// StartShadowMode begins shadow mode operation
func (sha *ShadowHaltsAdapter) StartShadowMode(ctx context.Context) error {
	sha.mu.Lock()
	if sha.running {
		sha.mu.Unlock()
		return fmt.Errorf("shadow mode already running")
	}
	sha.running = true
	sha.mu.Unlock()
	
	sha.logger.Printf("Starting halts shadow mode")
	
	// Start shadow feed monitoring
	go sha.shadowLoop(ctx)
	
	// Start parity checking
	go sha.parityLoop(ctx)
	
	observ.IncCounter("halts_shadow_mode_started_total", map[string]string{})
	
	return nil
}

// StopShadowMode stops shadow mode operation
func (sha *ShadowHaltsAdapter) StopShadowMode() error {
	sha.mu.Lock()
	defer sha.mu.Unlock()
	
	if !sha.running {
		return fmt.Errorf("shadow mode not running")
	}
	
	sha.running = false
	sha.logger.Printf("Stopped halts shadow mode")
	
	observ.IncCounter("halts_shadow_mode_stopped_total", map[string]string{})
	
	return nil
}

// shadowLoop monitors the live provider in shadow mode
func (sha *ShadowHaltsAdapter) shadowLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // Poll every 30 seconds
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !sha.isRunning() {
				return
			}
			sha.fetchShadowHalts(ctx)
		}
	}
}

// fetchShadowHalts fetches halt data from live provider
func (sha *ShadowHaltsAdapter) fetchShadowHalts(ctx context.Context) {
	start := time.Now()
	
	shadowHalts, err := sha.liveProvider.GetAllHalts(ctx)
	if err != nil {
		observ.IncCounter("halts_shadow_fetch_error_total", map[string]string{
			"error": "fetch_failed",
		})
		sha.logger.Printf("Shadow halts fetch failed: %v", err)
		return
	}
	
	latency := time.Since(start)
	observ.RecordDuration("halts_shadow_fetch_latency", latency, map[string]string{})
	
	sha.mu.Lock()
	sha.shadowHalts = shadowHalts
	sha.metrics.EventsReceived++
	sha.mu.Unlock()
	
	observ.IncCounter("halts_shadow_fetch_success_total", map[string]string{})
	observ.RecordGauge("halts_shadow_count", float64(len(shadowHalts)), map[string]string{})
}

// parityLoop compares shadow data with current system
func (sha *ShadowHaltsAdapter) parityLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute) // Check parity every minute
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !sha.isRunning() {
				return
			}
			sha.checkParity()
		}
	}
}

// checkParity compares shadow data with current system data
func (sha *ShadowHaltsAdapter) checkParity() {
	sha.mu.Lock()
	defer sha.mu.Unlock()
	
	sha.metrics.LastParity = time.Now()
	
	// Get all symbols from both systems
	allSymbols := make(map[string]bool)
	for symbol := range sha.currentHalts {
		allSymbols[symbol] = true
	}
	for symbol := range sha.shadowHalts {
		allSymbols[symbol] = true
	}
	
	matches := int64(0)
	mismatches := int64(0)
	shadowOnlyCount := int64(0)
	currentOnlyCount := int64(0)
	
	for symbol := range allSymbols {
		current, currentExists := sha.currentHalts[symbol]
		shadow, shadowExists := sha.shadowHalts[symbol]
		
		if !currentExists && shadowExists {
			// Shadow system has halt, current doesn't
			shadowOnlyCount++
			sha.recordMismatch("shadow_only", symbol, nil, shadow)
		} else if currentExists && !shadowExists {
			// Current system has halt, shadow doesn't
			currentOnlyCount++
			sha.recordMismatch("current_only", symbol, current, nil)
		} else if currentExists && shadowExists {
			// Both systems have data - compare
			if sha.compareHalts(current, shadow) {
				matches++
			} else {
				mismatches++
				sha.recordMismatch("status_mismatch", symbol, current, shadow)
			}
		}
		// If neither has data, that's a match
	}
	
	sha.metrics.ParityMatches += matches
	sha.metrics.ParityMismatches += mismatches
	sha.metrics.ShadowOnlyEvents += shadowOnlyCount
	sha.metrics.CurrentOnlyEvents += currentOnlyCount
	
	// Record metrics
	observ.RecordGauge("halts_parity_matches", float64(matches), map[string]string{})
	observ.RecordGauge("halts_parity_mismatches", float64(mismatches), map[string]string{})
	observ.RecordGauge("halts_shadow_only_events", float64(shadowOnlyCount), map[string]string{})
	observ.RecordGauge("halts_current_only_events", float64(currentOnlyCount), map[string]string{})
	
	if mismatches > 0 || shadowOnlyCount > 0 || currentOnlyCount > 0 {
		sha.logger.Printf("Halt parity check: %d matches, %d mismatches, %d shadow-only, %d current-only", 
			matches, mismatches, shadowOnlyCount, currentOnlyCount)
	}
}

// compareHalts compares two halt info records
func (sha *ShadowHaltsAdapter) compareHalts(current, shadow *HaltInfo) bool {
	// Compare halt status (most important)
	if current.Halted != shadow.Halted {
		return false
	}
	
	// If not halted, that's a match
	if !current.Halted && !shadow.Halted {
		return true
	}
	
	// For halted stocks, compare reasons if available
	if current.Reason != "" && shadow.Reason != "" {
		return current.Reason == shadow.Reason
	}
	
	return true // Status matches, reasons not comparable
}

// recordMismatch logs a parity mismatch
func (sha *ShadowHaltsAdapter) recordMismatch(reason, symbol string, current, shadow *HaltInfo) {
	sha.metrics.MismatchReasons[reason]++
	
	observ.IncCounter("halts_parity_mismatch_total", map[string]string{
		"reason": reason,
		"symbol": symbol,
	})
	
	// Log detailed mismatch for analysis
	sha.logger.Printf("Halt parity mismatch [%s] for %s - current: %+v, shadow: %+v", 
		reason, symbol, current, shadow)
}

// UpdateCurrentHalt updates the current system halt state (for simulation)
func (sha *ShadowHaltsAdapter) UpdateCurrentHalt(symbol string, halt *HaltInfo) {
	sha.mu.Lock()
	defer sha.mu.Unlock()
	
	if halt.Halted {
		sha.currentHalts[symbol] = halt
	} else {
		delete(sha.currentHalts, symbol)
	}
	
	observ.IncCounter("halts_current_update_total", map[string]string{
		"symbol": symbol,
		"halted": fmt.Sprintf("%t", halt.Halted),
	})
}

// GetShadowMetrics returns shadow mode metrics
func (sha *ShadowHaltsAdapter) GetShadowMetrics() map[string]interface{} {
	sha.mu.RLock()
	defer sha.mu.RUnlock()
	
	return map[string]interface{}{
		"events_received":     sha.metrics.EventsReceived,
		"parity_matches":      sha.metrics.ParityMatches,
		"parity_mismatches":   sha.metrics.ParityMismatches,
		"shadow_only_events":  sha.metrics.ShadowOnlyEvents,
		"current_only_events": sha.metrics.CurrentOnlyEvents,
		"last_parity_check":   sha.metrics.LastParity,
		"mismatch_reasons":    sha.metrics.MismatchReasons,
		"running":             sha.running,
	}
}

// isRunning safely checks if shadow mode is running
func (sha *ShadowHaltsAdapter) isRunning() bool {
	sha.mu.RLock()
	defer sha.mu.RUnlock()
	return sha.running
}

// HealthCheck validates shadow mode operation
func (sha *ShadowHaltsAdapter) HealthCheck(ctx context.Context) error {
	if !sha.isRunning() {
		return fmt.Errorf("shadow mode not running")
	}
	
	// Check live provider health
	if err := sha.liveProvider.HealthCheck(ctx); err != nil {
		return fmt.Errorf("live provider unhealthy: %w", err)
	}
	
	// Check if we're receiving data
	sha.mu.RLock()
	lastParity := sha.metrics.LastParity
	eventsReceived := sha.metrics.EventsReceived
	sha.mu.RUnlock()
	
	if eventsReceived == 0 {
		return fmt.Errorf("no shadow events received")
	}
	
	if time.Since(lastParity) > 5*time.Minute {
		return fmt.Errorf("parity check stale: %v", time.Since(lastParity))
	}
	
	return nil
}

// Close stops shadow mode and cleans up
func (sha *ShadowHaltsAdapter) Close() error {
	if err := sha.StopShadowMode(); err != nil {
		sha.logger.Printf("Error stopping shadow mode: %v", err)
	}
	
	if sha.liveProvider != nil {
		return sha.liveProvider.Close()
	}
	
	return nil
}

// MockHaltsProvider provides a simple mock halts provider for testing
type MockHaltsProvider struct {
	mu    sync.RWMutex
	halts map[string]*HaltInfo
}

// NewMockHaltsProvider creates a mock halts provider
func NewMockHaltsProvider() *MockHaltsProvider {
	return &MockHaltsProvider{
		halts: make(map[string]*HaltInfo),
	}
}

// GetHaltStatus returns halt status for a symbol
func (mhp *MockHaltsProvider) GetHaltStatus(ctx context.Context, symbol string) (*HaltInfo, error) {
	mhp.mu.RLock()
	defer mhp.mu.RUnlock()
	
	halt, exists := mhp.halts[symbol]
	if !exists {
		return &HaltInfo{
			Symbol:      symbol,
			Halted:      false,
			LastUpdated: time.Now(),
			Source:      "mock",
		}, nil
	}
	
	return halt, nil
}

// GetAllHalts returns all halts
func (mhp *MockHaltsProvider) GetAllHalts(ctx context.Context) (map[string]*HaltInfo, error) {
	mhp.mu.RLock()
	defer mhp.mu.RUnlock()
	
	result := make(map[string]*HaltInfo)
	for symbol, halt := range mhp.halts {
		result[symbol] = halt
	}
	
	return result, nil
}

// SetHalt sets halt status for testing
func (mhp *MockHaltsProvider) SetHalt(symbol string, halted bool, reason string) {
	mhp.mu.Lock()
	defer mhp.mu.Unlock()
	
	if halted {
		mhp.halts[symbol] = &HaltInfo{
			Symbol:      symbol,
			Halted:      true,
			Reason:      reason,
			HaltTime:    time.Now(),
			LastUpdated: time.Now(),
			Source:      "mock",
		}
	} else {
		delete(mhp.halts, symbol)
	}
}

// HealthCheck validates the mock provider
func (mhp *MockHaltsProvider) HealthCheck(ctx context.Context) error {
	return nil // Mock is always healthy
}

// Close cleans up the mock provider
func (mhp *MockHaltsProvider) Close() error {
	return nil
}

// StartShadowMode is a no-op for mock provider
func (mhp *MockHaltsProvider) StartShadowMode(ctx context.Context) error {
	return nil
}

// StopShadowMode is a no-op for mock provider
func (mhp *MockHaltsProvider) StopShadowMode() error {
	return nil
}

// GetShadowMetrics returns empty metrics for mock provider
func (mhp *MockHaltsProvider) GetShadowMetrics() map[string]interface{} {
	return map[string]interface{}{
		"running": false,
		"provider": "mock",
	}
}