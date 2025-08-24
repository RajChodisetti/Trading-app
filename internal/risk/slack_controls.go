package risk

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// SlackClient interface to avoid circular imports
type SlackClient interface {
	SendMessage(channel, message string) error
}

// SlackRiskControls provides Slack-based operational controls for caps and cooldowns
type SlackRiskControls struct {
	capsManager     *PositionCapsManager
	cooldownManager *CooldownManager
	slackClient     SlackClient
	auditPath       string
}

// AuditEntry records all operational changes for audit trail
type AuditEntry struct {
	Timestamp   time.Time              `json:"timestamp"`
	Action      string                 `json:"action"`
	UserID      string                 `json:"user_id"`
	Symbol      string                 `json:"symbol,omitempty"`
	OldValue    interface{}            `json:"old_value,omitempty"`
	NewValue    interface{}            `json:"new_value"`
	TTL         string                 `json:"ttl,omitempty"`
	Reason      string                 `json:"reason"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// NewSlackRiskControls creates new Slack-based risk controls
func NewSlackRiskControls(capsManager *PositionCapsManager, cooldownManager *CooldownManager, slackClient SlackClient, auditPath string) *SlackRiskControls {
	return &SlackRiskControls{
		capsManager:     capsManager,
		cooldownManager: cooldownManager,
		slackClient:     slackClient,
		auditPath:       auditPath,
	}
}

// HandleCapsCommand handles /caps Slack command
func (src *SlackRiskControls) HandleCapsCommand(userID, text string) (string, error) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		// Show current caps status
		return src.formatCapsStatus()
	}
	
	switch parts[0] {
	case "show", "status":
		return src.formatCapsStatus()
	default:
		return "Usage: `/caps` or `/caps show` to view current position caps and exposures", nil
	}
}

// HandleSetCapCommand handles /set-cap SYMBOL USD ttl=... reason="..." Slack command
func (src *SlackRiskControls) HandleSetCapCommand(userID, text string) (string, error) {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		return "Usage: `/set-cap SYMBOL USD [ttl=60m] [reason=\"market opportunity\"]`", nil
	}
	
	symbol := strings.ToUpper(parts[0])
	capUSD, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return fmt.Sprintf("Invalid cap amount: %s", parts[1]), nil
	}
	
	// Parse TTL (default 1 hour)
	ttl := 60 * time.Minute
	reason := "manual adjustment"
	
	// Parse additional parameters
	for i := 2; i < len(parts); i++ {
		part := parts[i]
		if strings.HasPrefix(part, "ttl=") {
			ttlStr := strings.TrimPrefix(part, "ttl=")
			if parsedTTL, err := time.ParseDuration(ttlStr); err == nil {
				ttl = parsedTTL
			}
		} else if strings.HasPrefix(part, "reason=") {
			reason = strings.Trim(strings.TrimPrefix(part, "reason="), "\"")
		}
	}
	
	// Get current cap for audit
	oldCap := src.capsManager.GetSymbolCap(symbol)
	
	// Update the cap
	if err := src.capsManager.UpdateSymbolCap(symbol, capUSD, ttl, userID, reason); err != nil {
		return fmt.Sprintf("Failed to update cap for %s: %v", symbol, err), nil
	}
	
	// Record audit entry
	src.recordAudit("set_cap", userID, symbol, oldCap.MaxPositionUSD, capUSD, ttl.String(), reason, nil)
	
	effectiveUntil := time.Now().Add(ttl)
	
	response := fmt.Sprintf("✅ Updated cap for %s:\n", symbol)
	response += fmt.Sprintf("• New cap: $%.0f (was $%.0f)\n", capUSD, oldCap.MaxPositionUSD)
	response += fmt.Sprintf("• Effective until: %s\n", effectiveUntil.Format("15:04:05 MST"))
	response += fmt.Sprintf("• Reason: %s", reason)
	
	// Send notification to channel
	src.sendCapChangeNotification(symbol, userID, oldCap.MaxPositionUSD, capUSD, effectiveUntil, reason)
	
	return response, nil
}

// HandleCooldownsCommand handles /cooldowns Slack command
func (src *SlackRiskControls) HandleCooldownsCommand(userID, text string) (string, error) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		// Show current cooldown status
		return src.formatCooldownsStatus()
	}
	
	switch parts[0] {
	case "show", "status":
		return src.formatCooldownsStatus()
	default:
		return "Usage: `/cooldowns` or `/cooldowns show` to view active cooldowns", nil
	}
}

// HandleSetCooldownCommand handles /set-cooldown SYMBOL SEC ttl=... reason="..." Slack command
func (src *SlackRiskControls) HandleSetCooldownCommand(userID, text string) (string, error) {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		return "Usage: `/set-cooldown SYMBOL SEC [ttl=60m] [reason=\"volatility spike\"]`", nil
	}
	
	symbol := strings.ToUpper(parts[0])
	cooldownSec, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Sprintf("Invalid cooldown seconds: %s", parts[1]), nil
	}
	
	// Parse TTL (default 1 hour)
	ttl := 60 * time.Minute
	reason := "manual adjustment"
	
	// Parse additional parameters
	for i := 2; i < len(parts); i++ {
		part := parts[i]
		if strings.HasPrefix(part, "ttl=") {
			ttlStr := strings.TrimPrefix(part, "ttl=")
			if parsedTTL, err := time.ParseDuration(ttlStr); err == nil {
				ttl = parsedTTL
			}
		} else if strings.HasPrefix(part, "reason=") {
			reason = strings.Trim(strings.TrimPrefix(part, "reason="), "\"")
		}
	}
	
	// Get current cooldown for audit
	oldCooldown := 30 // Default cooldown
	// In a full implementation, we'd get the actual current cooldown
	
	// Update the cooldown
	if err := src.cooldownManager.UpdateCooldown(symbol, cooldownSec, ttl, userID, reason); err != nil {
		return fmt.Sprintf("Failed to update cooldown for %s: %v", symbol, err), nil
	}
	
	// Record audit entry
	src.recordAudit("set_cooldown", userID, symbol, oldCooldown, cooldownSec, ttl.String(), reason, nil)
	
	effectiveUntil := time.Now().Add(ttl)
	
	response := fmt.Sprintf("✅ Updated cooldown for %s:\n", symbol)
	response += fmt.Sprintf("• New cooldown: %ds (was %ds)\n", cooldownSec, oldCooldown)
	response += fmt.Sprintf("• Effective until: %s\n", effectiveUntil.Format("15:04:05 MST"))
	response += fmt.Sprintf("• Reason: %s", reason)
	
	return response, nil
}

// HandleOverrideCapCommand handles /override-cap SYMBOL reason="emergency" Slack command
func (src *SlackRiskControls) HandleOverrideCapCommand(userID, text string) (string, error) {
	parts := strings.Fields(text)
	if len(parts) < 1 {
		return "Usage: `/override-cap SYMBOL [reason=\"emergency situation\"]`", nil
	}
	
	symbol := strings.ToUpper(parts[0])
	reason := "emergency override"
	
	// Parse reason
	for i := 1; i < len(parts); i++ {
		part := parts[i]
		if strings.HasPrefix(part, "reason=") {
			reason = strings.Trim(strings.TrimPrefix(part, "reason="), "\"")
		}
	}
	
	// Get current cap
	currentCap := src.capsManager.GetSymbolCap(symbol)
	
	// Set a very high temporary cap (effectively unlimited)
	emergencyCap := 1000000.0 // $1M emergency cap
	emergencyTTL := 30 * time.Minute
	
	if err := src.capsManager.UpdateSymbolCap(symbol, emergencyCap, emergencyTTL, userID, reason); err != nil {
		return fmt.Sprintf("Failed to set emergency override for %s: %v", symbol, err), nil
	}
	
	// Record audit entry
	src.recordAudit("override_cap", userID, symbol, currentCap.MaxPositionUSD, emergencyCap, emergencyTTL.String(), reason, map[string]interface{}{
		"emergency": true,
	})
	
	effectiveUntil := time.Now().Add(emergencyTTL)
	
	response := fmt.Sprintf("⚠️ EMERGENCY CAP OVERRIDE for %s:\n", symbol)
	response += fmt.Sprintf("• Emergency cap: $%.0f (was $%.0f)\n", emergencyCap, currentCap.MaxPositionUSD)
	response += fmt.Sprintf("• Expires: %s\n", effectiveUntil.Format("15:04:05 MST"))
	response += fmt.Sprintf("• Reason: %s\n", reason)
	response += fmt.Sprintf("• User: <@%s>", userID)
	
	// Send alert notification
	src.sendEmergencyOverrideAlert(symbol, userID, currentCap.MaxPositionUSD, emergencyCap, effectiveUntil, reason)
	
	return response, nil
}

// formatCapsStatus creates a formatted display of current caps and exposures
func (src *SlackRiskControls) formatCapsStatus() (string, error) {
	exposures, err := src.capsManager.GetAllExposures()
	if err != nil {
		return "Error retrieving exposures", err
	}
	
	maxConcentration, maxSymbol, err := src.capsManager.GetMaxConcentration()
	if err != nil {
		return "Error calculating concentration", err
	}
	
	response := "📊 *Position Caps Status*\n"
	response += "```\n"
	
	if len(exposures) == 0 {
		response += "No active positions\n"
	} else {
		response += fmt.Sprintf("%-6s %10s / %-10s %6s %8s\n", "Symbol", "Exposure", "Cap", "Usage", "Trades")
		response += strings.Repeat("-", 50) + "\n"
		
		for symbol, info := range exposures {
			usage := (info.CurrentExposureUSD / info.SymbolCapUSD) * 100
			status := "✅"
			if usage > 90 {
				status = "⚠️"
			} else if usage > 95 {
				status = "🚨"
			}
			
			response += fmt.Sprintf("%-6s %10.0f / %-10.0f %5.1f%% %3d/%-2d %s\n",
				symbol,
				info.CurrentExposureUSD,
				info.SymbolCapUSD,
				usage,
				info.DailyTradesCount,
				info.DailyTradesLimit,
				status,
			)
		}
	}
	
	response += "```\n"
	response += fmt.Sprintf("🎯 *Portfolio Concentration*: %.1f%% (%s)\n", maxConcentration, maxSymbol)
	response += fmt.Sprintf("📅 Last updated: %s", time.Now().Format("15:04:05 MST"))
	
	return response, nil
}

// formatCooldownsStatus creates a formatted display of active cooldowns
func (src *SlackRiskControls) formatCooldownsStatus() (string, error) {
	cooldowns := src.cooldownManager.GetAllCooldowns()
	
	response := "🔄 *Active Cooldowns*\n"
	
	if len(cooldowns) == 0 {
		response += "No active cooldowns\n"
		return response, nil
	}
	
	response += "```\n"
	response += fmt.Sprintf("%-6s %12s %8s %10s\n", "Symbol", "Last Trade", "Remaining", "Type")
	response += strings.Repeat("-", 40) + "\n"
	
	for symbol, info := range cooldowns {
		if info.RemainingCooldown > 0 {
			remainingStr := fmt.Sprintf("%.0fs", info.RemainingCooldown.Seconds())
			response += fmt.Sprintf("%-6s %12s %8s %10s\n",
				symbol,
				info.LastTradeTime.Format("15:04:05"),
				remainingStr,
				info.CooldownType,
			)
		}
	}
	
	response += "```\n"
	response += fmt.Sprintf("📅 As of: %s", time.Now().Format("15:04:05 MST"))
	
	return response, nil
}

// recordAudit writes an audit entry to the audit log
func (src *SlackRiskControls) recordAudit(action, userID, symbol string, oldValue, newValue interface{}, ttl, reason string, metadata map[string]interface{}) {
	entry := AuditEntry{
		Timestamp: time.Now(),
		Action:    action,
		UserID:    userID,
		Symbol:    symbol,
		OldValue:  oldValue,
		NewValue:  newValue,
		TTL:       ttl,
		Reason:    reason,
		Metadata:  metadata,
	}
	
	// Append to audit log
	if src.auditPath != "" {
		if data, err := json.Marshal(entry); err == nil {
			// Atomic append
			f, err := os.OpenFile(src.auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err == nil {
				f.WriteString(string(data) + "\n")
				f.Close()
			}
		}
	}
	
	// Also record as metric
	observ.IncCounter("risk_control_changes_total", map[string]string{
		"action": action,
		"symbol": symbol,
		"user":   userID,
	})
}

// sendCapChangeNotification sends a notification about cap changes to the channel
func (src *SlackRiskControls) sendCapChangeNotification(symbol, userID string, oldCap, newCap float64, effectiveUntil time.Time, reason string) {
	message := fmt.Sprintf("📊 *Position Cap Updated*\n")
	message += fmt.Sprintf("Symbol: %s\n", symbol)
	message += fmt.Sprintf("Old cap: $%.0f → New cap: $%.0f\n", oldCap, newCap)
	message += fmt.Sprintf("Updated by: <@%s>\n", userID)
	message += fmt.Sprintf("Effective until: %s\n", effectiveUntil.Format("15:04:05 MST"))
	message += fmt.Sprintf("Reason: %s", reason)
	
	// Send to risk channel
	src.slackClient.SendMessage("#trading-risk", message)
}

// sendEmergencyOverrideAlert sends an alert for emergency cap overrides
func (src *SlackRiskControls) sendEmergencyOverrideAlert(symbol, userID string, oldCap, newCap float64, effectiveUntil time.Time, reason string) {
	message := fmt.Sprintf("🚨 *EMERGENCY CAP OVERRIDE*\n")
	message += fmt.Sprintf("Symbol: %s\n", symbol)
	message += fmt.Sprintf("Emergency cap: $%.0f (was $%.0f)\n", newCap, oldCap)
	message += fmt.Sprintf("Override by: <@%s>\n", userID)
	message += fmt.Sprintf("Expires: %s\n", effectiveUntil.Format("15:04:05 MST"))
	message += fmt.Sprintf("Reason: %s", reason)
	
	// Send to both risk and alerts channels
	src.slackClient.SendMessage("#trading-risk", message)
	src.slackClient.SendMessage("#trading-alerts", message)
}