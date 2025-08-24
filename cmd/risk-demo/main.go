package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/adapters"
	"github.com/Rajchodisetti/trading-app/internal/alerts"
	"github.com/Rajchodisetti/trading-app/internal/config"
	"github.com/Rajchodisetti/trading-app/internal/portfolio"
	"github.com/Rajchodisetti/trading-app/internal/risk"
)

func main() {
	fmt.Println("🚀 Session 13: Risk Management System Demo")
	fmt.Println("==========================================")
	
	// Create mock portfolio manager
	portfolioMgr := portfolio.NewManager("data/demo_portfolio.json", 100000.0)
	if err := portfolioMgr.Load(); err != nil {
		log.Fatalf("Failed to load portfolio: %v", err)
	}
	
	// Create mock quotes adapter
	quotesAdapter := adapters.NewMockQuotesAdapter()
	
	// Create risk manager configuration
	riskConfig := risk.RiskManagerConfig{
		NAVTracker: risk.NAVTrackerConfig{
			UpdateIntervalSeconds:     1,
			QuoteStalenessThresholdMs: 2000,
			MaxHistoryEntries:         100,
			UseMidPrice:              true,
			PersistPath:              "data/demo_nav_state.json",
		},
		CircuitBreaker: getDefaultCircuitBreakerThresholds(),
		Volatility: risk.VolatilityConfig{
			LookbackDays:             21,
			EwmaAlpha:               0.94,
			ATRPeriod:               14,
			VolatilityFloor:         0.5,
			VolatilityCeiling:       3.0,
			QuietMarketThreshold:    0.10,
			VolatileMarketThreshold: 0.30,
			UpdateIntervalMinutes:   5,
		},
		EventLogPath:          "data/demo_risk_events.jsonl",
		MetricsEnabled:        true,
		AlertingEnabled:       true,
		UpdateIntervalSeconds: 1,
		DecisionTimeoutMs:     500,
	}
	
	// Create risk manager
	riskManager := risk.NewRiskManager(portfolioMgr, quotesAdapter, riskConfig)
	
	// Create Slack client for dashboard
	slackConfig := getSlackConfig()
	slackClient := alerts.NewSlackClient(slackConfig)
	riskDashboard := alerts.NewRiskDashboard(slackClient)
	
	// Start risk management system
	fmt.Println("📊 Starting risk management system...")
	if err := riskManager.Start(); err != nil {
		log.Fatalf("Failed to start risk manager: %v", err)
	}
	defer riskManager.Stop()
	
	// Create some demo positions
	fmt.Println("💼 Creating demo positions...")
	createDemoPositions(portfolioMgr)
	
	// Set up graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	
	// Start demo loop
	go runDemo(ctx, riskManager, riskDashboard)
	
	fmt.Println("✅ Risk management system running. Press Ctrl+C to stop.")
	fmt.Println("📈 Monitoring portfolio NAV, drawdowns, and circuit breaker state...")
	fmt.Println()
	
	<-sigChan
	fmt.Println("\n🛑 Shutting down risk management system...")
	
	// Print final status
	printFinalStatus(riskManager)
	
	fmt.Println("✅ Demo completed successfully!")
}

func runDemo(ctx context.Context, riskManager *risk.RiskManager, dashboard *alerts.RiskDashboard) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	decisionCount := 0
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Simulate trading decisions
			decision := simulateDecision(riskManager, decisionCount)
			decisionCount++
			
			// Print decision result
			printDecisionResult(decision)
			
			// Get current risk status
			status := riskManager.GetCurrentRiskStatus()
			
			// Send portfolio status to Slack (if enabled)
			if dashboard != nil {
				sendPortfolioUpdate(dashboard, status)
			}
			
			// Print risk metrics every 30 seconds
			if decisionCount%6 == 0 {
				printRiskMetrics(riskManager)
			}
		}
	}
}

func simulateDecision(riskManager *risk.RiskManager, count int) risk.DecisionResult {
	// Create a variety of decision contexts
	symbols := []string{"AAPL", "NVDA", "TSLA", "SPY", "QQQ"}
	intents := []string{"BUY_1X", "BUY_5X", "REDUCE", "HOLD"}
	
	symbol := symbols[count%len(symbols)]
	intent := intents[count%len(intents)]
	
	decisionCtx := risk.DecisionContext{
		Symbol:        symbol,
		Intent:        intent,
		Quantity:      100 + count*10,
		Price:         100.0 + float64(count)*2.5,
		Strategy:      "demo_strategy",
		Score:         0.5 + float64(count%10)*0.05,
		Features:      map[string]interface{}{"demo": true, "count": count},
		CorrelationID: fmt.Sprintf("demo_%d", count),
		Timestamp:     time.Now(),
	}
	
	return riskManager.EvaluateDecision(decisionCtx)
}

func createDemoPositions(portfolioMgr *portfolio.Manager) {
	positions := []struct {
		symbol   string
		quantity int
		price    float64
	}{
		{"AAPL", 100, 225.50},
		{"NVDA", 50, 118.75},
		{"SPY", 200, 450.25},
		{"TSLA", 25, 195.80},
	}
	
	for _, pos := range positions {
		err := portfolioMgr.UpdatePosition(pos.symbol, pos.quantity, pos.price, time.Now())
		if err != nil {
			log.Printf("Failed to create position for %s: %v", pos.symbol, err)
		}
	}
	
	fmt.Printf("   Created %d demo positions\n", len(positions))
}

func printDecisionResult(decision risk.DecisionResult) {
	status := "✅ APPROVED"
	if !decision.Approved {
		status = "❌ BLOCKED"
	}
	
	fmt.Printf("🔍 Decision %s [%dms]: %s %s (size: %.2fx, risk: %.3f)\n",
		decision.DecisionID[len(decision.DecisionID)-8:], // Last 8 chars
		decision.ProcessingTime.Milliseconds(),
		status,
		decision.Intent,
		decision.SizeMultiplier,
		decision.RiskScore,
	)
	
	if !decision.Approved && len(decision.BlockedBy) > 0 {
		fmt.Printf("   🚫 Blocked by: %v\n", decision.BlockedBy)
	}
	
	if len(decision.Warnings) > 0 {
		fmt.Printf("   ⚠️  Warnings: %v\n", decision.Warnings)
	}
}

func printRiskMetrics(riskManager *risk.RiskManager) {
	status := riskManager.GetCurrentRiskStatus()
	
	fmt.Println("\n📊 Current Risk Status:")
	fmt.Println("   ========================")
	
	if riskData, ok := status["risk_data"].(risk.RiskData); ok {
		fmt.Printf("   💰 NAV: $%.2f\n", riskData.CurrentNAV)
		fmt.Printf("   📉 Daily DD: %.2f%% | Weekly DD: %.2f%%\n", 
			riskData.DailyDrawdown, riskData.WeeklyDrawdown)
		fmt.Printf("   ⚡ Circuit State: %s (%.0f%% size)\n", 
			riskData.CircuitState, riskData.SizeMultiplier*100)
		fmt.Printf("   📈 Volatility: %s\n", riskData.VolatilityRegime)
		fmt.Printf("   🕐 Quote Age: %v\n", riskData.QuoteStaleness)
		
		if len(riskData.DataQuality.StaleQuotes) > 0 {
			fmt.Printf("   ⚠️  Stale Quotes: %v\n", riskData.DataQuality.StaleQuotes)
		}
		if len(riskData.DataQuality.MissingQuotes) > 0 {
			fmt.Printf("   ❌ Missing Quotes: %v\n", riskData.DataQuality.MissingQuotes)
		}
	}
	
	if systemHealth, ok := status["system_health"].(map[string]interface{}); ok {
		fmt.Printf("   🏥 System Health: %s\n", systemHealth["overall_status"])
		fmt.Printf("   🔧 Components: %d healthy, %d degraded, %d unhealthy\n",
			systemHealth["healthy_components"],
			systemHealth["degraded_components"],
			systemHealth["unhealthy_components"])
	}
	
	fmt.Println()
}

func sendPortfolioUpdate(dashboard *alerts.RiskDashboard, status map[string]interface{}) {
	// Extract data for dashboard
	if riskData, ok := status["risk_data"].(risk.RiskData); ok {
		// Create mock position P&L map
		positions := map[string]float64{
			"AAPL": 125.50,
			"NVDA": -45.25,
			"SPY":  89.75,
			"TSLA": -22.10,
		}
		
		// Send portfolio status (would only work with real Slack config)
		dashboard.SendPortfolioStatus(
			riskData.CurrentNAV,
			150.75,  // Daily P&L
			-85.25,  // Unrealized P&L
			riskData.DailyDrawdown,
			riskData.WeeklyDrawdown,
			riskData.CircuitState,
			riskData.SizeMultiplier,
			positions,
			riskData.DataQuality,
		)
	}
}

func printFinalStatus(riskManager *risk.RiskManager) {
	fmt.Println("\n📈 Final Risk Metrics:")
	fmt.Println("   ===================")
	
	metrics := riskManager.GetRiskMetrics()
	
	fmt.Printf("   📊 NAV Updates: %d\n", metrics.NAVUpdateLatency.Count)
	fmt.Printf("   ⚡ Circuit Transitions: %d\n", metrics.StateTransitions)
	fmt.Printf("   🚫 Threshold Breaches: %d\n", metrics.ThresholdBreaches)
	fmt.Printf("   🔧 Manual Overrides: %d\n", metrics.ManualOverrides)
	fmt.Printf("   🔄 Auto Recoveries: %d\n", metrics.AutoRecoveries)
	fmt.Printf("   ⏱️  Avg Decision Latency: %dms\n", metrics.DecisionLatency.P50.Milliseconds())
	fmt.Printf("   🛑 Orders Suppressed: %d\n", metrics.OrderSuppressions)
	fmt.Printf("   📊 Data Quality Score: %.3f\n", metrics.NAVDataQualityScore)
	fmt.Printf("   📈 Current Volatility: %.3f\n", metrics.CircuitBreakerState)
}

func getDefaultCircuitBreakerThresholds() risk.CircuitBreakerThresholds {
	return risk.CircuitBreakerThresholds{
		DailyWarningPct:     2.0,
		DailyReducedPct:     2.5,
		DailyRestrictedPct:  3.0,
		DailyMinimalPct:     3.5,
		DailyHaltPct:        4.0,
		WeeklyWarningPct:    5.0,
		WeeklyReducedPct:    6.0,
		WeeklyRestrictedPct: 7.0,
		WeeklyMinimalPct:    8.0,
		WeeklyHaltPct:       10.0,
		VolatilityMultiplier: 1.0,
		MaxVolatilityFactor:  2.0,
		NormalSize:      1.0,
		WarningSize:     1.0,
		ReducedSize:     0.7,
		RestrictedSize:  0.5,
		MinimalSize:     0.25,
		HaltedSize:      0.0,
		CoolingOffSize:  0.0,
		EmergencySize:   0.0,
	}
}

func getSlackConfig() config.Slack {
	return config.Slack{
		Enabled:                   false, // Disabled for demo
		WebhookURL:               os.Getenv("SLACK_WEBHOOK_URL"),
		ChannelDefault:           "#trading-risk",
		RateLimitPerMin:          10,
		RateLimitPerSymbolPerMin: 3,
		AlertOnBuy5x:             true,
		AlertOnBuy1x:             false,
		AlertOnRejectGates:       true,
	}
}