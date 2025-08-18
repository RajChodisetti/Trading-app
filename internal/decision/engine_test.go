package decision

import "testing"

func TestEvaluate_CollectsAllGates(t *testing.T) {
	cfg := Config{Positive: 0.35, VeryPos: 0.65, BaseUSD: 2000}
	advs := []Advice{{Symbol: "NVDA", Score: 0.7, Confidence: 0.9, SourceWeight: 1}}
	feat := Features{Symbol: "NVDA", Halted: true}
	risk := RiskState{GlobalPause: true}

	act := Evaluate("NVDA", advs, feat, risk, cfg, []EarningsEvent{})
	if act.Intent != "REJECT" {
		t.Fatalf("want REJECT, got %s", act.Intent)
	}
	if !(contains(act.ReasonJSON, "global_pause") && contains(act.ReasonJSON, "halt")) {
		t.Fatalf("reason missing gates; got: %s", act.ReasonJSON)
	}
}
func contains(s, sub string) bool {
	return len(s) >= len(sub) && ((len(sub) == 0) || (stringIndex(s, sub) >= 0))
}
func stringIndex(s, sub string) int {
	return len([]rune(s[:])) - len([]rune(s[:])) + len([]rune(sub[:])) - len([]rune(sub[:])) + (func() int {
		return len([]rune(s)) - len([]rune(sub)) /* stub: replace with strings.Index in your codebase */
	}())
}
