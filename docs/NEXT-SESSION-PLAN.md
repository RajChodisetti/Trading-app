# Next Session Plan: Session 5

## Session Theme
**"Add earnings embargo gate"**

## Pre-Session Checklist (5 min)
```bash
# 1. Health check
make doctor && make test

# 2. Create session card
scripts/new-session.sh "Add earnings embargo gate"

# 3. Review current status
cat docs/TODO.md
```

## Session 5 Acceptance Criteria
**"Symbol within earnings embargo window → REJECT with earnings_embargo gate"**

## Planning Phase (10-15 min)

### Contracts to Touch
- Add earnings calendar data structure
- Decision engine earnings embargo gate logic  
- New gate: `earnings_embargo` (hard gate)

### Files to Modify
1. **`internal/config/config.go`**:
   - Add EarningsEmbargo config struct
   - Configure embargo window (hours before/after)

2. **`fixtures/earnings_calendar.json`** (create new):
   - Earnings schedule with symbol, time, confirmed status
   - Test cases for active/upcoming/past earnings

3. **`internal/decision/engine.go`**:
   - Add earnings embargo gate logic (hard gate → REJECT)
   - Include earnings context in decision reason

4. **`cmd/decision/main.go`**:
   - Load and parse earnings calendar fixture
   - Pass earnings state to decision engine

5. **`scripts/run-tests.sh`**:
   - Add Session 5 test case with earnings embargo
   - Assert symbols in embargo are REJECTED

### Success Evidence Pattern
```bash
# Test case with symbol in earnings embargo
go run ./cmd/decision -config config/config.yaml -oneshot=true | grep '"event":"decision"'
# Expected: intent REJECT, gates_blocked includes "earnings_embargo"

# Test case with symbol outside embargo
# Expected: normal BUY/HOLD behavior
```

## Implementation Phase (30-50 min)

### Core Logic
1. **Add earnings embargo config** in `internal/config/config.go`
2. **Create earnings calendar fixture** with test data
3. **Implement earnings embargo gate** in decision engine (hard gate)
4. **Load earnings data** in main.go and pass to decision engine
5. **Update test suite** with earnings embargo test case

### Step 1: Configuration & Data
```go
// internal/config/config.go
type EarningsEmbargo struct {
    Enabled     bool `yaml:"enabled"`
    HoursBefore int  `yaml:"hours_before"` // e.g. 2
    HoursAfter  int  `yaml:"hours_after"`  // e.g. 1
}
```

### Step 2: Earnings Calendar Fixture
```json
// fixtures/earnings_calendar.json
{
  "earnings": [
    {
      "symbol": "AAPL",
      "earnings_time_utc": "2025-08-17T21:00:00Z",
      "confirmed": true
    }
  ]
}
```

### Step 3: Embargo Gate Logic
```go
// In Evaluate() function
if isInEarningsEmbargo(symbol, earningsCalendar, cfg.EarningsEmbargo, now) {
    reason.GatesBlocked = append(reason.GatesBlocked, "earnings_embargo")
    // Add earnings context to reason
}
```

## Validation Phase (10-15 min)

### End-to-End Test
```bash
make test  # All 5 cases should pass now

# Manual verification with earnings embargo fixture
go run ./cmd/decision -config config/config.yaml -oneshot=true
```

### Expected Output
```json
{
  "symbol": "AAPL",
  "intent": "REJECT", 
  "reason": {
    "gates_blocked": ["earnings_embargo"],
    "policy": "positive>=0.35; very_positive>=0.65",
    "earnings_embargo": {
      "earnings_time": "2025-08-17T21:00:00Z",
      "embargo_start": "2025-08-17T19:00:00Z",
      "embargo_end": "2025-08-17T22:00:00Z"
    },
    "what_would_change_it": "wait until after earnings embargo period"
  }
}
```

### Edge Cases to Test
- Earnings exactly at embargo boundary
- Multiple symbols with different earnings times
- Unconfirmed earnings (should not trigger embargo)
- Past earnings (should not block)
- Future earnings outside embargo window

## Post-Session (5 min)

### Update Session Documentation
Fill in `docs/sessions/session-YYYY-MM-DD-XX.md` with:
- Commands run and output
- Evidence of earnings embargo behavior
- Verdict: SUCCESS/BLOCKED/PARTIAL

### Update TODO.md
```markdown
## Done (add to list)
- [x] Session 5: Earnings embargo gate (2025-08-17)

## Next (promote from Later if needed) 
- [ ] Session 6: Transactional paper outbox with mock fills
- [ ] Session 7: Wire stub ingestion loop (HTTP/WebSocket)
```

## Session 5 Success Criteria
- [ ] `make test` passes with 5 test cases
- [ ] Symbols in earnings embargo properly REJECTED with "earnings_embargo" gate
- [ ] Embargo window timing logic works correctly (hours before/after)
- [ ] Decision reasons include earnings temporal context
- [ ] No regressions on existing functionality (Sessions 1-4)
- [ ] Session documented with evidence

**Ready to start Session 5? Run the pre-session checklist above!**