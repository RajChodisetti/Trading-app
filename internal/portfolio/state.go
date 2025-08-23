package portfolio

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Position represents a trading position for a single symbol
type Position struct {
	Quantity         int     `json:"quantity"`          // Current position size (shares/contracts)
	AvgEntryPrice    float64 `json:"avg_entry_price"`   // Average entry price
	EntryVWAP        float64 `json:"entry_vwap"`        // Volume-weighted entry price for stop-loss
	CurrentNotional  float64 `json:"current_notional"`  // Current position value
	UnrealizedPnL    float64 `json:"unrealized_pnl"`    // Unrealized profit/loss
	LastTradeAt      string  `json:"last_trade_at"`     // Timestamp of last trade
	TradeCountToday  int     `json:"trade_count_today"` // Number of trades today
	RealizedPnLToday float64 `json:"realized_pnl_today"` // Realized P&L today
}

// DailyStats tracks daily portfolio statistics
type DailyStats struct {
	Date                string  `json:"date"`                 // Date in YYYY-MM-DD format
	TotalExposureUSD    float64 `json:"total_exposure_usd"`   // Total position value
	ExposurePctCapital  float64 `json:"exposure_pct_capital"` // Exposure as % of capital
	NewExposureToday    float64 `json:"new_exposure_today"`   // New exposure added today
	TradesToday         int     `json:"trades_today"`         // Total trades today
	PnLToday           float64 `json:"pnl_today"`           // Total P&L today
}

// State represents the complete portfolio state
type State struct {
	Version     int64                `json:"version"`      // Monotonic version for atomic updates
	UpdatedAt   string               `json:"updated_at"`   // Last update timestamp
	Positions   map[string]Position  `json:"positions"`    // Positions by symbol
	DailyStats  DailyStats          `json:"daily_stats"`  // Current day statistics
	CapitalBase float64             `json:"capital_base"` // Total capital for calculations
}

// Manager handles portfolio state persistence and calculations
type Manager struct {
	filePath string
	state    State
	mu       sync.RWMutex
}

// NewManager creates a new portfolio manager with the given state file path
func NewManager(filePath string, capitalBase float64) *Manager {
	return &Manager{
		filePath: filePath,
		state: State{
			Positions:   make(map[string]Position),
			CapitalBase: capitalBase,
			DailyStats: DailyStats{
				Date: time.Now().UTC().Format("2006-01-02"),
			},
		},
	}
}

// Load loads portfolio state from disk, creating default state if file doesn't exist
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, use default state
			m.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			return m.saveUnsafe()
		}
		return fmt.Errorf("failed to read portfolio state: %w", err)
	}

	if err := json.Unmarshal(data, &m.state); err != nil {
		return fmt.Errorf("failed to unmarshal portfolio state: %w", err)
	}

	// Reset daily stats if it's a new day
	today := time.Now().UTC().Format("2006-01-02")
	if m.state.DailyStats.Date != today {
		m.resetDailyStats(today)
	}

	return nil
}

// Save atomically saves the portfolio state to disk
func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveUnsafe()
}

// saveUnsafe saves without acquiring lock (internal use only)
func (m *Manager) saveUnsafe() error {
	m.state.Version++
	m.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal portfolio state: %w", err)
	}

	// Atomic write using temp file + rename
	tempPath := m.filePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp portfolio state: %w", err)
	}

	if err := os.Rename(tempPath, m.filePath); err != nil {
		os.Remove(tempPath) // Clean up temp file
		return fmt.Errorf("failed to rename portfolio state: %w", err)
	}

	return nil
}

// GetPosition returns the current position for a symbol
func (m *Manager) GetPosition(symbol string) (Position, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pos, exists := m.state.Positions[symbol]
	return pos, exists
}

// GetAllPositions returns all current positions
func (m *Manager) GetAllPositions() map[string]Position {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	positions := make(map[string]Position, len(m.state.Positions))
	for symbol, pos := range m.state.Positions {
		positions[symbol] = pos
	}
	return positions
}

// GetDailyStats returns current daily statistics
func (m *Manager) GetDailyStats() DailyStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.DailyStats
}

// UpdatePosition updates a position based on a trade execution
func (m *Manager) UpdatePosition(symbol string, quantity int, price float64, timestamp time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Reset daily stats if it's a new day
	today := timestamp.Format("2006-01-02")
	if m.state.DailyStats.Date != today {
		m.resetDailyStats(today)
	}

	pos := m.state.Positions[symbol]
	
	// Update position
	if pos.Quantity == 0 {
		// New position
		pos.Quantity = quantity
		pos.AvgEntryPrice = price
		pos.EntryVWAP = price // Initialize VWAP with first trade price
		pos.CurrentNotional = float64(quantity) * price
	} else {
		// Existing position
		if (pos.Quantity > 0 && quantity > 0) || (pos.Quantity < 0 && quantity < 0) {
			// Adding to position
			totalCost := pos.AvgEntryPrice * float64(pos.Quantity) + price * float64(quantity)
			totalQuantity := pos.Quantity + quantity
			
			// Update VWAP for stop-loss calculations
			pos.EntryVWAP = totalCost / float64(totalQuantity)
			
			pos.Quantity = totalQuantity
			pos.AvgEntryPrice = totalCost / float64(pos.Quantity)
			pos.CurrentNotional = float64(pos.Quantity) * pos.AvgEntryPrice
		} else {
			// Reducing or closing position
			if absInt(quantity) >= absInt(pos.Quantity) {
				// Closing or reversing position - realize P&L
				realizedPnL := float64(pos.Quantity) * (price - pos.AvgEntryPrice)
				pos.RealizedPnLToday += realizedPnL
				m.state.DailyStats.PnLToday += realizedPnL
				
				// Set new position if reversing
				pos.Quantity += quantity
				if pos.Quantity != 0 {
					pos.AvgEntryPrice = price
					pos.CurrentNotional = float64(pos.Quantity) * price
				} else {
					pos.CurrentNotional = 0
				}
			} else {
				// Partial close - realize partial P&L
				realizedPnL := float64(quantity) * (price - pos.AvgEntryPrice)
				pos.RealizedPnLToday += realizedPnL
				m.state.DailyStats.PnLToday += realizedPnL
				pos.Quantity += quantity
				pos.CurrentNotional = float64(pos.Quantity) * pos.AvgEntryPrice
			}
		}
	}

	// Update trade tracking
	pos.LastTradeAt = timestamp.Format(time.RFC3339)
	pos.TradeCountToday++
	
	// Store updated position
	m.state.Positions[symbol] = pos
	
	// Update daily statistics
	m.state.DailyStats.TradesToday++
	
	// Recalculate portfolio exposure
	m.recalculateExposureUnsafe()
	
	return m.saveUnsafe()
}

// UpdateUnrealizedPnL updates unrealized P&L for a position based on current market price
func (m *Manager) UpdateUnrealizedPnL(symbol string, currentPrice float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pos, exists := m.state.Positions[symbol]
	if !exists || pos.Quantity == 0 {
		return nil
	}

	pos.UnrealizedPnL = float64(pos.Quantity) * (currentPrice - pos.AvgEntryPrice)
	pos.CurrentNotional = float64(pos.Quantity) * currentPrice
	m.state.Positions[symbol] = pos

	return m.saveUnsafe()
}

// CanTrade checks if a symbol can be traded based on cooldown period
func (m *Manager) CanTrade(symbol string, cooldownMinutes int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pos, exists := m.state.Positions[symbol]
	if !exists || pos.LastTradeAt == "" {
		return true
	}

	lastTrade, err := time.Parse(time.RFC3339, pos.LastTradeAt)
	if err != nil {
		return true // If can't parse, allow trade
	}

	return time.Since(lastTrade) >= time.Duration(cooldownMinutes)*time.Minute
}

// GetExposureUSD returns the total portfolio exposure in USD
func (m *Manager) GetExposureUSD() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.DailyStats.TotalExposureUSD
}

// GetExposurePercent returns the portfolio exposure as percentage of capital
func (m *Manager) GetExposurePercent() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.DailyStats.ExposurePctCapital
}

// GetTradeCount returns the number of trades for a symbol today
func (m *Manager) GetTradeCount(symbol string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	pos, exists := m.state.Positions[symbol]
	if !exists {
		return 0
	}
	return pos.TradeCountToday
}

// resetDailyStats resets daily statistics for a new day
func (m *Manager) resetDailyStats(date string) {
	// Reset daily counters in positions
	for symbol, pos := range m.state.Positions {
		pos.TradeCountToday = 0
		pos.RealizedPnLToday = 0
		m.state.Positions[symbol] = pos
	}
	
	// Reset daily stats
	m.state.DailyStats = DailyStats{
		Date:                date,
		TotalExposureUSD:    m.state.DailyStats.TotalExposureUSD,
		ExposurePctCapital:  m.state.DailyStats.ExposurePctCapital,
		NewExposureToday:    0,
		TradesToday:         0,
		PnLToday:           0,
	}
}

// recalculateExposureUnsafe recalculates total portfolio exposure
func (m *Manager) recalculateExposureUnsafe() {
	totalExposure := 0.0
	for _, pos := range m.state.Positions {
		totalExposure += abs(pos.CurrentNotional)
	}
	
	m.state.DailyStats.TotalExposureUSD = totalExposure
	if m.state.CapitalBase > 0 {
		m.state.DailyStats.ExposurePctCapital = (totalExposure / m.state.CapitalBase) * 100
	}
}

// abs returns absolute value of a float64
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// absInt returns absolute value of an int
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// GetNAV calculates current Net Asset Value (capital + realized + unrealized P&L)
func (m *Manager) GetNAV() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	// Start with capital base
	nav := m.state.CapitalBase
	
	// Add realized P&L from today
	nav += m.state.DailyStats.PnLToday
	
	// Add unrealized P&L from all positions
	for _, pos := range m.state.Positions {
		nav += pos.UnrealizedPnL
	}
	
	return nav
}

// GetPositionNotionals returns map of symbol to current notional value for sector exposure calculation
func (m *Manager) GetPositionNotionals() map[string]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	notionals := make(map[string]float64)
	for symbol, pos := range m.state.Positions {
		notionals[symbol] = pos.CurrentNotional
	}
	return notionals
}

// GetEntryVWAP returns the entry VWAP for a symbol (for stop-loss calculations)
func (m *Manager) GetEntryVWAP(symbol string) (float64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	pos, exists := m.state.Positions[symbol]
	if !exists || pos.Quantity == 0 {
		return 0, false
	}
	return pos.EntryVWAP, true
}