# Traio Service

Traio 的核心服务仓库，负责行情、账户、持仓、券商接入、数据存储以及本地 API。

## 仓库边界

- `cmd/server` 和 `internal/` 是服务核心。
- `cmd/mcp` 是服务的开源 MCP 适配器，不被服务核心反向依赖。
- 服务架构、API 规格和接入文档统一保存在 [`traio-doc`](https://github.com/nite0x/traio-doc/tree/main/docs/traio)。
- Tauri 桌面客户端位于独立的 `traio-desktop` 仓库；移动客户端位于独立的 `traio-app` 仓库。
- 本地数据库、配置、编译产物和 IBKR Gateway 安装目录均已忽略，不属于 Git 仓库内容。

## 架构

| 层 | 技术栈 | 职责 |
|----|--------|------|
| **Go 后端** | Gin + SQLite + gorilla/websocket | REST API / WebSocket / 券商集成 / 数据存储 |
| **MCP** | — | 外部工具接入（Claude 等） |

`traio-desktop` 会将这里的 `cmd/server` 编译为 Tauri sidecar。

## 快速开始

### 后端开发

```bash
make deps
make server        # 启动 Go 后端，监听 http://127.0.0.1:38181
```

### 测试

```bash
make test          # Go 单元测试
```

## 目录结构

```
traio/
├── cmd/
│   ├── server/        Go 后端入口
│   └── mcp/           MCP 工具入口
├── internal/          业务逻辑、券商封装、存储
└── bin/               编译产物（.gitignore）
```

## 后端 API

开发模式默认监听 `127.0.0.1:38181`；桌面发布版默认使用 `127.0.0.1:38180`。可通过 `TRAIO_SERVER_PORT` 覆盖。

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
GET  /api/v1/schwab/status
GET  /api/v1/schwab/oauth/url
POST /api/v1/schwab/oauth/exchange
POST /api/v1/server/shutdown
GET  /api/v1/ws?symbols=AAPL,MSFT   WebSocket（Schwab 实时行情推送）
```

完整 MCP 接入见 [`traio-doc/docs/traio/mcp.md`](https://github.com/nite0x/traio-doc/blob/main/docs/traio/mcp.md)。

## Schwab 实时行情

1. 在桌面端“设置”的配置 JSON 中填写 `schwab.client_id`、`schwab.client_secret` 和 Schwab Developer Portal 中登记的 `redirect_uri`，保存配置。
2. 点击“打开授权页”，登录 Schwab 并授权。
3. 浏览器跳转到回调地址后，将地址栏中的完整 URL 粘贴回设置页，点击“完成授权”。
4. 打开自选、今日或个股图表页。后端会维护唯一 Schwab Streamer 连接，并将 `LEVELONE_EQUITIES` 增量行情转发给客户端。

OAuth token 保存在本地 SQLite `oauth_tokens` 表中，并在过期前自动刷新。HTTP 报价仍作为桌面端回退数据源。

## 架构文档

- [券商账户同步架构](https://github.com/nite0x/traio-doc/blob/main/docs/traio/broker-sync.md)
- [IBKR Client Portal Gateway 管理手册](https://github.com/nite0x/traio-doc/blob/main/docs/traio/ibkr-gateway-management.md)
- [端到端加密设备同步架构](https://github.com/nite0x/traio-doc/blob/main/docs/traio/e2ee-device-sync.md)

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

- **服务核心**：Go、Gin、SQLite（modernc）、gorilla/websocket
- **辅助工具**：MCP stdio server
- **数据源**：Schwab、SnapTrade、IBKR CPAPI、Finnhub、EDGAR、Claude
