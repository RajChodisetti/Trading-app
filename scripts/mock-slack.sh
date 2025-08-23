#!/usr/bin/env bash
set -euo pipefail

PORT="${1:-8093}"
WEBHOOK_LOG="${2:-/tmp/mock-slack.jsonl}"

# Cleanup function
cleanup() {
    if [[ -n "${SERVER_PID:-}" ]]; then
        kill $SERVER_PID 2>/dev/null || true
    fi
}
trap cleanup EXIT

echo "Starting mock Slack webhook server on port $PORT"
echo "Webhook payloads will be logged to: $WEBHOOK_LOG"

# Clear previous log
> "$WEBHOOK_LOG"

# Start simple HTTP server using netcat or Python
if command -v python3 >/dev/null 2>&1; then
    # Use Python HTTP server with webhook handling
    python3 -c "
import http.server
import json
import sys
from urllib.parse import parse_qs

class MockSlackServer(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path == '/webhook' or self.path == '/':
            content_length = int(self.headers.get('Content-Length', 0))
            body = self.rfile.read(content_length)
            
            # Log webhook payload
            try:
                # Try to parse as JSON
                payload = json.loads(body.decode('utf-8'))
                import datetime
                log_entry = {
                    'timestamp': datetime.datetime.now().isoformat(),
                    'path': self.path,
                    'method': 'POST',
                    'headers': dict(self.headers),
                    'payload': payload
                }
            except:
                # If not JSON, log as text
                import datetime
                log_entry = {
                    'timestamp': datetime.datetime.now().isoformat(),
                    'path': self.path,
                    'method': 'POST',
                    'headers': dict(self.headers),
                    'body': body.decode('utf-8', errors='replace')
                }
            
            # Append to log file
            with open('$WEBHOOK_LOG', 'a') as f:
                f.write(json.dumps(log_entry) + '\n')
            
            # Respond with success
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(b'{\"ok\": true}')
        else:
            # Health check
            if self.path == '/health':
                self.send_response(200)
                self.send_header('Content-Type', 'text/plain')
                self.end_headers()
                self.wfile.write(b'ok')
            else:
                self.send_response(404)
                self.end_headers()
    
    def do_GET(self):
        if self.path == '/health':
            self.send_response(200)
            self.send_header('Content-Type', 'text/plain')
            self.end_headers()
            self.wfile.write(b'ok')
        elif self.path == '/logs':
            # Return recent webhook logs
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            try:
                with open('$WEBHOOK_LOG', 'r') as f:
                    logs = [json.loads(line.strip()) for line in f if line.strip()]
                self.wfile.write(json.dumps(logs).encode())
            except:
                self.wfile.write(b'[]')
        else:
            self.send_response(404)
            self.end_headers()
    
    def log_message(self, format, *args):
        # Suppress default logging
        pass

if __name__ == '__main__':
    server = http.server.HTTPServer(('127.0.0.1', $PORT), MockSlackServer)
    print('Mock Slack webhook server running on port $PORT')
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print('Server stopped')
" &
    SERVER_PID=$!
else
    # Fallback to netcat if Python not available
    echo "Python3 not found, using netcat fallback"
    while true; do
        {
            echo "HTTP/1.1 200 OK"
            echo "Content-Type: application/json"
            echo "Content-Length: 13"
            echo ""
            echo '{"ok": true}'
        } | nc -l -p $PORT -c "cat >> $WEBHOOK_LOG" || break
    done &
    SERVER_PID=$!
fi

echo "Mock Slack server started with PID: $SERVER_PID"

# Wait for server to be ready
sleep 1
if kill -0 $SERVER_PID 2>/dev/null; then
    echo "Mock Slack server is ready"
    # Wait for server or user interrupt
    wait $SERVER_PID 2>/dev/null || true
else
    echo "Failed to start mock Slack server"
    exit 1
fi