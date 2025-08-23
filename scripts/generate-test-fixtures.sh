#!/usr/bin/env bash
set -euo pipefail

# Generate test fixtures with current timestamps that won't expire
# This script ensures all timestamps are fresh and tests don't fail due to timing issues

FIXTURES_DIR="fixtures"

# Get current time in market hours (2 PM UTC = 10 AM ET during standard time)
CURRENT_TIME=$(date -u +"%Y-%m-%dT14:%M:%SZ")
PR_TIME=$(date -u -v-5M +"%Y-%m-%dT14:%M:%SZ")  # 5 minutes ago
EDITORIAL_TIME=$(date -u -v-2M +"%Y-%m-%dT14:%M:%SZ")  # 2 minutes ago
LATE_EDITORIAL_TIME=$(date -u -v+20M +"%Y-%m-%dT14:%M:%SZ")  # 20 minutes from now (outside corroboration window)

# Generate earnings embargo times (current time to 2 hours from now)
EARNINGS_START=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
EARNINGS_END=$(date -u -v+2H +"%Y-%m-%dT%H:%M:%SZ")

echo "Generating test fixtures with dynamic timestamps..."
echo "Current time: $CURRENT_TIME"
echo "PR time: $PR_TIME"
echo "Editorial time: $EDITORIAL_TIME"
echo "Earnings window: $EARNINGS_START to $EARNINGS_END"

# Generate main ticks.json with market hours data (no session/liquidity issues)
cat > "$FIXTURES_DIR/ticks.json" <<EOF
{
  "ticks": [
    {
      "ts_utc": "$CURRENT_TIME",
      "symbol": "AAPL",
      "bid": 210.00,
      "ask": 210.05,
      "last": 210.02,
      "vwap_5m": 209.80,
      "rel_volume": 1.2,
      "halted": false,
      "premarket": false,
      "postmarket": false
    },
    {
      "symbol": "NVDA",
      "bid": 450.00,
      "ask": 450.10,
      "last": 450.05,
      "vwap_5m": 449.50,
      "rel_volume": 1.1,
      "halted": true,
      "premarket": false,
      "postmarket": false
    },
    {
      "symbol": "BIOX",
      "bid": 15.00,
      "ask": 15.01,
      "last": 15.00,
      "vwap_5m": 15.02,
      "rel_volume": 1.2,
      "halted": false,
      "premarket": false,
      "postmarket": false
    }
  ]
}
EOF

# Generate news_pr_only.json with recent PR timestamp
cat > "$FIXTURES_DIR/news_pr_only.json" <<EOF
{
  "news": [
    {
      "id": "bw-300",
      "provider": "businesswire",
      "published_at_utc": "$PR_TIME",
      "headline": "BioX Reports Strong Phase 2 Efficacy Results",
      "body": "Company-issued press release claims positive efficacy.",
      "urls": ["https://example.com/biox-pr"],
      "tickers": ["BIOX"],
      "is_press_release": true,
      "is_correction": false,
      "supersedes_id": null,
      "source_weight": 1.2,
      "headline_hash": "pr-biox-2"
    }
  ]
}
EOF

# Generate news_pr_plus_editorial.json with PR + timely editorial
cat > "$FIXTURES_DIR/news_pr_plus_editorial.json" <<EOF
{
  "news": [
    {
      "id": "bw-400",
      "provider": "businesswire",
      "published_at_utc": "$PR_TIME",
      "headline": "BioX Reports Strong Phase 2 Efficacy Results",
      "body": "Company-issued press release claims positive efficacy.",
      "urls": ["https://example.com/biox-pr"],
      "tickers": ["BIOX"],
      "is_press_release": true,
      "is_correction": false,
      "supersedes_id": null,
      "source_weight": 0.4,
      "headline_hash": "pr-biox-3"
    },
    {
      "id": "reuters-300",
      "provider": "reuters",
      "published_at_utc": "$EDITORIAL_TIME",
      "headline": "Editors: BioX Phase 2 signals promising but needs Phase 3",
      "body": "Independent editorial coverage citing external analysts.",
      "urls": ["https://example.com/biox-editorial"],
      "tickers": ["BIOX"],
      "is_press_release": false,
      "is_correction": false,
      "supersedes_id": null,
      "source_weight": 1.0,
      "headline_hash": "ed-biox-2"
    }
  ]
}
EOF

# Generate news_pr_then_late_editorial.json with PR + late editorial (outside window)
cat > "$FIXTURES_DIR/news_pr_then_late_editorial.json" <<EOF
{
  "news": [
    {
      "id": "bw-500",
      "provider": "businesswire",
      "published_at_utc": "$PR_TIME",
      "headline": "BioX Reports Strong Phase 2 Efficacy Results",
      "body": "Company-issued press release claims positive efficacy.",
      "urls": ["https://example.com/biox-pr"],
      "tickers": ["BIOX"],
      "is_press_release": true,
      "is_correction": false,
      "supersedes_id": null,
      "source_weight": 0.4,
      "headline_hash": "pr-biox-4"
    },
    {
      "id": "reuters-400",
      "provider": "reuters",
      "published_at_utc": "$LATE_EDITORIAL_TIME",
      "headline": "Editors: BioX Phase 2 signals promising but needs Phase 3",
      "body": "Independent editorial coverage citing external analysts.",
      "urls": ["https://example.com/biox-editorial"],
      "tickers": ["BIOX"],
      "is_press_release": false,
      "is_correction": false,
      "supersedes_id": null,
      "source_weight": 1.0,
      "headline_hash": "ed-biox-3"
    }
  ]
}
EOF

# Generate default news.json with OLD PR (outside corroboration window for base tests)
# Use a fixed old timestamp that's definitely outside the 15-minute corroboration window
OLD_PR_TIME="2025-08-18T14:15:00Z"  # Several days ago (definitely outside window)

cat > "$FIXTURES_DIR/news.json" <<EOF
{
  "news": [
    {
      "id": "bw-default",
      "provider": "businesswire",
      "published_at_utc": "$OLD_PR_TIME",
      "headline": "BioX Reports Strong Phase 2 Efficacy Results",
      "body": "Company-issued press release claims positive efficacy.",
      "urls": ["https://example.com/biox-pr"],
      "tickers": ["BIOX"],
      "is_press_release": true,
      "is_correction": false,
      "supersedes_id": null,
      "source_weight": 1.2,
      "headline_hash": "pr-biox-default"
    }
  ]
}
EOF

echo "✅ Generated fresh test fixtures with current timestamps"
echo "   - Market hours ticks (no session/liquidity issues)"
echo "   - Default news with old PR: $OLD_PR_TIME (outside corroboration window)"
echo "   - PR timestamp: $PR_TIME (within corroboration window)"
echo "   - Editorial timestamp: $EDITORIAL_TIME (within window)"
echo "   - Late editorial: $LATE_EDITORIAL_TIME (outside window)"