package decision

import (
	"testing"
	"github.com/Rajchodisetti/trading-app/internal/portfolio"
	"github.com/Rajchodisetti/trading-app/internal/risk"
)

func TestEvaluate_CollectsAllGates(t *testing.T) {
	cfg := Config{Positive: 0.35, VeryPos: 0.65, BaseUSD: 2000}
	advs := []Advice{{Symbol: "NVDA", Score: 0.7, Confidence: 0.9, SourceWeight: 1}}
	feat := Features{Symbol: "NVDA", Halted: true}
	riskState := RiskState{GlobalPause: true}

	// Create mock risk managers (nil is fine for this test since global_pause will reject)
	var portfolioMgr *portfolio.Manager
	var stopLossMgr *risk.StopLossManager
	var sectorMgr *risk.SectorExposureManager
	var drawdownMgr *risk.DrawdownManager

	act := Evaluate("NVDA", advs, feat, riskState, cfg, []EarningsEvent{}, portfolioMgr, stopLossMgr, sectorMgr, drawdownMgr)
	if act.Intent != "REJECT" {
		t.Fatalf("want REJECT, got %s", act.Intent)
	}
	if !(contains(act.ReasonJSON, "global_pause") && contains(act.ReasonJSON, "halt")) {
		t.Fatalf("reason missing gates; got: %s", act.ReasonJSON)
	}
}
func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
