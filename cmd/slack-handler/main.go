package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Rajchodisetti/trading-app/internal/config"
	"github.com/Rajchodisetti/trading-app/internal/portfolio"
)

type SlashCommand struct {
	Token       string `json:"token"`
	TeamID      string `json:"team_id"`
	TeamDomain  string `json:"team_domain"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	UserID      string `json:"user_id"`
	UserName    string `json:"user_name"`
	Command     string `json:"command"`
	Text        string `json:"text"`
	ResponseURL string `json:"response_url"`
	TriggerID   string `json:"trigger_id"`
}

type SlashResponse struct {
	ResponseType string `json:"response_type"` // "ephemeral" or "in_channel"
	Text         string `json:"text"`
	Attachments  []struct {
		Color  string `json:"color,omitempty"`
		Fields []struct {
			Title string `json:"title"`
			Value string `json:"value"`
			Short bool   `json:"short"`
		} `json:"fields,omitempty"`
	} `json:"attachments,omitempty"`
}

type RuntimeOverrides struct {
	Version       int64              `json:"version"`
	UpdatedAt     string             `json:"updated_at"`
	GlobalPause   *bool              `json:"global_pause,omitempty"`
	FrozenSymbols []FrozenSymbol     `json:"frozen_symbols,omitempty"`
	LastCommands  []CommandAuditLog  `json:"last_commands,omitempty"`
}

type FrozenSymbol struct {
	Symbol   string `json:"symbol"`
	UntilUTC string `json:"until_utc"`
}

type CommandAuditLog struct {
	Timestamp string `json:"timestamp"`
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name"`
	Command   string `json:"command"`
	Args      string `json:"args,omitempty"`
	Result    string `json:"result"`
}

type Handler struct {
	signingSecret    string
	allowedUsers     []string
	runtimePath      string
	portfolioMgr     *portfolio.Manager
	mu               sync.RWMutex
	nonceCache       map[string]time.Time // nonce -> timestamp
	metrics          HandlerMetrics
}

type HandlerMetrics struct {
	CommandsReceived      int64
	InvalidSignatures     int64
	RBACDenied           int64
	CommandLatencyMs     map[string][]float64
}

func NewHandler(signingSecret string, allowedUsers []string, runtimePath string, portfolioMgr *portfolio.Manager) *Handler {
	h := &Handler{
		signingSecret: signingSecret,
		allowedUsers:  allowedUsers,
		runtimePath:   runtimePath,
		portfolioMgr:  portfolioMgr,
		nonceCache:    make(map[string]time.Time),
		metrics: HandlerMetrics{
			CommandLatencyMs: make(map[string][]float64),
		},
	}
	
	// Start cleanup goroutine for nonce cache
	go h.cleanupNonces()
	
	return h
}

func (h *Handler) cleanupNonces() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		h.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for nonce, timestamp := range h.nonceCache {
			if timestamp.Before(cutoff) {
				delete(h.nonceCache, nonce)
			}
		}
		h.mu.Unlock()
	}
}

func (h *Handler) verifySignature(body []byte, signature, timestamp string) bool {
	// Check timestamp skew (max 5 minutes)
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	
	if time.Now().Unix()-ts > 300 {
		return false
	}
	
	// Check nonce replay
	nonce := signature + timestamp
	h.mu.Lock()
	if _, exists := h.nonceCache[nonce]; exists {
		h.mu.Unlock()
		return false
	}
	h.nonceCache[nonce] = time.Now()
	h.mu.Unlock()
	
	// Verify HMAC signature
	mac := hmac.New(sha256.New, []byte(h.signingSecret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(body)
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	
	return hmac.Equal([]byte(expected), []byte(signature))
}

func (h *Handler) isUserAllowed(userID string) bool {
	if len(h.allowedUsers) == 0 {
		return true // No RBAC if allowlist is empty
	}
	
	for _, allowed := range h.allowedUsers {
		if allowed == userID {
			return true
		}
	}
	return false
}

func (h *Handler) loadRuntimeOverrides() (RuntimeOverrides, error) {
	var ro RuntimeOverrides
	
	data, err := os.ReadFile(h.runtimePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Initialize new file with default values
			ro = RuntimeOverrides{
				Version:   time.Now().UnixNano(),
				UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			}
			return ro, nil
		}
		return ro, err
	}
	
	if err := json.Unmarshal(data, &ro); err != nil {
		return ro, err
	}
	
	return ro, nil
}

func (h *Handler) saveRuntimeOverrides(ro RuntimeOverrides) error {
	// Atomic write using tmp file + rename
	tmpPath := h.runtimePath + ".tmp"
	
	data, err := json.MarshalIndent(ro, "", "  ")
	if err != nil {
		return err
	}
	
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	
	return os.Rename(tmpPath, h.runtimePath)
}

func (h *Handler) auditCommand(cmd SlashCommand, result string) error {
	ro, err := h.loadRuntimeOverrides()
	if err != nil {
		return err
	}
	
	audit := CommandAuditLog{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		UserID:    cmd.UserID,
		UserName:  cmd.UserName,
		Command:   cmd.Command,
		Args:      cmd.Text,
		Result:    result,
	}
	
	ro.LastCommands = append(ro.LastCommands, audit)
	
	// Keep only last 10 commands
	if len(ro.LastCommands) > 10 {
		ro.LastCommands = ro.LastCommands[len(ro.LastCommands)-10:]
	}
	
	ro.Version = time.Now().UnixNano()
	ro.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	
	return h.saveRuntimeOverrides(ro)
}

func (h *Handler) handlePause(cmd SlashCommand) SlashResponse {
	ro, err := h.loadRuntimeOverrides()
	if err != nil {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("❌ Error loading overrides: %v", err),
		}
	}
	
	globalPause := true
	ro.GlobalPause = &globalPause
	ro.Version = time.Now().UnixNano()
	ro.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	
	if err := h.saveRuntimeOverrides(ro); err != nil {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("❌ Error saving overrides: %v", err),
		}
	}
	
	reason := ""
	if cmd.Text != "" {
		reason = " (" + cmd.Text + ")"
	}
	
	result := "✅ Global pause ENABLED" + reason
	h.auditCommand(cmd, result)
	
	return SlashResponse{
		ResponseType: "ephemeral",
		Text:         result,
	}
}

func (h *Handler) handleResume(cmd SlashCommand) SlashResponse {
	ro, err := h.loadRuntimeOverrides()
	if err != nil {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("❌ Error loading overrides: %v", err),
		}
	}
	
	globalPause := false
	ro.GlobalPause = &globalPause
	ro.Version = time.Now().UnixNano()
	ro.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	
	if err := h.saveRuntimeOverrides(ro); err != nil {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("❌ Error saving overrides: %v", err),
		}
	}
	
	result := "✅ Global pause DISABLED - Trading resumed"
	h.auditCommand(cmd, result)
	
	return SlashResponse{
		ResponseType: "ephemeral",
		Text:         result,
	}
}

func (h *Handler) handleFreeze(cmd SlashCommand) SlashResponse {
	parts := strings.Fields(cmd.Text)
	if len(parts) == 0 {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         "❌ Usage: /freeze SYMBOL [ttl=minutes]",
		}
	}
	
	symbol := strings.ToUpper(parts[0])
	ttlMinutes := 60 // default 1 hour
	
	// Parse TTL if provided
	for _, part := range parts[1:] {
		if strings.HasPrefix(part, "ttl=") {
			if ttl, err := strconv.Atoi(part[4:]); err == nil {
				ttlMinutes = ttl
			}
		}
	}
	
	ro, err := h.loadRuntimeOverrides()
	if err != nil {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("❌ Error loading overrides: %v", err),
		}
	}
	
	// Remove existing freeze for this symbol
	filtered := make([]FrozenSymbol, 0, len(ro.FrozenSymbols))
	for _, fs := range ro.FrozenSymbols {
		if fs.Symbol != symbol {
			filtered = append(filtered, fs)
		}
	}
	ro.FrozenSymbols = filtered
	
	// Add new freeze
	untilUTC := time.Now().Add(time.Duration(ttlMinutes) * time.Minute)
	ro.FrozenSymbols = append(ro.FrozenSymbols, FrozenSymbol{
		Symbol:   symbol,
		UntilUTC: untilUTC.UTC().Format(time.RFC3339),
	})
	
	ro.Version = time.Now().UnixNano()
	ro.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	
	if err := h.saveRuntimeOverrides(ro); err != nil {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("❌ Error saving overrides: %v", err),
		}
	}
	
	result := fmt.Sprintf("🧊 %s FROZEN for %d minutes", symbol, ttlMinutes)
	h.auditCommand(cmd, result)
	
	return SlashResponse{
		ResponseType: "ephemeral",
		Text:         result,
	}
}

func (h *Handler) handleStatus(cmd SlashCommand) SlashResponse {
	ro, err := h.loadRuntimeOverrides()
	if err != nil {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("❌ Error loading overrides: %v", err),
		}
	}
	
	// Clean expired freezes
	now := time.Now()
	activeFreezes := make([]FrozenSymbol, 0, len(ro.FrozenSymbols))
	for _, fs := range ro.FrozenSymbols {
		if untilTime, err := time.Parse(time.RFC3339, fs.UntilUTC); err == nil {
			if now.Before(untilTime) {
				activeFreezes = append(activeFreezes, fs)
			}
		}
	}
	
	pauseStatus := "✅ RESUMED"
	if ro.GlobalPause != nil && *ro.GlobalPause {
		pauseStatus = "⏸️ PAUSED"
	}
	
	frozenStatus := "None"
	if len(activeFreezes) > 0 {
		var frozen []string
		for _, fs := range activeFreezes {
			untilTime, _ := time.Parse(time.RFC3339, fs.UntilUTC)
			remaining := int(time.Until(untilTime).Minutes())
			frozen = append(frozen, fmt.Sprintf("%s (%dm)", fs.Symbol, remaining))
		}
		frozenStatus = strings.Join(frozen, ", ")
	}
	
	recentCommands := "None"
	if len(ro.LastCommands) > 0 {
		var commands []string
		for i := len(ro.LastCommands) - 1; i >= 0 && len(commands) < 3; i-- {
			cmd := ro.LastCommands[i]
			ts, _ := time.Parse(time.RFC3339, cmd.Timestamp)
			commands = append(commands, fmt.Sprintf("%s by %s (%s ago)", 
				cmd.Command, cmd.UserName, time.Since(ts).Truncate(time.Minute)))
		}
		recentCommands = strings.Join(commands, "\n")
	}
	
	result := "📊 Trading System Status"
	h.auditCommand(cmd, result)
	
	return SlashResponse{
		ResponseType: "ephemeral",
		Text:         result,
		Attachments: []struct {
			Color  string `json:"color,omitempty"`
			Fields []struct {
				Title string `json:"title"`
				Value string `json:"value"`
				Short bool   `json:"short"`
			} `json:"fields,omitempty"`
		}{{
			Color: "good",
			Fields: []struct {
				Title string `json:"title"`
				Value string `json:"value"`
				Short bool   `json:"short"`
			}{
				{Title: "Global State", Value: pauseStatus, Short: true},
				{Title: "Frozen Symbols", Value: frozenStatus, Short: true},
				{Title: "Last Updated", Value: ro.UpdatedAt, Short: true},
				{Title: "Recent Commands", Value: recentCommands, Short: false},
			},
		}},
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	
	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	
	// Verify signature
	signature := r.Header.Get("X-Slack-Signature")
	timestamp := r.Header.Get("X-Slack-Request-Timestamp")
	
	if !h.verifySignature(body, signature, timestamp) {
		h.mu.Lock()
		h.metrics.InvalidSignatures++
		h.mu.Unlock()
		http.Error(w, "Invalid signature", http.StatusUnauthorized)
		return
	}
	
	// Parse command
	var cmd SlashCommand
	if err := json.Unmarshal(body, &cmd); err != nil {
		// Try form parsing (Slack sends form data)
		values, err := parseFormBody(string(body))
		if err != nil {
			http.Error(w, "Failed to parse command", http.StatusBadRequest)
			return
		}
		cmd = SlashCommand{
			Token:       values["token"],
			TeamID:      values["team_id"],
			TeamDomain:  values["team_domain"],
			ChannelID:   values["channel_id"],
			ChannelName: values["channel_name"],
			UserID:      values["user_id"],
			UserName:    values["user_name"],
			Command:     values["command"],
			Text:        values["text"],
			ResponseURL: values["response_url"],
			TriggerID:   values["trigger_id"],
		}
	}
	
	// Check RBAC
	if !h.isUserAllowed(cmd.UserID) {
		h.mu.Lock()
		h.metrics.RBACDenied++
		h.mu.Unlock()
		
		response := SlashResponse{
			ResponseType: "ephemeral",
			Text:         "❌ Access denied: You don't have permission to use trading commands",
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return
	}
	
	// Handle command
	var response SlashResponse
	switch cmd.Command {
	case "/pause":
		response = h.handlePause(cmd)
	case "/resume":
		response = h.handleResume(cmd)
	case "/freeze":
		response = h.handleFreeze(cmd)
	case "/status":
		response = h.handleStatus(cmd)
	case "/position":
		response = h.handlePosition(cmd)
	case "/exposure":
		response = h.handleExposure(cmd)
	case "/limits":
		response = h.handleLimits(cmd)
	default:
		response = SlashResponse{
			ResponseType: "ephemeral",
			Text:         "❌ Unknown command. Available: /pause, /resume, /freeze, /status, /position, /exposure, /limits",
		}
	}
	
	// Record metrics
	latency := time.Since(start).Milliseconds()
	h.mu.Lock()
	h.metrics.CommandsReceived++
	if h.metrics.CommandLatencyMs[cmd.Command] == nil {
		h.metrics.CommandLatencyMs[cmd.Command] = make([]float64, 0)
	}
	h.metrics.CommandLatencyMs[cmd.Command] = append(h.metrics.CommandLatencyMs[cmd.Command], float64(latency))
	h.mu.Unlock()
	
	// Send response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func parseFormBody(body string) (map[string]string, error) {
	values := make(map[string]string)
	pairs := strings.Split(body, "&")
	
	for _, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := kv[0]
		value := kv[1]
		// URL decode if needed
		values[key] = value
	}
	
	return values, nil
}

// handlePosition shows position details for a symbol
func (h *Handler) handlePosition(cmd SlashCommand) SlashResponse {
	if h.portfolioMgr == nil {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         "❌ Portfolio management is not enabled",
		}
	}

	symbol := strings.ToUpper(strings.TrimSpace(cmd.Text))
	if symbol == "" {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         "❌ Please provide a symbol. Usage: `/position AAPL`",
		}
	}

	pos, exists := h.portfolioMgr.GetPosition(symbol)
	if !exists || pos.Quantity == 0 {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("📋 No position in %s", symbol),
		}
	}

	// Calculate position details
	positionValue := pos.CurrentNotional
	side := "LONG"
	if pos.Quantity < 0 {
		side = "SHORT"
	}

	lastTradeTime := "Never"
	if pos.LastTradeAt != "" {
		if t, err := time.Parse(time.RFC3339, pos.LastTradeAt); err == nil {
			lastTradeTime = t.Format("2006-01-02 15:04:05 MST")
		}
	}

	return SlashResponse{
		ResponseType: "ephemeral",
		Text:         fmt.Sprintf("📊 Position Details: %s", symbol),
		Attachments: []struct {
			Color  string `json:"color,omitempty"`
			Fields []struct {
				Title string `json:"title"`
				Value string `json:"value"`
				Short bool   `json:"short"`
			} `json:"fields,omitempty"`
		}{
			{
				Color: "good",
				Fields: []struct {
					Title string `json:"title"`
					Value string `json:"value"`
					Short bool   `json:"short"`
				}{
					{Title: "Side", Value: side, Short: true},
					{Title: "Quantity", Value: fmt.Sprintf("%d", pos.Quantity), Short: true},
					{Title: "Avg Entry", Value: fmt.Sprintf("$%.2f", pos.AvgEntryPrice), Short: true},
					{Title: "Current Value", Value: fmt.Sprintf("$%.2f", positionValue), Short: true},
					{Title: "Unrealized P&L", Value: fmt.Sprintf("$%.2f", pos.UnrealizedPnL), Short: true},
					{Title: "Today's P&L", Value: fmt.Sprintf("$%.2f", pos.RealizedPnLToday), Short: true},
					{Title: "Trades Today", Value: fmt.Sprintf("%d", pos.TradeCountToday), Short: true},
					{Title: "Last Trade", Value: lastTradeTime, Short: true},
				},
			},
		},
	}
}

// handleExposure shows overall portfolio exposure
func (h *Handler) handleExposure(cmd SlashCommand) SlashResponse {
	if h.portfolioMgr == nil {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         "❌ Portfolio management is not enabled",
		}
	}

	stats := h.portfolioMgr.GetDailyStats()
	positions := h.portfolioMgr.GetAllPositions()

	activePositions := 0
	for _, pos := range positions {
		if pos.Quantity != 0 {
			activePositions++
		}
	}

	exposureColor := "good"
	if stats.ExposurePctCapital > 12 {
		exposureColor = "warning"
	}
	if stats.ExposurePctCapital > 14 {
		exposureColor = "danger"
	}

	return SlashResponse{
		ResponseType: "ephemeral",
		Text:         "📈 Portfolio Exposure Overview",
		Attachments: []struct {
			Color  string `json:"color,omitempty"`
			Fields []struct {
				Title string `json:"title"`
				Value string `json:"value"`
				Short bool   `json:"short"`
			} `json:"fields,omitempty"`
		}{
			{
				Color: exposureColor,
				Fields: []struct {
					Title string `json:"title"`
					Value string `json:"value"`
					Short bool   `json:"short"`
				}{
					{Title: "Total Exposure", Value: fmt.Sprintf("$%.2f", stats.TotalExposureUSD), Short: true},
					{Title: "% of Capital", Value: fmt.Sprintf("%.1f%%", stats.ExposurePctCapital), Short: true},
					{Title: "Active Positions", Value: fmt.Sprintf("%d", activePositions), Short: true},
					{Title: "New Exposure Today", Value: fmt.Sprintf("$%.2f", stats.NewExposureToday), Short: true},
					{Title: "Trades Today", Value: fmt.Sprintf("%d", stats.TradesToday), Short: true},
					{Title: "P&L Today", Value: fmt.Sprintf("$%.2f", stats.PnLToday), Short: true},
					{Title: "Date", Value: stats.Date, Short: true},
				},
			},
		},
	}
}

// handleLimits shows current portfolio limits and usage
func (h *Handler) handleLimits(cmd SlashCommand) SlashResponse {
	if h.portfolioMgr == nil {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         "❌ Portfolio management is not enabled",
		}
	}

	// Load current config to get limits
	cfg, err := config.Load("config/config.yaml")
	if err != nil {
		return SlashResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("❌ Error loading config: %v", err),
		}
	}

	stats := h.portfolioMgr.GetDailyStats()
	positions := h.portfolioMgr.GetAllPositions()

	// Find largest position
	largestPosition := 0.0
	largestSymbol := "None"
	for symbol, pos := range positions {
		if pos.Quantity != 0 {
			posValue := pos.CurrentNotional
			if posValue < 0 {
				posValue = -posValue
			}
			if posValue > largestPosition {
				largestPosition = posValue
				largestSymbol = symbol
			}
		}
	}

	exposureUsage := (stats.ExposurePctCapital / cfg.Portfolio.MaxPortfolioExposurePct) * 100
	positionUsage := (largestPosition / cfg.Portfolio.MaxPositionSizeUSD) * 100

	return SlashResponse{
		ResponseType: "ephemeral",
		Text:         "⚖️ Portfolio Limits & Usage",
		Attachments: []struct {
			Color  string `json:"color,omitempty"`
			Fields []struct {
				Title string `json:"title"`
				Value string `json:"value"`
				Short bool   `json:"short"`
			} `json:"fields,omitempty"`
		}{
			{
				Color: "good",
				Fields: []struct {
					Title string `json:"title"`
					Value string `json:"value"`
					Short bool   `json:"short"`
				}{
					{Title: "Max Position Size", Value: fmt.Sprintf("$%.0f", cfg.Portfolio.MaxPositionSizeUSD), Short: true},
					{Title: "Largest Position", Value: fmt.Sprintf("$%.2f (%s)", largestPosition, largestSymbol), Short: true},
					{Title: "Position Usage", Value: fmt.Sprintf("%.1f%%", positionUsage), Short: true},
					{Title: "Max Portfolio Exposure", Value: fmt.Sprintf("%.1f%%", cfg.Portfolio.MaxPortfolioExposurePct), Short: true},
					{Title: "Current Exposure", Value: fmt.Sprintf("%.1f%%", stats.ExposurePctCapital), Short: true},
					{Title: "Exposure Usage", Value: fmt.Sprintf("%.1f%%", exposureUsage), Short: true},
					{Title: "Daily Trade Limit", Value: fmt.Sprintf("%d per symbol", cfg.Portfolio.DailyTradeLimitPerSymbol), Short: true},
					{Title: "Cooldown Period", Value: fmt.Sprintf("%d minutes", cfg.Portfolio.CooldownMinutesPerSymbol), Short: true},
				},
			},
		},
	}
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	metrics := h.metrics
	h.mu.RUnlock()
	
	health := map[string]any{
		"status": "ok",
		"metrics": map[string]any{
			"commands_received":   metrics.CommandsReceived,
			"invalid_signatures":  metrics.InvalidSignatures,
			"rbac_denied":        metrics.RBACDenied,
			"nonce_cache_size":   len(h.nonceCache),
		},
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

func main() {
	var port string
	var signingSecret string
	var allowedUsers string
	var runtimePath string
	
	flag.StringVar(&port, "port", "8092", "HTTP server port")
	flag.StringVar(&signingSecret, "signing-secret", "", "Slack signing secret (or use env var)")
	flag.StringVar(&allowedUsers, "allowed-users", "", "Comma-separated list of allowed Slack user IDs")
	flag.StringVar(&runtimePath, "runtime-path", "data/runtime_overrides.json", "Path to runtime overrides file")
	flag.Parse()
	
	// Load config if available
	if cfg, err := config.Load("config/config.yaml"); err == nil {
		if signingSecret == "" && cfg.Security.SlackSigningSecretEnv != "" {
			signingSecret = os.Getenv(cfg.Security.SlackSigningSecretEnv)
		}
		if allowedUsers == "" && len(cfg.Security.AllowedSlackUserIds) > 0 {
			allowedUsers = strings.Join(cfg.Security.AllowedSlackUserIds, ",")
		}
		if runtimePath == "data/runtime_overrides.json" && cfg.RuntimeOverrides.FilePath != "" {
			runtimePath = cfg.RuntimeOverrides.FilePath
		}
	}
	
	// Environment override for signing secret
	if envSecret := os.Getenv("SLACK_SIGNING_SECRET"); envSecret != "" {
		signingSecret = envSecret
	}
	
	if signingSecret == "" {
		log.Fatal("Slack signing secret is required")
	}
	
	var userList []string
	if allowedUsers != "" {
		userList = strings.Split(allowedUsers, ",")
		for i, user := range userList {
			userList[i] = strings.TrimSpace(user)
		}
	}
	
	// Initialize portfolio manager if available
	var portfolioMgr *portfolio.Manager
	if cfg, err := config.Load("config/config.yaml"); err == nil && cfg.Portfolio.Enabled {
		portfolioMgr = portfolio.NewManager(cfg.Portfolio.StateFilePath, cfg.BaseUSD)
		if err := portfolioMgr.Load(); err != nil {
			log.Printf("Warning: failed to load portfolio state: %v", err)
		}
	}
	
	handler := NewHandler(signingSecret, userList, runtimePath, portfolioMgr)
	
	mux := http.NewServeMux()
	mux.Handle("/slack/commands", handler)
	mux.HandleFunc("/health", handler.Health)
	
	addr := ":" + port
	log.Printf("Slack handler listening on %s", addr)
	log.Printf("Runtime overrides path: %s", runtimePath)
	log.Printf("Allowed users: %v", userList)
	
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}