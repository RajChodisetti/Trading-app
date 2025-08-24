package risk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// addEvent adds a new event to the circuit breaker event log
func (cb *CircuitBreaker) addEvent(eventType string, data map[string]interface{}, correlationID, userID, reason string) {
	cb.lastEventID++
	
	event := CircuitBreakerEvent{
		ID:            fmt.Sprintf("cb_%d", cb.lastEventID),
		Timestamp:     time.Now(),
		Type:          eventType,
		Data:          data,
		CorrelationID: correlationID,
		UserID:        userID,
		Reason:        reason,
	}
	
	cb.events = append(cb.events, event)
	
	// Persist event asynchronously (in production, might use a queue)
	go func() {
		if err := cb.persistEvent(event); err != nil {
			observ.IncCounter("circuit_breaker_persist_errors_total", map[string]string{
				"event_type": eventType,
			})
		}
	}()
	
	// Update metrics
	observ.IncCounter("circuit_breaker_events_total", map[string]string{
		"event_type": eventType,
		"user_id":    userID,
	})
}

// persistEvent appends an event to the append-only event log
func (cb *CircuitBreaker) persistEvent(event CircuitBreakerEvent) error {
	// Create directory if it doesn't exist
	if err := os.MkdirAll("data", 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}
	
	// Open file in append mode
	file, err := os.OpenFile(cb.eventLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open event log: %w", err)
	}
	defer file.Close()
	
	// Serialize event as JSON
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}
	
	// Write to file with newline
	_, err = fmt.Fprintf(file, "%s\n", eventJSON)
	if err != nil {
		return fmt.Errorf("failed to write event: %w", err)
	}
	
	return nil
}

// loadEvents loads all events from the event log file
func (cb *CircuitBreaker) loadEvents() error {
	file, err := os.Open(cb.eventLog)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No events file yet - not an error
		}
		return fmt.Errorf("failed to open event log: %w", err)
	}
	defer file.Close()
	
	scanner := bufio.NewScanner(file)
	events := make([]CircuitBreakerEvent, 0)
	
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		
		var event CircuitBreakerEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			observ.IncCounter("circuit_breaker_parse_errors_total", map[string]string{
				"line": strconv.Itoa(lineNum),
			})
			continue // Skip malformed events
		}
		
		events = append(events, event)
		
		// Update last event ID
		if strings.HasPrefix(event.ID, "cb_") {
			if idStr := strings.TrimPrefix(event.ID, "cb_"); idStr != "" {
				if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
					if id > cb.lastEventID {
						cb.lastEventID = id
					}
				}
			}
		}
	}
	
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading event log: %w", err)
	}
	
	cb.events = events
	observ.SetGauge("circuit_breaker_events_loaded", float64(len(events)), nil)
	
	return nil
}

// replayEvents rebuilds circuit breaker state from event log
func (cb *CircuitBreaker) replayEvents() error {
	if len(cb.events) == 0 {
		return nil // No events to replay
	}
	
	// Reset state to initial
	cb.state = StateNormal
	cb.stateEnteredAt = time.Now()
	cb.sizeMultiplier = 1.0
	cb.manualHalt = false
	cb.manualRecovery = false
	cb.triggerCounts = make(map[string]int)
	
	// Replay events in order
	for _, event := range cb.events {
		if err := cb.applyEvent(event); err != nil {
			observ.IncCounter("circuit_breaker_replay_errors_total", map[string]string{
				"event_id":   event.ID,
				"event_type": event.Type,
			})
			// Continue replaying other events
		}
	}
	
	observ.IncCounter("circuit_breaker_replays_total", map[string]string{
		"events_count": strconv.Itoa(len(cb.events)),
	})
	
	return nil
}

// applyEvent applies a single event to update circuit breaker state
func (cb *CircuitBreaker) applyEvent(event CircuitBreakerEvent) error {
	switch event.Type {
	case EventStateChanged:
		return cb.applyStateChangedEvent(event)
		
	case EventManualOverride:
		return cb.applyManualOverrideEvent(event)
		
	case EventRecoveryInitiated:
		return cb.applyRecoveryInitiatedEvent(event)
		
	case EventConfigChanged:
		return cb.applyConfigChangedEvent(event)
		
	case EventNavUpdated, EventThresholdBreached, EventCoolingOffExpired:
		// These events don't directly change state during replay
		// State changes are captured by EventStateChanged
		return nil
		
	default:
		return fmt.Errorf("unknown event type: %s", event.Type)
	}
}

// applyStateChangedEvent applies a state change event
func (cb *CircuitBreaker) applyStateChangedEvent(event CircuitBreakerEvent) error {
	newStateStr, ok := event.Data["new_state"].(string)
	if !ok {
		return fmt.Errorf("missing or invalid new_state in event")
	}
	
	newState := CircuitBreakerState(newStateStr)
	sizeMultiplier, ok := event.Data["size_multiplier"].(float64)
	if !ok {
		sizeMultiplier = cb.getSizeMultiplierForState(newState)
	}
	
	// Update state without creating new events (we're replaying)
	cb.state = newState
	cb.stateEnteredAt = event.Timestamp
	cb.sizeMultiplier = sizeMultiplier
	
	// Update trigger counts
	if event.Reason != "" {
		cb.triggerCounts[event.Reason]++
	}
	
	// Handle special states
	switch newState {
	case StateHalted:
		// Set cooling off period (may be overridden by subsequent events)
		cb.coolingOffUntil = event.Timestamp.Add(cb.recoveryRequirements.CooldownPeriod)
		
	case StateCoolingOff:
		// Calculate cooling off end time
		if cooldownPeriod, ok := event.Data["cooldown_period_seconds"].(float64); ok {
			cb.coolingOffUntil = event.Timestamp.Add(time.Duration(cooldownPeriod) * time.Second)
		} else {
			cb.coolingOffUntil = event.Timestamp.Add(cb.recoveryRequirements.CooldownPeriod)
		}
	}
	
	return nil
}

// applyManualOverrideEvent applies a manual override event
func (cb *CircuitBreaker) applyManualOverrideEvent(event CircuitBreakerEvent) error {
	action, ok := event.Data["action"].(string)
	if !ok {
		return fmt.Errorf("missing action in manual override event")
	}
	
	switch action {
	case "halt":
		cb.manualHalt = true
		cb.manualRecovery = false
		
	case "recovery":
		cb.manualHalt = false
		cb.manualRecovery = true
		
	default:
		return fmt.Errorf("unknown manual override action: %s", action)
	}
	
	cb.overrideUser = event.UserID
	cb.overrideReason = event.Reason
	
	return nil
}

// applyRecoveryInitiatedEvent applies a recovery initiation event
func (cb *CircuitBreaker) applyRecoveryInitiatedEvent(event CircuitBreakerEvent) error {
	cb.manualRecovery = true
	cb.manualHalt = false
	cb.overrideUser = event.UserID
	cb.overrideReason = event.Reason
	
	return nil
}

// applyConfigChangedEvent applies a configuration change event
func (cb *CircuitBreaker) applyConfigChangedEvent(event CircuitBreakerEvent) error {
	// For now, just log that config changed
	// In a full implementation, we'd deserialize and apply the new config
	observ.IncCounter("circuit_breaker_config_changes_total", map[string]string{
		"user_id": event.UserID,
	})
	
	return nil
}

// GetEventHistory returns recent events for analysis and debugging
func (cb *CircuitBreaker) GetEventHistory(maxEvents int, eventTypes []string) []CircuitBreakerEvent {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	
	// Filter by event types if specified
	var filtered []CircuitBreakerEvent
	if len(eventTypes) > 0 {
		typeMap := make(map[string]bool)
		for _, t := range eventTypes {
			typeMap[t] = true
		}
		
		for _, event := range cb.events {
			if typeMap[event.Type] {
				filtered = append(filtered, event)
			}
		}
	} else {
		filtered = cb.events
	}
	
	// Return most recent events
	if maxEvents <= 0 || maxEvents >= len(filtered) {
		result := make([]CircuitBreakerEvent, len(filtered))
		copy(result, filtered)
		return result
	}
	
	start := len(filtered) - maxEvents
	result := make([]CircuitBreakerEvent, maxEvents)
	copy(result, filtered[start:])
	return result
}

// CompactEventLog removes old events to keep log size manageable
func (cb *CircuitBreaker) CompactEventLog(keepDays int) error {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	if keepDays <= 0 {
		keepDays = 30 // Default keep 30 days
	}
	
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	
	// Find events to keep
	var keptEvents []CircuitBreakerEvent
	for _, event := range cb.events {
		if event.Timestamp.After(cutoff) {
			keptEvents = append(keptEvents, event)
		}
	}
	
	// If no compaction needed, return
	if len(keptEvents) == len(cb.events) {
		return nil
	}
	
	// Backup old log
	backupPath := cb.eventLog + ".backup." + time.Now().Format("20060102")
	if err := os.Rename(cb.eventLog, backupPath); err != nil {
		return fmt.Errorf("failed to backup event log: %w", err)
	}
	
	// Write compacted events
	file, err := os.Create(cb.eventLog)
	if err != nil {
		return fmt.Errorf("failed to create compacted event log: %w", err)
	}
	defer file.Close()
	
	for _, event := range keptEvents {
		eventJSON, err := json.Marshal(event)
		if err != nil {
			continue // Skip malformed events
		}
		fmt.Fprintf(file, "%s\n", eventJSON)
	}
	
	// Update in-memory events
	cb.events = keptEvents
	
	observ.IncCounter("circuit_breaker_compactions_total", map[string]string{
		"events_removed": strconv.Itoa(len(cb.events) - len(keptEvents)),
		"events_kept":    strconv.Itoa(len(keptEvents)),
	})
	
	return nil
}

// GetEventSummary returns a summary of events for the current day
func (cb *CircuitBreaker) GetEventSummary() map[string]interface{} {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	
	today := time.Now().UTC().Format("2006-01-02")
	
	summary := map[string]interface{}{
		"date":          today,
		"total_events":  0,
		"by_type":       make(map[string]int),
		"by_user":       make(map[string]int),
		"state_changes": make([]map[string]interface{}, 0),
		"manual_actions": 0,
		"halt_count":    0,
	}
	
	for _, event := range cb.events {
		if event.Timestamp.UTC().Format("2006-01-02") != today {
			continue
		}
		
		summary["total_events"] = summary["total_events"].(int) + 1
		
		// Count by type
		byType := summary["by_type"].(map[string]int)
		byType[event.Type]++
		
		// Count by user
		if event.UserID != "" {
			byUser := summary["by_user"].(map[string]int)
			byUser[event.UserID]++
		}
		
		// Track state changes
		if event.Type == EventStateChanged {
			stateChanges := summary["state_changes"].([]map[string]interface{})
			stateChange := map[string]interface{}{
				"timestamp":      event.Timestamp,
				"previous_state": event.Data["previous_state"],
				"new_state":      event.Data["new_state"],
				"reason":         event.Reason,
			}
			summary["state_changes"] = append(stateChanges, stateChange)
			
			// Count halts
			if newState, ok := event.Data["new_state"].(string); ok {
				if newState == string(StateHalted) || newState == string(StateEmergency) {
					summary["halt_count"] = summary["halt_count"].(int) + 1
				}
			}
		}
		
		// Count manual actions
		if event.Type == EventManualOverride || event.Type == EventRecoveryInitiated {
			summary["manual_actions"] = summary["manual_actions"].(int) + 1
		}
	}
	
	return summary
}

// ValidateEventIntegrity checks the integrity of the event log
func (cb *CircuitBreaker) ValidateEventIntegrity() error {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	
	// Check for required fields in all events
	for i, event := range cb.events {
		if event.ID == "" {
			return fmt.Errorf("event %d missing ID", i)
		}
		if event.Type == "" {
			return fmt.Errorf("event %s missing type", event.ID)
		}
		if event.Timestamp.IsZero() {
			return fmt.Errorf("event %s missing timestamp", event.ID)
		}
	}
	
	// Check for monotonic timestamps (events should be ordered)
	for i := 1; i < len(cb.events); i++ {
		if cb.events[i].Timestamp.Before(cb.events[i-1].Timestamp) {
			return fmt.Errorf("event %s has timestamp before previous event", cb.events[i].ID)
		}
	}
	
	// Check for state consistency
	state := StateNormal
	for _, event := range cb.events {
		if event.Type == EventStateChanged {
			if newStateStr, ok := event.Data["new_state"].(string); ok {
				state = CircuitBreakerState(newStateStr)
			}
		}
	}
	
	// Final state should match current state
	if state != cb.state {
		return fmt.Errorf("replayed state %s doesn't match current state %s", state, cb.state)
	}
	
	return nil
}