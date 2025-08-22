package main

import (
	"encoding/json"
	"flag"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"
)

// ---- payload shapes (match fixtures) ----

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

// ---- wire event envelope ----

type WireEvent struct {
	Type    string      `json:"type"`     // news, tick, halt, earnings
	ID      string      `json:"id"`       // unique event identifier
	TsUTC   string      `json:"ts_utc"`   // event timestamp
	Payload interface{} `json:"payload"`  // actual event data
	V       int         `json:"v"`        // version
}

type StreamResponse struct {
	Events []WireEvent `json:"events"`
	Cursor string      `json:"cursor"`  // opaque cursor for next request
}

// ---- fixture loading and streaming ----

var fixtureEvents []WireEvent

func loadFixtures() {
	// Fixed seed for deterministic ordering
	rand.Seed(42)
	
	var events []WireEvent
	baseTime := time.Now().UTC()
	
	// Load news fixtures
	if newsData, err := os.ReadFile("fixtures/news.json"); err == nil {
		var newsPayload NewsPayload
		if err := json.Unmarshal(newsData, &newsPayload); err == nil {
			for i, item := range newsPayload.News {
				events = append(events, WireEvent{
					Type:    "news",
					ID:      item.ID,
					TsUTC:   baseTime.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
					Payload: item,
					V:       1,
				})
			}
		}
	}
	
	// Load ticks fixtures  
	if ticksData, err := os.ReadFile("fixtures/ticks.json"); err == nil {
		var ticksPayload TicksPayload
		if err := json.Unmarshal(ticksData, &ticksPayload); err == nil {
			for i, tick := range ticksPayload.Ticks {
				events = append(events, WireEvent{
					Type:    "tick", 
					ID:      tick.Symbol + "-tick-" + strconv.Itoa(i),
					TsUTC:   baseTime.Add(time.Duration(len(events)) * time.Second).Format(time.RFC3339),
					Payload: tick,
					V:       1,
				})
			}
		}
	}
	
	// Load halts fixtures
	if haltsData, err := os.ReadFile("fixtures/halts.json"); err == nil {
		var haltsPayload HaltsPayload
		if err := json.Unmarshal(haltsData, &haltsPayload); err == nil {
			for i, halt := range haltsPayload.Halts {
				events = append(events, WireEvent{
					Type:    "halt",
					ID:      halt.Symbol + "-halt-" + strconv.Itoa(i),
					TsUTC:   baseTime.Add(time.Duration(len(events)) * time.Second).Format(time.RFC3339),
					Payload: halt,
					V:       1,
				})
			}
		}
	}
	
	// Load earnings fixtures
	if earningsData, err := os.ReadFile("fixtures/earnings_calendar.json"); err == nil {
		var earningsPayload EarningsPayload
		if err := json.Unmarshal(earningsData, &earningsPayload); err == nil {
			for i, earning := range earningsPayload.Earnings {
				events = append(events, WireEvent{
					Type:    "earnings",
					ID:      earning.Symbol + "-earnings-" + strconv.Itoa(i), 
					TsUTC:   baseTime.Add(time.Duration(len(events)) * time.Second).Format(time.RFC3339),
					Payload: earning,
					V:       1,
				})
			}
		}
	}
	
	// Shuffle for more realistic mixed streaming, but keep deterministic with fixed seed
	rand.Shuffle(len(events), func(i, j int) {
		events[i], events[j] = events[j], events[i]
	})
	
	// Sort by timestamp to ensure proper ordering
	sort.Slice(events, func(i, j int) bool {
		return events[i].TsUTC < events[j].TsUTC
	})
	
	fixtureEvents = events
	log.Printf("Loaded %d fixture events for streaming", len(fixtureEvents))
}

func streamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	
	since := r.URL.Query().Get("since")
	startIdx := 0
	
	// Parse cursor to find starting position
	if since != "" {
		if idx, err := strconv.Atoi(since); err == nil && idx >= 0 && idx < len(fixtureEvents) {
			startIdx = idx
		}
	}
	
	// Return up to 10 events at a time
	batchSize := 10
	endIdx := startIdx + batchSize
	if endIdx > len(fixtureEvents) {
		endIdx = len(fixtureEvents)
	}
	
	events := fixtureEvents[startIdx:endIdx]
	nextCursor := strconv.Itoa(endIdx)
	
	response := StreamResponse{
		Events: events,
		Cursor: nextCursor,
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
	
	log.Printf("Streamed %d events, cursor %s->%s", len(events), since, nextCursor)
}

// ---- helpers ----

func health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func postJSON[T any](w http.ResponseWriter, r *http.Request, kind string) {
	defer r.Body.Close()
	var p T
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	// Pretty print first item for quick eyeball
	b, _ := json.MarshalIndent(p, "", "  ")
	log.Printf("[%s] %s received:\n%s\n", ts, kind, string(b))
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("ok"))
}

func serve(port string, routes map[string]http.HandlerFunc) {
	mux := http.NewServeMux()
	// common health
	mux.HandleFunc("/health", health)
	for path, fn := range routes {
		mux.HandleFunc(path, fn)
	}
	addr := ":" + port
	log.Printf("listening on %s", addr)
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatalf("server %s error: %v", port, err)
		}
	}()
}

func main() {
	var streamMode bool
	var port string
	
	flag.BoolVar(&streamMode, "stream", false, "enable streaming mode")
	flag.StringVar(&port, "port", "8091", "port for streaming mode")
	flag.Parse()
	
	if streamMode {
		// Wire streaming mode - single port with /stream endpoint
		log.Printf("Starting wire streaming mode on port %s", port)
		loadFixtures()
		
		mux := http.NewServeMux()
		mux.HandleFunc("/health", health)
		mux.HandleFunc("/stream", streamHandler)
		
		addr := ":" + port
		log.Printf("Wire stub listening on %s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatalf("wire stub server error: %v", err)
		}
	} else {
		// Original stub receiver mode - multiple ports
		log.Println("Starting original stub receiver mode")
		
		// 8081: halts
		serve("8081", map[string]http.HandlerFunc{
			"/halts": func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.Error(w, "POST only", http.StatusMethodNotAllowed)
					return
				}
				postJSON[HaltsPayload](w, r, "halts")
			},
		})
		// 8082: news (+ earnings)
		serve("8082", map[string]http.HandlerFunc{
			"/news": func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.Error(w, "POST only", http.StatusMethodNotAllowed)
					return
				}
				postJSON[NewsPayload](w, r, "news")
			},
			"/earnings": func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.Error(w, "POST only", http.StatusMethodNotAllowed)
					return
				}
				postJSON[EarningsPayload](w, r, "earnings")
			},
		})
		// 8083: ticks
		serve("8083", map[string]http.HandlerFunc{
			"/ticks": func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					http.Error(w, "POST only", http.StatusMethodNotAllowed)
					return
				}
				postJSON[TicksPayload](w, r, "ticks")
			},
		})

		// block forever
		select {}
	}
}
