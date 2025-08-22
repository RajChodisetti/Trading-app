package observ

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
)

type registry struct {
	mu       sync.Mutex
	counters map[string]map[string]int64 // name -> labelsKey -> count
	gauges   map[string]map[string]float64 // name -> labelsKey -> value
	hist     map[string]map[string][]float64
}

var reg = &registry{
	counters: map[string]map[string]int64{},
	gauges:   map[string]map[string]float64{},
	hist:     map[string]map[string][]float64{},
}

// canonicalize label map so key order is stable
func canonLabels(lbl map[string]string) string {
	if len(lbl) == 0 {
		return ""
	}
	keys := make([]string, 0, len(lbl))
	for k := range lbl {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(lbl[k])
	}
	return b.String()
}

func IncCounter(name string, labels map[string]string) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	m, ok := reg.counters[name]
	if !ok {
		m = map[string]int64{}
		reg.counters[name] = m
	}
	k := canonLabels(labels)
	m[k]++
}

func SetGauge(name string, value float64, labels map[string]string) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	m, ok := reg.gauges[name]
	if !ok {
		m = map[string]float64{}
		reg.gauges[name] = m
	}
	k := canonLabels(labels)
	m[k] = value
}

func Observe(name string, value float64, labels map[string]string) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	m, ok := reg.hist[name]
	if !ok {
		m = map[string][]float64{}
		reg.hist[name] = m
	}
	k := canonLabels(labels)
	m[k] = append(m[k], value)
}

// Basic text/JSON dump for quick checks (not Prometheus format on purpose)
func Handler() http.Handler {
	type dump struct {
		Counters map[string]map[string]int64     `json:"counters"`
		Gauges   map[string]map[string]float64   `json:"gauges"`
		Hist     map[string]map[string][]float64 `json:"histograms"`
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.mu.Lock()
		defer reg.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dump{Counters: reg.counters, Gauges: reg.gauges, Hist: reg.hist})
	})
}

// Simple health handler
func Health() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}
