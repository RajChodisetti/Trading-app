package risk

import (
	"github.com/Rajchodisetti/trading-app/internal/observ"
)

// SectorExposureManager manages sector exposure limits
type SectorExposureManager struct {
	sectorMap map[string]string // symbol -> sector
}

// NewSectorExposureManager creates a new sector exposure manager
func NewSectorExposureManager(sectorMap map[string]string) *SectorExposureManager {
	return &SectorExposureManager{
		sectorMap: sectorMap,
	}
}

// GetSector returns the sector for a given symbol
func (sem *SectorExposureManager) GetSector(symbol string) string {
	if sector, exists := sem.sectorMap[symbol]; exists {
		return sector
	}
	return "other" // Default sector for unmapped symbols
}

// CheckSectorLimit evaluates if a new position would exceed sector exposure limits
func (sem *SectorExposureManager) CheckSectorLimit(symbol string, proposedNotional, nav float64, positions map[string]float64, config SectorLimitsConfig) (bool, string) {
	if !config.Enabled {
		return false, ""
	}
	
	// Only apply sector limits if we have meaningful existing exposure (>5% of NAV)
	// This prevents blocking the very first trades in a fresh portfolio
	totalExistingExposure := 0.0
	for _, notional := range positions {
		if notional < 0 {
			totalExistingExposure += -notional
		} else {
			totalExistingExposure += notional
		}
	}
	
	if nav > 0 && (totalExistingExposure/nav)*100 < 5.0 {
		// Fresh portfolio with minimal exposure - allow trades
		return false, ""
	}
	
	sector := sem.GetSector(symbol)
	
	// Calculate current sector exposure
	currentSectorExposure := sem.calculateSectorExposure(sector, positions)
	
	// Calculate new sector exposure after proposed trade
	newSectorExposure := currentSectorExposure + proposedNotional
	newSectorExposurePct := (newSectorExposure / nav) * 100
	
	// Update sector exposure metric
	observ.SetGauge("sector_exposure_pct", newSectorExposurePct, map[string]string{"sector": sector})
	
	// Check if it would exceed the limit
	if newSectorExposurePct > config.MaxSectorExposurePct {
		observ.IncCounter("sector_limit_blocks_total", map[string]string{"sector": sector})
		return true, sector // Limit exceeded
	}
	
	return false, sector
}

// calculateSectorExposure calculates total absolute notional exposure for a sector
func (sem *SectorExposureManager) calculateSectorExposure(targetSector string, positions map[string]float64) float64 {
	total := 0.0
	
	for symbol, notional := range positions {
		sector := sem.GetSector(symbol)
		if sector == targetSector {
			// Use absolute value for gross exposure calculation
			if notional < 0 {
				total += -notional
			} else {
				total += notional
			}
		}
	}
	
	return total
}

// GetAllSectorExposures returns current exposure for all sectors
func (sem *SectorExposureManager) GetAllSectorExposures(positions map[string]float64, nav float64) map[string]float64 {
	sectorExposures := make(map[string]float64)
	
	// Initialize all known sectors
	sectors := make(map[string]bool)
	for _, sector := range sem.sectorMap {
		sectors[sector] = true
	}
	sectors["other"] = true // Include default sector
	
	for sector := range sectors {
		exposure := sem.calculateSectorExposure(sector, positions)
		exposurePct := (exposure / nav) * 100
		sectorExposures[sector] = exposurePct
		
		// Update metrics for all sectors
		observ.SetGauge("sector_exposure_pct", exposurePct, map[string]string{"sector": sector})
	}
	
	return sectorExposures
}

// SectorLimitsConfig represents sector limits configuration
type SectorLimitsConfig struct {
	Enabled              bool
	MaxSectorExposurePct float64
	SectorMap            map[string]string
}