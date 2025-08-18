# TODO - Trading System Development

## Now (Current Session Focus)
- [x] Initialize Claude with knowledge base context
- [x] Fix make test timeout issue (main.go hanging)
- [x] Fix test output parsing (jq intent extraction)
- [x] Update documentation (README, CLAUDE.md) with knowledge transfer
- [x] Set up session-based TODO workflow

## Next (Priority Queue)
- [ ] Session 6: Paper order outbox + idempotency (mock fills)
- [ ] Session 7: Wire stub ingestion loop (HTTP/WebSocket/NATS)

## Later (Future Enhancements)
- [ ] Transactional paper outbox with mock fills
- [ ] Wire stub ingestion loop (HTTP/WebSocket)
- [ ] Slack alerts and operational controls (/pause, /freeze)
- [ ] Real adapter integrations (one at a time)
- [ ] Portfolio caps and cooldown gates
- [ ] Drawdown monitoring and circuit breakers

## Done (Completed Sessions)
- [x] Session 1: Halts gate + reason logging (completed)
- [x] Session 2: Metrics + oneshot runner + make test flow (completed)
- [x] Session 2.5: Claude initialization + test fixes + documentation sync (2025-08-17)
- [x] Session 3: Add session and liquidity gates (2025-08-17)
- [x] Session 4: PR corroboration window logic (2025-08-17)
- [x] Session 5: Add earnings embargo gate (2025-08-17)
