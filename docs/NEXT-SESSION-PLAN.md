# Session 11 Plan: Wire WebSocket/SSE Streaming Transport

## Overview

Replace the current HTTP polling wire stub with real-time WebSocket or Server-Sent Events (SSE) streaming transport. Maintain deterministic testing while enabling low-latency data ingestion for production readiness.

## What's Changed vs. Previous Sessions

- **Real-time Transport**: Move from polling-based HTTP to push-based streaming for sub-second latency
- **Protocol Choice**: Evaluate WebSocket vs SSE based on simplicity and browser compatibility
- **Backward Compatibility**: Maintain existing wire protocol contracts while upgrading transport
- **Testing Strategy**: Keep deterministic fixture-based testing with streaming simulation

## Acceptance Criteria

- **Streaming Transport**: WebSocket or SSE client connects to wire stub and receives real-time events
- **Connection Management**: Automatic reconnection with exponential backoff, connection health monitoring
- **Protocol Compatibility**: All existing wire protocol events stream correctly with proper ordering
- **Testing**: `make test` wire_mode case passes with streaming; fixtures drive deterministic event sequences
- **Metrics**: Connection state, reconnection attempts, message rates, and latency tracking
- **Graceful Degradation**: Falls back to HTTP polling if streaming unavailable

## Implementation Plan

### 1) Protocol Selection & Architecture (15 min)

**Decision Criteria:**
- **WebSocket**: Full-duplex, binary support, connection overhead
- **SSE**: Simpler, HTTP/2 compatible, browser-friendly, text-only
- **Choice**: Start with SSE for simplicity, upgrade path to WebSocket later

**Architecture:**
```
Wire Stub Server (SSE) ←→ Decision Engine Client
     ↓ streams                    ↓ processes  
Fixture Events              → Decision Flow
```

### 2) SSE Wire Stub Server (20 min)

**Enhanced cmd/stubs/main.go:**
- Add `/stream` SSE endpoint alongside existing HTTP endpoints
- Stream fixture events with proper `data:`, `event:`, `id:` formatting
- Maintain cursor-based event ordering and replay capability
- Add connection tracking and client management

**SSE Event Format:**
```
event: tick
id: 1692123456789
data: {"symbol":"AAPL","last":210.02,"ts_utc":"2025-08-23T15:30:00Z"}

event: news
id: 1692123456790
data: {"symbol":"AAPL","score":0.7,"provider":"reuters","ts_utc":"2025-08-23T15:30:01Z"}
```

### 3) SSE Client Implementation (25 min)

**New internal/transport/sse.go:**
```go
type SSEClient struct {
    url           string
    eventChan     chan Event
    reconnectFunc func() error
    healthCheck   func() bool
}

func (c *SSEClient) Connect() error
func (c *SSEClient) Subscribe() <-chan Event
func (c *SSEClient) Reconnect() error
func (c *SSEClient) Close() error
```

**Connection Management:**
- Exponential backoff: 1s → 2s → 4s → 8s → 16s max
- Health checks every 30s with ping/pong or heartbeat events
- Graceful shutdown and cleanup on errors

### 4) Integration with Decision Engine (15 min)

**Update cmd/decision/main.go:**
- Add `-streaming` flag to enable SSE mode vs HTTP polling
- Wire streaming client into existing event processing loop
- Maintain same event handling logic, just different transport
- Add streaming metrics to observability endpoint

**Event Processing:**
```go
if *streaming {
    client := transport.NewSSEClient(wireURL + "/stream")
    eventChan := client.Subscribe()
    for event := range eventChan {
        processWireEvent(event) // existing logic unchanged
    }
}
```

### 5) Testing & Validation (15 min)

**Enhanced scripts/run-tests.sh:**
- Update wire_mode test to use streaming by default
- Add fallback test for HTTP polling compatibility
- Test reconnection scenarios with stub server restart
- Validate event ordering and cursor behavior

**Test Scenarios:**
- Normal streaming operation
- Network interruption and reconnection
- Server restart during streaming
- Mixed HTTP/SSE compatibility

## Success Metrics

### Technical Validation
- [ ] SSE connection established and events streaming
- [ ] Automatic reconnection after network/server failures
- [ ] Event ordering preserved across reconnections
- [ ] Latency improvement: <100ms vs previous polling intervals
- [ ] All existing wire_mode tests pass with streaming

### Operational Readiness
- [ ] Connection state visible in metrics endpoint
- [ ] Reconnection attempts and success rates tracked
- [ ] Message throughput and latency monitoring
- [ ] Graceful degradation to HTTP polling working

### Code Quality
- [ ] SSE client properly abstracts transport layer
- [ ] Existing decision engine code unchanged
- [ ] Clean configuration switches between modes
- [ ] Error handling and logging comprehensive

## Risk Mitigation

### Technical Risks
- **SSE browser compatibility**: Use standard EventSource API, polyfills available
- **Connection drops**: Implement robust reconnection with cursor resume
- **Memory leaks**: Proper goroutine cleanup and connection management
- **Event ordering**: Maintain sequence numbers and cursor-based replay

### Operational Risks
- **Wire stub stability**: Keep HTTP fallback for reliability
- **Debugging complexity**: Enhance logging for connection lifecycle
- **Performance regression**: Monitor latency and throughput vs polling

## Implementation Notes

### Configuration Updates
```yaml
wire:
  enabled: true
  url: "ws://localhost:8091"  # or http:// for polling
  transport: "sse"            # or "http" for polling
  reconnect:
    initial_delay_ms: 1000
    max_delay_ms: 16000
    max_attempts: -1          # infinite
  health_check_interval_s: 30
```

### Metrics Extensions
```json
{
  "wire_transport": "sse",
  "connection_state": "connected",
  "reconnect_attempts": 3,
  "last_reconnect": "2025-08-23T15:30:00Z",
  "messages_received": 1247,
  "average_latency_ms": 45
}
```

## Next Session Preview

**Session 12: Real Adapter Integrations**
- Replace mock providers with real market data APIs
- Start with quotes provider (Alpha Vantage, IEX, etc.)
- Implement API key management and rate limiting
- Maintain paper trading safety throughout

## Dependencies & Prerequisites

- Session 10 risk controls must be fully functional
- Wire stub HTTP endpoints remain available for fallback
- Existing fixture-based testing framework intact
- Metrics and observability infrastructure operational

---

**Estimated Duration**: 90 minutes
**Risk Level**: Medium (transport changes, connection management)
**Reversibility**: High (HTTP fallback maintained, feature flags)
**Evidence Required**: Streaming latency <100ms, reconnection working, all tests pass