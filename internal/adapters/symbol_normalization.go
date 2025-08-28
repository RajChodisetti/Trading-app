package adapters

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SymbolNormalizer handles symbol format conversion and corporate actions
type SymbolNormalizer struct {
	mu                sync.RWMutex
	corporateActions  map[string]*CorporateAction
	symbolMappings    map[string]string // provider -> symbol -> normalized symbol
	reverseMappings   map[string]map[string]string // normalized -> provider -> provider symbol
	lastUpdate        time.Time
}

// CorporateAction represents a corporate action affecting a symbol
type CorporateAction struct {
	Symbol          string            `json:"symbol"`           // Original symbol
	NewSymbol       string            `json:"new_symbol"`       // New symbol (if renamed)
	ActionType      CorporateActionType `json:"action_type"`    // Type of action
	EffectiveDate   time.Time         `json:"effective_date"`   // When action takes effect
	ExpirationDate  *time.Time        `json:"expiration_date"`  // When mapping expires
	Ratio           *SplitRatio       `json:"ratio,omitempty"`  // For splits/consolidations
	Details         map[string]any    `json:"details"`          // Additional details
	CreatedAt       time.Time         `json:"created_at"`       // When action was recorded
}

// CorporateActionType defines types of corporate actions
type CorporateActionType string

const (
	ActionSplit        CorporateActionType = "split"         // Stock split (e.g., 2:1)
	ActionReverseSplit CorporateActionType = "reverse_split" // Reverse split (e.g., 1:10)
	ActionRename       CorporateActionType = "rename"        // Symbol change
	ActionAcquisition  CorporateActionType = "acquisition"   // Company acquired
	ActionSpinoff      CorporateActionType = "spinoff"       // Spinoff event
	ActionDelisting    CorporateActionType = "delisting"     // Stock delisted
	ActionHalt         CorporateActionType = "halt"          // Trading halt
)

// SplitRatio represents a stock split or consolidation ratio
type SplitRatio struct {
	From int `json:"from"` // Original shares
	To   int `json:"to"`   // New shares (e.g., 1:2 split = From:1, To:2)
}

// SymbolMapping represents provider-specific symbol mappings
type SymbolMapping struct {
	NormalizedSymbol string            `json:"normalized_symbol"` // Standard format (e.g., "AAPL")
	ProviderSymbols  map[string]string `json:"provider_symbols"`  // provider -> provider-specific symbol
	LastUpdated      time.Time         `json:"last_updated"`      // When mapping was updated
}

// NewSymbolNormalizer creates a new symbol normalizer
func NewSymbolNormalizer() *SymbolNormalizer {
	return &SymbolNormalizer{
		corporateActions: make(map[string]*CorporateAction),
		symbolMappings:   make(map[string]string),
		reverseMappings:  make(map[string]map[string]string),
		lastUpdate:       time.Now(),
	}
}

// NormalizeSymbol converts a provider-specific symbol to normalized format
func (sn *SymbolNormalizer) NormalizeSymbol(provider, symbol string) (string, error) {
	if symbol == "" {
		return "", fmt.Errorf("empty symbol")
	}
	
	sn.mu.RLock()
	defer sn.mu.RUnlock()
	
	// Check if we have a specific mapping for this provider
	providerKey := provider + ":" + symbol
	if normalized, exists := sn.symbolMappings[providerKey]; exists {
		return normalized, nil
	}
	
	// Apply provider-specific normalization rules
	normalized := sn.applyProviderNormalization(provider, symbol)
	
	// Check for active corporate actions
	if action, exists := sn.corporateActions[normalized]; exists {
		if sn.isCorporateActionActive(action) {
			switch action.ActionType {
			case ActionRename:
				if action.NewSymbol != "" {
					return action.NewSymbol, nil
				}
			case ActionDelisting, ActionAcquisition:
				return "", fmt.Errorf("symbol %s is delisted/acquired as of %v", symbol, action.EffectiveDate)
			}
		}
	}
	
	return normalized, nil
}

// DenormalizeSymbol converts a normalized symbol to provider-specific format
func (sn *SymbolNormalizer) DenormalizeSymbol(provider, normalizedSymbol string) (string, error) {
	sn.mu.RLock()
	defer sn.mu.RUnlock()
	
	if providerMappings, exists := sn.reverseMappings[normalizedSymbol]; exists {
		if providerSymbol, exists := providerMappings[provider]; exists {
			return providerSymbol, nil
		}
	}
	
	// Apply reverse provider-specific rules
	return sn.applyProviderDenormalization(provider, normalizedSymbol), nil
}

// applyProviderNormalization applies provider-specific normalization rules
func (sn *SymbolNormalizer) applyProviderNormalization(provider, symbol string) string {
	// Clean and uppercase
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	
	switch provider {
	case "alphavantage":
		return sn.normalizeAlphaVantageSymbol(symbol)
	case "polygon":
		return sn.normalizePolygonSymbol(symbol)
	case "yahoo":
		return sn.normalizeYahooSymbol(symbol)
	case "finnhub":
		return sn.normalizeFinnhubSymbol(symbol)
	default:
		return sn.normalizeGenericSymbol(symbol)
	}
}

// applyProviderDenormalization applies provider-specific denormalization rules
func (sn *SymbolNormalizer) applyProviderDenormalization(provider, normalizedSymbol string) string {
	switch provider {
	case "alphavantage":
		return sn.denormalizeAlphaVantageSymbol(normalizedSymbol)
	case "polygon":
		return sn.denormalizePolygonSymbol(normalizedSymbol)
	case "yahoo":
		return sn.denormalizeYahooSymbol(normalizedSymbol)
	case "finnhub":
		return sn.denormalizeFinnhubSymbol(normalizedSymbol)
	default:
		return normalizedSymbol
	}
}

// normalizeAlphaVantageSymbol handles Alpha Vantage specific formats
func (sn *SymbolNormalizer) normalizeAlphaVantageSymbol(symbol string) string {
	// Alpha Vantage uses standard formats mostly
	// Handle special cases like BRK.A, BRK.B
	symbol = strings.ReplaceAll(symbol, "-", ".")
	
	// Common replacements
	replacements := map[string]string{
		"BRK-A": "BRK.A",
		"BRK-B": "BRK.B",
	}
	
	if normalized, exists := replacements[symbol]; exists {
		return normalized
	}
	
	return symbol
}

// normalizePolygonSymbol handles Polygon.io specific formats
func (sn *SymbolNormalizer) normalizePolygonSymbol(symbol string) string {
	// Polygon uses standard formats: AAPL, BRK.A, BRK.B
	// Remove any .US suffix
	if strings.HasSuffix(symbol, ".US") {
		symbol = strings.TrimSuffix(symbol, ".US")
	}
	
	return symbol
}

// normalizeYahooSymbol handles Yahoo Finance specific formats
func (sn *SymbolNormalizer) normalizeYahooSymbol(symbol string) string {
	// Yahoo often has exchange suffixes and special formats
	
	// Remove common exchange suffixes
	suffixes := []string{".US", ".TO", ".L", ".AX", ".HK"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(symbol, suffix) {
			symbol = strings.TrimSuffix(symbol, suffix)
			break
		}
	}
	
	// Handle special Yahoo formats
	symbol = strings.ReplaceAll(symbol, "^", "") // Remove ^ prefix for indices
	
	return symbol
}

// normalizeFinnhubSymbol handles Finnhub specific formats
func (sn *SymbolNormalizer) normalizeFinnhubSymbol(symbol string) string {
	// Finnhub typically uses standard US formats
	return symbol
}

// normalizeGenericSymbol handles general symbol cleanup
func (sn *SymbolNormalizer) normalizeGenericSymbol(symbol string) string {
	// Remove common prefixes/suffixes and normalize
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	
	// Remove common exchange indicators
	prefixes := []string{"NYSE:", "NASDAQ:", "NYSE:", "NMS:"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(symbol, prefix) {
			symbol = strings.TrimPrefix(symbol, prefix)
			break
		}
	}
	
	// Validate symbol format (letters, dots, dashes only)
	if !isValidSymbolFormat(symbol) {
		// Log invalid symbol but return as-is
		// In production, might want to reject invalid symbols
	}
	
	return symbol
}

// Denormalization methods (reverse the normalization)
func (sn *SymbolNormalizer) denormalizeAlphaVantageSymbol(symbol string) string {
	return symbol // Alpha Vantage mostly uses standard formats
}

func (sn *SymbolNormalizer) denormalizePolygonSymbol(symbol string) string {
	return symbol // Polygon uses standard formats
}

func (sn *SymbolNormalizer) denormalizeYahooSymbol(symbol string) string {
	// For US stocks, typically no suffix needed
	return symbol
}

func (sn *SymbolNormalizer) denormalizeFinnhubSymbol(symbol string) string {
	return symbol
}

// isValidSymbolFormat checks if symbol contains only valid characters
func isValidSymbolFormat(symbol string) bool {
	// Allow letters, numbers, dots, and dashes
	validSymbol := regexp.MustCompile(`^[A-Z0-9.-]+$`)
	return validSymbol.MatchString(symbol) && len(symbol) <= 12
}

// AddCorporateAction adds or updates a corporate action
func (sn *SymbolNormalizer) AddCorporateAction(action *CorporateAction) error {
	if action.Symbol == "" {
		return fmt.Errorf("corporate action requires symbol")
	}
	
	sn.mu.Lock()
	defer sn.mu.Unlock()
	
	action.CreatedAt = time.Now()
	sn.corporateActions[action.Symbol] = action
	
	// Log the corporate action
	logData := map[string]any{
		"symbol":          action.Symbol,
		"action_type":     string(action.ActionType),
		"effective_date":  action.EffectiveDate.Format(time.RFC3339),
	}
	
	if action.NewSymbol != "" {
		logData["new_symbol"] = action.NewSymbol
	}
	
	if action.Ratio != nil {
		logData["ratio"] = fmt.Sprintf("%d:%d", action.Ratio.From, action.Ratio.To)
	}
	
	// Would call observ.Log in real implementation
	// observ.Log("corporate_action_added", logData)
	
	return nil
}

// GetCorporateAction retrieves a corporate action for a symbol
func (sn *SymbolNormalizer) GetCorporateAction(symbol string) (*CorporateAction, bool) {
	sn.mu.RLock()
	defer sn.mu.RUnlock()
	
	action, exists := sn.corporateActions[symbol]
	return action, exists
}

// isCorporateActionActive checks if a corporate action is currently active
func (sn *SymbolNormalizer) isCorporateActionActive(action *CorporateAction) bool {
	now := time.Now()
	
	// Action is active if we're past the effective date
	if now.Before(action.EffectiveDate) {
		return false
	}
	
	// Action expires if expiration date is set and passed
	if action.ExpirationDate != nil && now.After(*action.ExpirationDate) {
		return false
	}
	
	return true
}

// AddSymbolMapping adds a custom symbol mapping for a provider
func (sn *SymbolNormalizer) AddSymbolMapping(provider, providerSymbol, normalizedSymbol string) {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	
	// Forward mapping: provider:symbol -> normalized
	providerKey := provider + ":" + providerSymbol
	sn.symbolMappings[providerKey] = normalizedSymbol
	
	// Reverse mapping: normalized -> provider -> provider symbol
	if sn.reverseMappings[normalizedSymbol] == nil {
		sn.reverseMappings[normalizedSymbol] = make(map[string]string)
	}
	sn.reverseMappings[normalizedSymbol][provider] = providerSymbol
}

// ValidateSymbol validates a symbol for trading
func (sn *SymbolNormalizer) ValidateSymbol(symbol string) error {
	if symbol == "" {
		return fmt.Errorf("empty symbol")
	}
	
	if len(symbol) > 12 {
		return fmt.Errorf("symbol too long: %s", symbol)
	}
	
	if !isValidSymbolFormat(symbol) {
		return fmt.Errorf("invalid symbol format: %s", symbol)
	}
	
	sn.mu.RLock()
	defer sn.mu.RUnlock()
	
	// Check if symbol is delisted or has blocking corporate actions
	if action, exists := sn.corporateActions[symbol]; exists {
		if sn.isCorporateActionActive(action) {
			switch action.ActionType {
			case ActionDelisting:
				return fmt.Errorf("symbol %s is delisted as of %v", symbol, action.EffectiveDate)
			case ActionAcquisition:
				return fmt.Errorf("symbol %s was acquired as of %v", symbol, action.EffectiveDate)
			case ActionHalt:
				return fmt.Errorf("symbol %s is halted as of %v", symbol, action.EffectiveDate)
			}
		}
	}
	
	return nil
}

// GetSymbolMappings returns all symbol mappings for debugging/monitoring
func (sn *SymbolNormalizer) GetSymbolMappings() map[string]string {
	sn.mu.RLock()
	defer sn.mu.RUnlock()
	
	// Return a copy to prevent mutation
	mappings := make(map[string]string)
	for k, v := range sn.symbolMappings {
		mappings[k] = v
	}
	
	return mappings
}

// GetActiveCorporateActions returns currently active corporate actions
func (sn *SymbolNormalizer) GetActiveCorporateActions() map[string]*CorporateAction {
	sn.mu.RLock()
	defer sn.mu.RUnlock()
	
	active := make(map[string]*CorporateAction)
	for symbol, action := range sn.corporateActions {
		if sn.isCorporateActionActive(action) {
			// Return a copy to prevent mutation
			active[symbol] = &CorporateAction{
				Symbol:         action.Symbol,
				NewSymbol:      action.NewSymbol,
				ActionType:     action.ActionType,
				EffectiveDate:  action.EffectiveDate,
				ExpirationDate: action.ExpirationDate,
				Ratio:          action.Ratio,
				Details:        action.Details,
				CreatedAt:      action.CreatedAt,
			}
		}
	}
	
	return active
}

// CleanupExpiredActions removes expired corporate actions
func (sn *SymbolNormalizer) CleanupExpiredActions() int {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	
	now := time.Now()
	removed := 0
	
	for symbol, action := range sn.corporateActions {
		if action.ExpirationDate != nil && now.After(*action.ExpirationDate) {
			delete(sn.corporateActions, symbol)
			removed++
		}
	}
	
	return removed
}

// LoadCorporateActionsFromConfig loads corporate actions from configuration
func (sn *SymbolNormalizer) LoadCorporateActionsFromConfig(config CorporateActionsConfig) error {
	for _, actionConfig := range config.Actions {
		effectiveDate, err := time.Parse(time.RFC3339, actionConfig.EffectiveDate)
		if err != nil {
			return fmt.Errorf("invalid effective date for %s: %v", actionConfig.Symbol, err)
		}
		
		action := &CorporateAction{
			Symbol:        actionConfig.Symbol,
			NewSymbol:     actionConfig.NewSymbol,
			ActionType:    CorporateActionType(actionConfig.ActionType),
			EffectiveDate: effectiveDate,
			Details:       actionConfig.Details,
		}
		
		if actionConfig.ExpirationDate != "" {
			expDate, err := time.Parse(time.RFC3339, actionConfig.ExpirationDate)
			if err != nil {
				return fmt.Errorf("invalid expiration date for %s: %v", actionConfig.Symbol, err)
			}
			action.ExpirationDate = &expDate
		}
		
		if actionConfig.Ratio != nil {
			action.Ratio = &SplitRatio{
				From: actionConfig.Ratio.From,
				To:   actionConfig.Ratio.To,
			}
		}
		
		if err := sn.AddCorporateAction(action); err != nil {
			return fmt.Errorf("failed to add corporate action for %s: %v", actionConfig.Symbol, err)
		}
	}
	
	return nil
}

// CorporateActionsConfig represents configuration for corporate actions
type CorporateActionsConfig struct {
	Actions []CorporateActionConfig `yaml:"actions"`
}

// CorporateActionConfig represents a single corporate action configuration
type CorporateActionConfig struct {
	Symbol         string            `yaml:"symbol"`
	NewSymbol      string            `yaml:"new_symbol,omitempty"`
	ActionType     string            `yaml:"action_type"`
	EffectiveDate  string            `yaml:"effective_date"`  // RFC3339 format
	ExpirationDate string            `yaml:"expiration_date,omitempty"` // RFC3339 format
	Ratio          *SplitRatio       `yaml:"ratio,omitempty"`
	Details        map[string]any    `yaml:"details,omitempty"`
}