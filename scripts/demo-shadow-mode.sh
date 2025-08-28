#!/bin/bash

# Demo Script: Alpha Vantage Shadow Mode with Promotion Gates
# This script demonstrates the live quote system with shadow mode and promotion gates

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
DECISION_BIN="./cmd/decision/decision"
HEALTH_URL="http://127.0.0.1:8090/healthz"
METRICS_URL="http://127.0.0.1:8090/metrics"
DEMO_DURATION=300  # 5 minutes

echo -e "${BLUE}=== Alpha Vantage Shadow Mode Demo ===${NC}"
echo ""
echo "This demo shows:"
echo "1. Shadow mode operation with canary rollout"
echo "2. Live quote adapter with health monitoring"
echo "3. Promotion gates evaluation"
echo "4. Comprehensive metrics and observability"
echo ""

# Check if decision binary exists
if [[ ! -f "$DECISION_BIN" ]]; then
    echo -e "${YELLOW}Building decision engine...${NC}"
    go build -o "$DECISION_BIN" ./cmd/decision
fi

# Check if Alpha Vantage API key is set
if [[ -z "${ALPHAVANTAGE_API_KEY:-}" ]]; then
    echo -e "${YELLOW}Warning: ALPHAVANTAGE_API_KEY not set${NC}"
    echo "Demo will run in mock mode only"
    echo ""
fi

# Create demo configuration
echo -e "${BLUE}Creating demo configuration...${NC}"

# Update config to enable shadow mode
cat > config/demo_live_feeds.yaml << 'EOF'
feeds:
  quotes:
    live_enabled: false                    # Start in shadow mode
    shadow_mode: true                      # Enable shadow comparisons
    provider: "alphavantage"
    canary_symbols: ["AAPL","SPY"]         # Start with these symbols
    priority_symbols: ["AAPL","NVDA","SPY","TSLA","QQQ"]
    canary_duration_minutes: 2             # Short for demo
    
    tiers:
      positions_ms: 800
      watchlist_ms: 2500
      others_ms: 6000
    
    freshness_ceiling_seconds: 5
    freshness_ceiling_ah_seconds: 60
    hysteresis_seconds: 3
    consecutive_breach_to_degrade: 3
    consecutive_ok_to_recover: 5
    
    cache:
      max_entries: 1000
      ttl_seconds: 30
      max_age_extend_seconds: 300
    
    requests_per_minute: 5
    daily_request_cap: 100                 # Conservative for demo
    budget_warning_pct: 0.15
    shadow_sample_rate: 0.5                # Sample 50% for demo
    
    health:
      degraded_error_rate: 0.01
      failed_error_rate: 0.05
      max_consecutive_errors: 3
      freshness_p95_threshold_ms: 5000
      success_rate_threshold: 0.99
    
    fallback_to_cache: true
    fallback_to_mock: true
EOF

# Create test fixture with realistic data
echo -e "${BLUE}Creating test data...${NC}"
cat > fixtures/demo_ticks.json << 'EOF'
{
  "ticks": [
    {
      "symbol": "AAPL",
      "last": 185.50,
      "vwap_5m": 185.25,
      "rel_volume": 1.2,
      "halted": false,
      "premarket": false,
      "postmarket": false,
      "bid": 185.45,
      "ask": 185.55
    },
    {
      "symbol": "SPY", 
      "last": 450.25,
      "vwap_5m": 450.10,
      "rel_volume": 0.9,
      "halted": false,
      "premarket": false,
      "postmarket": false,
      "bid": 450.20,
      "ask": 450.30
    },
    {
      "symbol": "NVDA",
      "last": 875.00,
      "vwap_5m": 874.50,
      "rel_volume": 1.5,
      "halted": false,
      "premarket": false,
      "postmarket": false,
      "bid": 874.75,
      "ask": 875.25
    }
  ]
}
EOF

# Start the decision engine in background
echo -e "${BLUE}Starting decision engine with shadow mode...${NC}"

# Set environment for demo
export QUOTES=alphavantage
export GLOBAL_PAUSE=false
export TRADING_MODE=paper

# Start decision engine
"$DECISION_BIN" -oneshot=false &
DECISION_PID=$!

# Function to cleanup on exit
cleanup() {
    echo -e "\n${YELLOW}Stopping demo...${NC}"
    if kill -0 $DECISION_PID 2>/dev/null; then
        kill $DECISION_PID
        wait $DECISION_PID 2>/dev/null || true
    fi
}
trap cleanup EXIT

# Wait for decision engine to start
echo "Waiting for decision engine to start..."
sleep 5

# Check if decision engine is running
if ! kill -0 $DECISION_PID 2>/dev/null; then
    echo -e "${RED}Decision engine failed to start${NC}"
    exit 1
fi

echo -e "${GREEN}Decision engine started (PID: $DECISION_PID)${NC}"
echo ""

# Function to fetch and display health status
check_health() {
    echo -e "${BLUE}=== Health Check ===${NC}"
    if curl -s "$HEALTH_URL" | jq . 2>/dev/null; then
        echo ""
    else
        echo -e "${YELLOW}Health endpoint not available yet${NC}"
    fi
}

# Function to fetch and display key metrics
check_metrics() {
    echo -e "${BLUE}=== Key Metrics ===${NC}"
    
    local metrics
    if metrics=$(curl -s "$METRICS_URL" 2>/dev/null); then
        echo "Counters:"
        echo "$metrics" | jq -r '.counters | to_entries[] | select(.key | contains("quote")) | "  \(.key): \(.value)"' 2>/dev/null || echo "  No quote counters yet"
        
        echo "Gauges:"
        echo "$metrics" | jq -r '.gauges | to_entries[] | select(.key | contains("quote") or contains("shadow") or contains("hotpath") or contains("budget")) | "  \(.key): \(.value)"' 2>/dev/null || echo "  No relevant gauges yet"
        
        echo "Histograms:"
        echo "$metrics" | jq -r '.histograms | to_entries[] | select(.key | contains("freshness") or contains("latency")) | "  \(.key): \(.value | length) samples"' 2>/dev/null || echo "  No histograms yet"
    else
        echo -e "${YELLOW}Metrics endpoint not available yet${NC}"
    fi
    echo ""
}

# Function to show promotion gates status
check_promotion_gates() {
    echo -e "${BLUE}=== Promotion Gates Status ===${NC}"
    
    # Check if we can use the promotion checker script
    if [[ -f "scripts/check-promotion.sh" ]]; then
        echo "Running promotion gates checker..."
        if timeout 10s scripts/check-promotion.sh --dry-run --healthz-url "$HEALTH_URL" 2>/dev/null; then
            echo ""
        else
            echo -e "${YELLOW}Promotion checker not ready yet${NC}"
        fi
    else
        echo -e "${YELLOW}Promotion checker script not found${NC}"
    fi
}

# Demo loop
echo -e "${BLUE}Running demo for $DEMO_DURATION seconds...${NC}"
echo "Press Ctrl+C to stop early"
echo ""

start_time=$(date +%s)
check_interval=30

while true; do
    current_time=$(date +%s)
    elapsed=$((current_time - start_time))
    
    if [[ $elapsed -ge $DEMO_DURATION ]]; then
        break
    fi
    
    echo -e "${GREEN}=== Demo Status (${elapsed}s elapsed) ===${NC}"
    
    # Check if decision engine is still running
    if ! kill -0 $DECISION_PID 2>/dev/null; then
        echo -e "${RED}Decision engine stopped unexpectedly${NC}"
        break
    fi
    
    # Show health and metrics
    check_health
    check_metrics
    check_promotion_gates
    
    echo -e "${YELLOW}Next update in ${check_interval}s...${NC}"
    echo ""
    
    sleep $check_interval
done

echo -e "${GREEN}Demo completed!${NC}"
echo ""
echo -e "${BLUE}=== Final Status ===${NC}"
check_health
check_metrics

echo -e "${BLUE}=== Demo Summary ===${NC}"
echo "✓ Shadow mode demonstrated with canary rollout"
echo "✓ Live quote adapter with health monitoring"
echo "✓ Comprehensive metrics and observability"
echo "✓ Promotion gates framework operational"
echo ""
echo "Key files created:"
echo "  - config/demo_live_feeds.yaml (demo configuration)"
echo "  - fixtures/demo_ticks.json (test data)"
echo ""
echo "Next steps:"
echo "1. Set ALPHAVANTAGE_API_KEY for live data"
echo "2. Run promotion gates checker for 30-60 minutes"
echo "3. Enable live_enabled: true when gates pass"
echo ""
echo -e "${GREEN}Demo complete!${NC}"