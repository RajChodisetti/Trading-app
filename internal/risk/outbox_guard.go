package risk

import (
	"context"
	"fmt"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/adapters"
	"github.com/Rajchodisetti/trading-app/internal/observ"
	"github.com/Rajchodisetti/trading-app/internal/outbox"
)

// OutboxGuard provides pre-send validation to catch price drift violations
type OutboxGuard struct {
	capsManager   *PositionCapsManager
	quotesAdapter adapters.QuotesAdapter
	observMgr     *RiskObservabilityManager
}

// OrderRequest represents an order request with cap validation context
type OrderRequest struct {
	Order           outbox.Order               `json:"order"`
	DecisionTime    time.Time                  `json:"decision_time"`
	DecisionContext DecisionContext            `json:"decision_context"`
	CheckHash       string                     `json:"check_hash"`      // Snapshot of cap state at decision time
	ExposureInfo    *ExposureInfo             `json:"exposure_info"`   // Original exposure calculation
	CooldownInfo    *CooldownInfo             `json:"cooldown_info"`   // Original cooldown check
}

// GuardResult represents the result of pre-send validation
type GuardResult struct {
	Approved        bool                       `json:"approved"`
	Reason          string                     `json:"reason"`
	OriginalExposure *ExposureInfo             `json:"original_exposure"`
	CurrentExposure  *ExposureInfo             `json:"current_exposure"`
	PriceDrift      float64                    `json:"price_drift_pct"`
	TimeSinceDecision time.Duration            `json:"time_since_decision"`
}

// NewOutboxGuard creates a new outbox guard
func NewOutboxGuard(capsManager *PositionCapsManager, quotesAdapter adapters.QuotesAdapter, observMgr *RiskObservabilityManager) *OutboxGuard {
	return &OutboxGuard{
		capsManager:   capsManager,
		quotesAdapter: quotesAdapter,
		observMgr:     observMgr,
	}
}

// ValidateAndWriteOrder validates an order against current market conditions and writes it if approved
func (og *OutboxGuard) ValidateAndWriteOrder(outboxWriter *outbox.Outbox, request *OrderRequest) error {
	start := time.Now()
	
	// Perform pre-send validation
	result, err := og.validateOrder(request)
	if err != nil {
		return fmt.Errorf("outbox guard validation failed: %w", err)
	}
	
	// Record validation metrics
	og.recordValidationMetrics(request, result, time.Since(start))
	
	if !result.Approved {
		// Order was blocked - emit cancellation event and return error
		cancellationOrder := outbox.Order{
			ID:             request.Order.ID + "_cancelled",
			Symbol:         request.Order.Symbol,
			Intent:         "CANCELLED",
			Timestamp:      time.Now(),
			Status:         "cancelled",
			IdempotencyKey: request.Order.IdempotencyKey + "_cancelled",
		}
		
		// Write cancellation to outbox
		if err := outboxWriter.WriteOrder(cancellationOrder); err != nil {
			return fmt.Errorf("failed to write cancellation: %w", err)
		}
		
		// Log structured event
		og.observMgr.LogStructuredEvent(
			"paper_order_cancelled",
			SeverityWarning,
			"outbox_guard",
			fmt.Sprintf("Order cancelled due to pre-send validation: %s", result.Reason),
			map[string]interface{}{
				"order_id":           request.Order.ID,
				"symbol":             request.Order.Symbol,
				"reason":             result.Reason,
				"price_drift_pct":    result.PriceDrift,
				"time_since_decision": result.TimeSinceDecision.Seconds(),
			},
			map[string]float64{
				"price_drift_pct":     result.PriceDrift,
				"validation_time_ms":  float64(time.Since(start).Milliseconds()),
			},
			request.DecisionContext.CorrelationID,
		)
		
		return fmt.Errorf("order blocked by outbox guard: %s", result.Reason)
	}
	
	// Order approved - write to outbox
	return outboxWriter.WriteOrder(request.Order)
}

// validateOrder performs the actual pre-send validation
func (og *OutboxGuard) validateOrder(request *OrderRequest) (*GuardResult, error) {
	// Calculate time since decision
	timeSinceDecision := time.Since(request.DecisionTime)
	
	// Get current quote for price drift check
	quote, err := og.quotesAdapter.GetQuote(context.Background(), request.Order.Symbol)
	if err != nil {
		return &GuardResult{
			Approved:          false,
			Reason:            "quote_fetch_error",
			TimeSinceDecision: timeSinceDecision,
		}, nil
	}
	
	// Calculate current mid price
	currentMidPrice := request.DecisionContext.Price // Fallback to decision price
	if quote.Bid > 0 && quote.Ask > 0 {
		currentMidPrice = (quote.Bid + quote.Ask) / 2
	} else if quote.Last > 0 {
		currentMidPrice = quote.Last
	}
	
	// Calculate price drift
	originalPrice := request.ExposureInfo.MidPrice
	priceDrift := ((currentMidPrice - originalPrice) / originalPrice) * 100
	
	// Recalculate exposure with current price
	currentNAV := og.capsManager.getCurrentNAV()
	canIncrease, reason, currentExposureInfo, err := og.capsManager.CanIncrease(
		request.Order.Symbol,
		request.Order.Intent,
		request.DecisionContext.Quantity,
		currentMidPrice,
		currentNAV,
	)
	
	if err != nil {
		return &GuardResult{
			Approved:          false,
			Reason:            "exposure_recalc_error",
			TimeSinceDecision: timeSinceDecision,
		}, err
	}
	
	result := &GuardResult{
		OriginalExposure:  request.ExposureInfo,
		CurrentExposure:   currentExposureInfo,
		PriceDrift:        priceDrift,
		TimeSinceDecision: timeSinceDecision,
	}
	
	// Check if caps are now violated due to price drift
	if !canIncrease {
		result.Approved = false
		result.Reason = fmt.Sprintf("cap_violation_on_send_%s", reason)
		return result, nil
	}
	
	// Check for significant price drift (configurable threshold)
	maxDriftPct := 2.0 // 2% max drift before cancellation
	if abs(priceDrift) > maxDriftPct {
		result.Approved = false
		result.Reason = fmt.Sprintf("price_drift_%.2f_pct_exceeds_%.2f", abs(priceDrift), maxDriftPct)
		return result, nil
	}
	
	// Check staleness - if too much time has passed since decision
	maxStalenessSec := 10.0 // 10 second max staleness
	if timeSinceDecision.Seconds() > maxStalenessSec {
		result.Approved = false
		result.Reason = fmt.Sprintf("decision_stale_%.1fs_exceeds_%.1fs", timeSinceDecision.Seconds(), maxStalenessSec)
		return result, nil
	}
	
	// All checks passed
	result.Approved = true
	result.Reason = "outbox_guard_passed"
	
	return result, nil
}

// recordValidationMetrics records metrics for the validation process
func (og *OutboxGuard) recordValidationMetrics(request *OrderRequest, result *GuardResult, duration time.Duration) {
	labels := map[string]string{
		"symbol":   request.Order.Symbol,
		"approved": fmt.Sprintf("%t", result.Approved),
	}
	
	if !result.Approved {
		labels["reason"] = result.Reason
	}
	
	// Record validation metrics
	observ.IncCounter("outbox_guard_validations_total", labels)
	observ.Observe("outbox_guard_validation_duration_ms", float64(duration.Milliseconds()), labels)
	
	if !result.Approved {
		observ.IncCounter("paper_order_cancelled_total", map[string]string{
			"symbol": request.Order.Symbol,
			"reason": result.Reason,
		})
	}
	
	// Record price drift metrics
	if result.PriceDrift != 0 {
		observ.Observe("outbox_guard_price_drift_pct", abs(result.PriceDrift), map[string]string{
			"symbol": request.Order.Symbol,
		})
	}
	
	// Record staleness metrics
	observ.Observe("outbox_guard_decision_staleness_sec", result.TimeSinceDecision.Seconds(), map[string]string{
		"symbol": request.Order.Symbol,
	})
}

// Helper function to calculate absolute value
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// CreateOrderRequest creates an OrderRequest from decision context and exposure info
func CreateOrderRequest(order outbox.Order, decisionTime time.Time, decisionCtx DecisionContext, exposureInfo *ExposureInfo, cooldownInfo *CooldownInfo) *OrderRequest {
	// Create a simple hash from cap state for race detection
	checkHash := fmt.Sprintf("%.2f_%.2f_%d", 
		exposureInfo.SymbolCapUSD, 
		exposureInfo.NAV, 
		exposureInfo.DailyTradesCount,
	)
	
	return &OrderRequest{
		Order:           order,
		DecisionTime:    decisionTime,
		DecisionContext: decisionCtx,
		CheckHash:       checkHash,
		ExposureInfo:    exposureInfo,
		CooldownInfo:    cooldownInfo,
	}
}