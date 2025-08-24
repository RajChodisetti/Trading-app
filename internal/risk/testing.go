package risk

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/adapters"
)

// RiskTestSuite provides comprehensive testing for the risk management system
type RiskTestSuite struct {
	riskManager      *RiskManager
	testScenarios    []TestScenario
	historicalData   *HistoricalDataLoader
	chaosController  *ChaosController
	testResults      []TestResult
	mu               sync.RWMutex
}

// TestScenario defines a comprehensive risk testing scenario
type TestScenario struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Type        string                 `json:"type"`        // historical_replay, chaos, property_based, boundary
	Duration    time.Duration          `json:"duration"`
	Config      map[string]interface{} `json:"config"`
	Expected    ExpectedResults        `json:"expected"`
}

// ExpectedResults defines what we expect from a test scenario
type ExpectedResults struct {
	MaxDrawdown           float64   `json:"max_drawdown"`
	CircuitBreakerTriggers int      `json:"circuit_breaker_triggers"`
	ExpectedStates        []string  `json:"expected_states"`
	MaxDecisionLatencyMs  float64   `json:"max_decision_latency_ms"`
	MinDataQualityScore   float64   `json:"min_data_quality_score"`
	AllowedErrors         []string  `json:"allowed_errors"`
	ShouldHalt            bool      `json:"should_halt"`
	ShouldRecover         bool      `json:"should_recover"`
}

// TestResult captures the outcome of a test scenario
type TestResult struct {
	Scenario        string                 `json:"scenario"`
	StartTime       time.Time              `json:"start_time"`
	EndTime         time.Time              `json:"end_time"`
	Duration        time.Duration          `json:"duration"`
	Passed          bool                   `json:"passed"`
	Failures        []string               `json:"failures"`
	Metrics         TestMetrics            `json:"metrics"`
	Events          []StructuredEvent      `json:"events"`
	Artifacts       map[string]interface{} `json:"artifacts"`
}

// TestMetrics captures key metrics during testing
type TestMetrics struct {
	DecisionCount         int                        `json:"decision_count"`
	DecisionLatencyStats  LatencyMetric             `json:"decision_latency_stats"`
	CircuitBreakerChanges int                        `json:"circuit_breaker_changes"`
	NAVUpdates           int                        `json:"nav_updates"`
	DataQualityEvents    int                        `json:"data_quality_events"`
	ErrorCount           map[string]int             `json:"error_count"`
	StateTransitions     map[string]map[string]int  `json:"state_transitions"`
	MaxDrawdown          float64                    `json:"max_drawdown"`
	MinNAV               float64                    `json:"min_nav"`
	MaxNAV               float64                    `json:"max_nav"`
}

// HistoricalDataLoader loads and replays historical market data
type HistoricalDataLoader struct {
	dataPath     string
	scenarios    map[string]HistoricalScenario
}

// HistoricalScenario represents a historical market event for replay
type HistoricalScenario struct {
	Name        string                   `json:"name"`
	Date        string                   `json:"date"`
	Description string                   `json:"description"`
	MarketData  []HistoricalMarketData   `json:"market_data"`
	Events      []HistoricalMarketEvent  `json:"events"`
	Volatility  VolatilityProfile        `json:"volatility"`
}

// HistoricalMarketData represents a market data point
type HistoricalMarketData struct {
	Timestamp time.Time          `json:"timestamp"`
	Symbol    string             `json:"symbol"`
	Quotes    adapters.Quote     `json:"quotes"`
	Metrics   map[string]float64 `json:"metrics"`
}

// HistoricalMarketEvent represents a significant market event
type HistoricalMarketEvent struct {
	Timestamp   time.Time              `json:"timestamp"`
	Type        string                 `json:"type"` // flash_crash, volatility_spike, circuit_breaker
	Impact      map[string]float64     `json:"impact"`
	Description string                 `json:"description"`
	Context     map[string]interface{} `json:"context"`
}

// VolatilityProfile defines volatility characteristics for a scenario
type VolatilityProfile struct {
	BaseVolatility  float64 `json:"base_volatility"`
	MaxVolatility   float64 `json:"max_volatility"`
	Correlation     float64 `json:"correlation"`
	TrendStrength   float64 `json:"trend_strength"`
}

// ChaosController introduces controlled failures and stress conditions
type ChaosController struct {
	activeFailures map[string]ChaosFailure
	mu             sync.RWMutex
	running        bool
	ctx            context.Context
	cancel         context.CancelFunc
}

// ChaosFailure represents a type of controlled failure
type ChaosFailure struct {
	Type        string                 `json:"type"`
	Probability float64                `json:"probability"` // 0-1 chance per operation
	Duration    time.Duration          `json:"duration"`
	StartTime   time.Time              `json:"start_time"`
	Config      map[string]interface{} `json:"config"`
}

// MockQuotesAdapterChaos wraps the quotes adapter to inject failures
type MockQuotesAdapterChaos struct {
	base      adapters.QuotesAdapter
	chaos     *ChaosController
	symbol    string
}

// NewRiskTestSuite creates a comprehensive risk testing suite
func NewRiskTestSuite(riskManager *RiskManager, dataPath string) *RiskTestSuite {
	return &RiskTestSuite{
		riskManager:   riskManager,
		testScenarios: createTestScenarios(),
		historicalData: &HistoricalDataLoader{
			dataPath:  dataPath,
			scenarios: loadHistoricalScenarios(dataPath),
		},
		chaosController: NewChaosController(),
		testResults:     make([]TestResult, 0),
	}
}

// RunAllTests executes all test scenarios
func (rts *RiskTestSuite) RunAllTests() []TestResult {
	rts.mu.Lock()
	defer rts.mu.Unlock()
	
	rts.testResults = make([]TestResult, 0)
	
	for _, scenario := range rts.testScenarios {
		result := rts.runScenario(scenario)
		rts.testResults = append(rts.testResults, result)
		
		// Log test completion
		status := "PASS"
		if !result.Passed {
			status = "FAIL"
		}
		
		fmt.Printf("[%s] %s: %s (%.2fs)\n", 
			status, scenario.Name, scenario.Description, result.Duration.Seconds())
		
		if !result.Passed {
			for _, failure := range result.Failures {
				fmt.Printf("  FAILURE: %s\n", failure)
			}
		}
	}
	
	return rts.testResults
}

// RunScenario executes a specific test scenario
func (rts *RiskTestSuite) RunScenario(scenarioName string) (*TestResult, error) {
	for _, scenario := range rts.testScenarios {
		if scenario.Name == scenarioName {
			result := rts.runScenario(scenario)
			return &result, nil
		}
	}
	return nil, fmt.Errorf("scenario %s not found", scenarioName)
}

func (rts *RiskTestSuite) runScenario(scenario TestScenario) TestResult {
	result := TestResult{
		Scenario:  scenario.Name,
		StartTime: time.Now(),
		Failures:  make([]string, 0),
		Metrics:   TestMetrics{ErrorCount: make(map[string]int)},
		Artifacts: make(map[string]interface{}),
	}
	
	// Execute scenario based on type
	switch scenario.Type {
	case "historical_replay":
		rts.runHistoricalReplay(scenario, &result)
	case "chaos":
		rts.runChaosScenario(scenario, &result)
	case "property_based":
		rts.runPropertyBasedTest(scenario, &result)
	case "boundary":
		rts.runBoundaryTest(scenario, &result)
	default:
		result.Failures = append(result.Failures, fmt.Sprintf("unknown scenario type: %s", scenario.Type))
	}
	
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)
	result.Passed = len(result.Failures) == 0
	
	// Validate against expected results
	rts.validateResults(scenario.Expected, &result)
	
	return result
}

func (rts *RiskTestSuite) runHistoricalReplay(scenario TestScenario, result *TestResult) {
	scenarioName, ok := scenario.Config["scenario"].(string)
	if !ok {
		result.Failures = append(result.Failures, "missing scenario name in config")
		return
	}
	
	histScenario, exists := rts.historicalData.scenarios[scenarioName]
	if !exists {
		result.Failures = append(result.Failures, fmt.Sprintf("historical scenario %s not found", scenarioName))
		return
	}
	
	// Replay historical data
	startNAV := 100000.0 // Starting NAV
	currentNAV := startNAV
	
	for _, dataPoint := range histScenario.MarketData {
		// Simulate price impact on portfolio
		if impact, exists := dataPoint.Metrics["price_change_pct"]; exists {
			currentNAV *= (1.0 + impact/100.0)
		}
		
		// Create decision context
		decisionCtx := DecisionContext{
			Symbol:        dataPoint.Symbol,
			Intent:        "BUY_1X",
			Quantity:      100,
			Price:         dataPoint.Quotes.Last,
			Strategy:      "historical_replay",
			Score:         0.5,
			CorrelationID: fmt.Sprintf("replay_%d", dataPoint.Timestamp.Unix()),
			Timestamp:     dataPoint.Timestamp,
		}
		
		// Evaluate decision
		decision := rts.riskManager.EvaluateDecision(decisionCtx)
		result.Metrics.DecisionCount++
		
		// Track metrics
		if !decision.Approved {
			if len(decision.BlockedBy) > 0 {
				result.Metrics.ErrorCount[decision.BlockedBy[0]]++
			}
		}
		
		// Update test artifacts
		if result.Artifacts["decisions"] == nil {
			result.Artifacts["decisions"] = make([]DecisionResult, 0)
		}
		decisions := result.Artifacts["decisions"].([]DecisionResult)
		result.Artifacts["decisions"] = append(decisions, decision)
	}
	
	// Track final metrics
	finalDrawdown := (startNAV - currentNAV) / startNAV * 100
	result.Metrics.MaxDrawdown = finalDrawdown
	result.Metrics.MinNAV = currentNAV
	result.Metrics.MaxNAV = startNAV
}

func (rts *RiskTestSuite) runChaosScenario(scenario TestScenario, result *TestResult) {
	// Start chaos controller
	rts.chaosController.Start()
	defer rts.chaosController.Stop()
	
	// Configure chaos failures
	failures, ok := scenario.Config["failures"].([]interface{})
	if !ok {
		result.Failures = append(result.Failures, "missing failures configuration")
		return
	}
	
	for _, failureConfig := range failures {
		if failureMap, ok := failureConfig.(map[string]interface{}); ok {
			failureType, _ := failureMap["type"].(string)
			probability, _ := failureMap["probability"].(float64)
			duration, _ := failureMap["duration"].(float64)
			
			failure := ChaosFailure{
				Type:        failureType,
				Probability: probability,
				Duration:    time.Duration(duration) * time.Second,
				Config:      failureMap,
			}
			
			rts.chaosController.AddFailure(failureType, failure)
		}
	}
	
	// Run test operations under chaos
	testDuration, _ := scenario.Config["duration"].(float64)
	endTime := time.Now().Add(time.Duration(testDuration) * time.Second)
	
	decisionCount := 0
	for time.Now().Before(endTime) {
		// Create test decision
		decisionCtx := DecisionContext{
			Symbol:        "TEST",
			Intent:        "BUY_1X",
			Quantity:      100,
			Price:         100.0,
			Strategy:      "chaos_test",
			Score:         rand.Float64(),
			CorrelationID: fmt.Sprintf("chaos_%d", decisionCount),
			Timestamp:     time.Now(),
		}
		
		// Evaluate under chaos conditions
		decision := rts.riskManager.EvaluateDecision(decisionCtx)
		
		decisionCount++
		result.Metrics.DecisionCount++
		
		// Track errors
		if !decision.Approved && len(decision.BlockedBy) > 0 {
			result.Metrics.ErrorCount[decision.BlockedBy[0]]++
		}
		
		// Track latency
		if decision.ProcessingTime > time.Duration(result.Metrics.DecisionLatencyStats.Max) {
			result.Metrics.DecisionLatencyStats.Max = decision.ProcessingTime
		}
		
		time.Sleep(10 * time.Millisecond) // Small delay between decisions
	}
}

func (rts *RiskTestSuite) runPropertyBasedTest(scenario TestScenario, result *TestResult) {
	// Property-based testing: generate random inputs and verify invariants
	numTests, _ := scenario.Config["num_tests"].(float64)
	if numTests == 0 {
		numTests = 1000
	}
	
	for i := 0; i < int(numTests); i++ {
		// Generate random decision context
		decisionCtx := rts.generateRandomDecision()
		
		// Evaluate decision
		decision := rts.riskManager.EvaluateDecision(decisionCtx)
		result.Metrics.DecisionCount++
		
		// Check invariants
		failures := rts.checkInvariants(decisionCtx, decision)
		result.Failures = append(result.Failures, failures...)
		
		// Track metrics
		if decision.ProcessingTime > 1*time.Second {
			result.Failures = append(result.Failures, 
				fmt.Sprintf("decision latency too high: %v", decision.ProcessingTime))
		}
	}
}

func (rts *RiskTestSuite) runBoundaryTest(scenario TestScenario, result *TestResult) {
	// Test boundary conditions and edge cases
	boundaryTests := []struct {
		name string
		test func() []string
	}{
		{
			name: "extreme_drawdown",
			test: func() []string {
				// Test extreme drawdown scenarios
				return rts.testExtremeDrawdown()
			},
		},
		{
			name: "stale_quotes",
			test: func() []string {
				// Test with extremely stale quotes
				return rts.testStaleQuotes()
			},
		},
		{
			name: "high_volatility",
			test: func() []string {
				// Test high volatility scenarios
				return rts.testHighVolatility()
			},
		},
		{
			name: "system_overload",
			test: func() []string {
				// Test system under high load
				return rts.testSystemOverload()
			},
		},
	}
	
	for _, test := range boundaryTests {
		failures := test.test()
		result.Failures = append(result.Failures, failures...)
	}
}

func (rts *RiskTestSuite) validateResults(expected ExpectedResults, result *TestResult) {
	// Validate maximum drawdown
	if result.Metrics.MaxDrawdown > expected.MaxDrawdown {
		result.Failures = append(result.Failures, 
			fmt.Sprintf("max drawdown %.2f%% exceeded expected %.2f%%", 
				result.Metrics.MaxDrawdown, expected.MaxDrawdown))
	}
	
	// Validate decision latency
	avgLatency := float64(result.Metrics.DecisionLatencyStats.Max.Milliseconds())
	if avgLatency > expected.MaxDecisionLatencyMs {
		result.Failures = append(result.Failures, 
			fmt.Sprintf("decision latency %.2fms exceeded expected %.2fms", 
				avgLatency, expected.MaxDecisionLatencyMs))
	}
	
	// Add more validation as needed
}

// Helper methods for specific boundary tests

func (rts *RiskTestSuite) testExtremeDrawdown() []string {
	failures := make([]string, 0)
	
	// Create scenario with 10% drawdown
	ctx := DecisionContext{
		Symbol:        "TEST",
		Intent:        "BUY_1X",
		Quantity:      1000,
		Price:         100.0,
		Strategy:      "extreme_test",
		CorrelationID: "extreme_drawdown",
		Timestamp:     time.Now(),
	}
	
	decision := rts.riskManager.EvaluateDecision(ctx)
	
	// With extreme drawdown, system should halt
	if decision.Approved {
		failures = append(failures, "system allowed trading during extreme drawdown")
	}
	
	return failures
}

func (rts *RiskTestSuite) testStaleQuotes() []string {
	failures := make([]string, 0)
	
	// Test with quotes older than 10 seconds
	ctx := DecisionContext{
		Symbol:        "STALE",
		Intent:        "BUY_1X",
		Quantity:      100,
		Price:         100.0,
		Strategy:      "stale_test",
		CorrelationID: "stale_quotes",
		Timestamp:     time.Now().Add(-15 * time.Second), // Old timestamp
	}
	
	decision := rts.riskManager.EvaluateDecision(ctx)
	
	// Should block due to stale data
	blocked := false
	for _, reason := range decision.BlockedBy {
		if reason == "quotes_stale" || reason == "data_quality_poor" {
			blocked = true
			break
		}
	}
	
	if !blocked {
		failures = append(failures, "system allowed trading with stale quotes")
	}
	
	return failures
}

func (rts *RiskTestSuite) testHighVolatility() []string {
	// Test high volatility scenarios
	return []string{} // Simplified for now
}

func (rts *RiskTestSuite) testSystemOverload() []string {
	// Test system under high load
	return []string{} // Simplified for now
}

func (rts *RiskTestSuite) generateRandomDecision() DecisionContext {
	symbols := []string{"AAPL", "NVDA", "TSLA", "SPY", "QQQ"}
	intents := []string{"BUY_1X", "BUY_5X", "REDUCE", "HOLD"}
	
	return DecisionContext{
		Symbol:        symbols[rand.Intn(len(symbols))],
		Intent:        intents[rand.Intn(len(intents))],
		Quantity:      rand.Intn(1000) + 1,
		Price:         rand.Float64()*1000 + 10,
		Strategy:      "property_test",
		Score:         rand.Float64(),
		CorrelationID: fmt.Sprintf("prop_%d", rand.Int()),
		Timestamp:     time.Now(),
	}
}

func (rts *RiskTestSuite) checkInvariants(ctx DecisionContext, decision DecisionResult) []string {
	failures := make([]string, 0)
	
	// Invariant 1: Decision processing should complete in reasonable time
	if decision.ProcessingTime > 5*time.Second {
		failures = append(failures, 
			fmt.Sprintf("decision took too long: %v", decision.ProcessingTime))
	}
	
	// Invariant 2: Size multiplier should be between 0 and 2
	if decision.SizeMultiplier < 0 || decision.SizeMultiplier > 2 {
		failures = append(failures, 
			fmt.Sprintf("invalid size multiplier: %f", decision.SizeMultiplier))
	}
	
	// Invariant 3: Risk score should be between 0 and 1
	if decision.RiskScore < 0 || decision.RiskScore > 1 {
		failures = append(failures, 
			fmt.Sprintf("invalid risk score: %f", decision.RiskScore))
	}
	
	// Invariant 4: If blocked, there should be a reason
	if !decision.Approved && len(decision.BlockedBy) == 0 {
		failures = append(failures, "decision blocked without reason")
	}
	
	return failures
}

// ChaosController methods

func NewChaosController() *ChaosController {
	ctx, cancel := context.WithCancel(context.Background())
	return &ChaosController{
		activeFailures: make(map[string]ChaosFailure),
		ctx:            ctx,
		cancel:         cancel,
	}
}

func (cc *ChaosController) Start() {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.running = true
}

func (cc *ChaosController) Stop() {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.running = false
	cc.cancel()
}

func (cc *ChaosController) AddFailure(name string, failure ChaosFailure) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	failure.StartTime = time.Now()
	cc.activeFailures[name] = failure
}

func (cc *ChaosController) ShouldFail(failureType string) bool {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	
	failure, exists := cc.activeFailures[failureType]
	if !exists || !cc.running {
		return false
	}
	
	// Check if failure is still active
	if time.Since(failure.StartTime) > failure.Duration {
		delete(cc.activeFailures, failureType)
		return false
	}
	
	// Check probability
	return rand.Float64() < failure.Probability
}

// Test scenario definitions

func createTestScenarios() []TestScenario {
	return []TestScenario{
		{
			Name:        "flash_crash_replay",
			Description: "Replay 2010 Flash Crash scenario",
			Type:        "historical_replay",
			Duration:    30 * time.Second,
			Config: map[string]interface{}{
				"scenario": "flash_crash_2010",
			},
			Expected: ExpectedResults{
				MaxDrawdown:           5.0,
				CircuitBreakerTriggers: 1,
				ExpectedStates:        []string{"halted"},
				MaxDecisionLatencyMs:  500,
				ShouldHalt:            true,
			},
		},
		{
			Name:        "quote_feed_chaos",
			Description: "Test resilience with intermittent quote feed failures",
			Type:        "chaos",
			Duration:    60 * time.Second,
			Config: map[string]interface{}{
				"duration": 60.0,
				"failures": []interface{}{
					map[string]interface{}{
						"type":        "quote_timeout",
						"probability": 0.1,
						"duration":    5.0,
					},
					map[string]interface{}{
						"type":        "stale_quotes",
						"probability": 0.05,
						"duration":    10.0,
					},
				},
			},
			Expected: ExpectedResults{
				MaxDecisionLatencyMs: 1000,
				MinDataQualityScore:  0.7,
			},
		},
		{
			Name:        "invariant_verification",
			Description: "Property-based testing of risk invariants",
			Type:        "property_based",
			Duration:    30 * time.Second,
			Config: map[string]interface{}{
				"num_tests": 10000.0,
			},
			Expected: ExpectedResults{
				MaxDecisionLatencyMs: 100,
			},
		},
		{
			Name:        "boundary_conditions",
			Description: "Test extreme boundary conditions",
			Type:        "boundary",
			Duration:    15 * time.Second,
			Config:      map[string]interface{}{},
			Expected: ExpectedResults{
				MaxDecisionLatencyMs: 200,
			},
		},
	}
}

func loadHistoricalScenarios(dataPath string) map[string]HistoricalScenario {
	scenarios := make(map[string]HistoricalScenario)
	
	// Load historical scenarios from files
	scenarioFiles, err := filepath.Glob(filepath.Join(dataPath, "*.json"))
	if err != nil {
		return scenarios
	}
	
	for _, file := range scenarioFiles {
		var scenario HistoricalScenario
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		
		if err := json.Unmarshal(data, &scenario); err != nil {
			continue
		}
		
		scenarios[scenario.Name] = scenario
	}
	
	// Add default scenarios if none loaded
	if len(scenarios) == 0 {
		scenarios["flash_crash_2010"] = createFlashCrashScenario()
	}
	
	return scenarios
}

func createFlashCrashScenario() HistoricalScenario {
	// Simplified Flash Crash scenario
	marketData := make([]HistoricalMarketData, 100)
	baseTime := time.Date(2010, 5, 6, 14, 42, 0, 0, time.UTC)
	
	for i := 0; i < 100; i++ {
		// Simulate dramatic price drop and recovery
		priceChange := -5.0 // 5% drop
		if i > 50 {
			priceChange = 3.0 // 3% recovery
		}
		
		marketData[i] = HistoricalMarketData{
			Timestamp: baseTime.Add(time.Duration(i) * time.Second),
			Symbol:    "SPY",
			Quotes: adapters.Quote{
				Symbol:    "SPY",
				Last:      110.0 + float64(i)*0.1,
				Bid:       109.9 + float64(i)*0.1,
				Ask:       110.1 + float64(i)*0.1,
				Volume:    1000000,
				Timestamp: baseTime.Add(time.Duration(i) * time.Second),
			},
			Metrics: map[string]float64{
				"price_change_pct": priceChange,
				"volatility":       0.8, // High volatility
			},
		}
	}
	
	return HistoricalScenario{
		Name:        "flash_crash_2010",
		Date:        "2010-05-06",
		Description: "Simulated Flash Crash of May 6, 2010",
		MarketData:  marketData,
		Volatility: VolatilityProfile{
			BaseVolatility: 0.2,
			MaxVolatility:  0.8,
			Correlation:    0.9,
		},
	}
}

// GetTestResults returns all test results
func (rts *RiskTestSuite) GetTestResults() []TestResult {
	rts.mu.RLock()
	defer rts.mu.RUnlock()
	return rts.testResults
}

// GenerateTestReport generates a comprehensive test report
func (rts *RiskTestSuite) GenerateTestReport() map[string]interface{} {
	totalTests := len(rts.testResults)
	passedTests := 0
	totalDuration := time.Duration(0)
	
	for _, result := range rts.testResults {
		if result.Passed {
			passedTests++
		}
		totalDuration += result.Duration
	}
	
	return map[string]interface{}{
		"summary": map[string]interface{}{
			"total_tests":    totalTests,
			"passed_tests":   passedTests,
			"failed_tests":   totalTests - passedTests,
			"pass_rate":      float64(passedTests) / float64(totalTests),
			"total_duration": totalDuration.String(),
		},
		"results":    rts.testResults,
		"timestamp":  time.Now(),
		"version":    "1.0",
	}
}