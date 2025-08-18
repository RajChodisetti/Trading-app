package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Thresholds struct {
	Positive float64 `yaml:"positive"`
	VeryPos  float64 `yaml:"very_positive"`
}

type Session struct {
	AllowAfterHours bool `yaml:"allow_after_hours"`
	BlockPremarket  bool `yaml:"block_premarket"`  // optional override
	BlockPostmarket bool `yaml:"block_postmarket"` // optional override
}

type Liquidity struct {
	MaxSpreadBps float64 `yaml:"max_spread_bps"`
}

type Corroboration struct {
	RequirePositivePR bool `yaml:"require_positive_pr"`
	WindowSeconds     int  `yaml:"window_seconds"`
}

type EarningsEmbargo struct {
	Enabled             bool `yaml:"enabled"`
	BlockOnEstimated    bool `yaml:"block_on_estimated"`
	MinutesBefore       int  `yaml:"minutes_before"`
	MinutesAfter        int  `yaml:"minutes_after"`
}

type Root struct {
	TradingMode      string          `yaml:"trading_mode"` // paper | live | dry-run
	GlobalPause      bool            `yaml:"global_pause"`
	Thresholds       Thresholds      `yaml:"thresholds"`
	Session          Session         `yaml:"session"`
	Liquidity        Liquidity       `yaml:"liquidity"`
	Corroboration    Corroboration   `yaml:"corroboration"`
	EarningsEmbargo  EarningsEmbargo `yaml:"earnings"`
	BaseUSD          float64         `yaml:"base_usd"`
}

func Load(path string) (Root, error) {
	var c Root
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.BaseUSD == 0 {
		c.BaseUSD = 2000
	}
	return c, nil
}
