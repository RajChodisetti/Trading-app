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

type Paper struct {
	OutboxPath         string `yaml:"outbox_path"`
	LatencyMsMin       int    `yaml:"latency_ms_min"`
	LatencyMsMax       int    `yaml:"latency_ms_max"`
	SlippageBpsMin     int    `yaml:"slippage_bps_min"`
	SlippageBpsMax     int    `yaml:"slippage_bps_max"`
	DedupeWindowSecs   int    `yaml:"dedupe_window_seconds"`
}

type Wire struct {
	Enabled         bool   `yaml:"enabled"`
	BaseURL         string `yaml:"base_url"`
	PollIntervalMs  int    `yaml:"poll_interval_ms"`
	TimeoutMs       int    `yaml:"timeout_ms"`
	MaxRetries      int    `yaml:"max_retries"`
	BackoffBaseMs   int    `yaml:"backoff_base_ms"`
	BackoffMaxMs    int    `yaml:"backoff_max_ms"`
}

type Root struct {
	TradingMode      string          `yaml:"trading_mode"` // paper | live | dry-run
	GlobalPause      bool            `yaml:"global_pause"`
	Thresholds       Thresholds      `yaml:"thresholds"`
	Session          Session         `yaml:"session"`
	Liquidity        Liquidity       `yaml:"liquidity"`
	Corroboration    Corroboration   `yaml:"corroboration"`
	EarningsEmbargo  EarningsEmbargo `yaml:"earnings"`
	Paper            Paper           `yaml:"paper"`
	Wire             Wire            `yaml:"wire"`
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
	
	// Set paper trading defaults
	if c.Paper.OutboxPath == "" {
		c.Paper.OutboxPath = "data/outbox.jsonl"
	}
	if c.Paper.LatencyMsMin == 0 {
		c.Paper.LatencyMsMin = 100
	}
	if c.Paper.LatencyMsMax == 0 {
		c.Paper.LatencyMsMax = 2000
	}
	if c.Paper.SlippageBpsMin == 0 {
		c.Paper.SlippageBpsMin = 1
	}
	if c.Paper.SlippageBpsMax == 0 {
		c.Paper.SlippageBpsMax = 5
	}
	if c.Paper.DedupeWindowSecs == 0 {
		c.Paper.DedupeWindowSecs = 90
	}
	
	// Set wire defaults
	if c.Wire.BaseURL == "" {
		c.Wire.BaseURL = "http://localhost:8091"
	}
	if c.Wire.PollIntervalMs == 0 {
		c.Wire.PollIntervalMs = 1000
	}
	if c.Wire.TimeoutMs == 0 {
		c.Wire.TimeoutMs = 5000
	}
	if c.Wire.MaxRetries == 0 {
		c.Wire.MaxRetries = 3
	}
	if c.Wire.BackoffBaseMs == 0 {
		c.Wire.BackoffBaseMs = 100
	}
	if c.Wire.BackoffMaxMs == 0 {
		c.Wire.BackoffMaxMs = 5000
	}
	
	return c, nil
}
