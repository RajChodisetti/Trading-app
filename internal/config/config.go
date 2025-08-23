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
	Enabled                     bool          `yaml:"enabled"`
	BaseURL                     string        `yaml:"base_url"`
	Transport                   string        `yaml:"transport"`                     // "sse" or "http"
	TimeoutSeconds              int           `yaml:"timeout_seconds"`
	MaxEvents                   int           `yaml:"max_events"`                    // -1 = unlimited
	MaxDurationSeconds          int           `yaml:"max_duration_seconds"`          // -1 = unlimited
	Reconnect                   WireReconnect `yaml:"reconnect"`
	HeartbeatSeconds            int           `yaml:"heartbeat_seconds"`
	MaxChannelBuffer            int           `yaml:"max_channel_buffer"`
	FallbackToHttpAfterFailures int           `yaml:"fallback_to_http_after_failures"`
	
	// Legacy HTTP polling settings (for backward compatibility)
	PollIntervalMs  int    `yaml:"poll_interval_ms"`
	TimeoutMs       int    `yaml:"timeout_ms"`
	MaxRetries      int    `yaml:"max_retries"`
	BackoffBaseMs   int    `yaml:"backoff_base_ms"`
	BackoffMaxMs    int    `yaml:"backoff_max_ms"`
}

type WireReconnect struct {
	InitialDelayMs int `yaml:"initial_delay_ms"`
	MaxDelayMs     int `yaml:"max_delay_ms"`
	MaxAttempts    int `yaml:"max_attempts"`    // -1 = infinite
	JitterMs       int `yaml:"jitter_ms"`
}

type Slack struct {
	Enabled                     bool   `yaml:"enabled"`
	WebhookURL                  string `yaml:"webhook_url"`
	ChannelDefault              string `yaml:"channel_default"`
	AlertOnBuy1x                bool   `yaml:"alert_on_buy_1x"`
	AlertOnBuy5x                bool   `yaml:"alert_on_buy_5x"`
	AlertOnRejectGates          bool   `yaml:"alert_on_reject_gates"`
	RateLimitPerMin             int    `yaml:"rate_limit_per_min"`
	RateLimitPerSymbolPerMin    int    `yaml:"rate_limit_per_symbol_per_min"`
}

type RuntimeOverrides struct {
	Enabled              bool   `yaml:"enabled"`
	FilePath             string `yaml:"file_path"`
	RefreshIntervalMs    int    `yaml:"refresh_interval_ms"`
	ExpiryHoursDefault   int    `yaml:"expiry_hours_default"`
}

type Security struct {
	SlackSigningSecretEnv   string   `yaml:"slack_signing_secret_env"`
	AllowedSlackUserIds     []string `yaml:"allowed_slack_user_ids"`
}

type Portfolio struct {
	Enabled                     bool    `yaml:"enabled"`
	StateFilePath               string  `yaml:"state_file_path"`
	MaxPositionSizeUSD          float64 `yaml:"max_position_size_usd"`
	MaxPortfolioExposurePct     float64 `yaml:"max_portfolio_exposure_pct"`
	DailyTradeLimitPerSymbol    int     `yaml:"daily_trade_limit_per_symbol"`
	CooldownMinutesPerSymbol    int     `yaml:"cooldown_minutes_per_symbol"`
	MaxDailyExposureIncreasePct float64 `yaml:"max_daily_exposure_increase_pct"`
	ResetDailyLimitsAtHour      int     `yaml:"reset_daily_limits_at_hour"`
	PositionDecayDays           int     `yaml:"position_decay_days"`
}

type StopLoss struct {
	Enabled               bool    `yaml:"enabled"`
	DefaultStopLossPct    float64 `yaml:"default_stop_loss_pct"`
	EmergencyStopLossPct  float64 `yaml:"emergency_stop_loss_pct"`
	AllowAfterHours       bool    `yaml:"allow_after_hours"`
	CooldownHours         int     `yaml:"cooldown_hours"`
}

type SectorLimits struct {
	Enabled               bool               `yaml:"enabled"`
	MaxSectorExposurePct  float64            `yaml:"max_sector_exposure_pct"`
	SectorMap             map[string]string  `yaml:"sector_map"`
}

type Drawdown struct {
	Enabled                      bool    `yaml:"enabled"`
	DailyWarningPct              float64 `yaml:"daily_warning_pct"`
	DailyPausePct                float64 `yaml:"daily_pause_pct"`
	WeeklyWarningPct             float64 `yaml:"weekly_warning_pct"`
	WeeklyPausePct               float64 `yaml:"weekly_pause_pct"`
	SizeMultiplierOnWarningPct   float64 `yaml:"size_multiplier_on_warning_pct"`
}

type RiskControls struct {
	StopLoss      StopLoss      `yaml:"stop_loss"`
	SectorLimits  SectorLimits  `yaml:"sector_limits"`
	Drawdown      Drawdown      `yaml:"drawdown"`
}

type Monitoring struct {
	DashboardRecentTrades      int `yaml:"dashboard_recent_trades"`
	HealthCheckIntervalMinutes int `yaml:"health_check_interval_minutes"`
}

type Root struct {
	TradingMode       string            `yaml:"trading_mode"` // paper | live | dry-run
	GlobalPause       bool              `yaml:"global_pause"`
	Thresholds        Thresholds        `yaml:"thresholds"`
	Session           Session           `yaml:"session"`
	Liquidity         Liquidity         `yaml:"liquidity"`
	Corroboration     Corroboration     `yaml:"corroboration"`
	EarningsEmbargo   EarningsEmbargo   `yaml:"earnings"`
	Paper             Paper             `yaml:"paper"`
	Wire              Wire              `yaml:"wire"`
	Slack             Slack             `yaml:"slack"`
	RuntimeOverrides  RuntimeOverrides  `yaml:"runtime_overrides"`
	Security          Security          `yaml:"security"`
	Portfolio         Portfolio         `yaml:"portfolio"`
	RiskControls      RiskControls      `yaml:"risk_controls"`
	Monitoring        Monitoring        `yaml:"monitoring"`
	BaseUSD           float64           `yaml:"base_usd"`
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
	
	// Set Slack defaults
	if c.Slack.ChannelDefault == "" {
		c.Slack.ChannelDefault = "#trading-alerts"
	}
	if c.Slack.RateLimitPerMin == 0 {
		c.Slack.RateLimitPerMin = 10
	}
	if c.Slack.RateLimitPerSymbolPerMin == 0 {
		c.Slack.RateLimitPerSymbolPerMin = 3
	}
	
	// Set runtime overrides defaults
	if c.RuntimeOverrides.FilePath == "" {
		c.RuntimeOverrides.FilePath = "data/runtime_overrides.json"
	}
	if c.RuntimeOverrides.RefreshIntervalMs == 0 {
		c.RuntimeOverrides.RefreshIntervalMs = 10000
	}
	if c.RuntimeOverrides.ExpiryHoursDefault == 0 {
		c.RuntimeOverrides.ExpiryHoursDefault = 24
	}
	
	// Set security defaults
	if c.Security.SlackSigningSecretEnv == "" {
		c.Security.SlackSigningSecretEnv = "SLACK_SIGNING_SECRET"
	}
	
	return c, nil
}
