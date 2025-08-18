# Session Workflow: How to Provide Requirements

## Step-by-Step Process

### 1. Pre-Session: Define Requirements
**Before starting any session, you should provide:**

#### A. High-Level Feature Requirements
Create or update a feature specification document:
```bash
# Create feature spec (if new feature)
touch docs/features/session-gates.md
```

**Example Feature Spec Structure:**
```markdown
# Session Gates Feature

## Business Requirements
- Block trading during pre-market hours (before 9:30 AM ET)
- Block trading during post-market hours (after 4:00 PM ET)
- Configurable enable/disable per gate

## Technical Requirements  
- Add session configuration to config.yaml
- Update Features struct with premarket/postmarket flags
- Add "session" to gates_blocked when violated
- Preserve existing behavior for regular hours

## Acceptance Criteria
1. Pre-market tick → REJECT with gates_blocked=["session"]
2. Post-market tick → REJECT with gates_blocked=["session"]  
3. Regular hours tick → existing behavior unchanged
4. Configuration allows enabling/disabling gates independently
```

#### B. Session-Specific Acceptance Criteria
**One sentence behavior statement**:
- "After-hours ticks → BUY blocked with gates_blocked=['session']"
- "Wide spreads > 50bps → BUY blocked with gates_blocked=['liquidity']" 
- "PR without editorial → REJECT until corroboration window expires"

### 2. Start Session
```bash
# Start session with clear theme (5-8 words max)
scripts/new-session.sh "Add session gate for after-hours"
```

### 3. Fill Session Template
The generated session file needs YOU to fill in:

```markdown
## Part 1 — Development
- **Theme:** Add session gate for after-hours  
- **Acceptance:** After-hours ticks → BUY blocked with gates_blocked=['session']
- **Rails:** TRADING_MODE=paper | GLOBAL_PAUSE=true
- **Contracts touched:** MarketTick (premarket/postmarket fields)
- **Changes:**
  - Code: internal/decision/engine.go, cmd/decision/main.go
  - Config: Add session section to config.yaml
  - README/Docs: Update gate documentation
  - ADRs: None
```

## Where Requirements Come From

### User/Product Requirements
**You provide these in several ways:**

1. **Direct conversation**: "I want to add a session gate that blocks after-hours trading"

2. **Feature specification document**: 
   ```bash
   # Create detailed specs in docs/features/
   docs/features/session-gates.md
   docs/features/liquidity-gates.md  
   docs/features/pr-corroboration.md
   ```

3. **GitHub issues/tickets** (if using issue tracking)

4. **Business rules document**: 
   ```bash
   docs/BUSINESS-RULES.md  # Trading rules and constraints
   ```

### Technical Requirements (Claude derives from user requirements)
- Implementation approach
- File changes needed
- Test scenarios
- Edge cases to consider

## Requirement Communication Templates

### Option 1: Direct Session Request
```
"I want to implement session gates. Requirements:
- Block pre-market trading (before 9:30 AM ET)  
- Block post-market trading (after 4:00 PM ET)
- Make it configurable in config.yaml
- Use the after-hours fixture for testing"
```

### Option 2: Feature Specification  
```
"Please implement the session gates feature according to the spec in 
docs/features/session-gates.md. Focus on the basic blocking behavior first."
```

### Option 3: User Story Format
```
"As a risk manager, I want to prevent trading during after-hours 
so that we avoid low-liquidity periods. 

Acceptance criteria:
- Pre-market ticks result in REJECT decisions
- Post-market ticks result in REJECT decisions  
- Regular hours continue working normally
- Gates are logged in decision reasons"
```

## Session Planning Conversation

### Typical Session Start
**You say**: "Let's implement session gates for after-hours trading blocking"

**Claude asks clarification**:
- "Should this block both pre-market AND post-market?"
- "Do you want it configurable or always-on?"
- "Should we use the existing after-hours fixture?"
- "Any specific time ranges or use market standard hours?"

**You provide details**:
- "Yes, block both pre and post market"
- "Make it configurable - add session.block_premarket and session.block_postmarket"  
- "Use fixtures/ticks_after_hours_wide_spread.json"
- "Use standard US market hours for now"

**Claude creates session plan**:
- Acceptance criteria: "After-hours ticks → REJECT with gates_blocked=['session']"
- Files to change: engine.go, main.go, config.yaml
- Test approach: Add case to run-tests.sh
- Evidence: Decision logs showing session gate blocking

## Advanced: Multi-Session Features

### Large Feature Breakdown
If you want a complex feature like "PR Corroboration System":

```
Session 4A: "Basic PR detection and flagging"
Session 4B: "Editorial corroboration window logic" 
Session 4C: "Advice de-weighting for uncorroborated PRs"
```

**You provide**:
1. **Overall feature vision** in docs/features/pr-corroboration.md
2. **Session-by-session breakdown** with acceptance criteria  
3. **Priority order** if sessions depend on each other

## Example: Complete Session Requirements

```markdown
# Feature Request: Liquidity Gates

## Business Need
Avoid trading when spreads are too wide (indicates poor liquidity)

## Requirements  
- Add max_spread_bps configuration (default: 50)
- Calculate spread from bid/ask in MarketTick
- Block when spread > threshold
- Add "liquidity" to gates_blocked  

## Acceptance Criteria
"Wide spreads > 50bps → BUY blocked with gates_blocked=['liquidity']"

## Test Data
Use fixtures/ticks_after_hours_wide_spread.json (already has wide spreads)

## Session Theme
"Add liquidity gate for wide spreads"
```

**This gives Claude everything needed to plan and execute the session successfully.**