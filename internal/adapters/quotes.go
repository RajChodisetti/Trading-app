package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// QuotesAdapter provides market data quotes with configurable sources
type QuotesAdapter interface {
	GetQuote(ctx context.Context, symbol string) (*Quote, error)
	GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error)
	HealthCheck(ctx context.Context) error
	Close() error
}

// Quote represents normalized market data from any provider
type Quote struct {
	Symbol      string    `json:"symbol"`       // Normalized symbol (uppercase, no class suffixes)
	Bid         float64   `json:"bid"`          // Best bid price
	Ask         float64   `json:"ask"`          // Best ask price  
	Last        float64   `json:"last"`         // Last traded price
	Volume      int64     `json:"volume"`       // Daily volume
	Timestamp   time.Time `json:"timestamp"`    // Quote timestamp from provider
	Session     string    `json:"session"`      // "PRE"|"RTH"|"POST"|"CLOSED"|"UNKNOWN"
	Halted      bool      `json:"halted"`       // Trading halt status
	Source      string    `json:"source"`       // "alphavantage"|"polygon"|"mock"|"sim"
	StalenessMs int64     `json:"staleness_ms"` // Age in milliseconds at retrieval time
}

// ValidateQuote performs comprehensive quote validation with fail-closed behavior
func ValidateQuote(quote *Quote) error {
	if quote == nil {
		return fmt.Errorf("quote is nil")
	}
	
	// Normalize symbol
	quote.Symbol = strings.ToUpper(strings.TrimSpace(quote.Symbol))
	if quote.Symbol == "" {
		return fmt.Errorf("empty symbol")
	}
	
	// Price validation (fail-closed: reject invalid prices)
	if quote.Bid <= 0 || quote.Ask <= 0 || quote.Last <= 0 {
		return fmt.Errorf("invalid quote prices: bid=%.4f ask=%.4f last=%.4f", 
			quote.Bid, quote.Ask, quote.Last)
	}
	
	// Spread validation (ask must be >= bid)
	if quote.Ask < quote.Bid {
		return fmt.Errorf("invalid spread: ask(%.4f) < bid(%.4f)", quote.Ask, quote.Bid)
	}
	
	// Volume validation
	if quote.Volume < 0 {
		return fmt.Errorf("negative volume: %d", quote.Volume)
	}
	
	// Timestamp validation (not too far in future)
	now := time.Now()
	if quote.Timestamp.After(now.Add(5 * time.Minute)) {
		return fmt.Errorf("quote timestamp too far in future: %v", quote.Timestamp)
	}
	
	// Session validation
	validSessions := map[string]bool{
		"PRE": true, "RTH": true, "POST": true, "CLOSED": true, "UNKNOWN": true,
	}
	if !validSessions[quote.Session] {
		return fmt.Errorf("invalid session: %s", quote.Session)
	}
	
	return nil
}

// IsStale checks if quote exceeds staleness threshold
func (q *Quote) IsStale(maxAgeMs int64) bool {
	return q.StalenessMs > maxAgeMs
}

// SpreadBps calculates bid-ask spread in basis points
func (q *Quote) SpreadBps() float64 {
	if q.Bid <= 0 {
		return 0
	}
	return ((q.Ask - q.Bid) / q.Bid) * 10000
}

// SessionType represents different market session states
type SessionType string

const (
	SessionPremarket  SessionType = "PRE"
	SessionRegular    SessionType = "RTH"  
	SessionPostmarket SessionType = "POST"
	SessionClosed     SessionType = "CLOSED"
	SessionUnknown    SessionType = "UNKNOWN"
)

// GetCurrentSession returns the current market session for US equities
// This is a simple implementation - production would use NYSE calendar
func GetCurrentSession() SessionType {
	now := time.Now()
	
	// Convert to Eastern Time (NYSE timezone)
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return SessionUnknown
	}
	et := now.In(loc)
	
	// Weekend check
	weekday := et.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return SessionClosed
	}
	
	hour := et.Hour()
	minute := et.Minute()
	timeInMinutes := hour*60 + minute
	
	// Market hours in minutes from midnight
	premarketStart := 4 * 60      // 4:00 AM ET
	marketOpen := 9*60 + 30       // 9:30 AM ET  
	marketClose := 16 * 60        // 4:00 PM ET
	postmarketEnd := 20 * 60      // 8:00 PM ET
	
	switch {
	case timeInMinutes >= premarketStart && timeInMinutes < marketOpen:
		return SessionPremarket
	case timeInMinutes >= marketOpen && timeInMinutes < marketClose:
		return SessionRegular
	case timeInMinutes >= marketClose && timeInMinutes < postmarketEnd:
		return SessionPostmarket
	default:
		return SessionClosed
	}
}

// QuoteError represents different types of quote fetch errors
type QuoteError struct {
	Type    string // "network", "rate_limit", "provider_error", "bad_symbol", "stale"
	Symbol  string
	Message string
	Cause   error
}

func (e *QuoteError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s error for %s: %s (%v)", e.Type, e.Symbol, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s error for %s: %s", e.Type, e.Symbol, e.Message)
}

// Common error constructors
func NewNetworkError(symbol, message string, cause error) *QuoteError {
	return &QuoteError{Type: "network", Symbol: symbol, Message: message, Cause: cause}
}

func NewRateLimitError(symbol, message string) *QuoteError {
	return &QuoteError{Type: "rate_limit", Symbol: symbol, Message: message}
}

func NewProviderError(symbol, message string, cause error) *QuoteError {
	return &QuoteError{Type: "provider_error", Symbol: symbol, Message: message, Cause: cause}
}

func NewBadSymbolError(symbol, message string) *QuoteError {
	return &QuoteError{Type: "bad_symbol", Symbol: symbol, Message: message}
}

func NewStaleError(symbol string, staleness time.Duration) *QuoteError {
	return &QuoteError{
		Type: "stale", 
		Symbol: symbol, 
		Message: fmt.Sprintf("quote too stale: %v", staleness),
	}
}