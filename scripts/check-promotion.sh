#!/bin/bash

# Promotion Gates Checker for Alpha Vantage Shadow Mode
# Runs every 30s, computes rolling window metrics, prints PASS/FAIL
# Usage: ./scripts/check-promotion.sh [options]
#
# Options:
#   --healthz-url URL    Health endpoint URL (default: http://127.0.0.1:8090/healthz)
#   --window-minutes N   Rolling window in minutes (default: 30)
#   --check-interval N   Check interval in seconds (default: 30)
#   --output-file FILE   Output results to file (optional)
#   --verbose            Enable verbose output

set -euo pipefail

# Default configuration
HEALTHZ_URL="http://127.0.0.1:8090/healthz"
WINDOW_MINUTES=30
CHECK_INTERVAL=30
OUTPUT_FILE=""
VERBOSE=0
DRY_RUN=0

# Promotion gate thresholds
FRESHNESS_P95_THRESHOLD_MS=5000      # P95 freshness must be < 5s
SUCCESS_RATE_THRESHOLD=0.99          # 99% success rate required  
DECISION_LATENCY_P95_THRESHOLD_MS=200 # Decision P95 must be < 200ms
MIN_SAMPLES=50                       # Minimum samples required
HOTPATH_CALLS_MAX=0                  # Must be exactly 0

# State file for rolling window
STATE_DIR="/tmp/promotion-checker"
STATE_FILE="$STATE_DIR/promotion_state.json"
RESULTS_FILE="$STATE_DIR/promotion_results.log"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print usage
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Promotion Gates Checker for Alpha Vantage Shadow Mode

OPTIONS:
    --healthz-url URL      Health endpoint URL (default: $HEALTHZ_URL)
    --window-minutes N     Rolling window in minutes (default: $WINDOW_MINUTES)
    --check-interval N     Check interval in seconds (default: $CHECK_INTERVAL)
    --output-file FILE     Output results to file (optional)
    --verbose              Enable verbose output
    --dry-run              Single check and exit (no loop)
    --help                 Show this help message

EXAMPLES:
    # Run with defaults
    $0

    # Check every 10s with 60-minute window
    $0 --check-interval 10 --window-minutes 60

    # Single check with verbose output
    $0 --dry-run --verbose

    # Output to file
    $0 --output-file /var/log/promotion-gates.log
EOF
}

# Function to log messages
log() {
    local level=$1
    local message=$2
    local timestamp=$(date '+%Y-%m-%d %H:%M:%S')
    
    case $level in
        "INFO")
            if [[ $VERBOSE -eq 1 ]]; then
                echo -e "${BLUE}[INFO]${NC} $timestamp - $message"
            fi
            ;;
        "WARN")
            echo -e "${YELLOW}[WARN]${NC} $timestamp - $message" >&2
            ;;
        "ERROR")
            echo -e "${RED}[ERROR]${NC} $timestamp - $message" >&2
            ;;
        "SUCCESS")
            echo -e "${GREEN}[PASS]${NC} $timestamp - $message"
            ;;
        "FAIL")
            echo -e "${RED}[FAIL]${NC} $timestamp - $message"
            ;;
    esac
    
    # Write to output file if specified
    if [[ -n "$OUTPUT_FILE" ]]; then
        echo "[$level] $timestamp - $message" >> "$OUTPUT_FILE"
    fi
}

# Function to create state directory
init_state_dir() {
    mkdir -p "$STATE_DIR"
    if [[ ! -f "$STATE_FILE" ]]; then
        echo '{"samples": [], "last_check": ""}' > "$STATE_FILE"
    fi
}

# Function to fetch health data
fetch_health_data() {
    local url=$1
    local temp_file=$(mktemp)
    
    if curl -s --connect-timeout 5 --max-time 10 "$url" > "$temp_file" 2>/dev/null; then
        if jq . "$temp_file" >/dev/null 2>&1; then
            cat "$temp_file"
        else
            log "ERROR" "Invalid JSON response from health endpoint"
            rm -f "$temp_file"
            return 1
        fi
    else
        log "ERROR" "Failed to fetch health data from $url"
        rm -f "$temp_file"
        return 1
    fi
    
    rm -f "$temp_file"
}

# Function to add sample to rolling window
add_sample() {
    local sample=$1
    local state_json=$(cat "$STATE_FILE")
    local window_start=$(date -d "$WINDOW_MINUTES minutes ago" '+%Y-%m-%dT%H:%M:%SZ')
    
    # Add new sample and filter old samples
    echo "$state_json" | jq --arg sample "$sample" --arg window_start "$window_start" '
        .samples += [$sample | fromjson] |
        .samples = [.samples[] | select(.timestamp >= $window_start)] |
        .last_check = now | strftime("%Y-%m-%dT%H:%M:%SZ")
    ' > "$STATE_FILE"
}

# Function to calculate metrics from rolling window
calculate_metrics() {
    local state_json=$(cat "$STATE_FILE")
    
    echo "$state_json" | jq -r '
        if (.samples | length) == 0 then
            "{\"sample_count\": 0, \"freshness_p95\": null, \"success_rate\": null, \"decision_p95\": null, \"hotpath_calls\": null}"
        else
            {
                sample_count: (.samples | length),
                freshness_p95: (
                    [.samples[].metrics.freshness_p95_ms] | 
                    sort | 
                    .[((. | length) * 0.95) | floor]
                ),
                success_rate: (
                    (.samples | map(.metrics.success_rate) | add) / (.samples | length)
                ),
                decision_p95: (
                    [.samples[].metrics.decision_latency_p95_ms // 0] | 
                    sort | 
                    .[((. | length) * 0.95) | floor]
                ),
                hotpath_calls: (
                    [.samples[].metrics.hotpath_calls] | max
                ),
                first_sample: (.samples[0].timestamp),
                last_sample: (.samples[-1].timestamp)
            }
        end
    '
}

# Function to evaluate promotion gates
evaluate_gates() {
    local metrics=$1
    local gates_passed=0
    local gates_total=5
    local gate_results=()
    
    local sample_count=$(echo "$metrics" | jq -r '.sample_count')
    local freshness_p95=$(echo "$metrics" | jq -r '.freshness_p95')
    local success_rate=$(echo "$metrics" | jq -r '.success_rate')
    local decision_p95=$(echo "$metrics" | jq -r '.decision_p95')
    local hotpath_calls=$(echo "$metrics" | jq -r '.hotpath_calls')
    
    # Gate 1: Minimum samples
    if [[ "$sample_count" -ge $MIN_SAMPLES ]]; then
        gates_passed=$((gates_passed + 1))
        gate_results+=("✓ Minimum samples ($sample_count >= $MIN_SAMPLES)")
    else
        gate_results+=("✗ Minimum samples ($sample_count < $MIN_SAMPLES)")
    fi
    
    # Gate 2: Freshness P95
    if [[ "$freshness_p95" != "null" ]] && (( $(echo "$freshness_p95 <= $FRESHNESS_P95_THRESHOLD_MS" | bc -l) )); then
        gates_passed=$((gates_passed + 1))
        gate_results+=("✓ Freshness P95 (${freshness_p95}ms <= ${FRESHNESS_P95_THRESHOLD_MS}ms)")
    else
        gate_results+=("✗ Freshness P95 (${freshness_p95}ms > ${FRESHNESS_P95_THRESHOLD_MS}ms)")
    fi
    
    # Gate 3: Success rate
    if [[ "$success_rate" != "null" ]] && (( $(echo "$success_rate >= $SUCCESS_RATE_THRESHOLD" | bc -l) )); then
        gates_passed=$((gates_passed + 1))
        gate_results+=("✓ Success rate ($(printf "%.2f" $(echo "$success_rate * 100" | bc))% >= $(echo "$SUCCESS_RATE_THRESHOLD * 100" | bc)%)")
    else
        gate_results+=("✗ Success rate ($(printf "%.2f" $(echo "$success_rate * 100" | bc))% < $(echo "$SUCCESS_RATE_THRESHOLD * 100" | bc)%)")
    fi
    
    # Gate 4: Decision latency P95
    if [[ "$decision_p95" != "null" ]] && (( $(echo "$decision_p95 <= $DECISION_LATENCY_P95_THRESHOLD_MS" | bc -l) )); then
        gates_passed=$((gates_passed + 1))
        gate_results+=("✓ Decision P95 (${decision_p95}ms <= ${DECISION_LATENCY_P95_THRESHOLD_MS}ms)")
    else
        gate_results+=("✗ Decision P95 (${decision_p95}ms > ${DECISION_LATENCY_P95_THRESHOLD_MS}ms)")
    fi
    
    # Gate 5: Hotpath calls (must be exactly 0)
    if [[ "$hotpath_calls" != "null" ]] && [[ "$hotpath_calls" -eq $HOTPATH_CALLS_MAX ]]; then
        gates_passed=$((gates_passed + 1))
        gate_results+=("✓ Hotpath calls ($hotpath_calls == $HOTPATH_CALLS_MAX)")
    else
        gate_results+=("✗ Hotpath calls ($hotpath_calls != $HOTPATH_CALLS_MAX)")
    fi
    
    # Output results
    local timestamp=$(date '+%Y-%m-%d %H:%M:%S')
    
    if [[ $gates_passed -eq $gates_total ]]; then
        log "SUCCESS" "Promotion gates PASSED ($gates_passed/$gates_total)"
        echo "PROMOTION_STATUS: PASS"
    else
        log "FAIL" "Promotion gates FAILED ($gates_passed/$gates_total)"
        echo "PROMOTION_STATUS: FAIL"
    fi
    
    # Print gate details if verbose or if any gates failed
    if [[ $VERBOSE -eq 1 ]] || [[ $gates_passed -ne $gates_total ]]; then
        for result in "${gate_results[@]}"; do
            log "INFO" "$result"
        done
        
        log "INFO" "Window: $(echo "$metrics" | jq -r '.first_sample') to $(echo "$metrics" | jq -r '.last_sample')"
        log "INFO" "Sample count: $sample_count"
    fi
    
    # Write detailed results to results file
    {
        echo "=== Promotion Gate Check - $timestamp ==="
        echo "Status: $(if [[ $gates_passed -eq $gates_total ]]; then echo "PASS"; else echo "FAIL"; fi)"
        echo "Gates passed: $gates_passed/$gates_total"
        echo ""
        for result in "${gate_results[@]}"; do
            echo "$result"
        done
        echo ""
        echo "Metrics:"
        echo "$metrics" | jq '.'
        echo ""
    } >> "$RESULTS_FILE"
}

# Function to perform single check
perform_check() {
    log "INFO" "Performing promotion gate check"
    
    # Fetch current health data
    local health_data
    if ! health_data=$(fetch_health_data "$HEALTHZ_URL"); then
        log "ERROR" "Cannot fetch health data, skipping check"
        return 1
    fi
    
    log "INFO" "Fetched health data successfully"
    
    # Create sample with timestamp
    local timestamp=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
    local sample=$(echo '{}' | jq --arg timestamp "$timestamp" --argjson health "$health_data" '
        {
            timestamp: $timestamp,
            metrics: {
                freshness_p95_ms: ($health.metrics.freshness_p95_ms // null),
                success_rate: ($health.metrics.success_rate // null),
                decision_latency_p95_ms: ($health.metrics.decision_latency_p95_ms // null),
                hotpath_calls: ($health.metrics.hotpath_calls // null)
            }
        }
    ')
    
    # Add sample to rolling window
    add_sample "$sample"
    
    # Calculate metrics from rolling window
    local metrics
    if ! metrics=$(calculate_metrics); then
        log "ERROR" "Failed to calculate metrics"
        return 1
    fi
    
    # Evaluate promotion gates
    evaluate_gates "$metrics"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --healthz-url)
            HEALTHZ_URL="$2"
            shift 2
            ;;
        --window-minutes)
            WINDOW_MINUTES="$2"
            shift 2
            ;;
        --check-interval)
            CHECK_INTERVAL="$2"
            shift 2
            ;;
        --output-file)
            OUTPUT_FILE="$2"
            shift 2
            ;;
        --verbose)
            VERBOSE=1
            shift
            ;;
        --dry-run)
            DRY_RUN=1
            shift
            ;;
        --help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

# Check dependencies
if ! command -v curl &> /dev/null; then
    log "ERROR" "curl is required but not installed"
    exit 1
fi

if ! command -v jq &> /dev/null; then
    log "ERROR" "jq is required but not installed"
    exit 1
fi

if ! command -v bc &> /dev/null; then
    log "ERROR" "bc is required but not installed"
    exit 1
fi

# Initialize state directory
init_state_dir

log "INFO" "Starting promotion gates checker"
log "INFO" "Health endpoint: $HEALTHZ_URL"
log "INFO" "Window: ${WINDOW_MINUTES} minutes"
log "INFO" "Check interval: ${CHECK_INTERVAL} seconds"
log "INFO" "State directory: $STATE_DIR"

if [[ -n "$OUTPUT_FILE" ]]; then
    log "INFO" "Output file: $OUTPUT_FILE"
    # Ensure output directory exists
    mkdir -p "$(dirname "$OUTPUT_FILE")"
fi

# Main loop
if [[ $DRY_RUN -eq 1 ]]; then
    log "INFO" "Running single check (dry run)"
    perform_check
    exit 0
fi

log "INFO" "Starting continuous monitoring (Ctrl+C to stop)"

trap 'log "INFO" "Shutting down promotion gates checker"; exit 0' SIGINT SIGTERM

while true; do
    perform_check
    sleep "$CHECK_INTERVAL"
done