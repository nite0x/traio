.PHONY: server server-dev deps tidy build-server build-mcp build-binaries \
        bundle-ibkr-gateway test

IBKR_SRC ?= $(HOME)/Downloads/clientportal.gw

BIN_DIR ?= bin

DEV_PORT := 38181

# ── Go 后端 ─────────────────────────────────────────────────────────────────

server:
	@lsof -ti :$(DEV_PORT) | xargs kill -9 2>/dev/null || true
	go run ./cmd/server

deps:
	go mod download

tidy:
	go mod tidy

build-server:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -o $(BIN_DIR)/traio-server ./cmd/server

build-mcp:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -o $(BIN_DIR)/traio-mcp ./cmd/mcp

build-binaries: build-server build-mcp
	@echo "built $(BIN_DIR)/traio-server $(BIN_DIR)/traio-mcp"

test:
	go test ./...

# ── 工具 ────────────────────────────────────────────────────────────────────

bundle-ibkr-gateway:
	@test -d "$(IBKR_SRC)" || (echo "IBKR_SRC not found: $(IBKR_SRC)"; exit 1)
	@test -f "$(IBKR_SRC)/bin/run.sh" || (echo "invalid gateway dir (missing bin/run.sh): $(IBKR_SRC)"; exit 1)
	rm -rf third_party/clientportal.gw/*
	mkdir -p third_party/clientportal.gw
	cp -R "$(IBKR_SRC)/." third_party/clientportal.gw/
	@echo "bundled IBKR gateway -> third_party/clientportal.gw"

server-dev: build-server
	TRAIO_RUNTIME_DIR="$(HOME)/Library/Application Support/Traio" bin/traio-server
