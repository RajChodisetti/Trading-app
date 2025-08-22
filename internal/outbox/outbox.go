package outbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Order struct {
	ID          string    `json:"id"`
	Symbol      string    `json:"symbol"`
	Intent      string    `json:"intent"`
	Timestamp   time.Time `json:"timestamp"`
	Status      string    `json:"status"`
	IdempotencyKey string `json:"idempotency_key"`
}

type Fill struct {
	OrderID      string    `json:"order_id"`
	Symbol       string    `json:"symbol"`
	Quantity     float64   `json:"quantity"`
	Price        float64   `json:"price"`
	Side         string    `json:"side"`
	Timestamp    time.Time `json:"timestamp"`
	LatencyMs    int       `json:"latency_ms"`
	SlippageBps  int       `json:"slippage_bps"`
}

type OutboxEntry struct {
	Type  string      `json:"type"`
	Data  interface{} `json:"data"`
	Event time.Time   `json:"event"`
}

type Outbox struct {
	path string
	dedupeWindow time.Duration
}

func New(path string, dedupeWindowSecs int) (*Outbox, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	
	return &Outbox{
		path: path,
		dedupeWindow: time.Duration(dedupeWindowSecs) * time.Second,
	}, nil
}

func (o *Outbox) WriteOrder(order Order) error {
	entry := OutboxEntry{
		Type:  "order",
		Data:  order,
		Event: time.Now().UTC(),
	}
	return o.appendEntry(entry)
}

func (o *Outbox) WriteFill(fill Fill) error {
	entry := OutboxEntry{
		Type:  "fill",
		Data:  fill,
		Event: time.Now().UTC(),
	}
	return o.appendEntry(entry)
}

func (o *Outbox) appendEntry(entry OutboxEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	
	f, err := os.OpenFile(o.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	
	_, err = f.WriteString(string(data) + "\n")
	return err
}

func (o *Outbox) HasRecentOrder(idempotencyKey string) (bool, error) {
	if _, err := os.Stat(o.path); os.IsNotExist(err) {
		return false, nil
	}
	
	data, err := os.ReadFile(o.path)
	if err != nil {
		return false, err
	}
	
	cutoff := time.Now().UTC().Add(-o.dedupeWindow)
	lines := string(data)
	
	for _, line := range splitLines(lines) {
		if line == "" {
			continue
		}
		
		var entry OutboxEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		
		if entry.Type != "order" || entry.Event.Before(cutoff) {
			continue
		}
		
		orderData, err := json.Marshal(entry.Data)
		if err != nil {
			continue
		}
		
		var order Order
		if err := json.Unmarshal(orderData, &order); err != nil {
			continue
		}
		
		if order.IdempotencyKey == idempotencyKey {
			return true, nil
		}
	}
	
	return false, nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}