# Session 7 Plan: Wire Stub Ingestion Loop

## Overview
Create a wire protocol stub that can ingest live-like data streams via HTTP/WebSocket/NATS, replacing the current fixture-based approach for more realistic testing and preparation for real adapter integration.

## Acceptance Criteria
- **Core Behavior**: System ingests streaming data via configurable wire protocol (HTTP polling, WebSocket, or NATS) instead of loading static fixtures
- **Success Evidence**: `make test` passes with wire stub generating equivalent data to fixtures, decision latency remains under 5ms, ingestion metrics captured

## Implementation Plan

### 1. Wire Stub Infrastructure (30-40 min)
- Create `internal/wire/` package with:
  - `stub.go` - HTTP server that serves fixture-like data via endpoints
  - `client.go` - HTTP/WebSocket client for ingesting from stub server
  - `types.go` - Common data structures for wire protocol
- Configure wire stub endpoints:
  - `GET /news` - Returns news events (with optional since parameter)
  - `GET /ticks` - Returns market data ticks
  - `GET /halts` - Returns halt status
  - `GET /earnings` - Returns earnings calendar
  - `WebSocket /stream` - Combined stream of all data types

### 2. Ingestion Loop Integration (20-30 min)
- Modify `cmd/decision/main.go` to support wire mode:
  - Add `-wire-mode` flag to switch between fixture and wire ingestion
  - Add `-wire-url` flag to specify stub server URL
  - Implement periodic polling loop (configurable interval, default 1s)
  - Maintain existing fixture mode for backward compatibility
- Update config structure:
  - Add `wire` section with polling intervals and endpoints
  - Environment variable overrides for wire settings

### 3. Testing & Validation (15-20 min)
- Extend `scripts/run-tests.sh` with Case 7: wire ingestion
- Start wire stub server, run decision engine in wire mode
- Verify decisions match fixture-based results
- Add wire ingestion metrics (poll count, data freshness, latency)
- Test graceful handling of wire service downtime

## Configuration Knobs
```yaml
wire:
  enabled: false                    # default: use fixture mode
  base_url: "http://localhost:8091" # wire stub server
  poll_interval_ms: 1000           # how often to poll for updates
  timeout_ms: 5000                 # request timeout
  retry_attempts: 3                # retries on failure
```

## Expected Files Changed
- `internal/wire/stub.go` - HTTP server serving fixture data
- `internal/wire/client.go` - Ingestion client
- `internal/wire/types.go` - Wire protocol data structures  
- `internal/config/config.go` - Add Wire config section
- `config/config.yaml` - Add wire configuration
- `cmd/decision/main.go` - Wire mode integration
- `scripts/run-tests.sh` - Case 7: wire ingestion test

## Success Evidence Patterns
```bash
# Terminal 1: Start wire stub
go run ./cmd/wire-stub -port 8091

# Terminal 2: Run decision engine in wire mode  
go run ./cmd/decision -wire-mode=true -wire-url=http://localhost:8091 -oneshot=true

# Expected: Same decision outputs as fixture mode
# Expected: Wire ingestion metrics in output
```

## Risk Mitigation
- **Wire service downtime**: Graceful degradation, cache last known state
- **Network latency**: Configurable timeouts and retry logic
- **Data inconsistency**: Validate wire data structure matches fixtures
- **Performance regression**: Monitor decision latency, maintain <5ms target

## Session Notes Template
```markdown
### Implementation Details
- Wire stub server runs on localhost:8091 by default
- Polling-based ingestion with exponential backoff on failures
- Data validation ensures wire format matches fixture schema
- Metrics track: wire_polls_total, wire_data_freshness_seconds, wire_errors_total

### Edge Cases Handled
- Wire server unavailable: fall back to last known data, log warnings
- Malformed wire data: skip invalid entries, continue processing
- Network timeouts: retry with backoff, maintain decision pipeline
```

This session bridges fixture-based development with real-time data ingestion, preparing the system for live adapter integration while maintaining deterministic testing capabilities.