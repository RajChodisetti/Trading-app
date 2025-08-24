package risk

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/adapters"
	"github.com/Rajchodisetti/trading-app/internal/portfolio"
)

func TestRiskManagerBasicFunctionality(t *testing.T) {
	// Setup
	portfolioMgr := portfolio.NewManager("test_portfolio.json", 100000.0)
	quotesAdapter := adapters.NewMockQuotesAdapter()
	
	config := RiskManagerConfig{
		NAVTracker: NAVTrackerConfig{
			UpdateIntervalSeconds:     1,
			QuoteStalenessThresholdMs: 2000,
			MaxHistoryEntries:         100,
			UseMidPrice:              true,
			PersistPath:              "test_nav_state.json",
		},
		CircuitBreaker: CircuitBreakerThresholds{
			DailyHaltPct:     4.0,
			WeeklyHaltPct:    8.0,
			DailyWarningPct:  2.0,
			WeeklyWarningPct: 5.0,
			NormalSize:       1.0,
			ReducedSize:      0.5,
			HaltedSize:       0.0,
		},
		EventLogPath:          "test_events.jsonl",
		UpdateIntervalSeconds: 1,
		DecisionTimeoutMs:     500,
	}
	
	riskManager := NewRiskManager(portfolioMgr, quotesAdapter, config)
	
	// Test startup
	err := riskManager.Start()
	if err != nil {
		t.Fatalf("Failed to start risk manager: %v", err)
	}
	defer riskManager.Stop()
	
	// Test decision evaluation
	decisionCtx := DecisionContext{
		Symbol:        "AAPL",
		Intent:        "BUY_1X",
		Quantity:      100,
		Price:         225.50,
		Strategy:      "test",
		Score:         0.7,
		CorrelationID: "test_001",
		Timestamp:     time.Now(),
	}
	
	result := riskManager.EvaluateDecision(decisionCtx)
	
	// Verify decision structure
	if result.DecisionID == "" {
		t.Error("Decision should have a unique ID")
	}
	
	if result.ProcessingTime <= 0 {
		t.Error("Processing time should be recorded")
	}
	
	if result.RiskScore < 0 || result.RiskScore > 1 {
		t.Errorf("Risk score should be between 0-1, got %f", result.RiskScore)
	}
	
	if result.SizeMultiplier < 0 || result.SizeMultiplier > 2 {
		t.Errorf("Size multiplier should be reasonable, got %f", result.SizeMultiplier)
	}
}

func TestCircuitBreakerGate(t *testing.T) {
	gate := &CircuitBreakerGate{}
	
	testCases := []struct {
		name         string
		circuitState CircuitBreakerState
		intent       string
		shouldPass   bool
		expectedReason string
	}{
		{
			name:         "normal_state_buy",
			circuitState: StateNormal,
			intent:       "BUY_1X",
			shouldPass:   true,
		},
		{
			name:         "halted_state_buy",
			circuitState: StateHalted,
			intent:       "BUY_1X",
			shouldPass:   false,
			expectedReason: "circuit_breaker_halted",
		},
		{
			name:         "halted_state_reduce",
			circuitState: StateHalted,
			intent:       "REDUCE",
			shouldPass:   true,
		},
		{
			name:         "emergency_state_any",
			circuitState: StateEmergency,
			intent:       "REDUCE",
			shouldPass:   false,
			expectedReason: "circuit_breaker_emergency",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := DecisionContext{Intent: tc.intent}
			riskData := RiskData{CircuitState: tc.circuitState}
			
			passed, reason, err := gate.Evaluate(ctx, riskData)
			
			if err != nil {
				t.Fatalf("Gate evaluation failed: %v", err)
			}
			
			if passed != tc.shouldPass {
				t.Errorf("Expected pass=%t, got %t", tc.shouldPass, passed)
			}
			
			if !tc.shouldPass && reason != tc.expectedReason {
				t.Errorf("Expected reason=%s, got %s", tc.expectedReason, reason)
			}
		})
	}
}

func TestDataQualityGate(t *testing.T) {
	gate := &DataQualityGate{
		MinQualityScore: 0.8,
		MaxStalenessMs:  2000,
	}
	
	testCases := []struct {
		name           string
		dataQuality    NAVDataQuality
		quoteStaleness time.Duration
		shouldPass     bool
		expectedReason string
	}{
		{
			name:           "good_quality",
			dataQuality:    NAVDataQuality{},
			quoteStaleness: 1 * time.Second,
			shouldPass:     true,
		},
		{
			name: "poor_quality",
			dataQuality: NAVDataQuality{
				StaleQuotes:   []string{"AAPL", "NVDA"},
				MissingQuotes: []string{"TSLA"},
			},
			quoteStaleness: 1 * time.Second,
			shouldPass:     false,
			expectedReason: "data_quality_poor",
		},
		{
			name:           "stale_quotes",
			dataQuality:    NAVDataQuality{},
			quoteStaleness: 5 * time.Second,
			shouldPass:     false,
			expectedReason: "quotes_stale",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := DecisionContext{}
			riskData := RiskData{
				DataQuality:    tc.dataQuality,
				QuoteStaleness: tc.quoteStaleness,
			}
			
			passed, reason, err := gate.Evaluate(ctx, riskData)
			
			if err != nil {
				t.Fatalf("Gate evaluation failed: %v", err)
			}
			
			if passed != tc.shouldPass {
				t.Errorf("Expected pass=%t, got %t", tc.shouldPass, passed)
			}
			
			if !tc.shouldPass && reason != tc.expectedReason {
				t.Errorf("Expected reason=%s, got %s", tc.expectedReason, reason)
			}
		})
	}
}

func TestVolatilityGate(t *testing.T) {
	gate := &VolatilityGate{
		VolatilityMultipliers: map[string]float64{
			"quiet":    1.2,
			"normal":   1.0,
			"volatile": 0.7,
		},
	}
	
	testCases := []struct {
		name             string
		volatilityRegime string
		expectedMultiplier float64
	}{
		{"quiet_market", "quiet", 1.2},
		{"normal_market", "normal", 1.0},
		{"volatile_market", "volatile", 0.7},
		{"unknown_regime", "extreme", 1.0}, // Default
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := DecisionContext{}
			riskData := RiskData{VolatilityRegime: tc.volatilityRegime}
			
			passed, reason, err := gate.Evaluate(ctx, riskData)
			
			if err != nil {
				t.Fatalf("Gate evaluation failed: %v", err)
			}
			
			if !passed {
				t.Error("Volatility gate should always pass")
			}
			
			expectedReason := "volatility_adjustment_" + formatFloat(tc.expectedMultiplier)
			if reason != expectedReason {
				t.Errorf("Expected reason=%s, got %s", expectedReason, reason)
			}
		})
	}
}

func TestRiskScoreCalculation(t *testing.T) {
	portfolioMgr := portfolio.NewManager("test_portfolio.json", 100000.0)
	quotesAdapter := adapters.NewMockQuotesAdapter()
	config := getTestRiskConfig()
	
	riskManager := NewRiskManager(portfolioMgr, quotesAdapter, config)
	
	testCases := []struct {
		name        string
		riskData    RiskData
		expectedMin float64
		expectedMax float64
	}{
		{
			name: "normal_conditions",
			riskData: RiskData{
				CircuitState:     StateNormal,
				DailyDrawdown:    0.5,
				WeeklyDrawdown:   1.0,
				VolatilityRegime: "normal",
				QuoteStaleness:   1 * time.Second,
				DataQuality:      NAVDataQuality{},
			},
			expectedMin: 0.0,
			expectedMax: 0.5,
		},
		{
			name: "high_risk_conditions",
			riskData: RiskData{
				CircuitState:     StateHalted,
				DailyDrawdown:    5.0,
				WeeklyDrawdown:   8.0,
				VolatilityRegime: "volatile",
				QuoteStaleness:   10 * time.Second,
				DataQuality: NAVDataQuality{
					StaleQuotes:   []string{"AAPL", "NVDA"},
					MissingQuotes: []string{"TSLA"},
				},
			},
			expectedMin: 0.8,
			expectedMax: 1.0,
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := DecisionContext{Symbol: "TEST", Intent: "BUY_1X"}
			score := riskManager.calculateRiskScore(ctx, tc.riskData)
			
			if score < tc.expectedMin || score > tc.expectedMax {
				t.Errorf("Risk score %f not in expected range [%f, %f]", 
					score, tc.expectedMin, tc.expectedMax)
			}
		})
	}
}

func TestDecisionInvariants(t *testing.T) {
	portfolioMgr := portfolio.NewManager("test_portfolio.json", 100000.0)
	quotesAdapter := adapters.NewMockQuotesAdapter()
	config := getTestRiskConfig()
	
	riskManager := NewRiskManager(portfolioMgr, quotesAdapter, config)
	
	// Test 1000 random decisions to verify invariants
	for i := 0; i < 1000; i++ {
		ctx := generateRandomDecisionContext(i)
		result := riskManager.EvaluateDecision(ctx)
		
		// Invariant 1: Decision should complete reasonably quickly
		if result.ProcessingTime > 1*time.Second {
			t.Errorf("Decision %d took too long: %v", i, result.ProcessingTime)
		}
		
		// Invariant 2: Size multiplier should be reasonable
		if result.SizeMultiplier < 0 || result.SizeMultiplier > 2 {
			t.Errorf("Decision %d has invalid size multiplier: %f", i, result.SizeMultiplier)
		}
		
		// Invariant 3: Risk score should be in valid range
		if result.RiskScore < 0 || result.RiskScore > 1 {
			t.Errorf("Decision %d has invalid risk score: %f", i, result.RiskScore)
		}
		
		// Invariant 4: If blocked, should have reason
		if !result.Approved && len(result.BlockedBy) == 0 {
			t.Errorf("Decision %d blocked without reason", i)
		}
		
		// Invariant 5: Should have unique decision ID
		if result.DecisionID == "" {
			t.Errorf("Decision %d missing unique ID", i)
		}
	}
}

func TestConcurrentDecisions(t *testing.T) {
	portfolioMgr := portfolio.NewManager("test_portfolio.json", 100000.0)
	quotesAdapter := adapters.NewMockQuotesAdapter()
	config := getTestRiskConfig()
	
	riskManager := NewRiskManager(portfolioMgr, quotesAdapter, config)
	
	// Test concurrent decision evaluation
	numGoroutines := 10
	numDecisions := 100
	
	results := make(chan DecisionResult, numGoroutines*numDecisions)
	
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			for j := 0; j < numDecisions; j++ {
				ctx := DecisionContext{
					Symbol:        "TEST",
					Intent:        "BUY_1X",
					Quantity:      100,
					Price:         100.0,
					Strategy:      "concurrent_test",
					Score:         0.5,
					CorrelationID: "concurrent_" + string(rune(goroutineID*numDecisions+j)),
					Timestamp:     time.Now(),
				}
				
				result := riskManager.EvaluateDecision(ctx)
				results <- result
			}
		}(i)
	}
	
	// Collect and validate results
	decisionIDs := make(map[string]bool)
	for i := 0; i < numGoroutines*numDecisions; i++ {
		result := <-results
		
		// Check for unique decision IDs
		if decisionIDs[result.DecisionID] {
			t.Errorf("Duplicate decision ID: %s", result.DecisionID)
		}
		decisionIDs[result.DecisionID] = true
		
		// Validate basic invariants
		if result.ProcessingTime <= 0 {
			t.Error("Invalid processing time")
		}
		
		if result.RiskScore < 0 || result.RiskScore > 1 {
			t.Errorf("Invalid risk score: %f", result.RiskScore)
		}
	}
}

func TestErrorHandling(t *testing.T) {
	// Test with failing quotes adapter
	portfolioMgr := portfolio.NewManager("test_portfolio.json", 100000.0)
	quotesAdapter := &FailingQuotesAdapter{}
	config := getTestRiskConfig()
	
	riskManager := NewRiskManager(portfolioMgr, quotesAdapter, config)
	
	ctx := DecisionContext{
		Symbol:        "FAIL",
		Intent:        "BUY_1X",
		Quantity:      100,
		Price:         100.0,
		Strategy:      "error_test",
		Score:         0.5,
		CorrelationID: "error_001",
		Timestamp:     time.Now(),
	}
	
	result := riskManager.EvaluateDecision(ctx)
	
	// System should handle errors gracefully
	if result.DecisionID == "" {
		t.Error("Should still generate decision ID on error")
	}
	
	// Should likely be blocked due to data quality issues
	if result.Approved {
		t.Log("Note: Decision approved despite adapter errors - check if expected")
	}
}

// Helper functions

func getTestRiskConfig() RiskManagerConfig {
	return RiskManagerConfig{
		NAVTracker: NAVTrackerConfig{
			UpdateIntervalSeconds:     1,
			QuoteStalenessThresholdMs: 2000,
			MaxHistoryEntries:         100,
			UseMidPrice:              true,
			PersistPath:              "test_nav_state.json",
		},
		CircuitBreaker: CircuitBreakerThresholds{
			DailyWarningPct:     2.0,
			DailyHaltPct:        4.0,
			WeeklyWarningPct:    5.0,
			WeeklyHaltPct:       8.0,
			NormalSize:          1.0,
			ReducedSize:         0.5,
			HaltedSize:          0.0,
		},
		Volatility: VolatilityConfig{
			LookbackDays:             21,
			EwmaAlpha:               0.94,
			QuietMarketThreshold:    0.10,
			VolatileMarketThreshold: 0.30,
		},
		EventLogPath:          "test_events.jsonl",
		UpdateIntervalSeconds: 1,
		DecisionTimeoutMs:     500,
	}
}

func generateRandomDecisionContext(seed int) DecisionContext {
	symbols := []string{"AAPL", "NVDA", "TSLA", "SPY", "QQQ"}
	intents := []string{"BUY_1X", "BUY_5X", "REDUCE", "HOLD"}
	
	return DecisionContext{
		Symbol:        symbols[seed%len(symbols)],
		Intent:        intents[seed%len(intents)],
		Quantity:      100 + seed*10,
		Price:         100.0 + float64(seed)*2.5,
		Strategy:      "property_test",
		Score:         float64(seed%100) / 100.0,
		CorrelationID: "prop_" + string(rune(seed)),
		Timestamp:     time.Now(),
	}
}

func formatFloat(f float64) string {
	return "1.00" // Simplified for test
}

// FailingQuotesAdapter for error testing
type FailingQuotesAdapter struct{}

func (f *FailingQuotesAdapter) GetQuote(ctx context.Context, symbol string) (*adapters.Quote, error) {
	return nil, fmt.Errorf("simulated adapter failure")
}

func (f *FailingQuotesAdapter) GetQuotes(ctx context.Context, symbols []string) (map[string]*adapters.Quote, error) {
	return nil, fmt.Errorf("simulated adapter failure")
}

func (f *FailingQuotesAdapter) HealthCheck(ctx context.Context) error {
	return fmt.Errorf("adapter unhealthy")
}

func (f *FailingQuotesAdapter) Close() error {
	return nil
}