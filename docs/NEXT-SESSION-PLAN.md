# Next Session Plan: Session 6

## Session Theme
**"Paper order outbox + idempotency (mock fills)"**

## Pre-Session Checklist (5 min)
```bash
# 1. Health check
make doctor && make test

# 2. Create session card
scripts/new-session.sh "Paper order outbox + idempotency (mock fills)"

# 3. Review current status
cat docs/TODO.md
```

## Session 6 Acceptance Criteria
**"BUY/SELL decisions generate persistent paper orders with mock fills and prevent duplicates on restart"**

## Planning Phase (10-15 min)

### Contracts to Touch
- Decision output to outbox transformation
- Paper order structure with fill simulation
- Idempotency tracking mechanism

### Files to Modify
1. **`internal/outbox/`** (create new package):
   - `outbox.go` - Core outbox pattern implementation
   - `fills.go` - Mock fill generation with realistic latency/slippage
   - `persistence.go` - JSON file-based storage for paper mode

2. **`cmd/decision/main.go`**:
   - Integration with outbox after decision evaluation
   - Idempotency checks on startup
   - Paper order lifecycle management

3. **`fixtures/outbox_test.json`** (create new):
   - Test scenarios for outbox behavior
   - Mock fill generation test cases
   - Idempotency verification data

4. **`scripts/run-tests.sh`**:
   - Add Session 6 test case with paper order generation
   - Assert outbox persistence and fill simulation
   - Verify no duplicate orders on restart

### Success Evidence Pattern
```bash
# Test case: Decision generates paper order
go run ./cmd/decision -oneshot=true | grep '"event":"paper_order"'
# Expected: BUY decisions create outbox entries with order IDs

# Test case: Mock fills generated
# Expected: Paper orders get filled with realistic timestamps and prices

# Test case: Restart idempotency
# Expected: Restarting doesn't duplicate existing orders
```

## Implementation Phase (30-50 min)

### Core Logic
1. **Create outbox package** for transactional order persistence
2. **Implement mock fill engine** with realistic market simulation
3. **Add idempotency layer** to prevent duplicate order execution
4. **Integrate with decision pipeline** to capture BUY/SELL intents
5. **Update test suite** with outbox and fill scenarios

### Step 1: Outbox Package Structure
```go
// internal/outbox/outbox.go
type PaperOrder struct {
    ID          string    `json:"id"`
    Symbol      string    `json:"symbol"`
    Intent      string    `json:"intent"`      // BUY_1X, BUY_5X, REDUCE
    Quantity    float64   `json:"quantity"`    // shares
    Price       float64   `json:"price"`       // limit price
    Status      string    `json:"status"`      // PENDING, FILLED, REJECTED
    CreatedAt   time.Time `json:"created_at"`
    FilledAt    *time.Time `json:"filled_at,omitempty"`
    FillPrice   *float64  `json:"fill_price,omitempty"`
    ReasonJSON  string    `json:"reason_json"` // decision reason
}

type Outbox interface {
    Store(order PaperOrder) error
    GetPending() ([]PaperOrder, error) 
    MarkFilled(id string, fillPrice float64) error
}
```

### Step 2: Mock Fill Engine
```go
// internal/outbox/fills.go
type MockFillEngine struct {
    Latency time.Duration  // 100ms-2s random
    Slippage float64       // 0.01-0.05% random slippage
}

func (m *MockFillEngine) SimulateFill(order PaperOrder) (float64, time.Duration)
```

### Step 3: Integration Points
- Decision engine output → Paper order creation
- Order lifecycle → Fill simulation → Completion
- Startup → Load pending orders → Resume processing

### Key Safety Rails
- Only in `trading_mode: paper`
- All orders tagged with decision session ID
- Outbox file location configurable
- Orders include full decision reasoning for audit

## Validation Phase (10-15 min)

### End-to-End Tests
1. **Case 6A - Order Generation**: BUY decision creates paper order in outbox
2. **Case 6B - Fill Simulation**: Pending orders get filled with mock data  
3. **Case 6C - Idempotency**: Restart doesn't create duplicate orders
4. **Case 6D - Order Lifecycle**: Complete flow from decision to fill

### Edge Cases to Cover
- Empty outbox on first run
- Corrupt outbox file recovery
- Fill engine timing variations
- Order ID uniqueness across restarts

## Success Metrics
- ✅ `make test` passes with 6 test cases (5 existing + 1 new outbox case)
- ✅ BUY decisions generate paper orders with unique IDs
- ✅ Mock fills generated with realistic latency and slippage
- ✅ Restart idempotency prevents order duplication
- ✅ Outbox persists across application restarts
- ✅ All existing functionality remains intact

## Post-Session Actions
1. Document outbox evidence in session markdown
2. Update TODO.md with Session 6 completion
3. Git commit and push changes
4. Update NEXT-SESSION-PLAN.md for Session 7 (Wire stub ingestion loop)

**Ready to start Session 6? Run the pre-session checklist above!**