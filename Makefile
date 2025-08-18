SHELL := /bin/bash

# --- Project metadata ---
PROJECT := news-trader
PROTO_SRC := ./contracts/contracts.proto
GO_PROTO_OUT := ./gen/go

# --- Default environment (can be overridden) ---
export TRADING_MODE ?= paper
export GLOBAL_PAUSE ?= true

export NEWS_FEED ?= stub
export QUOTES ?= sim
export HALTS ?= sim
export SENTIMENT ?= stub
export BROKER ?= paper
export ALERTS ?= stdout

# --- Phony targets ---
.PHONY: help up down logs seed replay proto clean dirs

help:
	@echo "Make targets:"
	@echo "  make up        - start services (docker compose if available; otherwise placeholder)"
	@echo "  make down      - stop services"
	@echo "  make logs      - tail compose logs"
	@echo "  make seed      - load fixtures into stub feeders (placeholder)"
	@echo "  make replay    - run tiny replay using ./fixtures (placeholder)"
	@echo "  make proto     - generate Go code from contracts/contracts.proto"
	@echo "  make clean     - remove generated code"
	@echo ""
	@echo "Env flags (override on command line):"
	@echo "  TRADING_MODE=$(TRADING_MODE) GLOBAL_PAUSE=$(GLOBAL_PAUSE)"
	@echo "  NEWS_FEED=$(NEWS_FEED) QUOTES=$(QUOTES) HALTS=$(HALTS) SENTIMENT=$(SENTIMENT) BROKER=$(BROKER) ALERTS=$(ALERTS)"

dirs:
	@mkdir -p $(GO_PROTO_OUT)

up:
	@echo "Starting $(PROJECT) with adapters: NEWS_FEED=$(NEWS_FEED) QUOTES=$(QUOTES) HALTS=$(HALTS) SENTIMENT=$(SENTIMENT) BROKER=$(BROKER) ALERTS=$(ALERTS)"
	@if command -v docker-compose >/dev/null 2>&1; then \
		docker-compose up -d ; \
	elif command -v docker >/dev/null 2>&1; then \
		docker compose up -d ; \
	else \
		echo "Docker not installed — this is a placeholder. Start your services manually."; \
	fi

down:
	@if command -v docker-compose >/dev/null 2>&1; then \
		docker-compose down ; \
	elif command -v docker >/dev/null 2>&1; then \
		docker compose down ; \
	else \
		echo "Docker not installed — nothing to stop."; \
	fi

logs:
	@if command -v docker-compose >/dev/null 2>&1; then \
		docker-compose logs -f --tail=200 ; \
	elif command -v docker >/dev/null 2>&1; then \
		docker compose logs -f --tail=200 ; \
	else \
		echo "Docker not installed — no logs to tail."; \
	fi

seed:
	@echo "Seeding fixtures into stub services (placeholder)."
	@echo "Add commands here to POST ./fixtures/*.json to your stub endpoints."

replay:
	@echo "Running tiny replay (placeholder)."
	@echo "Implement ./cmd/replay or a script, then call it here. For now, this just echoes."

proto: dirs
	@if ! command -v protoc >/dev/null 2>&1; then \
		echo "Please install protoc (Protocol Buffers compiler)."; \
		exit 1; \
	fi
	@echo "Generating Go types from $(PROTO_SRC) → $(GO_PROTO_OUT)"
	@protoc -I ./contracts \
		--go_out=$(GO_PROTO_OUT) --go_opt=paths=source_relative \
		$(PROTO_SRC)

clean:
	@rm -rf $(GO_PROTO_OUT)
	@echo "Cleaned generated artifacts."

.PHONY: doctor init session

doctor:
	@echo "Checking tools..."
	@command -v go >/dev/null 2>&1     && echo "✓ go found" || echo "✗ go missing"
	@command -v protoc >/dev/null 2>&1 && echo "✓ protoc found" || echo "✗ protoc missing"
	@command -v docker >/dev/null 2>&1 && echo "✓ docker found" || echo "✗ docker missing"
	@echo "Mode: TRADING_MODE=$(TRADING_MODE) GLOBAL_PAUSE=$(GLOBAL_PAUSE)"
	@echo "Adapters: NEWS_FEED=$(NEWS_FEED) QUOTES=$(QUOTES) HALTS=$(HALTS) SENTIMENT=$(SENTIMENT) BROKER=$(BROKER) ALERTS=$(ALERTS)"

init:
	@mkdir -p config contracts gen/go internal/cmd internal/fixtures docs
	@[ -f config/config.yaml ] || cp config/config.example.yaml config/config.yaml
	@echo "Project initialized. Edit config/config.yaml and run 'make proto'."

# Creates a vibe session card you can fill in (docs/session-<date>.md)
session:
	@d=$$(date +%Y-%m-%d_%H-%M-%S); \
	file=docs/session-$$d.md; \
	echo "Theme: <one-liner>" > $$file; \
	echo "Acceptance: <one sentence>" >> $$file; \
	echo "Rails: TRADING_MODE=$(TRADING_MODE), GLOBAL_PAUSE=$(GLOBAL_PAUSE)" >> $$file; \
	echo "Proof: metric <x>, log <y>" >> $$file; \
	echo "Timebox: 60-90 min" >> $$file; \
	echo "Notes:" >> $$file; \
	echo "Created $$file"

	
test:
	@scripts/run-tests.sh