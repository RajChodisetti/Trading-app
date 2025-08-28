package adapters

import (
	"context"
	"fmt"
	"sync"
)

// SymbolAwareQuotesAdapter wraps a quotes adapter with symbol normalization
type SymbolAwareQuotesAdapter struct {
	adapter    QuotesAdapter
	normalizer *SymbolNormalizer
	provider   string
	mu         sync.RWMutex
}

// NewSymbolAwareQuotesAdapter creates a symbol-aware wrapper around a quotes adapter
func NewSymbolAwareQuotesAdapter(provider string, adapter QuotesAdapter, normalizer *SymbolNormalizer) *SymbolAwareQuotesAdapter {
	return &SymbolAwareQuotesAdapter{
		adapter:    adapter,
		normalizer: normalizer,
		provider:   provider,
	}
}

// GetQuote gets a quote with automatic symbol normalization
func (sawa *SymbolAwareQuotesAdapter) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	// First validate the symbol
	if err := sawa.normalizer.ValidateSymbol(symbol); err != nil {
		return nil, NewBadSymbolError(symbol, err.Error())
	}
	
	// Normalize symbol for our internal use
	normalizedSymbol, err := sawa.normalizer.NormalizeSymbol(sawa.provider, symbol)
	if err != nil {
		return nil, NewBadSymbolError(symbol, fmt.Sprintf("normalization failed: %v", err))
	}
	
	// Convert to provider-specific format
	providerSymbol, err := sawa.normalizer.DenormalizeSymbol(sawa.provider, normalizedSymbol)
	if err != nil {
		return nil, NewBadSymbolError(symbol, fmt.Sprintf("denormalization failed: %v", err))
	}
	
	// Get quote using provider-specific symbol
	quote, err := sawa.adapter.GetQuote(ctx, providerSymbol)
	if err != nil {
		return nil, err
	}
	
	// Ensure the returned quote uses the normalized symbol
	quote.Symbol = normalizedSymbol
	
	return quote, nil
}

// GetQuotes gets multiple quotes with automatic symbol normalization
func (sawa *SymbolAwareQuotesAdapter) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	// Validate and normalize all symbols first
	normalizedSymbols := make([]string, 0, len(symbols))
	symbolMapping := make(map[string]string) // normalized -> original
	providerSymbols := make([]string, 0, len(symbols))
	
	for _, symbol := range symbols {
		// Validate symbol
		if err := sawa.normalizer.ValidateSymbol(symbol); err != nil {
			// Log validation error but continue with other symbols
			// In production, might want to include failed symbols in response
			continue
		}
		
		// Normalize symbol
		normalizedSymbol, err := sawa.normalizer.NormalizeSymbol(sawa.provider, symbol)
		if err != nil {
			// Log normalization error but continue
			continue
		}
		
		// Convert to provider format
		providerSymbol, err := sawa.normalizer.DenormalizeSymbol(sawa.provider, normalizedSymbol)
		if err != nil {
			// Log denormalization error but continue
			continue
		}
		
		normalizedSymbols = append(normalizedSymbols, normalizedSymbol)
		symbolMapping[normalizedSymbol] = symbol
		providerSymbols = append(providerSymbols, providerSymbol)
	}
	
	// Get quotes from provider
	providerQuotes, err := sawa.adapter.GetQuotes(ctx, providerSymbols)
	if err != nil {
		return nil, err
	}
	
	// Convert back to normalized symbols
	results := make(map[string]*Quote)
	for _, normalizedSymbol := range normalizedSymbols {
		providerSymbol, _ := sawa.normalizer.DenormalizeSymbol(sawa.provider, normalizedSymbol)
		
		if quote, exists := providerQuotes[providerSymbol]; exists {
			// Ensure quote uses normalized symbol
			quote.Symbol = normalizedSymbol
			results[normalizedSymbol] = quote
		}
	}
	
	return results, nil
}

// HealthCheck passes through to underlying adapter
func (sawa *SymbolAwareQuotesAdapter) HealthCheck(ctx context.Context) error {
	return sawa.adapter.HealthCheck(ctx)
}

// Close passes through to underlying adapter
func (sawa *SymbolAwareQuotesAdapter) Close() error {
	return sawa.adapter.Close()
}

// Enhanced Provider Manager with Symbol Normalization
type SymbolAwareProviderManager struct {
	*ProviderManager
	normalizer *SymbolNormalizer
}

// NewSymbolAwareProviderManager creates a provider manager with symbol normalization
func NewSymbolAwareProviderManager(config ProviderManagerConfig, normalizer *SymbolNormalizer) *SymbolAwareProviderManager {
	return &SymbolAwareProviderManager{
		ProviderManager: NewProviderManager(config),
		normalizer:      normalizer,
	}
}

// RegisterProvider registers a provider with symbol normalization wrapper
func (sapm *SymbolAwareProviderManager) RegisterProvider(name string, provider QuotesAdapter) {
	// Wrap the provider with symbol normalization
	symbolAwareProvider := NewSymbolAwareQuotesAdapter(name, provider, sapm.normalizer)
	
	// Register the wrapped provider
	sapm.ProviderManager.RegisterProvider(name, symbolAwareProvider)
}

// GetQuote gets a quote with full symbol normalization and provider management
func (sapm *SymbolAwareProviderManager) GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	// First validate the symbol globally
	if err := sapm.normalizer.ValidateSymbol(symbol); err != nil {
		return nil, NewBadSymbolError(symbol, err.Error())
	}
	
	// Check for corporate actions that might affect the symbol
	if action, exists := sapm.normalizer.GetCorporateAction(symbol); exists {
		if sapm.normalizer.isCorporateActionActive(action) {
			switch action.ActionType {
			case ActionRename:
				// Automatically use new symbol
				if action.NewSymbol != "" {
					symbol = action.NewSymbol
				}
			case ActionSplit, ActionReverseSplit:
				// Log the split for price adjustment awareness
				// The quote will still be fetched, but consumers should be aware
				// This would trigger observ.Log in real implementation
			case ActionDelisting, ActionAcquisition:
				return nil, NewBadSymbolError(symbol, fmt.Sprintf("symbol affected by %s", action.ActionType))
			}
		}
	}
	
	// Use the underlying provider manager with normalized symbol
	return sapm.ProviderManager.GetQuote(ctx, symbol)
}

// GetQuotes gets multiple quotes with symbol normalization
func (sapm *SymbolAwareProviderManager) GetQuotes(ctx context.Context, symbols []string) (map[string]*Quote, error) {
	// Pre-process symbols for corporate actions
	processedSymbols := make([]string, 0, len(symbols))
	
	for _, symbol := range symbols {
		if err := sapm.normalizer.ValidateSymbol(symbol); err != nil {
			// Skip invalid symbols but log the error
			continue
		}
		
		// Check corporate actions
		if action, exists := sapm.normalizer.GetCorporateAction(symbol); exists {
			if sapm.normalizer.isCorporateActionActive(action) {
				switch action.ActionType {
				case ActionRename:
					if action.NewSymbol != "" {
						processedSymbols = append(processedSymbols, action.NewSymbol)
					} else {
						processedSymbols = append(processedSymbols, symbol)
					}
				case ActionDelisting, ActionAcquisition:
					// Skip delisted/acquired symbols
					continue
				default:
					processedSymbols = append(processedSymbols, symbol)
				}
			} else {
				processedSymbols = append(processedSymbols, symbol)
			}
		} else {
			processedSymbols = append(processedSymbols, symbol)
		}
	}
	
	return sapm.ProviderManager.GetQuotes(ctx, processedSymbols)
}

// CorporateActionAwareQuote extends Quote with corporate action information
type CorporateActionAwareQuote struct {
	*Quote
	CorporateAction *CorporateAction `json:"corporate_action,omitempty"`
	AdjustedPrice   *float64         `json:"adjusted_price,omitempty"`   // Price adjusted for splits
	SplitMultiplier *float64         `json:"split_multiplier,omitempty"` // Multiplier for price adjustment
}

// GetQuoteWithCorporateActions gets a quote with corporate action adjustments
func (sapm *SymbolAwareProviderManager) GetQuoteWithCorporateActions(ctx context.Context, symbol string) (*CorporateActionAwareQuote, error) {
	// Get the base quote
	quote, err := sapm.GetQuote(ctx, symbol)
	if err != nil {
		return nil, err
	}
	
	// Create corporate action aware quote
	caQuote := &CorporateActionAwareQuote{
		Quote: quote,
	}
	
	// Check for corporate actions
	if action, exists := sapm.normalizer.GetCorporateAction(symbol); exists {
		if sapm.normalizer.isCorporateActionActive(action) {
			caQuote.CorporateAction = action
			
			// Calculate split adjustments
			if action.ActionType == ActionSplit || action.ActionType == ActionReverseSplit {
				if action.Ratio != nil {
					var multiplier float64
					if action.ActionType == ActionSplit {
						// For splits, divide price by ratio (2:1 split = price/2)
						multiplier = float64(action.Ratio.From) / float64(action.Ratio.To)
					} else {
						// For reverse splits, multiply price by ratio (1:10 reverse = price*10)
						multiplier = float64(action.Ratio.To) / float64(action.Ratio.From)
					}
					
					caQuote.SplitMultiplier = &multiplier
					adjustedPrice := quote.Last * multiplier
					caQuote.AdjustedPrice = &adjustedPrice
				}
			}
		}
	}
	
	return caQuote, nil
}

// Symbol validation utilities

// IsValidUSSymbol checks if symbol follows US stock symbol format
func IsValidUSSymbol(symbol string) bool {
	if len(symbol) == 0 || len(symbol) > 5 {
		return false
	}
	
	// US symbols are typically all letters
	for _, char := range symbol {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	
	return true
}

// IsValidUSSymbolWithClasses checks if symbol follows US format including share classes
func IsValidUSSymbolWithClasses(symbol string) bool {
	if len(symbol) == 0 || len(symbol) > 8 {
		return false
	}
	
	// Handle formats like BRK.A, BRK.B, GOOG, GOOGL
	if symbol == "BRK.A" || symbol == "BRK.B" {
		return true
	}
	
	// Standard format check
	return IsValidUSSymbol(symbol) || (len(symbol) <= 6 && isValidSymbolFormat(symbol))
}

// Common corporate actions for initialization

// GetCommonCorporateActions returns commonly known corporate actions
func GetCommonCorporateActions() []CorporateActionConfig {
	return []CorporateActionConfig{
		// Example corporate actions (in real system, these would come from data feed)
		// {
		//     Symbol:        "FB",
		//     NewSymbol:     "META",
		//     ActionType:    "rename",
		//     EffectiveDate: "2022-06-09T00:00:00Z",
		//     Details: map[string]any{
		//         "reason": "Company rebrand to Meta Platforms",
		//     },
		// },
		// Note: Real corporate actions would be loaded from external data sources
	}
}

// InitializeSymbolNormalization sets up symbol normalization with common mappings
func InitializeSymbolNormalization() *SymbolNormalizer {
	normalizer := NewSymbolNormalizer()
	
	// Add common symbol mappings
	commonMappings := map[string]map[string]string{
		// provider -> { providerSymbol: normalizedSymbol }
		"alphavantage": {
			"BRK-A": "BRK.A",
			"BRK-B": "BRK.B",
		},
		"yahoo": {
			"BRK-A.US": "BRK.A",
			"BRK-B.US": "BRK.B",
		},
	}
	
	for provider, mappings := range commonMappings {
		for providerSymbol, normalized := range mappings {
			normalizer.AddSymbolMapping(provider, providerSymbol, normalized)
		}
	}
	
	// Load common corporate actions
	corporateActionsConfig := CorporateActionsConfig{
		Actions: GetCommonCorporateActions(),
	}
	
	if err := normalizer.LoadCorporateActionsFromConfig(corporateActionsConfig); err != nil {
		// Log error but continue - this is initialization, not critical
		// In real system: observ.Log("symbol_normalization_init_warning", map[string]any{"error": err.Error()})
	}
	
	return normalizer
}