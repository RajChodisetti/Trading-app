package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Rajchodisetti/trading-app/internal/decision"
)

type haltsFile struct {
	Halts []struct {
		Symbol string `json:"symbol"`
		Halted bool   `json:"halted"`
	} `json:"halts"`
}

type newsFile struct {
	News []struct {
		ID       string   `json:"id"`
		Tickers  []string `json:"tickers"`
		IsPR     bool     `json:"is_press_release"`
		HeadHash string   `json:"headline_hash"`
		Provider string   `json:"provider"`
	} `json:"news"`
}

type ticksFile struct {
	Ticks []struct {
		Symbol    string  `json:"symbol"`
		Last      float64 `json:"last"`
		VWAP5m    float64 `json:"vwap_5m"`
		RelVolume float64 `json:"rel_volume"`
		Halted    bool    `json:"halted"`
	} `json:"ticks"`
}

func mustRead(path string, v any) {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		log.Fatalf("json %s: %v", path, err)
	}
}

func main() {
	log.SetFlags(0)
	// Load fixtures
	var hf haltsFile
	mustRead("fixtures/halts.json", &hf)
	var nf newsFile
	mustRead("fixtures/news.json", &nf)
	var tf ticksFile
	mustRead("fixtures/ticks.json", &tf)

	// Build a tiny world-state
	halted := map[string]bool{}
	for _, h := range hf.Halts {
		halted[strings.ToUpper(h.Symbol)] = h.Halted
	}

	type key struct{ sym string }
	features := map[key]decision.Features{}
	for _, t := range tf.Ticks {
		features[key{strings.ToUpper(t.Symbol)}] = decision.Features{
			Symbol:    strings.ToUpper(t.Symbol),
			Halted:    t.Halted,
			Last:      t.Last,
			VWAP5m:    t.VWAP5m,
			RelVolume: t.RelVolume,
		}
	}
	// Basic dedupe by headline hash
	seenHash := map[string]bool{}

	// Create advices from news + a tiny trend hint when last>VWAP5m
	advBySym := map[string][]decision.Advice{}
	for _, n := range nf.News {
		if seenHash[n.HeadHash] {
			continue
		}
		seenHash[n.HeadHash] = true
		score := 0.6 // pretend positive editorial
		conf := 0.8
		sw := 1.0
		if n.IsPR {
			score = 0.4
			conf = 0.6
			sw = 0.4
		}
		for _, sym := range n.Tickers {
			advBySym[strings.ToUpper(sym)] = append(advBySym[strings.ToUpper(sym)], decision.Advice{
				Symbol: strings.ToUpper(sym), Score: score, Confidence: conf, SourceWeight: sw,
			})
		}
	}
	for sym, feat := range features {
		if feat.Last > feat.VWAP5m {
			advBySym[sym.sym] = append(advBySym[sym.sym], decision.Advice{
				Symbol: sym.sym, Score: 0.3, Confidence: 0.7, SourceWeight: 1.0, // trend-lite
			})
		}
	}

	cfg := decision.Config{Positive: 0.35, VeryPos: 0.65, BaseUSD: 2000}
	risk := decision.RiskState{GlobalPause: true} // rails ON for session #1

	// Evaluate for AAPL & NVDA if present
	for _, sym := range []string{"AAPL", "NVDA"} {
		feat := features[key{sym}]
		// If halts fixture says halted, reflect it
		if h, ok := halted[sym]; ok {
			feat.Halted = h
		}

		act := decision.Evaluate(sym, advBySym[sym], feat, risk, cfg, []decision.EarningsEvent{})
		fmt.Printf("{\"symbol\":\"%s\",\"intent\":\"%s\",\"reason\":%s}\n",
			sym, act.Intent, act.ReasonJSON)
	}
}
