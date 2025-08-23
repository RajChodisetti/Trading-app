package stubs

import (
	"encoding/json"
	"math/rand"
	"os"
	"strconv"
	"time"
)

// WireEvent represents a streaming event with envelope metadata
type WireEvent struct {
	Type    string      `json:"type"`     // news, tick, halt, earnings
	ID      string      `json:"id"`       // unique event identifier  
	TsUTC   string      `json:"ts_utc"`   // event timestamp
	Payload interface{} `json:"payload"`  // actual event data
	V       int         `json:"v"`        // version
}

// Fixture payload types matching the existing fixtures
type Halt struct {
	Symbol string `json:"symbol"`
	Halted bool   `json:"halted"`
	TS     string `json:"ts_utc"`
	Reason string `json:"reason"`
}

type HaltsPayload struct {
	Halts []Halt `json:"halts"`
}

type NewsItem struct {
	ID             string   `json:"id"`
	Provider       string   `json:"provider"`
	PublishedAtUTC string   `json:"published_at_utc"`
	Headline       string   `json:"headline"`
	Body           string   `json:"body"`
	URLs           []string `json:"urls"`
	Tickers        []string `json:"tickers"`
	IsPressRelease bool     `json:"is_press_release"`
	IsCorrection   bool     `json:"is_correction"`
	SupersedesID   *string  `json:"supersedes_id"`
	SourceWeight   float64  `json:"source_weight"`
	HeadlineHash   string   `json:"headline_hash"`
}

type NewsPayload struct {
	News []NewsItem `json:"news"`
}

type Tick struct {
	TSUTC      string  `json:"ts_utc"`
	Symbol     string  `json:"symbol"`
	Bid        float64 `json:"bid"`
	Ask        float64 `json:"ask"`
	Last       float64 `json:"last"`
	VWAP5m     float64 `json:"vwap_5m"`
	RelVolume  float64 `json:"rel_volume"`
	Halted     bool    `json:"halted"`
	Premarket  bool    `json:"premarket,omitempty"`
	Postmarket bool    `json:"postmarket,omitempty"`
}

type TicksPayload struct {
	Ticks []Tick `json:"ticks"`
}

type EarningsEvent struct {
	Symbol string `json:"symbol"`
	Start  string `json:"start_utc"`
	End    string `json:"end_utc"`
	Type   string `json:"type"`
}

type EarningsPayload struct {
	Earnings []EarningsEvent `json:"earnings"`
}

// LoadFixtureEvents loads all fixture files and creates a deterministic event stream
func LoadFixtureEvents() ([]WireEvent, error) {
	// Fixed seed for deterministic ordering across runs
	rand.Seed(42)
	
	var events []WireEvent
	baseTime := time.Now().UTC()
	eventCounter := 0
	
	// Load news fixtures
	if newsData, err := os.ReadFile("fixtures/news.json"); err == nil {
		var newsPayload NewsPayload
		if err := json.Unmarshal(newsData, &newsPayload); err == nil {
			for _, item := range newsPayload.News {
				events = append(events, WireEvent{
					Type:    "news",
					ID:      strconv.Itoa(eventCounter),
					TsUTC:   baseTime.Add(time.Duration(eventCounter) * time.Second).Format(time.RFC3339),
					Payload: item,
					V:       1,
				})
				eventCounter++
			}
		}
	}
	
	// Load ticks fixtures  
	if ticksData, err := os.ReadFile("fixtures/ticks.json"); err == nil {
		var ticksPayload TicksPayload
		if err := json.Unmarshal(ticksData, &ticksPayload); err == nil {
			for _, tick := range ticksPayload.Ticks {
				events = append(events, WireEvent{
					Type:    "tick", 
					ID:      strconv.Itoa(eventCounter),
					TsUTC:   baseTime.Add(time.Duration(eventCounter) * time.Second).Format(time.RFC3339),
					Payload: tick,
					V:       1,
				})
				eventCounter++
			}
		}
	}
	
	// Load halts fixtures
	if haltsData, err := os.ReadFile("fixtures/halts.json"); err == nil {
		var haltsPayload HaltsPayload
		if err := json.Unmarshal(haltsData, &haltsPayload); err == nil {
			for _, halt := range haltsPayload.Halts {
				events = append(events, WireEvent{
					Type:    "halt",
					ID:      strconv.Itoa(eventCounter),
					TsUTC:   baseTime.Add(time.Duration(eventCounter) * time.Second).Format(time.RFC3339),
					Payload: halt,
					V:       1,
				})
				eventCounter++
			}
		}
	}
	
	// Load earnings fixtures
	if earningsData, err := os.ReadFile("fixtures/earnings_calendar.json"); err == nil {
		var earningsPayload EarningsPayload
		if err := json.Unmarshal(earningsData, &earningsPayload); err == nil {
			for _, earning := range earningsPayload.Earnings {
				events = append(events, WireEvent{
					Type:    "earnings",
					ID:      strconv.Itoa(eventCounter),
					TsUTC:   baseTime.Add(time.Duration(eventCounter) * time.Second).Format(time.RFC3339),
					Payload: earning,
					V:       1,
				})
				eventCounter++
			}
		}
	}
	
	return events, nil
}

// StreamResponse represents HTTP polling response format (for backward compatibility)
type StreamResponse struct {
	Events []WireEvent `json:"events"`
	Cursor string      `json:"cursor"`
}