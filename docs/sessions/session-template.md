# Session <!--SESSION_ID--> — <!--DATE--> — <!--THEME-->

## Part 1 — Development
- **Theme:** <!--THEME-->
- **Acceptance:** <!--One sentence behavior, e.g. “NVDA halted ⇒ any BUY is REJECT with gates [halt]”-->
- **Rails:** `TRADING_MODE=paper` | `GLOBAL_PAUSE=<true|false>`
- **Contracts touched:** <!--list messages from contracts.proto if any-->
- **Changes:**
  - Code:
  - Config:
  - README/Docs:
  - ADRs:

### Implementation notes
- <!-- bullets on approach, tradeoffs, future cleanups -->

---

## Part 2 — Test Run & Edge Cases
### Commands
```bash
# primary run
go run ./cmd/decision -config config/config.yaml

# metrics
curl -s localhost:8090/metrics | jq .
