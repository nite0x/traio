.PHONY: server tui deps tidy build-server build-tui build-mcp build-binaries macos-release macos-dist bundle-ibkr-gateway icons dev dev-fresh clean-local test test-launch ios-check ios-framework

IBKR_SRC ?= /Users/nite/Downloads/clientportal.gw
FLUTTER ?= /Users/nite/env/flutter/bin/flutter

# Go 二进制输出目录
BIN_DIR ?= bin

# macOS 打包输出目录（最终 traio.app 复制到这里）
OUT_DIR ?= dist

# 发布包名称前缀，产物: $(OUT_DIR)/$(RELEASE_NAME).app
VERSION ?= 0.1.0
RELEASE_NAME ?= traio-$(VERSION)-macos

FLUTTER_APP := flutter/build/macos/Build/Products/Release/traio.app

server:
	go run ./cmd/server

tui:
	go run ./cmd/tui -api http://127.0.0.1:38180

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

icons:
	python3 scripts/generate_app_icons.py

# Build macOS .app with embedded Go binaries + optional IBKR gateway.
macos-release: build-binaries icons
	cd flutter && $(FLUTTER) build macos --release
	@test -d "$(FLUTTER_APP)" || (echo "missing $(FLUTTER_APP)"; exit 1)
	@mkdir -p "$(OUT_DIR)"
	rm -rf "$(OUT_DIR)/$(RELEASE_NAME).app"
	cp -R "$(FLUTTER_APP)" "$(OUT_DIR)/$(RELEASE_NAME).app"
	@echo "packaged -> $(OUT_DIR)/$(RELEASE_NAME).app"

# 同上，并额外打 tar.gz 到 OUT_DIR
macos-dist: macos-release
	tar -C "$(OUT_DIR)" -czf "$(OUT_DIR)/$(RELEASE_NAME).tar.gz" "$(RELEASE_NAME).app"
	@echo "archive  -> $(OUT_DIR)/$(RELEASE_NAME).tar.gz"

build-tui:
	go build -o bin/traio-tui ./cmd/tui

# iOS: cross-compile-check the backend for GOOS=ios without needing Xcode.
# This verifies all Go deps (incl. modernc SQLite) build for iOS arm64.
# The mobile/ package is Schwab-only (no IBKR) via build tags.
ios-check:
	GOOS=ios GOARCH=arm64 CGO_ENABLED=1 go build ./internal/... ./mobile/
	@echo "iOS cross-compile check OK (internal/... + mobile/)"

# iOS: build the gomobile xcframework embedded into the Flutter iOS project.
# Requires full Xcode (not just CommandLineTools) and gomobile:
#   go install golang.org/x/mobile/cmd/gomobile@latest
#   go install golang.org/x/mobile/cmd/gobind@latest
#   gomobile init
ios-framework: ios-check
	gomobile bind -target=ios -o flutter/ios/Frameworks/Traio.xcframework ./mobile/
	@echo "built flutter/ios/Frameworks/Traio.xcframework"

# Copy a local IBKR Gateway tree into third_party/ for offline distribution.
# Usage: make bundle-ibkr-gateway IBKR_SRC=/path/to/clientportal.gw
bundle-ibkr-gateway:
	@test -d "$(IBKR_SRC)" || (echo "IBKR_SRC not found: $(IBKR_SRC)"; exit 1)
	@test -f "$(IBKR_SRC)/bin/run.sh" || (echo "invalid gateway dir (missing bin/run.sh): $(IBKR_SRC)"; exit 1)
	rm -rf third_party/clientportal.gw/*
	mkdir -p third_party/clientportal.gw
	cp -R "$(IBKR_SRC)/." third_party/clientportal.gw/
	@echo "bundled IBKR gateway -> third_party/clientportal.gw"

TRAIO_RUNTIME_DIR ?= $(HOME)/Library/Application Support/Traio
TRAIO_SERVER_BIN := $(CURDIR)/$(BIN_DIR)/traio-server

# 清理 dev/安装缓存（保留 ibkr-gateway 与 data/）
clean-local:
	@bash scripts/clean-local.sh

# 日常开发：重建 server，清理 runtime 状态，启动 Flutter
dev: build-server
	@bash scripts/clean-local.sh --state
	cd flutter && TRAIO_RUNTIME_DIR="$(TRAIO_RUNTIME_DIR)" TRAIO_SERVER_BIN="$(TRAIO_SERVER_BIN)" $(FLUTTER) run -d macos

# 遇到端口/缓存问题时：完整清理后再 dev
dev-fresh: clean-local build-server
	cd flutter && TRAIO_RUNTIME_DIR="$(TRAIO_RUNTIME_DIR)" TRAIO_SERVER_BIN="$(TRAIO_SERVER_BIN)" $(FLUTTER) run -d macos

test:
	go test ./...
	cd flutter && $(FLUTTER) test

# Open the macOS Flutter app for UI/startup testing without auto-starting traio-server.
test-launch:
	cd flutter && TRAIO_SKIP_BACKEND_AUTO_START=1 TRAIO_RUNTIME_DIR="$(TRAIO_RUNTIME_DIR)" $(FLUTTER) run -d macos

server-dev: build-server
	TRAIO_RUNTIME_DIR="$(TRAIO_RUNTIME_DIR)" bin/traio-server
