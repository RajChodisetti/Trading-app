package alerts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/config"
)

type SlackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

type SlackAttachment struct {
	Color  string       `json:"color"`
	Fields []SlackField `json:"fields"`
}

type SlackMessage struct {
	Channel     string            `json:"channel,omitempty"`
	Text        string            `json:"text"`
	Attachments []SlackAttachment `json:"attachments,omitempty"`
}

type AlertRequest struct {
	Symbol       string    `json:"symbol"`
	Intent       string    `json:"intent"`
	Score        float64   `json:"score"`
	GatesBlocked []string  `json:"gates_blocked"`
	TradingMode  string    `json:"trading_mode"`
	GlobalPause  bool      `json:"global_pause"`
	Timestamp    time.Time `json:"timestamp"`
}

type queuedAlert struct {
	req       AlertRequest
	attempts  int
	nextRetry time.Time
	hash      string
}

type SlackClient struct {
	cfg           config.Slack
	httpClient    *http.Client
	queue         chan queuedAlert
	dedupeCache   map[string]time.Time
	rateLimiter   map[string][]time.Time // global + per-symbol rate limits
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	metrics       *AlertMetrics
}

type AlertMetrics struct {
	AlertsSentTotal       int64
	WebhookErrorsTotal    int64
	AlertQueueDepth       int64
	RateLimitHitsTotal    int64
	AlertQueueDropped     int64
}

func NewSlackClient(cfg config.Slack) *SlackClient {
	ctx, cancel := context.WithCancel(context.Background())
	
	client := &SlackClient{
		cfg:         cfg,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		queue:       make(chan queuedAlert, 1000), // bounded queue
		dedupeCache: make(map[string]time.Time),
		rateLimiter: make(map[string][]time.Time),
		ctx:         ctx,
		cancel:      cancel,
		metrics:     &AlertMetrics{},
	}

	// Start worker goroutine
	go client.worker()
	
	// Start cleanup goroutine for expired dedupe entries
	go client.cleanup()

	return client
}

func (s *SlackClient) SendAlert(req AlertRequest) {
	if !s.cfg.Enabled {
		return
	}

	// Check alert policy
	if !s.shouldAlert(req) {
		return
	}

	// Generate dedupe hash
	hash := s.generateHash(req)
	
	// Check dedupe cache (60s window)
	s.mu.Lock()
	if lastSent, exists := s.dedupeCache[hash]; exists {
		if time.Since(lastSent) < 60*time.Second {
			s.mu.Unlock()
			return // Skip duplicate
		}
	}
	s.dedupeCache[hash] = time.Now()
	s.mu.Unlock()

	// Check rate limits
	if s.isRateLimited(req.Symbol) {
		s.mu.Lock()
		s.metrics.RateLimitHitsTotal++
		s.mu.Unlock()
		return
	}

	alert := queuedAlert{
		req:       req,
		attempts:  0,
		nextRetry: time.Now(),
		hash:      hash,
	}

	// Try to enqueue, drop oldest non-critical if full
	select {
	case s.queue <- alert:
		s.mu.Lock()
		s.metrics.AlertQueueDepth++
		s.mu.Unlock()
	default:
		// Queue is full, try to drop oldest non-critical alert
		s.dropOldestNonCritical(alert)
	}
}

func (s *SlackClient) shouldAlert(req AlertRequest) bool {
	switch req.Intent {
	case "BUY_5X":
		return s.cfg.AlertOnBuy5x
	case "BUY_1X":
		return s.cfg.AlertOnBuy1x && req.Score >= 0.65 // High score threshold
	case "REJECT":
		return s.cfg.AlertOnRejectGates && len(req.GatesBlocked) > 0
	default:
		return false
	}
}

func (s *SlackClient) generateHash(req AlertRequest) string {
	// Hash based on symbol, intent, and rounded score (avoid duplicate similar scores)
	data := fmt.Sprintf("%s:%s:%.2f", req.Symbol, req.Intent, req.Score)
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash)[:16]
}

func (s *SlackClient) isRateLimited(symbol string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Minute)

	// Clean old entries and check global rate limit
	globalKey := "global"
	if times, exists := s.rateLimiter[globalKey]; exists {
		filtered := make([]time.Time, 0, len(times))
		for _, t := range times {
			if t.After(cutoff) {
				filtered = append(filtered, t)
			}
		}
		s.rateLimiter[globalKey] = filtered
		
		if len(filtered) >= s.cfg.RateLimitPerMin {
			return true
		}
	}

	// Check per-symbol rate limit
	if times, exists := s.rateLimiter[symbol]; exists {
		filtered := make([]time.Time, 0, len(times))
		for _, t := range times {
			if t.After(cutoff) {
				filtered = append(filtered, t)
			}
		}
		s.rateLimiter[symbol] = filtered
		
		if len(filtered) >= s.cfg.RateLimitPerSymbolPerMin {
			return true
		}
	}

	// Record this request
	s.rateLimiter[globalKey] = append(s.rateLimiter[globalKey], now)
	s.rateLimiter[symbol] = append(s.rateLimiter[symbol], now)

	return false
}

func (s *SlackClient) dropOldestNonCritical(newAlert queuedAlert) {
	// Try to drain one item from queue if it's non-critical
	select {
	case oldAlert := <-s.queue:
		// If old alert is critical (BUY_5X), put it back and drop new one
		if oldAlert.req.Intent == "BUY_5X" {
			select {
			case s.queue <- oldAlert:
				s.mu.Lock()
				s.metrics.AlertQueueDropped++
				s.mu.Unlock()
				return
			default:
				// Queue still full, drop both
			}
		}
		
		// Drop old non-critical alert, enqueue new one
		select {
		case s.queue <- newAlert:
			s.mu.Lock()
			s.metrics.AlertQueueDepth++
			s.metrics.AlertQueueDropped++
			s.mu.Unlock()
		default:
			s.mu.Lock()
			s.metrics.AlertQueueDropped++
			s.mu.Unlock()
		}
	default:
		// Queue was emptied, try to enqueue again
		select {
		case s.queue <- newAlert:
			s.mu.Lock()
			s.metrics.AlertQueueDepth++
			s.mu.Unlock()
		default:
			s.mu.Lock()
			s.metrics.AlertQueueDropped++
			s.mu.Unlock()
		}
	}
}

func (s *SlackClient) worker() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case alert := <-s.queue:
			s.mu.Lock()
			s.metrics.AlertQueueDepth--
			s.mu.Unlock()
			
			if time.Now().Before(alert.nextRetry) {
				// Put back in queue for later
				go func() {
					time.Sleep(time.Until(alert.nextRetry))
					select {
					case s.queue <- alert:
						s.mu.Lock()
						s.metrics.AlertQueueDepth++
						s.mu.Unlock()
					case <-s.ctx.Done():
						return
					default:
						// Queue full, drop
						s.mu.Lock()
						s.metrics.AlertQueueDropped++
						s.mu.Unlock()
					}
				}()
				continue
			}

			if s.sendWebhook(alert.req) {
				s.mu.Lock()
				s.metrics.AlertsSentTotal++
				s.mu.Unlock()
			} else {
				alert.attempts++
				if alert.attempts < 3 {
					// Exponential backoff with jitter
					backoff := time.Duration(math.Pow(2, float64(alert.attempts))) * time.Second
					jitter := time.Duration(rand.Float64() * float64(backoff) * 0.1)
					alert.nextRetry = time.Now().Add(backoff + jitter)
					
					// Requeue for retry
					select {
					case s.queue <- alert:
						s.mu.Lock()
						s.metrics.AlertQueueDepth++
						s.mu.Unlock()
					case <-s.ctx.Done():
						return
					default:
						// Queue full, drop
						s.mu.Lock()
						s.metrics.AlertQueueDropped++
						s.mu.Unlock()
					}
				} else {
					// Max retries exceeded, drop alert
					s.mu.Lock()
					s.metrics.WebhookErrorsTotal++
					s.mu.Unlock()
				}
			}
		}
	}
}

func (s *SlackClient) sendWebhook(req AlertRequest) bool {
	msg := s.formatMessage(req)
	
	payload, err := json.Marshal(msg)
	if err != nil {
		log.Printf("Failed to marshal Slack message: %v", err)
		return false
	}

	// Truncate large payloads
	if len(payload) > 4000 {
		payload = payload[:3900]
		payload = append(payload, []byte("...\"}")...)
	}

	resp, err := s.httpClient.Post(s.cfg.WebhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("Slack webhook error: %v", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Printf("Slack webhook failed with status %d", resp.StatusCode)
		return false
	}

	return true
}

func (s *SlackClient) formatMessage(req AlertRequest) SlackMessage {
	// Format alert text
	emoji := "📈"
	color := "good"
	
	switch req.Intent {
	case "BUY_5X":
		emoji = "🚨"
		color = "warning"
	case "REJECT":
		emoji = "🛑"
		color = "danger"
	}

	text := fmt.Sprintf("%s %s Alert: %s", emoji, req.Intent, req.Symbol)
	
	// Format gates blocked
	gatesText := "✅ All passed"
	if len(req.GatesBlocked) > 0 {
		// Scrub sensitive info and truncate
		gates := make([]string, len(req.GatesBlocked))
		copy(gates, req.GatesBlocked)
		if len(gates) > 5 {
			gates = append(gates[:4], "...")
		}
		gatesText = "❌ " + strings.Join(gates, ", ")
	}

	fields := []SlackField{
		{Title: "Intent", Value: req.Intent, Short: true},
		{Title: "Score", Value: fmt.Sprintf("%.3f", req.Score), Short: true},
		{Title: "Gates", Value: gatesText, Short: true},
		{Title: "Time", Value: req.Timestamp.Format("15:04:05 MST"), Short: true},
	}

	// Add trading mode if not standard
	if req.TradingMode != "paper" || req.GlobalPause {
		mode := req.TradingMode
		if req.GlobalPause {
			mode += " (PAUSED)"
		}
		fields = append(fields, SlackField{
			Title: "Mode", 
			Value: mode, 
			Short: true,
		})
	}

	return SlackMessage{
		Channel: s.cfg.ChannelDefault,
		Text:    text,
		Attachments: []SlackAttachment{{
			Color:  color,
			Fields: fields,
		}},
	}
}

func (s *SlackClient) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			// Clean up old dedupe entries (older than 5 minutes)
			cutoff := time.Now().Add(-5 * time.Minute)
			for hash, timestamp := range s.dedupeCache {
				if timestamp.Before(cutoff) {
					delete(s.dedupeCache, hash)
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *SlackClient) Close() {
	s.cancel()
}

func (s *SlackClient) GetMetrics() AlertMetrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return *s.metrics
}