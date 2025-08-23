# Vibe Coding Session Guide

## Session Protocol (60-90 minutes max)

### Pre-Session (5 min)
1. **Check TODO.md** - Review "Now" section for current focus
2. **Run Health Check**:
   ```bash
   make doctor     # Tool dependencies
   make test       # Ensure baseline works
   ```
3. **Create Session Card**:
   ```bash
   scripts/new-session.sh "Session Theme (one-liner)"
   ```

### Session Structure (50-80 min)

#### Phase 1: Planning (10-15 min)
1. **Define Acceptance Criteria** (one sentence behavior)
   - Example: "After-hours ticks → BUY blocked with gates_blocked=['session']"
2. **Identify Contracts** - Which protobuf messages will be touched?
3. **Choose Fixtures** - Which test scenarios will prove the behavior?
4. **Safety Rails Check**:
   - Ensure `trading_mode: paper` and `global_pause: true`
   - Verify tiny notional amounts in config

#### Phase 2: Implementation (30-50 min)
1. **Red-Green-Refactor Cycle**:
   ```bash
   # Red: Add failing test case
   make test  # Should fail on new scenario
   
   # Green: Minimal implementation
   # Edit internal/decision/engine.go or relevant files
   
   # Refactor: Clean up, add logging
   make test  # Should pass
   ```

2. **Evidence Collection** (every 15-20 min):
   ```bash
   # Capture decision logs
   go run ./cmd/decision -oneshot=true > session-evidence.jsonl
   
   # Check metrics  
   curl -s localhost:8090/metrics | jq . > session-metrics.json
   ```

#### Phase 3: Validation (10-15 min)
1. **End-to-End Test**:
   ```bash
   make test       # All scenarios pass
   go test ./...   # Unit tests pass
   ```

2. **Edge Case Verification**:
   - Test boundary conditions
   - Verify gate combinations
   - Check reason logging completeness

#### Phase 4: Comprehensive Validation (5-10 min)
**REQUIRED**: After implementation is complete, run full validation suite:

1. **Unit Test Compilation**:
   ```bash
   go test ./...   # Ensure all modules compile and pass
   ```

2. **Core Functionality Test**:
   ```bash
   GLOBAL_PAUSE=false go run ./cmd/decision -oneshot=true
   # Verify: All risk managers initialize, decisions made correctly
   ```

3. **Environment Override Validation**:
   ```bash
   GLOBAL_PAUSE=true go run ./cmd/decision -oneshot=true | grep global_pause
   TRADING_MODE=live GLOBAL_PAUSE=false go run ./cmd/decision -oneshot=true | grep trading_mode
   ```

4. **Integration Component Tests**:
   ```bash
   # Test key integrations (Slack, metrics, etc.)
   # Example: Start Slack handler, test health endpoint
   ```

5. **Configuration Validation**:
   - Verify all new config parameters load correctly
   - Test with different environment variable combinations
   - Ensure backward compatibility maintained

### Post-Session (5-10 min)
1. **Document Evidence** in session markdown:
   - Commands run
   - Expected vs actual output
   - Decision logs with reasoning
   - Verdict (SUCCESS/BLOCKED/PARTIAL)

2. **Update TODO.md**:
   - Move completed items to "Done" with date
   - Add discovered work to "Next"
   - Update "Now" for next session

3. **Next Session Planning**:
   - **REQUIRED**: Update `docs/NEXT-SESSION-PLAN.md` with next session details
   - Define clear acceptance criteria and implementation plan
   - Identify dependencies and prerequisites
   - Estimate duration and risk level

4. **Git Operations**:
   ```bash
   # Stage all changes
   git add .
   
   # Commit with session summary
   git commit -m "$(cat <<'EOF'
   Complete Session N: [Theme]
   
   - [Key change 1]
   - [Key change 2] 
   - [Key change 3]
   
   All tests pass, ready for Session N+1
   
   🤖 Generated with [Claude Code](https://claude.ai/code)
   
   Co-Authored-By: Claude <noreply@anthropic.com>
   EOF
   )"
   
   # Push to remote
   git push origin main
   ```

4. **Update Next Session Plan**:
   ```bash
   # Update docs/NEXT-SESSION-PLAN.md for next session
   # Include theme, acceptance criteria, files to modify, success pattern
   ```

5. **Session Handoff**:
   - Ensure all session artifacts are committed
   - Update CLAUDE.md if architecture changed
   - Next session is clearly defined in TODO.md

## Session Templates by Type

### Gate Implementation Session
**Theme**: "Add [gate_name] gate with [condition]"
**Files to Touch**:
- `internal/decision/engine.go` - Add gate logic
- `fixtures/[scenario].json` - Test data
- `scripts/run-tests.sh` - Test assertions
- `config/config.yaml` - Configuration knobs

**Evidence Pattern**:
```bash
# Before: gate not implemented
grep -A5 -B5 "gates_blocked" evidence-before.jsonl

# After: gate blocks appropriately  
grep -A5 -B5 "gates_blocked" evidence-after.jsonl
```

### Strategy/Fusion Session
**Theme**: "Add [strategy_name] advice generation"
**Files to Touch**:
- `cmd/decision/main.go` - Strategy integration
- `internal/decision/engine.go` - Fusion weights
- `fixtures/[inputs].json` - Strategy test data

**Evidence Pattern**:
```bash
# Show per_strategy contributions
jq '.reason.per_strategy' evidence.jsonl

# Verify fused_score calculation
jq '.reason.fused_score' evidence.jsonl
```

### Infrastructure Session  
**Theme**: "Wire [component] integration"
**Files to Touch**:
- `cmd/[service]/main.go` - Service integration
- `internal/[module]/` - New module
- `docker-compose.yml` - Infrastructure
- `Makefile` - New targets

## Safety Checklist (NEVER SKIP)

### Before Every Code Change
- [ ] `trading_mode: paper` in config
- [ ] `global_pause: true` in config  
- [ ] Base amounts are tiny (≤ $2000)
- [ ] Using fixtures, not real feeds

### Before Every Commit
- [ ] `make test` passes
- [ ] `go test ./...` passes
- [ ] No secrets in code/logs
- [ ] Decision reasons are complete
- [ ] Session evidence captured

### Before Provider Swaps (Sessions 9+)
- [ ] Canary on 3-5 symbols only
- [ ] Keep global_pause=true initially
- [ ] Monitor latency metrics
- [ ] Have rollback plan ready

## Anti-Patterns (What NOT to Do)

❌ **Session Scope Creep**
- Don't add multiple gates in one session
- Don't refactor while adding features
- Don't optimize prematurely

❌ **Safety Shortcuts**  
- Don't test with real money/feeds
- Don't skip evidence collection
- Don't commit broken tests

❌ **Documentation Debt**
- Don't skip session cards
- Don't batch TODO updates
- Don't leave incomplete evidence

## Success Metrics

### Per Session
- ✅ One vertical slice completed
- ✅ All tests pass with evidence
- ✅ Clear reason logs with new behavior
- ✅ Session documented with verdict

### Overall Progress
- 📈 Decision latency stays ≤ 50ms
- 📈 Gate coverage increases
- 📈 Reason completeness ≥ 95%
- 📈 Zero test regressions

## Next Session Quick Start

1. **Choose Theme** from TODO.md "Next" section
2. **Prep Fixtures** - Create or identify test data needed
3. **Define Success** - One sentence acceptance criteria
4. **Time Box** - Set 90-minute maximum
5. **Evidence First** - Know what logs/metrics will prove success

Remember: **Small sessions, strong evidence, safety always on.**