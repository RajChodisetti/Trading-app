# Session 7 Plan: HTTP Polling Wire Stub (Focused Scope)

## Overview
Replace fixture loading with HTTP polling ingestion using cursor-based deterministic streaming. Single transport (HTTP poll), deterministic via fixed seed + cursor, adds streaming semantics with health/metrics.

## Acceptance Criteria
- **Core Behavior**: Decision engine polls HTTP stub with cursor, gets versioned events, produces identical decisions to fixture mode
- **Success Evidence**: `make test` Case 7 passes with wire stub generating deterministic fixture-equivalent data, ingestion latency <10ms, clear cursor progression

## Implementation Plan

### 1. Evolve cmd/stubs for Wire Streaming (25-30 min)
- Extend existing `cmd/stubs` (avoid parallel stub stacks):
  - Add `/stream` endpoint with `?since=cursor` parameter
  - Serve fixture data with versioned envelope: `{type, id, ts_utc, payload, v}`
  - Deterministic ordering via fixed seed, cursor tracks position
  - Add `/health` endpoint for readiness checks
- Wire event envelope:
  ```json
  {"type":"news","id":"bw-123","ts_utc":"2025-08-22T06:00:00Z","payload":{...news},"v":1}
  {"type":"tick","id":"tick-456","ts_utc":"2025-08-22T06:00:01Z","payload":{...tick},"v":1}
  ```

### 2. HTTP Polling Client in Decision Engine (25-30 min)  
- Modify `cmd/decision/main.go`:
  - Add `-wire-mode`, `-wire-url`, `-max-events`, `-duration-seconds` flags
  - HTTP polling loop with cursor state: GET `/stream?since=cursor`
  - Exponential backoff + jitter on failures, fallback to last good snapshot
  - At-least-once delivery, idempotent event processing
- Replace `-oneshot=true` logic for streaming mode:
  - Stop after `-max-events` (for CI predictability)
  - Or stop after `-duration-seconds`
  - Default: run forever (existing server mode)

### 3. Deterministic Cursor + Ordering (15-20 min)
- Fixed seed reproducibility: stub generates identical event sequence
- Cursor semantics: opaque string, tracks position in deterministic stream
- Per-symbol ordering preservation within event types
- Watermark/lag metrics: track cursor progress vs latest available

## Configuration Knobs
```yaml
wire:
  enabled: false                    # default: use fixture mode  
  base_url: "http://localhost:8091" # stub server URL
  poll_interval_ms: 1000           # polling frequency
  timeout_ms: 5000                 # request timeout
  max_retries: 3                   # exponential backoff retries
  backoff_base_ms: 100             # initial backoff
  backoff_max_ms: 5000             # max backoff cap
```

## Expected Files Changed
- `cmd/stubs/main.go` - Add `/stream` and `/health` endpoints
- `cmd/decision/main.go` - HTTP polling loop integration 
- `internal/config/config.go` - Wire config section
- `config/config.yaml` - Wire configuration
- `scripts/run-tests.sh` - Case 7: wire ingestion test

## Success Evidence Patterns
```bash
# Terminal 1: Start enhanced stub server
go run ./cmd/stubs -port 8091

# Terminal 2: Wire mode with bounded execution
go run ./cmd/decision -wire-mode -wire-url=http://localhost:8091 -max-events=10

# Expected: Identical decisions to fixture mode, cursor progression logged
# Expected: Ingestion latency <10ms, health check success
```

## Metrics & Health
- **Ingestion metrics**: `wire_polls_total`, `wire_events_ingested`, `wire_cursor_lag_seconds`
- **Health semantics**: `/health` returns 200 when stub ready, 503 when starting up
- **Latency breakdown**: separate wire fetch time from decision eval time
- **Error tracking**: connection failures, parse errors, cursor gaps

## Risk Mitigation
- **Stub downtime**: Exponential backoff, fallback to last snapshot, continue decisions
- **Cursor gaps**: Log warnings, attempt recovery, track lag metrics
- **Event flooding**: Bounded execution via `-max-events` prevents CI timeouts
- **Determinism**: Fixed seed ensures reproducible test outcomes

## Session Notes Template
```markdown
### Implementation Details
- Reused cmd/stubs to avoid parallel infrastructure
- Cursor-based pagination with deterministic fixture streaming  
- Versioned event envelope for future compatibility
- Health checks prevent test flakiness via readiness waiting

### Edge Cases Handled
- Network failures: exponential backoff with jitter, graceful degradation
- Parse errors: skip malformed events, continue processing
- Cursor resets: detect and log, re-establish baseline
```

This focused session establishes wire protocol foundations while maintaining deterministic testing and preparing for real adapter integration.