.PHONY: server server-dev deps tidy build-server build-mcp build-binaries test

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

# Distribution builds intentionally contain only the core service. MCP is
# deployed separately and is not part of local installations.
build-binaries: build-server
	@echo "built $(BIN_DIR)/traio-server"

test:
	go test ./...

server-dev: build-server
	TRAIO_RUNTIME_DIR="$(HOME)/Library/Application Support/Traio" bin/traio-server
