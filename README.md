# Traio

面向有技术背景的美股交易者的个人交易终端：自选、行情、K 线、指标、资讯、下单、持仓 — 桌面 / 移动 / 终端三端统一由 Go 后端驱动。

## 快速开始

```bash
make deps
make server    # http://127.0.0.1:38180 — 无需 config.yaml
make tui       # 另开终端
```

首次启动后，在 **Flutter 桌面端 → 设置**（或 `PUT /api/v1/settings`）填写 API Key 等配置，全部保存在本地数据库。

### 一体化 macOS 应用

Flutter 桌面端启动时会 **自动拉起内嵌的 `traio-server`**：

```bash
make build-binaries          # 编译 Go 二进制
make macos-release           # 默认输出 dist/traio-0.1.0-macos.app

# 指定打包目录与版本名
make macos-release OUT_DIR=~/Desktop/releases VERSION=0.2.0
make macos-dist OUT_DIR=./dist   # 额外生成 .tar.gz
```

`.app` 内 `Contents/Resources/` 包含 `traio-server`、`traio-mcp`、可选 `third_party/clientportal.gw/`。
后端默认固定监听 `127.0.0.1:38180`；桌面端启动前会先探测已有服务，避免重复拉起多个 server。

只测试 Flutter 启动、不拉起后端：

```bash
make test-launch
```

MCP 接入见 [docs/mcp.md](docs/mcp.md)。

## 分发给别人用

Traio 由 **Go 后端 + Flutter 桌面端** 组成，Gateway 有三种提供方式：

| 方式 | 适用场景 | 配置 |
|------|----------|------|
| **项目内捆绑** | 离线分发、网络受限 | `bundled_gateway_dir: "./third_party/clientportal.gw"` |
| **指定本地目录** | 已自行解压 Gateway | `gateway_dir: "/path/to/clientportal.gw"` |
| **自动下载** | 能访问 IBKR CDN | 留空 `bundled_gateway_dir`，可选 `download_proxy` |

### 发布包建议结构

```
traio-0.1.0-darwin-arm64/
├── traio-server          # go build 产物
├── config.yaml.example
├── third_party/clientportal.gw/   # make bundle-ibkr-gateway 生成
└── README.md
```

### 使用者步骤

1. 安装 **Java 17+**（Gateway 依赖）
2. 运行 `./traio-server`
3. 打开 Flutter 桌面端 → **设置**，填写 API Key / IBKR 等
4. IBKR 手动登录：浏览器打开 `https://localhost:5680/sso/Login`

> Gateway 版权归 Interactive Brokers，分发 release 时请附带 IBKR 许可说明，建议从官网下载或使用 `make bundle-ibkr-gateway` 打入 release 包，**不要提交到 git**。

Docker：

```bash
cp config.yaml.example config.yaml
make docker
```

## 目录

| 路径 | 说明 |
|------|------|
| `cmd/server` | REST + WebSocket 后端 |
| `cmd/tui` | Bubbletea 终端 |
| `internal/` | 业务逻辑、券商封装、存储 |
| `tui/` | 终端 UI 组件 |
| `flutter/` | Flutter 展示层（Riverpod + Dio） |

## API（骨架）

- `GET /health`
- `GET /api/v1/watchlist/groups`
- `GET /api/v1/quotes/:symbol`
- `GET /api/v1/positions`
- `GET /api/v1/news/:symbol`
- `GET /api/v1/ws` — WebSocket 心跳（行情推送待接 Schwab）

## 开发阶段

1. Go 后端：Schwab OAuth、行情、持仓、WebSocket、SQLite 自选
2. 终端 MVP：Bubbletea 接 API
3. go-talib + K 线聚合
4. Flutter 桌面
5. Flutter 移动
6. AI / 财务 / 策略

## Flutter

首次需生成平台工程（本仓库仅含 `lib/` 骨架）：

```bash
cd flutter
flutter create . --org com.traio --project-name traio
flutter pub get
flutter run -d macos   # 或 windows / ios / android
```

## 技术栈

- **后端**：Gin、SQLite、gorilla/websocket、Bubbletea
- **前端**：Flutter、Riverpod、Dio
- **数据源**：Schwab、SnapTrade、IBKR CPAPI、Finnhub、EDGAR、Claude
