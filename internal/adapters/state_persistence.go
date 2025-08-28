package adapters

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// AdapterState represents persistent state for adapters
type AdapterState struct {
	Version          int                    `json:"version"`
	LastUpdated      string                 `json:"last_updated"`
	HealthState      map[string]interface{} `json:"health_state"`
	BudgetState      map[string]interface{} `json:"budget_state"`
	CacheMetrics     map[string]interface{} `json:"cache_metrics"`
	LastHealthCheck  string                 `json:"last_health_check"`
	ConsecutiveStats map[string]interface{} `json:"consecutive_stats"`
}

// StatePersistenceManager handles saving and loading adapter state
type StatePersistenceManager struct {
	mu           sync.RWMutex
	filePath     string
	saveInterval time.Duration
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

// NewStatePersistenceManager creates a new state persistence manager
func NewStatePersistenceManager(filePath string, saveInterval time.Duration) *StatePersistenceManager {
	// Ensure directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		observ.Log("state_persistence_dir_error", map[string]any{
			"error": err.Error(),
			"dir":   dir,
		})
	}
	
	return &StatePersistenceManager{
		filePath:     filePath,
		saveInterval: saveInterval,
		stopCh:       make(chan struct{}),
	}
}

// Start begins periodic state persistence
func (spm *StatePersistenceManager) Start() {
	spm.wg.Add(1)
	go spm.persistenceLoop()
	
	observ.Log("state_persistence_started", map[string]any{
		"file_path":     spm.filePath,
		"save_interval": spm.saveInterval.String(),
	})
}

// Stop gracefully stops state persistence
func (spm *StatePersistenceManager) Stop() error {
	close(spm.stopCh)
	spm.wg.Wait()
	
	// Perform final save
	if err := spm.saveCurrentState(); err != nil {
		observ.Log("state_persistence_final_save_error", map[string]any{
			"error": err.Error(),
		})
		return err
	}
	
	observ.Log("state_persistence_stopped", nil)
	return nil
}

// LoadState loads persisted state from disk
func (spm *StatePersistenceManager) LoadState() (*AdapterState, error) {
	spm.mu.RLock()
	defer spm.mu.RUnlock()
	
	if _, err := os.Stat(spm.filePath); os.IsNotExist(err) {
		// No existing state file
		return &AdapterState{
			Version:          1,
			LastUpdated:      time.Now().UTC().Format(time.RFC3339),
			HealthState:      make(map[string]interface{}),
			BudgetState:      make(map[string]interface{}),
			CacheMetrics:     make(map[string]interface{}),
			ConsecutiveStats: make(map[string]interface{}),
		}, nil
	}
	
	data, err := os.ReadFile(spm.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}
	
	var state AdapterState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}
	
	observ.Log("state_persistence_loaded", map[string]any{
		"file_path":    spm.filePath,
		"version":      state.Version,
		"last_updated": state.LastUpdated,
	})
	
	return &state, nil
}

// SaveState saves the current adapter state to disk
func (spm *StatePersistenceManager) SaveState(liveAdapter *LiveQuoteAdapter) error {
	spm.mu.Lock()
	defer spm.mu.Unlock()
	
	// Collect current state
	state := spm.collectCurrentState(liveAdapter)
	
	// Marshal to JSON
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	
	// Write to temporary file first
	tempPath := spm.filePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary state file: %w", err)
	}
	
	// Atomic rename
	if err := os.Rename(tempPath, spm.filePath); err != nil {
		return fmt.Errorf("failed to rename state file: %w", err)
	}
	
	observ.Log("state_persistence_saved", map[string]any{
		"file_path": spm.filePath,
		"version":   state.Version,
	})
	
	return nil
}

// RestoreState applies persisted state to a live adapter
func (spm *StatePersistenceManager) RestoreState(liveAdapter *LiveQuoteAdapter, state *AdapterState) error {
	liveAdapter.mu.Lock()
	defer liveAdapter.mu.Unlock()
	
	// Restore health state
	if healthStateStr, ok := state.HealthState["current"].(string); ok {
		switch healthStateStr {
		case "healthy":
			liveAdapter.healthState = HealthHealthy
		case "degraded":
			liveAdapter.healthState = HealthDegraded
		case "failed":
			liveAdapter.healthState = HealthFailed
		}
	}
	
	// Restore consecutive stats (but don't restore actual counts to prevent flapping)
	// We only use this for logging to show the previous state
	if consecutiveBreach, ok := state.ConsecutiveStats["breaches"].(float64); ok {
		observ.Log("state_persistence_restored", map[string]any{
			"previous_health_state":      state.HealthState["current"],
			"previous_consecutive_breach": int(consecutiveBreach),
			"previous_last_check":        state.LastHealthCheck,
		})
	}
	
	// Restore budget state (only if recent)
	if budgetResetStr, ok := state.BudgetState["reset_time"].(string); ok {
		if resetTime, err := time.Parse(time.RFC3339, budgetResetStr); err == nil {
			// Only restore if reset time is in the future (same day)
			if resetTime.After(time.Now()) {
				if budgetUsed, ok := state.BudgetState["requests_today"].(float64); ok {
					liveAdapter.budgetTracker.mu.Lock()
					liveAdapter.budgetTracker.requestsToday = int(budgetUsed)
					liveAdapter.budgetTracker.resetTime = resetTime
					liveAdapter.budgetTracker.mu.Unlock()
					
					observ.Log("budget_state_restored", map[string]any{
						"requests_restored": int(budgetUsed),
						"reset_time":        resetTime.Format(time.RFC3339),
					})
				}
			}
		}
	}
	
	return nil
}

// persistenceLoop runs the periodic save loop
func (spm *StatePersistenceManager) persistenceLoop() {
	defer spm.wg.Done()
	
	ticker := time.NewTicker(spm.saveInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			if err := spm.saveCurrentState(); err != nil {
				observ.Log("state_persistence_save_error", map[string]any{
					"error": err.Error(),
				})
			}
		case <-spm.stopCh:
			return
		}
	}
}

// saveCurrentState saves the current state (placeholder for actual implementation)
func (spm *StatePersistenceManager) saveCurrentState() error {
	// This would be called with the actual live adapter instance
	// For now, just create a minimal state
	state := &AdapterState{
		Version:          1,
		LastUpdated:      time.Now().UTC().Format(time.RFC3339),
		HealthState:      make(map[string]interface{}),
		BudgetState:      make(map[string]interface{}),
		CacheMetrics:     make(map[string]interface{}),
		ConsecutiveStats: make(map[string]interface{}),
	}
	
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}
	
	tempPath := spm.filePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary state file: %w", err)
	}
	
	if err := os.Rename(tempPath, spm.filePath); err != nil {
		return fmt.Errorf("failed to rename state file: %w", err)
	}
	
	return nil
}

// collectCurrentState collects current state from live adapter
func (spm *StatePersistenceManager) collectCurrentState(liveAdapter *LiveQuoteAdapter) *AdapterState {
	liveAdapter.mu.RLock()
	defer liveAdapter.mu.RUnlock()
	
	// Collect health state
	healthState := map[string]interface{}{
		"current":                string(liveAdapter.healthState),
		"last_transition":        liveAdapter.lastHealthTransition.Format(time.RFC3339),
		"consecutive_breaches":   liveAdapter.consecutiveBreaches,
		"consecutive_oks":        liveAdapter.consecutiveOks,
	}
	
	// Collect budget state
	liveAdapter.budgetTracker.mu.RLock()
	budgetState := map[string]interface{}{
		"requests_today": liveAdapter.budgetTracker.requestsToday,
		"daily_cap":      liveAdapter.budgetTracker.dailyCap,
		"reset_time":     liveAdapter.budgetTracker.resetTime.Format(time.RFC3339),
	}
	liveAdapter.budgetTracker.mu.RUnlock()
	
	// Collect cache metrics
	liveAdapter.cache.mu.RLock()
	cacheMetrics := map[string]interface{}{
		"entries":   len(liveAdapter.cache.entries),
		"hits":      liveAdapter.cache.hits,
		"misses":    liveAdapter.cache.misses,
		"evictions": liveAdapter.cache.evictions,
	}
	liveAdapter.cache.mu.RUnlock()
	
	// Collect consecutive stats
	consecutiveStats := map[string]interface{}{
		"breaches": liveAdapter.consecutiveBreaches,
		"oks":      liveAdapter.consecutiveOks,
	}
	
	return &AdapterState{
		Version:          1,
		LastUpdated:      time.Now().UTC().Format(time.RFC3339),
		HealthState:      healthState,
		BudgetState:      budgetState,
		CacheMetrics:     cacheMetrics,
		LastHealthCheck:  liveAdapter.lastHealthTransition.Format(time.RFC3339),
		ConsecutiveStats: consecutiveStats,
	}
}

// GracefulShutdownManager handles graceful shutdown of adapters
type GracefulShutdownManager struct {
	adapters       []QuotesAdapter
	persistence    *StatePersistenceManager
	shutdownTimeout time.Duration
	mu             sync.Mutex
}

// NewGracefulShutdownManager creates a new graceful shutdown manager
func NewGracefulShutdownManager(persistence *StatePersistenceManager, shutdownTimeout time.Duration) *GracefulShutdownManager {
	return &GracefulShutdownManager{
		adapters:       make([]QuotesAdapter, 0),
		persistence:    persistence,
		shutdownTimeout: shutdownTimeout,
	}
}

// RegisterAdapter registers an adapter for graceful shutdown
func (gsm *GracefulShutdownManager) RegisterAdapter(adapter QuotesAdapter) {
	gsm.mu.Lock()
	defer gsm.mu.Unlock()
	gsm.adapters = append(gsm.adapters, adapter)
}

// Shutdown performs graceful shutdown of all registered adapters
func (gsm *GracefulShutdownManager) Shutdown() error {
	gsm.mu.Lock()
	defer gsm.mu.Unlock()
	
	observ.Log("graceful_shutdown_started", map[string]any{
		"adapter_count": len(gsm.adapters),
		"timeout":       gsm.shutdownTimeout.String(),
	})
	
	// Create a channel to track completion
	done := make(chan struct{})
	var shutdownErr error
	
	go func() {
		defer close(done)
		
		// Stop persistence first
		if gsm.persistence != nil {
			if err := gsm.persistence.Stop(); err != nil {
				shutdownErr = fmt.Errorf("failed to stop persistence: %w", err)
				return
			}
		}
		
		// Close all adapters
		for i, adapter := range gsm.adapters {
			if err := adapter.Close(); err != nil {
				observ.Log("adapter_close_error", map[string]any{
					"adapter_index": i,
					"error":         err.Error(),
				})
				if shutdownErr == nil {
					shutdownErr = fmt.Errorf("failed to close adapter %d: %w", i, err)
				}
			}
		}
		
		observ.Log("graceful_shutdown_completed", nil)
	}()
	
	// Wait for completion or timeout
	select {
	case <-done:
		return shutdownErr
	case <-time.After(gsm.shutdownTimeout):
		observ.Log("graceful_shutdown_timeout", map[string]any{
			"timeout": gsm.shutdownTimeout.String(),
		})
		return fmt.Errorf("graceful shutdown timed out after %v", gsm.shutdownTimeout)
	}
}

// UpdateLiveAdapterWithPersistence updates a live adapter to use persistence
func UpdateLiveAdapterWithPersistence(liveAdapter *LiveQuoteAdapter, persistence *StatePersistenceManager) error {
	// Load existing state
	state, err := persistence.LoadState()
	if err != nil {
		observ.Log("state_load_error", map[string]any{
			"error": err.Error(),
		})
		return err
	}
	
	// Restore state
	if err := persistence.RestoreState(liveAdapter, state); err != nil {
		observ.Log("state_restore_error", map[string]any{
			"error": err.Error(),
		})
		return err
	}
	
	return nil
}