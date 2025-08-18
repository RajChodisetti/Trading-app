package main

import (
	"encoding/json"
	"log"
	"net/http"
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
