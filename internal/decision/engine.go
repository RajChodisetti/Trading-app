package decision

import (
	"encoding/json"
	"math"
	"time"
	
	"github.com/Rajchodisetti/trading-app/internal/observ"
)

type Advice struct {
	Symbol       string
	Score        float64 // [-1..1]
	Confidence   float64 // [0..1]
	SourceWeight float64 // [0..1]
	TTLSeconds   int     // optional
	Provider     string  // e.g., "businesswire", "reuters"
	IsPR         bool    // true if this is a press release
	PublishedAt  time.Time // event time for corroboration window
}

type Features struct {
	Symbol     string
	Halted     bool
	Last       float64
	VWAP5m     float64
	RelVolume  float64
	Premarket  bool
	Postmarket bool
	SpreadBps  float64
}

type RiskState struct {
	GlobalPause       bool
	BlockPremarket    bool
	BlockPostmarket   bool
	MaxSpreadBps      float64
	FrozenSymbols     []string
}

type Config struct {
	Positive        float64 // e.g., 0.35
	VeryPos         float64 // e.g., 0.65
	BaseUSD         float64 // e.g., 2000
	Corroboration   CorroborationConfig
	EarningsEmbargo EarningsEmbargoConfig
}

type CorroborationConfig struct {
	RequirePositivePR bool // require corroboration for positive PR-driven decisions
	WindowSeconds     int  // corroboration window in seconds (e.g., 900 = 15 min)
}

type EarningsEmbargoConfig struct {
	Enabled          bool
	BlockOnEstimated bool
	MinutesBefore    int
	MinutesAfter     int
}

type EarningsEvent struct {
	Symbol   string
	StartUTC time.Time
	EndUTC   time.Time
	Status   string // "confirmed" or "estimated"
}

type Reason struct {
	FusedScore      float64                 `json:"fused_score"`
	PerStrategy     map[string]float64      `json:"per_strategy"`
	GatesPassed     []string                `json:"gates_passed"`
	GatesBlocked    []string                `json:"gates_blocked"`
	Policy          string                  `json:"policy"`
	WhatWouldChange string                  `json:"what_would_change_it,omitempty"`
	Corroboration   *CorroborationState     `json:"corroboration,omitempty"`
	EarningsEmbargo *EarningsEmbargoState   `json:"earnings_embargo,omitempty"`
}

type CorroborationState struct {
	Required bool      `json:"required"`
	Until    time.Time `json:"until"`
	Seen     []string  `json:"seen"`     // source types seen: ["pr"], ["editorial"], etc.
	Missing  []string  `json:"missing"`  // source types still needed: ["editorial"]
}

type EarningsEmbargoState struct {
	StartUTC time.Time `json:"start_utc"`
	EndUTC   time.Time `json:"end_utc"`
	Status   string    `json:"status"`
}

type ProposedAction struct {
	Symbol         string
	Intent         string // BUY_1X | BUY_5X | REDUCE | HOLD | REJECT
	BaseAmountUSD  float64
	ScaledNotional float64
	ReasonJSON     string
}

// Fuse: super simple weighted sum with tanh squash
func fuse(advs []Advice) (float64, map[string]float64) {
	per := map[string]float64{}
	sum := 0.0
	for _, a := range advs {
		w := a.Confidence
		if w <= 0 {
			w = 0.5
		}
		w *= a.SourceWeight
		contrib := a.Score * w
		per[a.Symbol] += contrib // here we use Symbol as a stand-in for strategy label in this tiny demo
		sum += contrib
	}
	// squash to [-1..1]
	fs := math.Tanh(sum)
	return fs, per
}

// fuseWithoutPR: fuse advice excluding PR contributions (for window expiry scenario)
func fuseWithoutPR(advs []Advice) (float64, map[string]float64) {
	per := map[string]float64{}
	sum := 0.0
	for _, a := range advs {
		if a.IsPR {
			continue // exclude PR advice
		}
		w := a.Confidence
		if w <= 0 {
			w = 0.5
		}
		w *= a.SourceWeight
		contrib := a.Score * w
		per[a.Symbol] += contrib
		sum += contrib
	}
	// squash to [-1..1]
	fs := math.Tanh(sum)
	return fs, per
}

// Evaluate applies gates then threshold mapping.
// For session #1 we only use GlobalPause and Halt gates + thresholds.
func Evaluate(symbol string, advs []Advice, feat Features, risk RiskState, cfg Config, earningsEvents []EarningsEvent) ProposedAction {
	now := time.Now()
	
	// Check corroboration requirements
	needsCorroboration, corrobState := analyzeCorroboration(advs, cfg.Corroboration, now)
	
	// Track corroboration metrics
	if corrobState != nil && corrobState.Required {
		observ.IncCounter("corroboration_pending_total", map[string]string{"symbol": symbol})
		
		if len(corrobState.Missing) == 0 {
			observ.IncCounter("corroboration_satisfied_total", map[string]string{"symbol": symbol})
		} else if now.After(corrobState.Until) {
			observ.IncCounter("corroboration_expired_total", map[string]string{"symbol": symbol})
		}
	}
	
	// Check earnings embargo
	earningsEmbargoActive, earningsState := analyzeEarningsEmbargo(symbol, earningsEvents, cfg.EarningsEmbargo, now)
	
	// Track earnings embargo metrics
	if earningsEmbargoActive && earningsState != nil {
		observ.IncCounter("earnings_embargo_blocks_total", map[string]string{"symbol": symbol})
	}
	
	var fused float64
	var per map[string]float64
	
	if needsCorroboration && corrobState != nil {
		// Check if any editorial/regulatory source exists outside the window
		hasLateCorroboration := false
		for _, adv := range advs {
			if adv.Score <= 0 {
				continue
			}
			sourceType := classifySource(adv.Provider, adv.IsPR)
			if canCorroborate(sourceType) && adv.PublishedAt.After(corrobState.Until) {
				hasLateCorroboration = true
				break
			}
		}
		
		// Handle different corroboration scenarios
		if now.After(corrobState.Until) || hasLateCorroboration {
			// Window expired or corroboration came too late, use non-PR advice only
			fused, per = fuseWithoutPR(advs)
		} else {
			// Within window but pending corroboration
			fused, per = fuse(advs)
		}
	} else {
		// No corroboration needed or satisfied
		fused, per = fuse(advs)
	}

	reason := Reason{
		FusedScore:      fused,
		PerStrategy:     per,
		GatesPassed:     []string{},
		GatesBlocked:    []string{},
		Policy:          "positive>=0.35; very_positive>=0.65",
		Corroboration:   corrobState,
		EarningsEmbargo: earningsState,
	}

	// Collect all violated gates
	if risk.GlobalPause {
		reason.GatesBlocked = append(reason.GatesBlocked, "global_pause")
	}
	if feat.Halted {
		reason.GatesBlocked = append(reason.GatesBlocked, "halt")
	}

	// Session gates - block pre/post market trading
	if risk.BlockPremarket && feat.Premarket {
		reason.GatesBlocked = append(reason.GatesBlocked, "session")
	}
	if risk.BlockPostmarket && feat.Postmarket {
		reason.GatesBlocked = append(reason.GatesBlocked, "session")
	}

	// Liquidity gate - block wide spreads
	if feat.SpreadBps > risk.MaxSpreadBps {
		reason.GatesBlocked = append(reason.GatesBlocked, "liquidity")
	}

	// Frozen symbol gate - check if symbol is frozen
	for _, frozen := range risk.FrozenSymbols {
		if frozen == symbol {
			reason.GatesBlocked = append(reason.GatesBlocked, "frozen")
			break
		}
	}

	// Corroboration soft gate - convert would-be BUY to HOLD
	corroborationBlocked := false
	if needsCorroboration && corrobState != nil && !now.After(corrobState.Until) && len(corrobState.Missing) > 0 {
		// Only apply if this would be a BUY decision
		if fused >= cfg.Positive {
			reason.GatesBlocked = append(reason.GatesBlocked, "corroboration")
			reason.WhatWouldChange = "editorial/regulatory confirmation before " + corrobState.Until.Format(time.RFC3339)
			corroborationBlocked = true
		}
	}

	// Earnings embargo soft gate - convert would-be BUY to HOLD
	earningsBlocked := false
	if earningsEmbargoActive && earningsState != nil {
		// Only apply if this would be a BUY decision
		if fused >= cfg.Positive {
			reason.GatesBlocked = append(reason.GatesBlocked, "earnings_embargo")
			reason.WhatWouldChange = "wait until " + earningsState.EndUTC.Format(time.RFC3339)
			earningsBlocked = true
		}
	}

	// Hard gates (halt, session, liquidity, global_pause, frozen) -> REJECT
	hardGates := []string{"global_pause", "halt", "session", "liquidity", "frozen"}
	hasHardGate := false
	for _, gate := range reason.GatesBlocked {
		for _, hardGate := range hardGates {
			if gate == hardGate {
				hasHardGate = true
				break
			}
		}
		if hasHardGate {
			break
		}
	}

	if hasHardGate {
		rj, _ := json.Marshal(reason)
		return ProposedAction{Symbol: symbol, Intent: "REJECT", ReasonJSON: string(rj)}
	}

	// Otherwise, proceed to intent mapping
	reason.GatesPassed = append(reason.GatesPassed, "no_halt", "caps_ok", "session_ok")

	intent := "HOLD"
	usd := 0.0
	
	// If corroboration or earnings embargo is blocking, force HOLD regardless of score
	if corroborationBlocked {
		intent = "HOLD"
		usd = 0.0
		observ.IncCounter("corroboration_blocks_total", map[string]string{"symbol": symbol})
	} else if earningsBlocked {
		intent = "HOLD"
		usd = 0.0
	} else {
		// Normal threshold mapping
		if fused >= cfg.VeryPos {
			intent = "BUY_5X"
			usd = cfg.BaseUSD * 5
		} else if fused >= cfg.Positive {
			intent = "BUY_1X"
			usd = cfg.BaseUSD
		}
	}

	rj, _ := json.Marshal(reason)
	return ProposedAction{
		Symbol:         symbol,
		Intent:         intent,
		BaseAmountUSD:  cfg.BaseUSD,
		ScaledNotional: usd,
		ReasonJSON:     string(rj),
	}
}

// TTL helper (not used yet, but you'll use it in next sessions)
func notExpired(created time.Time, ttlSeconds int) bool {
	if ttlSeconds <= 0 {
		return true
	}
	return time.Since(created) <= time.Duration(ttlSeconds)*time.Second
}

// classifySource determines the source type for corroboration logic
func classifySource(provider string, isPR bool) string {
	if isPR {
		return "pr"
	}
	
	// Editorial sources
	editorialProviders := map[string]bool{
		"reuters":   true,
		"ap":        true,
		"bloomberg": true,
	}
	
	if editorialProviders[provider] {
		return "editorial"
	}
	
	// Regulatory/exchange sources
	regulatoryProviders := map[string]bool{
		"sec":    true,
		"edgar":  true,
		"nasdaq": true,
		"nyse":   true,
	}
	
	if regulatoryProviders[provider] {
		return "regulatory"
	}
	
	return "other"
}

// canCorroborate checks if sourceType can corroborate a PR
func canCorroborate(sourceType string) bool {
	return sourceType == "editorial" || sourceType == "regulatory"
}

// analyzeCorroboration determines if corroboration is needed and satisfied
func analyzeCorroboration(advs []Advice, cfg CorroborationConfig, now time.Time) (bool, *CorroborationState) {
	if !cfg.RequirePositivePR {
		return false, nil
	}
	
	var positiveSourceWeights, prWeights float64
	var prAdvice *Advice
	var hasCorroboration bool
	var earliest time.Time
	seen := map[string]bool{}
	
	// Analyze all advice to understand PR vs corroboration
	for _, adv := range advs {
		if adv.Score <= 0 {
			continue // only consider positive advice
		}
		
		sourceType := classifySource(adv.Provider, adv.IsPR)
		seen[sourceType] = true
		
		if sourceType == "pr" {
			prWeights += adv.Score * adv.Confidence * adv.SourceWeight
			if prAdvice == nil || adv.PublishedAt.Before(prAdvice.PublishedAt) {
				earliest = adv.PublishedAt
				prAdvice = &adv
			}
		}
		
		positiveSourceWeights += adv.Score * adv.Confidence * adv.SourceWeight
	}
	
	// No positive PR, no corroboration needed
	if prWeights == 0 {
		return false, nil
	}
	
	// Check if PR is the primary driver (>50% of positive weight)
	isPrimaryDriver := (prWeights / positiveSourceWeights) > 0.5
	if !isPrimaryDriver {
		return false, nil
	}
	
	// PR is primary driver, check corroboration within window
	windowEnd := earliest.Add(time.Duration(cfg.WindowSeconds) * time.Second)
	
	// Check for corroboration within window
	hasCorroboration = false
	for _, adv := range advs {
		if adv.Score <= 0 {
			continue
		}
		sourceType := classifySource(adv.Provider, adv.IsPR)
		if canCorroborate(sourceType) && !adv.PublishedAt.Before(earliest) && adv.PublishedAt.Before(windowEnd) {
			hasCorroboration = true
			break
		}
	}
	
	seenList := make([]string, 0, len(seen))
	for sourceType := range seen {
		seenList = append(seenList, sourceType)
	}
	
	missing := []string{}
	if !hasCorroboration {
		missing = append(missing, "editorial")
	}
	
	state := &CorroborationState{
		Required: true,
		Until:    windowEnd,
		Seen:     seenList,
		Missing:  missing,
	}
	
	// If corroboration is satisfied within window, no gate needed
	if hasCorroboration {
		return false, nil // corroboration satisfied
	}
	
	// Check if any editorial/regulatory source exists outside the window
	hasLateCorroboration := false
	for _, adv := range advs {
		if adv.Score <= 0 {
			continue
		}
		sourceType := classifySource(adv.Provider, adv.IsPR)
		if canCorroborate(sourceType) && adv.PublishedAt.After(windowEnd) {
			hasLateCorroboration = true
			break
		}
	}
	
	// If corroboration came after window, treat as expired (ignore PR weight)
	if hasLateCorroboration {
		return true, state // corroboration required but window expired
	}
	
	// If no corroboration found yet and we're still within window, block decision
	if !now.After(windowEnd) {
		return true, state // corroboration required and pending
	}
	
	// Window has expired with no corroboration at all
	return true, state // corroboration required but window expired
}

// analyzeEarningsEmbargo checks if symbol is in an active earnings embargo window
func analyzeEarningsEmbargo(symbol string, events []EarningsEvent, cfg EarningsEmbargoConfig, now time.Time) (bool, *EarningsEmbargoState) {
	if !cfg.Enabled {
		return false, nil
	}
	
	// Find active earnings events for this symbol
	for _, event := range events {
		if event.Symbol != symbol {
			continue
		}
		
		// Skip estimated events if configured to do so
		if event.Status == "estimated" && !cfg.BlockOnEstimated {
			continue
		}
		
		// Calculate embargo window with buffer
		embargoStart := event.StartUTC.Add(-time.Duration(cfg.MinutesBefore) * time.Minute)
		embargoEnd := event.EndUTC.Add(time.Duration(cfg.MinutesAfter) * time.Minute)
		
		// Check if we're currently in the embargo window
		if now.After(embargoStart) && now.Before(embargoEnd) {
			state := &EarningsEmbargoState{
				StartUTC: embargoStart,
				EndUTC:   embargoEnd,
				Status:   event.Status,
			}
			return true, state
		}
	}
	
	return false, nil
}
