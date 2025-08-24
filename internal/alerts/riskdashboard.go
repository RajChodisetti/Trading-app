package alerts

import (
	"fmt"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/risk"
)

// RiskDashboard creates rich Slack Block Kit messages for portfolio risk monitoring
type RiskDashboard struct {
	slackClient *SlackClient
}

// NewRiskDashboard creates a new risk dashboard
func NewRiskDashboard(slackClient *SlackClient) *RiskDashboard {
	return &RiskDashboard{
		slackClient: slackClient,
	}
}

// SendPortfolioStatus sends a comprehensive portfolio status dashboard
func (rd *RiskDashboard) SendPortfolioStatus(
	nav float64,
	dailyPnL, unrealizedPnL float64,
	dailyDD, weeklyDD float64,
	circuitState risk.CircuitBreakerState,
	sizeMultiplier float64,
	positions map[string]float64,
	dataQuality risk.NAVDataQuality,
) error {
	blocks := rd.buildPortfolioStatusBlocks(
		nav, dailyPnL, unrealizedPnL, dailyDD, weeklyDD,
		circuitState, sizeMultiplier, positions, dataQuality,
	)
	
	message := SlackMessage{
		Text:   fmt.Sprintf("📊 Portfolio Status - %s", time.Now().Format("15:04:05 EST")),
		Blocks: blocks,
	}
	
	return rd.slackClient.SendMessage(message)
}

// SendDrawdownAlert sends a formatted drawdown alert with action buttons
func (rd *RiskDashboard) SendDrawdownAlert(
	alertType string,
	dailyDD, weeklyDD float64,
	threshold float64,
	currentState risk.CircuitBreakerState,
	newState risk.CircuitBreakerState,
	affectedPositions map[string]float64,
) error {
	blocks := rd.buildDrawdownAlertBlocks(
		alertType, dailyDD, weeklyDD, threshold,
		currentState, newState, affectedPositions,
	)
	
	// Determine alert color
	color := rd.getAlertColor(newState)
	
	message := SlackMessage{
		Text:        fmt.Sprintf("🚨 %s Drawdown Alert", alertType),
		Blocks:      blocks,
		Attachments: []SlackAttachment{{Color: color}},
	}
	
	return rd.slackClient.SendMessage(message)
}

// SendCircuitBreakerAction sends an interactive message for circuit breaker controls
func (rd *RiskDashboard) SendCircuitBreakerControls(
	currentState risk.CircuitBreakerState,
	nav float64,
	dailyDD, weeklyDD float64,
) error {
	blocks := rd.buildCircuitBreakerControlsBlocks(currentState, nav, dailyDD, weeklyDD)
	
	message := SlackMessage{
		Text:   "🔧 Circuit Breaker Controls",
		Blocks: blocks,
	}
	
	return rd.slackClient.SendMessage(message)
}

// buildPortfolioStatusBlocks creates Block Kit blocks for portfolio status
func (rd *RiskDashboard) buildPortfolioStatusBlocks(
	nav float64,
	dailyPnL, unrealizedPnL float64,
	dailyDD, weeklyDD float64,
	circuitState risk.CircuitBreakerState,
	sizeMultiplier float64,
	positions map[string]float64,
	dataQuality risk.NAVDataQuality,
) []interface{} {
	blocks := []interface{}{}
	
	// Header block
	headerText := fmt.Sprintf("📊 *Portfolio Status* (Updated: %s)", time.Now().Format("15:04:05 EST"))
	blocks = append(blocks, map[string]interface{}{
		"type": "header",
		"text": map[string]interface{}{
			"type": "plain_text",
			"text": headerText,
		},
	})
	
	// NAV and P&L section
	navFields := []map[string]interface{}{
		{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*NAV:* $%.2f", nav),
		},
		{
			"type": "mrkdwn", 
			"text": fmt.Sprintf("*Daily P&L:* %s$%.2f", rd.getPnLEmoji(dailyPnL), dailyPnL),
		},
		{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*Unrealized P&L:* %s$%.2f", rd.getPnLEmoji(unrealizedPnL), unrealizedPnL),
		},
		{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*Active Positions:* %d", len(positions)),
		},
	}
	
	blocks = append(blocks, map[string]interface{}{
		"type":   "section",
		"fields": navFields,
	})
	
	// Drawdown and circuit breaker section
	ddFields := []map[string]interface{}{
		{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*Daily Drawdown:* %.2f%%", dailyDD),
		},
		{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*Weekly Drawdown:* %.2f%%", weeklyDD),
		},
		{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*Circuit Breaker:* %s %s", rd.getStateEmoji(circuitState), string(circuitState)),
		},
		{
			"type": "mrkdwn",
			"text": fmt.Sprintf("*Size Multiplier:* %.0f%%", sizeMultiplier*100),
		},
	}
	
	blocks = append(blocks, map[string]interface{}{
		"type":   "section",
		"fields": ddFields,
	})
	
	// Position details (if not too many)
	if len(positions) > 0 && len(positions) <= 6 {
		positionText := "*Position P&L:*\n"
		for symbol, pnl := range positions {
			positionText += fmt.Sprintf("• %s: %s$%.2f\n", symbol, rd.getPnLEmoji(pnl), pnl)
		}
		
		blocks = append(blocks, map[string]interface{}{
			"type": "section",
			"text": map[string]interface{}{
				"type": "mrkdwn",
				"text": positionText,
			},
		})
	}
	
	// Data quality warnings
	if len(dataQuality.StaleQuotes) > 0 || len(dataQuality.MissingQuotes) > 0 {
		warningText := "⚠️ *Data Quality Issues:*\n"
		if len(dataQuality.StaleQuotes) > 0 {
			warningText += fmt.Sprintf("• Stale quotes: %v\n", dataQuality.StaleQuotes)
		}
		if len(dataQuality.MissingQuotes) > 0 {
			warningText += fmt.Sprintf("• Missing quotes: %v\n", dataQuality.MissingQuotes)
		}
		
		blocks = append(blocks, map[string]interface{}{
			"type": "section",
			"text": map[string]interface{}{
				"type": "mrkdwn",
				"text": warningText,
			},
		})
	}
	
	// Action buttons
	blocks = append(blocks, map[string]interface{}{
		"type": "actions",
		"elements": []map[string]interface{}{
			{
				"type":      "button",
				"text":      map[string]interface{}{"type": "plain_text", "text": "Detailed Metrics"},
				"value":     "detailed_metrics",
				"action_id": "show_detailed_metrics",
			},
			{
				"type":      "button",
				"text":      map[string]interface{}{"type": "plain_text", "text": "Risk Controls"},
				"value":     "risk_controls",
				"action_id": "show_risk_controls",
				"style":     "primary",
			},
		},
	})
	
	return blocks
}

// buildDrawdownAlertBlocks creates Block Kit blocks for drawdown alerts
func (rd *RiskDashboard) buildDrawdownAlertBlocks(
	alertType string,
	dailyDD, weeklyDD float64,
	threshold float64,
	currentState, newState risk.CircuitBreakerState,
	affectedPositions map[string]float64,
) []interface{} {
	blocks := []interface{}{}
	
	// Alert header
	alertEmoji := rd.getAlertEmoji(alertType)
	headerText := fmt.Sprintf("%s *%s Drawdown Alert*", alertEmoji, alertType)
	
	blocks = append(blocks, map[string]interface{}{
		"type": "header",
		"text": map[string]interface{}{
			"type": "plain_text",
			"text": headerText,
		},
	})
	
	// Alert details
	alertText := fmt.Sprintf("Drawdown threshold breached at %s\n", time.Now().Format("15:04:05"))
	alertText += fmt.Sprintf("• Daily: *%.2f%%* | Weekly: *%.2f%%*\n", dailyDD, weeklyDD)
	alertText += fmt.Sprintf("• Threshold: *%.1f%%*\n", threshold)
	alertText += fmt.Sprintf("• State: %s → %s", string(currentState), string(newState))
	
	blocks = append(blocks, map[string]interface{}{
		"type": "section",
		"text": map[string]interface{}{
			"type": "mrkdwn",
			"text": alertText,
		},
	})
	
	// Affected positions
	if len(affectedPositions) > 0 {
		posText := "*Affected Positions:*\n"
		totalLoss := 0.0
		
		for symbol, pnl := range affectedPositions {
			posText += fmt.Sprintf("• %s: %s$%.2f\n", symbol, rd.getPnLEmoji(pnl), pnl)
			if pnl < 0 {
				totalLoss += pnl
			}
		}
		
		posText += fmt.Sprintf("\n*Total Loss: $%.2f*", totalLoss)
		
		blocks = append(blocks, map[string]interface{}{
			"type": "section",
			"text": map[string]interface{}{
				"type": "mrkdwn",
				"text": posText,
			},
		})
	}
	
	// Action buttons based on new state
	buttons := rd.getActionButtons(newState)
	if len(buttons) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"type":     "actions",
			"elements": buttons,
		})
	}
	
	return blocks
}

// buildCircuitBreakerControlsBlocks creates interactive circuit breaker controls
func (rd *RiskDashboard) buildCircuitBreakerControlsBlocks(
	currentState risk.CircuitBreakerState,
	nav float64,
	dailyDD, weeklyDD float64,
) []interface{} {
	blocks := []interface{}{}
	
	// Header
	blocks = append(blocks, map[string]interface{}{
		"type": "header",
		"text": map[string]interface{}{
			"type": "plain_text",
			"text": "🔧 Circuit Breaker Controls",
		},
	})
	
	// Current status
	statusText := fmt.Sprintf("*Current State:* %s %s\n", rd.getStateEmoji(currentState), string(currentState))
	statusText += fmt.Sprintf("*NAV:* $%.2f\n", nav)
	statusText += fmt.Sprintf("*Drawdowns:* %.2f%% daily, %.2f%% weekly", dailyDD, weeklyDD)
	
	blocks = append(blocks, map[string]interface{}{
		"type": "section",
		"text": map[string]interface{}{
			"type": "mrkdwn",
			"text": statusText,
		},
	})
	
	// Control buttons
	controlButtons := []map[string]interface{}{}
	
	// Emergency halt (always available)
	controlButtons = append(controlButtons, map[string]interface{}{
		"type":      "button",
		"text":      map[string]interface{}{"type": "plain_text", "text": "🚨 Emergency Halt"},
		"value":     "emergency_halt",
		"action_id": "emergency_halt",
		"style":     "danger",
		"confirm": map[string]interface{}{
			"title": map[string]interface{}{"type": "plain_text", "text": "Confirm Emergency Halt"},
			"text":  map[string]interface{}{"type": "mrkdwn", "text": "This will immediately halt all trading. Are you sure?"},
			"confirm": map[string]interface{}{"type": "plain_text", "text": "Yes, Halt Trading"},
			"deny":    map[string]interface{}{"type": "plain_text", "text": "Cancel"},
		},
	})
	
	// Size reduction options
	if currentState != risk.StateEmergency && currentState != risk.StateHalted {
		controlButtons = append(controlButtons, map[string]interface{}{
			"type":        "static_select",
			"placeholder": map[string]interface{}{"type": "plain_text", "text": "Reduce Size"},
			"action_id":   "reduce_size",
			"options": []map[string]interface{}{
				{"text": map[string]interface{}{"type": "plain_text", "text": "50% Size"}, "value": "0.5"},
				{"text": map[string]interface{}{"type": "plain_text", "text": "25% Size"}, "value": "0.25"},
				{"text": map[string]interface{}{"type": "plain_text", "text": "10% Size"}, "value": "0.1"},
			},
		})
	}
	
	// Recovery option (if halted)
	if currentState == risk.StateHalted || currentState == risk.StateCoolingOff || currentState == risk.StateEmergency {
		controlButtons = append(controlButtons, map[string]interface{}{
			"type":      "button",
			"text":      map[string]interface{}{"type": "plain_text", "text": "🔄 Initiate Recovery"},
			"value":     "initiate_recovery",
			"action_id": "initiate_recovery",
			"style":     "primary",
		})
	}
	
	// Reason dropdown
	controlButtons = append(controlButtons, map[string]interface{}{
		"type":        "static_select",
		"placeholder": map[string]interface{}{"type": "plain_text", "text": "Select Reason"},
		"action_id":   "action_reason",
		"options": []map[string]interface{}{
			{"text": map[string]interface{}{"type": "plain_text", "text": "Market Volatility"}, "value": "market_volatility"},
			{"text": map[string]interface{}{"type": "plain_text", "text": "Position Risk"}, "value": "position_risk"},
			{"text": map[string]interface{}{"type": "plain_text", "text": "Data Quality"}, "value": "data_quality"},
			{"text": map[string]interface{}{"type": "plain_text", "text": "Manual Override"}, "value": "manual_override"},
			{"text": map[string]interface{}{"type": "plain_text", "text": "Risk Assessment"}, "value": "risk_assessment"},
		},
	})
	
	blocks = append(blocks, map[string]interface{}{
		"type":     "actions",
		"elements": controlButtons,
	})
	
	return blocks
}

// getActionButtons returns appropriate action buttons for each circuit breaker state
func (rd *RiskDashboard) getActionButtons(state risk.CircuitBreakerState) []map[string]interface{} {
	switch state {
	case risk.StateWarning, risk.StateReduced:
		return []map[string]interface{}{
			{
				"type":      "button",
				"text":      map[string]interface{}{"type": "plain_text", "text": "Acknowledge"},
				"value":     "acknowledge",
				"action_id": "acknowledge_warning",
			},
			{
				"type":      "button",
				"text":      map[string]interface{}{"type": "plain_text", "text": "Emergency Halt"},
				"value":     "emergency_halt",
				"action_id": "emergency_halt",
				"style":     "danger",
			},
		}
		
	case risk.StateHalted, risk.StateCoolingOff:
		return []map[string]interface{}{
			{
				"type":      "button",
				"text":      map[string]interface{}{"type": "plain_text", "text": "Initiate Recovery"},
				"value":     "initiate_recovery",
				"action_id": "initiate_recovery",
				"style":     "primary",
			},
			{
				"type":      "button",
				"text":      map[string]interface{}{"type": "plain_text", "text": "Escalate"},
				"value":     "escalate",
				"action_id": "escalate_emergency",
				"style":     "danger",
			},
		}
		
	case risk.StateEmergency:
		return []map[string]interface{}{
			{
				"type":      "button",
				"text":      map[string]interface{}{"type": "plain_text", "text": "Contact Risk Manager"},
				"value":     "contact_risk_manager",
				"action_id": "contact_risk_manager",
				"style":     "danger",
			},
		}
		
	default:
		return []map[string]interface{}{}
	}
}

// Helper methods for emojis and colors

func (rd *RiskDashboard) getStateEmoji(state risk.CircuitBreakerState) string {
	switch state {
	case risk.StateNormal:
		return "🟢"
	case risk.StateWarning:
		return "🟡"
	case risk.StateReduced, risk.StateRestricted:
		return "🟠"
	case risk.StateMinimal:
		return "🔴"
	case risk.StateHalted, risk.StateCoolingOff:
		return "🛑"
	case risk.StateEmergency:
		return "🚨"
	default:
		return "❓"
	}
}

func (rd *RiskDashboard) getPnLEmoji(pnl float64) string {
	if pnl > 0 {
		return "🟢"
	} else if pnl < 0 {
		return "🔴"
	}
	return "⚪"
}

func (rd *RiskDashboard) getAlertEmoji(alertType string) string {
	switch alertType {
	case "WARNING":
		return "⚠️"
	case "CRITICAL":
		return "🚨"
	case "EMERGENCY":
		return "🆘"
	default:
		return "ℹ️"
	}
}

func (rd *RiskDashboard) getAlertColor(state risk.CircuitBreakerState) string {
	switch state {
	case risk.StateWarning:
		return "#ff9900" // Orange
	case risk.StateReduced, risk.StateRestricted:
		return "#ff6600" // Dark orange
	case risk.StateHalted, risk.StateCoolingOff:
		return "#cc0000" // Red
	case risk.StateEmergency:
		return "#990000" // Dark red
	default:
		return "#36a64f" // Green
	}
}

// Note: SlackAttachment is defined in slack.go