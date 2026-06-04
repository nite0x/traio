# Traio

面向有技术背景的美股交易者的个人交易终端：自选、行情、K 线、指标、资讯、下单、持仓。

## 架构

| 层 | 技术栈 | 职责 |
|----|--------|------|
| **Go 后端** | Gin + SQLite + gorilla/websocket | REST API / WebSocket / 券商集成 / 数据存储 |
| **Tauri 桌面端** | React + TypeScript + Tauri | macOS / Windows / Linux 桌面应用 |
| **Flutter 移动端** | Flutter + Riverpod | iOS / Android 移动应用（Go backend 内嵌为 gomobile xcframework） |
| **TUI** | Bubbletea | 终端调试客户端 |
| **MCP** | — | 外部工具接入（Claude 等） |

> **重要：** `flutter/` 目录仅用于移动端（iOS / Android）。桌面端由 `tauri/` 承载，两者不混用。

## 快速开始

### 后端开发

```bash
make deps
make server        # 启动 Go 后端，监听 http://127.0.0.1:38180
make tui           # 另开终端，用 TUI 访问同一个 server
```

### Tauri 桌面端开发

```bash
make tauri-dev     # 自动构建 traio-server，再启动 Tauri 开发服务器
```

构建发布包：

```bash
make tauri-build   # 输出 tauri/src-tauri/target/release/bundle/
```

### Flutter 移动端开发

首次初始化 Flutter 项目（仅在 `ios/` / `android/` 目录不存在时需要）：

```bash
cd flutter
flutter create . --org com.traio --project-name traio
flutter pub get
```

运行 iOS 模拟器：

```bash
cd flutter && flutter run -d ios
```

构建 iOS gomobile xcframework（需要完整 Xcode）：

```bash
make ios-framework
```

### 测试

```bash
make test          # Go 单元测试
cd flutter && flutter test   # Flutter widget 测试
```

## 目录结构

```
traio/
├── cmd/
│   ├── server/        Go 后端入口
│   ├── tui/           Bubbletea 终端
│   └── mcp/           MCP 工具入口
├── internal/          业务逻辑、券商封装、存储
├── mobile/            gomobile 绑定层（供 Flutter iOS 内嵌）
├── tui/               终端 UI 组件
├── tauri/             桌面端（React + TypeScript + Tauri）
│   ├── src/           前端源码
│   └── src-tauri/     Rust/Tauri 壳
├── flutter/           移动端（iOS / Android）
│   └── lib/
│       ├── core/      API client、主题、内嵌 backend 接口
│       └── mobile/    移动端页面与组件
├── bin/               编译产物（.gitignore）
└── docs/              文档
```

## 后端 API

Go 后端固定监听 `127.0.0.1:38180`。

```
GET  /health
GET  /api/v1/watchlist/groups
GET  /api/v1/watchlist/groups/:id/items
GET  /api/v1/quotes/:symbol
GET  /api/v1/positions
GET  /api/v1/account/equity
GET  /api/v1/ibkr/gateway/status
POST /api/v1/ibkr/gateway/start
POST /api/v1/ibkr/gateway/stop
GET  /api/v1/settings
PUT  /api/v1/settings
POST /api/v1/server/shutdown
GET  /api/v1/ws              WebSocket（行情推送）
```

完整 MCP 接入见 [docs/mcp.md](docs/mcp.md)。

## IBKR Gateway

Interactive Brokers 需要本地运行 Gateway。有三种方式提供：

| 方式 | 适用场景 |
|------|----------|
| 项目内捆绑 | 离线分发：`make bundle-ibkr-gateway IBKR_SRC=/path/to/clientportal.gw` |
| 指定本地目录 | 已自行解压，在 `config.yaml` 设置 `gateway_dir` |
| 自动下载 | 能访问 IBKR CDN，留空 `bundled_gateway_dir` |

登录：浏览器打开 `https://localhost:5680/sso/Login`，完成认证后 Gateway session 保持有效。

> Gateway 版权归 Interactive Brokers。**不要将 `third_party/clientportal.gw/` 提交到 git。**

## 技术栈

- **后端**：Go、Gin、SQLite（modernc）、gorilla/websocket、Bubbletea
- **桌面端**：React、TypeScript、Vite、Tauri、React Router、TanStack Query、Recharts
- **移动端**：Flutter、Riverpod、Dio、gomobile
- **数据源**：Schwab、SnapTrade、IBKR CPAPI、Finnhub、EDGAR、Claude
