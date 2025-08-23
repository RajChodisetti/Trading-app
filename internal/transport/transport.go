package transport

import (
	"context"
	"encoding/json"
	"time"
)

// EventEnvelope wraps all wire events with metadata for ordering and resume
type EventEnvelope struct {
	V       int             `json:"v"`        // Version for future compatibility
	Type    string          `json:"type"`     // Event type: tick, news, halt, etc.
	ID      string          `json:"id"`       // Monotonic ID for ordering and deduplication
	TS      time.Time       `json:"ts_utc"`   // Server timestamp when event was emitted
	Payload json.RawMessage `json:"payload"`  // Raw event data
}

// Client represents a wire transport client (SSE, WebSocket, HTTP polling)
type Client interface {
	// Start begins consuming events and returns a channel of envelopes
	// Context cancellation stops the client gracefully
	Start(ctx context.Context) (<-chan EventEnvelope, error)
	
	// Close shuts down the client and cleans up resources
	Close() error
	
	// LastEventID returns the last successfully processed event ID for resume
	LastEventID() string
	
	// ConnectionState returns current connection state for metrics
	ConnectionState() ConnectionState
}

// ConnectionState represents the current state of a transport connection
type ConnectionState int

const (
	StateDisconnected ConnectionState = iota // 0 = down
	StateConnecting                          // 1 = connecting  
	StateConnected                           // 2 = up
)

// String returns human-readable connection state
func (s ConnectionState) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting" 
	case StateConnected:
		return "connected"
	default:
		return "unknown"
	}
}

// Transport configuration for different client types
type Config struct {
	// Common settings
	BaseURL   string        `yaml:"base_url"`
	Transport string        `yaml:"transport"` // "sse", "ws", or "http"
	Timeout   time.Duration `yaml:"timeout"`
	
	// Reconnection settings
	Reconnect ReconnectConfig `yaml:"reconnect"`
	
	// SSE-specific settings
	HeartbeatSeconds     int `yaml:"heartbeat_seconds"`
	MaxChannelBuffer     int `yaml:"max_channel_buffer"`
	FallbackAfterFailures int `yaml:"fallback_to_http_after_failures"`
	
	// Test settings
	MaxEvents     int           `yaml:"max_events"`      // For bounded test runs
	MaxDuration   time.Duration `yaml:"max_duration"`    // For bounded test runs
}

type ReconnectConfig struct {
	InitialDelayMs int `yaml:"initial_delay_ms"`
	MaxDelayMs     int `yaml:"max_delay_ms"`
	MaxAttempts    int `yaml:"max_attempts"`    // -1 for infinite
	JitterMs       int `yaml:"jitter_ms"`
}

// ClientFactory creates transport clients based on configuration
func NewClient(config Config) (Client, error) {
	switch config.Transport {
	case "sse":
		return NewSSEClient(config)
	case "http":
		return NewHTTPClient(config)
	default:
		return NewHTTPClient(config) // Default fallback
	}
}