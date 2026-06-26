.PHONY: server tui deps tidy build-server build-tui build-mcp build-binaries \
        build-tauri-sidecar bundle-ibkr-gateway icons clean-local test \
        tauri-dev tauri-build ios-check ios-framework

IBKR_SRC ?= /Users/nite/Downloads/clientportal.gw
FLUTTER   ?= /Users/nite/env/flutter/bin/flutter

BIN_DIR ?= bin
TAURI_BIN_DIR := tauri/src-tauri/binaries
RUST_TARGET := $(shell rustc -vV | sed -n 's/^host: //p')

DEV_PORT := 38181
PROD_PORT := 38180

# ── Go 后端 ─────────────────────────────────────────────────────────────────

server:
	@lsof -ti :$(DEV_PORT) | xargs kill -9 2>/dev/null || true
	go run ./cmd/server

tui:
	go run ./cmd/tui -api http://127.0.0.1:$(DEV_PORT)

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

# Tauri sidecar：按 host triple 命名，供 externalBin 打包进 .app
build-tauri-sidecar:
	@mkdir -p $(TAURI_BIN_DIR)
	CGO_ENABLED=0 go build -o $(TAURI_BIN_DIR)/traio-server-$(RUST_TARGET) ./cmd/server
	@echo "built $(TAURI_BIN_DIR)/traio-server-$(RUST_TARGET)"

build-tui:
	go build -o bin/traio-tui ./cmd/tui

test:
	go test ./...

# ── Tauri 桌面端 ─────────────────────────────────────────────────────────────
# 开发/打包均由 Tauri 在启动时通过 sidecar 拉起 traio-server
tauri-dev: build-tauri-sidecar
	cd tauri && npm run tauri dev

# 正式构建 .app / .dmg（macOS）
tauri-build: build-tauri-sidecar
	cd tauri && npm run tauri build

# ── Flutter 移动端（iOS / Android）──────────────────────────────────────────

# iOS: 验证 Go 后端可以为 iOS 交叉编译（不需要 Xcode）
ios-check:
	GOOS=ios GOARCH=arm64 CGO_ENABLED=1 go build ./internal/... ./mobile/
	@echo "iOS cross-compile check OK"

# iOS: 编译 gomobile xcframework 供 Flutter iOS 内嵌
# 前置条件：
#   go install golang.org/x/mobile/cmd/gomobile@latest
#   go install golang.org/x/mobile/cmd/gobind@latest
#   gomobile init
ios-framework: ios-check
	gomobile bind -target=ios -o flutter/ios/Frameworks/Traio.xcframework ./mobile/
	@echo "built flutter/ios/Frameworks/Traio.xcframework"

# ── 工具 ────────────────────────────────────────────────────────────────────

icons:
	python3 scripts/generate_app_icons.py

bundle-ibkr-gateway:
	@test -d "$(IBKR_SRC)" || (echo "IBKR_SRC not found: $(IBKR_SRC)"; exit 1)
	@test -f "$(IBKR_SRC)/bin/run.sh" || (echo "invalid gateway dir (missing bin/run.sh): $(IBKR_SRC)"; exit 1)
	rm -rf third_party/clientportal.gw/*
	mkdir -p third_party/clientportal.gw
	cp -R "$(IBKR_SRC)/." third_party/clientportal.gw/
	@echo "bundled IBKR gateway -> third_party/clientportal.gw"

clean-local:
	@bash scripts/clean-local.sh

server-dev: build-server
	TRAIO_RUNTIME_DIR="$(HOME)/Library/Application Support/Traio" bin/traio-server
