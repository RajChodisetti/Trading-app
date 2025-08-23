package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

// HTTPClient implements Client interface for HTTP polling transport (fallback)
type HTTPClient struct {
	config      Config
	url         string
	eventChan   chan EventEnvelope
	lastEventID string
	state       int32 // atomic ConnectionState
	
	client *http.Client
	cancel context.CancelFunc
	
	// Metrics
	messagesReceived int64
	pollCount       int64
}

// NewHTTPClient creates a new HTTP polling client
func NewHTTPClient(config Config) (*HTTPClient, error) {
	if config.MaxChannelBuffer <= 0 {
		config.MaxChannelBuffer = 1000 // Smaller buffer for polling
	}
	
	client := &HTTPClient{
		config:    config,
		url:       config.BaseURL + "/stream", // Same endpoint, different method
		eventChan: make(chan EventEnvelope, config.MaxChannelBuffer),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
	
	atomic.StoreInt32(&client.state, int32(StateDisconnected))
	return client, nil
}

// Start begins HTTP polling
func (c *HTTPClient) Start(ctx context.Context) (<-chan EventEnvelope, error) {
	ctx, c.cancel = context.WithCancel(ctx)
	
	go c.pollLoop(ctx)
	
	return c.eventChan, nil
}

// Close shuts down the HTTP client
func (c *HTTPClient) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	close(c.eventChan)
	return nil
}

// LastEventID returns the last processed event ID
func (c *HTTPClient) LastEventID() string {
	return c.lastEventID
}

// ConnectionState returns current connection state
func (c *HTTPClient) ConnectionState() ConnectionState {
	return ConnectionState(atomic.LoadInt32(&c.state))
}

// pollLoop handles HTTP polling with cursor-based pagination
func (c *HTTPClient) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second) // Poll every second
	defer ticker.Stop()
	
	atomic.StoreInt32(&c.state, int32(StateConnected))
	
	for {
		select {
		case <-ctx.Done():
			atomic.StoreInt32(&c.state, int32(StateDisconnected))
			return
		case <-ticker.C:
			if err := c.pollOnce(ctx); err != nil {
				log.Printf("HTTP poll error: %v", err)
				atomic.StoreInt32(&c.state, int32(StateDisconnected))
				time.Sleep(time.Second) // Brief pause on error
				atomic.StoreInt32(&c.state, int32(StateConnected))
			}
		}
	}
}

// pollOnce performs a single HTTP poll request
func (c *HTTPClient) pollOnce(ctx context.Context) error {
	atomic.AddInt64(&c.pollCount, 1)
	
	// Build URL with cursor
	pollURL := c.url
	if c.lastEventID != "" {
		u, _ := url.Parse(pollURL)
		q := u.Query()
		q.Set("cursor", c.lastEventID)
		u.RawQuery = q.Encode()
		pollURL = u.String()
	}
	
	req, err := http.NewRequestWithContext(ctx, "GET", pollURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	
	// Parse response - expecting array of events
	var response struct {
		Events []map[string]interface{} `json:"events"`
		Cursor string                   `json:"cursor"`
	}
	
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	
	// Process each event
	for _, event := range response.Events {
		envelope, err := c.parseEvent(event)
		if err != nil {
			log.Printf("HTTP parse event error: %v", err)
			continue
		}
		
		select {
		case c.eventChan <- envelope:
			atomic.AddInt64(&c.messagesReceived, 1)
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Channel full, drop event
			log.Printf("HTTP: dropping event due to backpressure")
		}
	}
	
	// Update cursor
	if response.Cursor != "" {
		c.lastEventID = response.Cursor
	}
	
	return nil
}

// parseEvent converts HTTP response event to EventEnvelope
func (c *HTTPClient) parseEvent(event map[string]interface{}) (EventEnvelope, error) {
	// Extract fields
	eventType, _ := event["type"].(string)
	eventID, _ := event["id"].(string)
	
	// Parse timestamp
	var ts time.Time
	if tsStr, ok := event["ts_utc"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339, tsStr); err == nil {
			ts = parsed
		}
	}
	if ts.IsZero() {
		ts = time.Now()
	}
	
	// Marshal payload
	payload, err := json.Marshal(event)
	if err != nil {
		return EventEnvelope{}, fmt.Errorf("marshal payload: %w", err)
	}
	
	return EventEnvelope{
		V:       1,
		Type:    eventType,
		ID:      eventID,
		TS:      ts,
		Payload: payload,
	}, nil
}

// GetMetrics returns current client metrics
func (c *HTTPClient) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"connection_state":  c.ConnectionState().String(),
		"messages_received": atomic.LoadInt64(&c.messagesReceived),
		"poll_count":        atomic.LoadInt64(&c.pollCount),
		"last_event_id":     c.lastEventID,
	}
}