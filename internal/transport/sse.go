package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SSEClient implements Client interface for Server-Sent Events transport
type SSEClient struct {
	config      Config
	url         string
	eventChan   chan EventEnvelope
	lastEventID string
	state       int32 // atomic ConnectionState
	
	// Connection management
	client     *http.Client
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.RWMutex
	
	// Metrics
	reconnectAttempts int64
	messagesReceived  int64
	dupesDropped      int64
	gapsDetected      int64
	consecutiveFailures int64
}

// NewSSEClient creates a new SSE client with the given configuration
func NewSSEClient(config Config) (*SSEClient, error) {
	if config.MaxChannelBuffer <= 0 {
		config.MaxChannelBuffer = 10000
	}
	
	if config.HeartbeatSeconds <= 0 {
		config.HeartbeatSeconds = 10
	}
	
	client := &SSEClient{
		config:    config,
		url:       config.BaseURL + "/stream",
		eventChan: make(chan EventEnvelope, config.MaxChannelBuffer),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	
	atomic.StoreInt32(&client.state, int32(StateDisconnected))
	return client, nil
}

// Start begins consuming SSE events
func (c *SSEClient) Start(ctx context.Context) (<-chan EventEnvelope, error) {
	ctx, c.cancel = context.WithCancel(ctx)
	
	c.wg.Add(1)
	go c.consumeLoop(ctx)
	
	return c.eventChan, nil
}

// Close shuts down the SSE client
func (c *SSEClient) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	close(c.eventChan)
	return nil
}

// LastEventID returns the last processed event ID
func (c *SSEClient) LastEventID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastEventID
}

// ConnectionState returns current connection state
func (c *SSEClient) ConnectionState() ConnectionState {
	return ConnectionState(atomic.LoadInt32(&c.state))
}

// consumeLoop handles the main SSE connection and reconnection logic
func (c *SSEClient) consumeLoop(ctx context.Context) {
	defer c.wg.Done()
	
	backoff := c.config.Reconnect.InitialDelayMs
	
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		
		// Check for fallback to HTTP after consecutive failures
		if atomic.LoadInt64(&c.consecutiveFailures) >= int64(c.config.FallbackAfterFailures) && c.config.FallbackAfterFailures > 0 {
			log.Printf("SSE: %d consecutive failures, falling back to HTTP polling", c.consecutiveFailures)
			// TODO: Signal fallback to parent
			return
		}
		
		atomic.StoreInt32(&c.state, int32(StateConnecting))
		
		if err := c.connectAndConsume(ctx); err != nil {
			atomic.AddInt64(&c.consecutiveFailures, 1)
			atomic.StoreInt32(&c.state, int32(StateDisconnected))
			
			if ctx.Err() != nil {
				return // Context cancelled
			}
			
			log.Printf("SSE connection failed: %v, reconnecting in %dms", err, backoff)
			
			// Exponential backoff with jitter
			jitter := rand.Intn(c.config.Reconnect.JitterMs)
			delay := time.Duration(backoff+jitter) * time.Millisecond
			
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			
			// Increase backoff for next attempt
			backoff *= 2
			if backoff > c.config.Reconnect.MaxDelayMs {
				backoff = c.config.Reconnect.MaxDelayMs
			}
			
			atomic.AddInt64(&c.reconnectAttempts, 1)
		} else {
			// Successful connection, reset backoff and failure count
			backoff = c.config.Reconnect.InitialDelayMs
			atomic.StoreInt64(&c.consecutiveFailures, 0)
		}
	}
}

// connectAndConsume establishes SSE connection and processes events
func (c *SSEClient) connectAndConsume(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	
	// Set SSE headers
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	
	// Resume from last event ID if available
	c.mu.RLock()
	lastID := c.lastEventID
	c.mu.RUnlock()
	
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	
	atomic.StoreInt32(&c.state, int32(StateConnected))
	log.Printf("SSE connected to %s", c.url)
	
	return c.processEventStream(ctx, resp.Body)
}

// processEventStream reads and parses SSE events from the response body
func (c *SSEClient) processEventStream(ctx context.Context, body io.Reader) error {
	scanner := bufio.NewScanner(body)
	
	var eventType, eventID, eventData string
	seenIDs := make(map[string]bool) // Simple duplicate detection
	
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		
		line := scanner.Text()
		
		// Handle SSE format: field:value or comment
		if strings.HasPrefix(line, ":") {
			// Comment line (heartbeat)
			continue
		}
		
		if line == "" {
			// End of event, process it
			if eventType != "" && eventID != "" && eventData != "" {
				if err := c.processEvent(eventType, eventID, eventData, seenIDs); err != nil {
					log.Printf("SSE process event error: %v", err)
				}
			}
			
			// Reset for next event
			eventType, eventID, eventData = "", "", ""
			continue
		}
		
		// Parse field:value
		if colonPos := strings.Index(line, ":"); colonPos > 0 {
			field := line[:colonPos]
			value := strings.TrimSpace(line[colonPos+1:])
			
			switch field {
			case "event":
				eventType = value
			case "id":
				eventID = value
			case "data":
				eventData = value
			}
		}
	}
	
	return scanner.Err()
}

// processEvent validates and enqueues a single SSE event
func (c *SSEClient) processEvent(eventType, eventID, eventData string, seenIDs map[string]bool) error {
	// Duplicate detection
	if seenIDs[eventID] {
		atomic.AddInt64(&c.dupesDropped, 1)
		return nil
	}
	seenIDs[eventID] = true
	
	// Gap detection (simple numeric ID check)
	if c.lastEventID != "" {
		if lastNum, err := strconv.ParseInt(c.lastEventID, 10, 64); err == nil {
			if currentNum, err := strconv.ParseInt(eventID, 10, 64); err == nil {
				if currentNum > lastNum+1 {
					atomic.AddInt64(&c.gapsDetected, 1)
					log.Printf("SSE gap detected: last=%s, current=%s", c.lastEventID, eventID)
					// TODO: Trigger backfill
				}
			}
		}
	}
	
	// Parse event data
	var payload json.RawMessage
	if err := json.Unmarshal([]byte(eventData), &payload); err != nil {
		return fmt.Errorf("parse event data: %w", err)
	}
	
	envelope := EventEnvelope{
		V:       1,
		Type:    eventType,
		ID:      eventID,
		TS:      time.Now(), // TODO: Parse from payload if available
		Payload: payload,
	}
	
	// Try to enqueue event with backpressure handling
	select {
	case c.eventChan <- envelope:
		atomic.AddInt64(&c.messagesReceived, 1)
		
		// Update last event ID
		c.mu.Lock()
		c.lastEventID = eventID
		c.mu.Unlock()
		
	default:
		// Channel full, apply backpressure policy
		c.handleBackpressure(envelope)
	}
	
	return nil
}

// handleBackpressure implements drop policy when event channel is full
func (c *SSEClient) handleBackpressure(envelope EventEnvelope) {
	// Drop oldest events, prioritizing critical events (halts > news > ticks)
	for i := 0; i < len(c.eventChan); i++ {
		select {
		case oldEvent := <-c.eventChan:
			// Keep critical events, drop ticks first
			if envelope.Type == "halt" || envelope.Type == "news" {
				// Try to put critical event back
				select {
				case c.eventChan <- envelope:
					return
				default:
					// Still full, drop old tick
					if oldEvent.Type == "tick" {
						continue // Drop the old tick, retry with critical event
					}
				}
			}
		default:
			return // Channel not actually full anymore
		}
	}
	
	// If we get here, drop the new event and count it
	log.Printf("SSE: dropping event due to backpressure: %s %s", envelope.Type, envelope.ID)
	atomic.AddInt64(&c.dupesDropped, 1) // Reuse counter for simplicity
}

// GetMetrics returns current client metrics
func (c *SSEClient) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"connection_state":     c.ConnectionState().String(),
		"reconnect_attempts":   atomic.LoadInt64(&c.reconnectAttempts),
		"messages_received":    atomic.LoadInt64(&c.messagesReceived),
		"dupes_dropped":        atomic.LoadInt64(&c.dupesDropped),
		"gaps_detected":        atomic.LoadInt64(&c.gapsDetected),
		"consecutive_failures": atomic.LoadInt64(&c.consecutiveFailures),
		"last_event_id":        c.LastEventID(),
	}
}