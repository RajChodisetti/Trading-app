package outbox

import (
	"math/rand"
	"time"
)

type FillSimulator struct {
	latencyMsMin  int
	latencyMsMax  int
	slippageBpsMin int
	slippageBpsMax int
}

func NewFillSimulator(latencyMsMin, latencyMsMax, slippageBpsMin, slippageBpsMax int) *FillSimulator {
	return &FillSimulator{
		latencyMsMin:   latencyMsMin,
		latencyMsMax:   latencyMsMax,
		slippageBpsMin: slippageBpsMin,
		slippageBpsMax: slippageBpsMax,
	}
}

func (fs *FillSimulator) SimulateFill(order Order, marketPrice float64) (Fill, time.Duration) {
	latencyMs := fs.latencyMsMin + rand.Intn(fs.latencyMsMax-fs.latencyMsMin+1)
	slippageBps := fs.slippageBpsMin + rand.Intn(fs.slippageBpsMax-fs.slippageBpsMin+1)
	
	var quantity float64
	var side string
	
	switch order.Intent {
	case "BUY_1X":
		quantity = 1.0
		side = "BUY"
	case "BUY_5X":
		quantity = 5.0
		side = "BUY"
	case "REDUCE":
		quantity = 1.0
		side = "SELL"
	default:
		quantity = 0
		side = "NONE"
	}
	
	slippageMultiplier := 1.0 + float64(slippageBps)/10000.0
	if side == "BUY" {
		marketPrice *= slippageMultiplier
	} else if side == "SELL" {
		marketPrice /= slippageMultiplier
	}
	
	fill := Fill{
		OrderID:     order.ID,
		Symbol:      order.Symbol,
		Quantity:    quantity,
		Price:       marketPrice,
		Side:        side,
		Timestamp:   time.Now().UTC().Add(time.Duration(latencyMs) * time.Millisecond),
		LatencyMs:   latencyMs,
		SlippageBps: slippageBps,
	}
	
	return fill, time.Duration(latencyMs) * time.Millisecond
}