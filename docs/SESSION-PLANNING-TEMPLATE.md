# Session N Plan: [Session Title]

## Overview

[2-3 sentences describing the main objective of this session and how it fits into the overall system evolution]

## What's Changed vs. Previous Sessions

- **[Key Change 1]**: [Brief explanation of architectural/approach changes]
- **[Key Change 2]**: [What's different from previous implementations]
- **[Key Change 3]**: [New constraints, requirements, or scope changes]

## Acceptance Criteria

- **[Feature 1]**: [One sentence describing expected behavior with specific inputs/outputs]
- **[Feature 2]**: [Concrete, testable criterion with success metrics]
- **[Feature 3]**: [Clear boundary conditions and error handling expectations]
- **[Integration]**: [How new features integrate with existing system]
- **[Testing]**: `make test` [specific test case] passes: [detailed success conditions]

## Implementation Plan

### 1) [Phase 1 Name] (X min)

**[Component/File to modify]:**
- [Specific change 1]
- [Specific change 2]
- [Configuration/setup requirements]

**[Technical Details]:**
[Code snippets, data structures, or API contracts if helpful]

### 2) [Phase 2 Name] (Y min)

**[Next component]:**
- [Implementation steps]
- [Integration points]
- [Error handling considerations]

### 3) [Phase 3 Name] (Z min)

**[Testing/validation phase]:**
- [Test scenarios to implement]
- [Edge cases to cover]
- [Metrics/logging to add]

### 4) [Phase 4 Name] (W min)

**[Final integration]:**
- [System integration steps]
- [Configuration updates]
- [Documentation updates]

## Success Metrics

### Technical Validation
- [ ] [Specific technical milestone 1]
- [ ] [Performance/latency requirement]
- [ ] [Integration test passing]
- [ ] [Backward compatibility maintained]
- [ ] [Error handling working correctly]

### Operational Readiness
- [ ] [Observability/monitoring requirement]
- [ ] [Configuration management working]
- [ ] [Deployment/rollback considerations]
- [ ] [Documentation updated]

### Code Quality
- [ ] [Code organization/abstraction quality]
- [ ] [Test coverage adequate]
- [ ] [Error messages and logging clear]
- [ ] [Performance impact acceptable]

## Risk Mitigation

### Technical Risks
- **[Risk 1]**: [Mitigation strategy]
- **[Risk 2]**: [Fallback plan]
- **[Risk 3]**: [Testing approach to minimize risk]

### Operational Risks
- **[Risk 1]**: [Operational mitigation]
- **[Risk 2]**: [Monitoring/alerting strategy]
- **[Risk 3]**: [Rollback/recovery plan]

## Implementation Notes

### Configuration Updates
```yaml
[Show new config structure or changes to config.yaml]
```

### New Dependencies
[List any new libraries, tools, or external dependencies]

### API/Contract Changes
[Document any changes to internal APIs or data contracts]

### Metrics Extensions
```json
{
  "[metric_name]": "[description]",
  "[new_field]": "[value_type and meaning]"
}
```

## Next Session Preview

**Session N+1: [Next Session Title]**
- [High-level next steps]
- [Dependencies from this session]
- [Anticipated challenges]

## Dependencies & Prerequisites

- [Previous session X] must be [completion state]
- [System component Y] must be [functional state]
- [Configuration Z] must be [ready state]
- [Testing infrastructure] must be [operational state]

---

**Estimated Duration**: X minutes
**Risk Level**: [Low/Medium/High] ([primary risk factors])
**Reversibility**: [High/Medium/Low] ([rollback strategy])
**Evidence Required**: [Key success indicators that must be demonstrated]

## Validation Checklist

### Pre-Session
- [ ] Previous session fully validated and committed
- [ ] `go test ./...` passes (unit tests)
- [ ] `make test` passes (integration tests)
- [ ] Dependencies and prerequisites verified

### During Implementation
- [ ] Each phase validated before proceeding to next
- [ ] Evidence collection at each milestone
- [ ] Error handling tested at each step
- [ ] Integration points validated

### Post-Session Validation
- [ ] Unit tests compile and pass: `go test ./...`
- [ ] Core functionality: `GLOBAL_PAUSE=false go run ./cmd/decision -oneshot=true`
- [ ] Environment overrides: `GLOBAL_PAUSE=true`, `TRADING_MODE=live`
- [ ] Integration components (Slack, metrics, etc.) tested
- [ ] Configuration validation with different parameter combinations
- [ ] Full integration test suite: `make test`

### Session Completion
- [ ] Session evidence documented in `docs/sessions/SESSION-N.md`
- [ ] TODO.md updated with completion and next priorities
- [ ] Next session plan drafted in `docs/NEXT-SESSION-PLAN.md`
- [ ] All changes committed with comprehensive commit message
- [ ] System ready for next session handoff