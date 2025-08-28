package adapters

import (
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// CostGovernor tracks and manages API costs across providers
type CostGovernor struct {
	mu                sync.RWMutex
	providers         map[string]*ProviderCostTracker
	dailyCostLimitUSD float64
	warningThreshold  float64 // Percentage of limit to trigger warning
	
	// Daily tracking
	resetTime time.Time
}

// ProviderCostTracker tracks costs for a single provider
type ProviderCostTracker struct {
	Provider            string    `json:"provider"`
	RequestsToday       int64     `json:"requests_today"`
	CostPerRequestUSD   float64   `json:"cost_per_request_usd"`
	EstimatedCostUSD    float64   `json:"estimated_cost_usd"`
	DailyLimitUSD       float64   `json:"daily_limit_usd"`
	LastRequest         time.Time `json:"last_request"`
	
	// Rate limiting
	RequestsPerMinute   int       `json:"requests_per_minute"`
	RequestsThisMinute  int       `json:"requests_this_minute"`
	MinuteResetTime     time.Time `json:"minute_reset_time"`
	
	// Budget warnings
	LastWarning         time.Time `json:"last_warning"`
}

// CostSummary provides aggregated cost information
type CostSummary struct {
	TotalCostUSD      float64                       `json:"total_cost_usd"`
	DailyLimitUSD     float64                       `json:"daily_limit_usd"`
	RemainingBudgetUSD float64                      `json:"remaining_budget_usd"`
	BudgetUsedPct     float64                       `json:"budget_used_pct"`
	ProvidersAtLimit  []string                      `json:"providers_at_limit"`
	CostByProvider    map[string]*ProviderCostTracker `json:"cost_by_provider"`
	ResetTime         time.Time                     `json:"reset_time"`
	
	// Projections
	ProjectedDailyCost float64 `json:"projected_daily_cost"`
	ProjectedOverage   float64 `json:"projected_overage"`
}

// NewCostGovernor creates a new cost governor
func NewCostGovernor(dailyLimitUSD float64) *CostGovernor {
	return &CostGovernor{
		providers:         make(map[string]*ProviderCostTracker),
		dailyCostLimitUSD: dailyLimitUSD,
		warningThreshold:  0.8, // 80% warning threshold
		resetTime:         getNextMidnightUTC(),
	}
}

// RegisterProvider registers a provider for cost tracking
func (cg *CostGovernor) RegisterProvider(provider string, costPerRequestUSD float64, requestsPerMinute int) {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	
	cg.providers[provider] = &ProviderCostTracker{
		Provider:           provider,
		CostPerRequestUSD:  costPerRequestUSD,
		RequestsPerMinute:  requestsPerMinute,
		MinuteResetTime:    time.Now().Add(time.Minute),
	}
	
	observ.Log("cost_governance_provider_registered", map[string]any{
		"provider":               provider,
		"cost_per_request_usd":   costPerRequestUSD,
		"requests_per_minute":    requestsPerMinute,
	})
}

// CanMakeRequest checks if a request is allowed within budget and rate limits
func (cg *CostGovernor) CanMakeRequest(provider string) (bool, string) {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	
	// Reset daily budget if needed
	if time.Now().After(cg.resetTime) {
		cg.resetDailyBudget()
	}
	
	tracker, exists := cg.providers[provider]
	if !exists {
		return false, "provider not registered"
	}
	
	// Reset minute counter if needed
	if time.Now().After(tracker.MinuteResetTime) {
		tracker.RequestsThisMinute = 0
		tracker.MinuteResetTime = time.Now().Add(time.Minute)
	}
	
	// Check rate limit
	if tracker.RequestsThisMinute >= tracker.RequestsPerMinute {
		return false, "rate_limit_exceeded"
	}
	
	// Check daily cost budget
	projectedCost := tracker.EstimatedCostUSD + tracker.CostPerRequestUSD
	totalProjectedCost := cg.calculateTotalCost() + tracker.CostPerRequestUSD
	
	if totalProjectedCost > cg.dailyCostLimitUSD {
		return false, "daily_cost_budget_exceeded"
	}
	
	// Check individual provider limits if configured
	if tracker.DailyLimitUSD > 0 && projectedCost > tracker.DailyLimitUSD {
		return false, "provider_daily_limit_exceeded"
	}
	
	// Check warning thresholds
	budgetUsedPct := totalProjectedCost / cg.dailyCostLimitUSD
	if budgetUsedPct > cg.warningThreshold && time.Since(tracker.LastWarning) > time.Hour {
		tracker.LastWarning = time.Now()
		
		observ.Log("cost_governance_warning", map[string]any{
			"provider":           provider,
			"budget_used_pct":    budgetUsedPct * 100,
			"total_cost_usd":     totalProjectedCost,
			"daily_limit_usd":    cg.dailyCostLimitUSD,
			"warning_threshold":  cg.warningThreshold * 100,
		})
	}
	
	return true, ""
}

// RecordRequest records a successful request and updates costs
func (cg *CostGovernor) RecordRequest(provider string) {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	
	tracker, exists := cg.providers[provider]
	if !exists {
		return
	}
	
	tracker.RequestsToday++
	tracker.RequestsThisMinute++
	tracker.EstimatedCostUSD += tracker.CostPerRequestUSD
	tracker.LastRequest = time.Now()
	
	// Emit cost metrics
	observ.SetGauge("provider_cost_estimate_usd", tracker.EstimatedCostUSD, map[string]string{"provider": provider})
	observ.SetGauge("provider_requests_today", float64(tracker.RequestsToday), map[string]string{"provider": provider})
	observ.SetGauge("provider_budget_remaining", cg.dailyCostLimitUSD - cg.calculateTotalCost(), nil)
}

// GetCostSummary returns comprehensive cost information
func (cg *CostGovernor) GetCostSummary() *CostSummary {
	cg.mu.RLock()
	defer cg.mu.RUnlock()
	
	totalCost := cg.calculateTotalCost()
	remainingBudget := cg.dailyCostLimitUSD - totalCost
	budgetUsedPct := totalCost / cg.dailyCostLimitUSD
	
	// Calculate projections based on current usage rate
	hoursElapsed := time.Since(time.Now().Truncate(24 * time.Hour)).Hours()
	if hoursElapsed > 0 {
		projectedDailyCost := totalCost * (24.0 / hoursElapsed)
		projectedOverage := 0.0
		if projectedDailyCost > cg.dailyCostLimitUSD {
			projectedOverage = projectedDailyCost - cg.dailyCostLimitUSD
		}
		
		// Find providers at limit
		var providersAtLimit []string
		for name, tracker := range cg.providers {
			if tracker.RequestsThisMinute >= tracker.RequestsPerMinute {
				providersAtLimit = append(providersAtLimit, name)
			}
		}
		
		// Copy provider trackers to prevent mutation
		providersCopy := make(map[string]*ProviderCostTracker)
		for name, tracker := range cg.providers {
			providersCopy[name] = &ProviderCostTracker{
				Provider:           tracker.Provider,
				RequestsToday:      tracker.RequestsToday,
				CostPerRequestUSD:  tracker.CostPerRequestUSD,
				EstimatedCostUSD:   tracker.EstimatedCostUSD,
				DailyLimitUSD:      tracker.DailyLimitUSD,
				LastRequest:        tracker.LastRequest,
				RequestsPerMinute:  tracker.RequestsPerMinute,
				RequestsThisMinute: tracker.RequestsThisMinute,
				MinuteResetTime:    tracker.MinuteResetTime,
				LastWarning:        tracker.LastWarning,
			}
		}
		
		return &CostSummary{
			TotalCostUSD:       totalCost,
			DailyLimitUSD:      cg.dailyCostLimitUSD,
			RemainingBudgetUSD: remainingBudget,
			BudgetUsedPct:      budgetUsedPct,
			ProvidersAtLimit:   providersAtLimit,
			CostByProvider:     providersCopy,
			ResetTime:          cg.resetTime,
			ProjectedDailyCost: projectedDailyCost,
			ProjectedOverage:   projectedOverage,
		}
	}
	
	return &CostSummary{
		TotalCostUSD:       totalCost,
		DailyLimitUSD:      cg.dailyCostLimitUSD,
		RemainingBudgetUSD: remainingBudget,
		BudgetUsedPct:      budgetUsedPct,
		CostByProvider:     make(map[string]*ProviderCostTracker),
		ResetTime:          cg.resetTime,
	}
}

// UpdateProviderLimit updates the daily limit for a provider
func (cg *CostGovernor) UpdateProviderLimit(provider string, dailyLimitUSD float64) {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	
	if tracker, exists := cg.providers[provider]; exists {
		tracker.DailyLimitUSD = dailyLimitUSD
		
		observ.Log("cost_governance_provider_limit_updated", map[string]any{
			"provider":         provider,
			"daily_limit_usd":  dailyLimitUSD,
		})
	}
}

// GetProviderBudgetStatus returns budget status for a specific provider
func (cg *CostGovernor) GetProviderBudgetStatus(provider string) (used, limit float64, remainingPct float64, found bool) {
	cg.mu.RLock()
	defer cg.mu.RUnlock()
	
	tracker, exists := cg.providers[provider]
	if !exists {
		return 0, 0, 0, false
	}
	
	if tracker.DailyLimitUSD > 0 {
		remainingPct = (tracker.DailyLimitUSD - tracker.EstimatedCostUSD) / tracker.DailyLimitUSD
		return tracker.EstimatedCostUSD, tracker.DailyLimitUSD, remainingPct, true
	}
	
	// Use overall limit if no provider-specific limit
	remainingPct = (cg.dailyCostLimitUSD - cg.calculateTotalCost()) / cg.dailyCostLimitUSD
	return tracker.EstimatedCostUSD, cg.dailyCostLimitUSD, remainingPct, true
}

// ForceReset forces a reset of daily budgets (for testing/debugging)
func (cg *CostGovernor) ForceReset() {
	cg.mu.Lock()
	defer cg.mu.Unlock()
	
	cg.resetDailyBudget()
	
	observ.Log("cost_governance_force_reset", map[string]any{
		"reset_time": cg.resetTime.Format(time.RFC3339),
	})
}

// Helper methods

// calculateTotalCost calculates total cost across all providers
func (cg *CostGovernor) calculateTotalCost() float64 {
	var total float64
	for _, tracker := range cg.providers {
		total += tracker.EstimatedCostUSD
	}
	return total
}

// resetDailyBudget resets all daily counters
func (cg *CostGovernor) resetDailyBudget() {
	for _, tracker := range cg.providers {
		tracker.RequestsToday = 0
		tracker.EstimatedCostUSD = 0.0
	}
	cg.resetTime = getNextMidnightUTC()
	
	observ.Log("cost_governance_daily_reset", map[string]any{
		"next_reset": cg.resetTime.Format(time.RFC3339),
	})
}

// getNextMidnightUTC returns the next midnight in UTC
func getNextMidnightUTC() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

// AdaptiveCadenceManager manages request timing based on cost and priority
type AdaptiveCadenceManager struct {
	mu                sync.RWMutex
	costGovernor      *CostGovernor
	baseCadenceMs     map[string]int // tier -> base cadence in ms
	currentCadenceMs  map[string]int // tier -> current cadence in ms
	budgetThresholds  []BudgetThreshold
}

// BudgetThreshold defines cadence adjustments based on budget usage
type BudgetThreshold struct {
	BudgetUsedPct    float64 // Percentage of budget used
	CadenceMultiplier float64 // Multiplier for base cadence (>1 = slower)
}

// NewAdaptiveCadenceManager creates a new adaptive cadence manager
func NewAdaptiveCadenceManager(costGovernor *CostGovernor, baseCadence map[string]int) *AdaptiveCadenceManager {
	return &AdaptiveCadenceManager{
		costGovernor:     costGovernor,
		baseCadenceMs:    baseCadence,
		currentCadenceMs: make(map[string]int),
		budgetThresholds: []BudgetThreshold{
			{BudgetUsedPct: 0.90, CadenceMultiplier: 3.0}, // 90% used -> 3x slower
			{BudgetUsedPct: 0.80, CadenceMultiplier: 2.0}, // 80% used -> 2x slower  
			{BudgetUsedPct: 0.70, CadenceMultiplier: 1.5}, // 70% used -> 1.5x slower
			{BudgetUsedPct: 0.50, CadenceMultiplier: 1.0}, // 50% used -> normal
		},
	}
}

// GetCurrentCadence returns the current cadence for a tier
func (acm *AdaptiveCadenceManager) GetCurrentCadence(tier string) int {
	acm.mu.RLock()
	defer acm.mu.RUnlock()
	
	// Update cadence based on current budget usage
	acm.updateCadence()
	
	if cadence, exists := acm.currentCadenceMs[tier]; exists {
		return cadence
	}
	
	// Return base cadence if not found
	if baseCadence, exists := acm.baseCadenceMs[tier]; exists {
		return baseCadence
	}
	
	return 5000 // Default 5 second cadence
}

// updateCadence updates cadence based on current budget usage
func (acm *AdaptiveCadenceManager) updateCadence() {
	summary := acm.costGovernor.GetCostSummary()
	budgetUsedPct := summary.BudgetUsedPct
	
	// Find appropriate multiplier
	multiplier := 1.0
	for _, threshold := range acm.budgetThresholds {
		if budgetUsedPct >= threshold.BudgetUsedPct {
			multiplier = threshold.CadenceMultiplier
			break
		}
	}
	
	// Apply multiplier to all tiers
	for tier, baseCadence := range acm.baseCadenceMs {
		newCadence := int(float64(baseCadence) * multiplier)
		if acm.currentCadenceMs[tier] != newCadence {
			acm.currentCadenceMs[tier] = newCadence
			
			observ.Log("adaptive_cadence_updated", map[string]any{
				"tier":             tier,
				"base_cadence_ms":  baseCadence,
				"new_cadence_ms":   newCadence,
				"multiplier":       multiplier,
				"budget_used_pct":  budgetUsedPct * 100,
			})
		}
	}
}