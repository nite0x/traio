# Traio Service

Traio 的核心服务仓库，负责行情、账户、持仓、券商接入、数据存储以及本地 API。

## 仓库边界

- `cmd/server` 和 `internal/` 是服务核心。
- `cmd/mcp` 是开发期的 stdio MCP 适配器，不被服务核心反向依赖；本地安装包与标准服务构建都不会携带或启动它。
- 服务架构、API 规格和接入文档统一保存在 [`traio-doc`](https://github.com/nite0x/traio-doc/tree/main/docs/traio)。
- Tauri 桌面客户端位于独立的 `traio-desktop` 仓库；移动客户端位于独立的 `traio-app` 仓库。
- 本地数据库、配置和编译产物均已忽略，不属于 Git 仓库内容。
- IBKR Client Portal Gateway 的安装与生命周期由独立的
  [`ibkr-gateway-manager`](../ibkr-gateway-manager) 仓库负责。

## 架构

| 层 | 技术栈 | 职责 |
|----|--------|------|
| **Go 后端** | Gin + SQLite + gorilla/websocket | REST API / WebSocket / 券商集成 / 数据存储 |
| **MCP（独立部署）** | — | 通过稳定服务域名接入外部工具（Claude 等） |

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

开发模式默认监听 `127.0.0.1:38181`；桌面发布版由系统分配一个空闲 loopback 端口，并将实际地址写入运行目录的 `api-url` 文件。可通过 `TRAIO_SERVER_PORT` 覆盖。

```
GET  /health
GET  /api/v1/watchlist/groups
GET  /api/v1/watchlist/groups/:id/items
GET  /api/v1/quotes/:symbol
GET  /api/v1/account/equity
GET  /api/v1/portfolio/overview
GET  /api/v1/portfolio/positions
GET  /api/v1/portfolio/positions/:positionId
GET  /api/v1/portfolio/cash
POST /api/v1/portfolio/sync
GET  /api/v1/portfolio/sync-status
GET  /api/v1/settings
PUT  /api/v1/settings
GET  /api/v1/schwab/status
GET  /api/v1/schwab/oauth/url
POST /api/v1/schwab/oauth/exchange
POST /api/v1/server/shutdown
GET  /api/v1/ws?symbols=AAPL,MSFT   WebSocket（Schwab 实时行情推送）
```

## 统一资产身份

Core 为跨券商持仓维护稳定的 `instrument_id`。同步时优先使用券商永久资产 ID，
缺失时使用规范化的资产类型、市场和 symbol 匹配。`instruments` 保存 Traio 统一资产，
`broker_instruments` 保存 IBKR、Schwab、Alpaca 外部 ID 到统一资产的映射。

`GET /api/v1/portfolio/positions` 只按 `instrument_id` 返回聚合持仓和稳定
`position_id`，并在 `legs` 中保留各券商账户的组成明细。原始持仓接口与旧 snapshot
接口已移除；无法解析 `instrument_id` 的持仓会使同步失败，不会降级为 symbol 聚合。

MCP 应作为独立服务部署并使用稳定域名配置 `TRAIO_API`；桌面本地安装包不暴露 MCP。完整接入见 [`traio-doc/docs/traio/mcp.md`](https://github.com/nite0x/traio-doc/blob/main/docs/traio/mcp.md)。

## Schwab 实时行情

1. 在桌面端“设置”的配置 JSON 中填写 `schwab.client_id`、`schwab.client_secret` 和 Schwab Developer Portal 中登记的 `redirect_uri`，保存配置。
2. 点击“打开授权页”，登录 Schwab 并授权。
3. 浏览器跳转到回调地址后，将地址栏中的完整 URL 粘贴回设置页，点击“完成授权”。
4. 打开自选、今日或个股图表页。后端会维护唯一 Schwab Streamer 连接，并将 `LEVELONE_EQUITIES` 增量行情转发给客户端。

OAuth token 保存在本地 SQLite `oauth_tokens` 表中，并在过期前自动刷新。HTTP 报价仍作为桌面端回退数据源。

## 架构文档

- [券商账户同步架构](https://github.com/nite0x/traio-doc/blob/main/docs/traio/broker-sync.md)
- [券商接入架构与开发指南](docs/broker-integrations.md)
- [统一资产身份与 instrument_id](https://github.com/nite0x/traio-doc/blob/main/docs/traio/instrument-identity.md)
- [端到端加密设备同步架构](https://github.com/nite0x/traio-doc/blob/main/docs/traio/e2ee-device-sync.md)

## IBKR Gateway Manager

Traio 不安装、启动、停止或升级 Client Portal Gateway。IBKR provider 在
`broker_providers` 中保存 Gateway Manager 的 `manager_url` 和只写的
`manager_api_token`，后端通过 `/healthz`、`/management/v1/gateways` 和
`/management/v1/gateways/{id}/status` 读取 Manager 与实例状态。

创建 IBKR connection 时必须从 Manager 返回的实例中选择 `gateway_id`。Traio 后端会
再次解析该实例并保存它的 `proxy_url` 为 connection 的 `gateway_url`；如果实例代理启用
认证，还需把实例的 `proxy_token` 作为 connection 的 `gateway_token` 保存。Manager
控制面地址和全局 Token 不能作为 Gateway 连接地址或代理 Token 使用。

实例的安装、启动、登录、升级和回滚仍由独立的
[`ibkr-gateway-manager`](../ibkr-gateway-manager) 管理；Traio 设置页的“打开 Gateway
管理”会跳转到 Manager 的 `/manager/` 页面。

## Docker 部署（包含 Web 前端）

Docker 镜像使用 `traio-desktop` 作为额外构建上下文：Node 阶段执行
`npm run build:web`，Go 阶段构建 `traio-server`，最终镜像只保留 Go
二进制、健康检查工具和编译后的前端文件。前端位于 `/opt/traio/web`，由 Go
服务提供静态资源和 React Router fallback；浏览器通过同源 `/api/v1` 访问 API。

两个仓库需要位于同一父目录：

```text
open/
├── traio/
└── traio-desktop/
```

在 `traio` 目录构建 EC2 使用的 amd64 镜像：

```bash
docker buildx build \
  --platform linux/amd64 \
  --build-context frontend=../traio-desktop \
  --tag traio-server:local \
  --load \
  .
```

本地启动：

```bash
cp docker.env.example .env
docker compose up -d --no-build
docker compose ps
curl http://127.0.0.1:8080/health
```

Compose 只把容器端口发布到宿主机 `127.0.0.1:8080`。当前 API 认证恢复前，
不要改成 `0.0.0.0:8080` 或在安全组中开放该端口。SQLite 和 API Token 保存在
`traio-data` volume 中；外部 IBKR Gateway 的数据与生命周期不由此 Compose 管理。

上传到没有源码的服务器：

```bash
docker save traio-server:local | gzip > traio-server-linux-amd64.tar.gz
ssh ubuntu@SERVER 'mkdir -p ~/traio-deploy'
scp traio-server-linux-amd64.tar.gz compose.yaml docker.env.example ubuntu@SERVER:~/traio-deploy/
```

服务器加载并启动：

```bash
cd ~/traio-deploy
cp docker.env.example .env
gunzip -c traio-server-linux-amd64.tar.gz | docker load
docker compose up -d --no-build
docker compose ps
```

配置公网域名后，在 `.env` 中设置 API host：

```dotenv
TRAIO_ALLOWED_API_HOSTS=traio.nite0x.com
```

个人部署推荐使用内置账号登录。在 `.env` 中填写：

```dotenv
TRAIO_AUTH_MODE=password
TRAIO_BOOTSTRAP_ADMIN_USERNAME=owner
TRAIO_BOOTSTRAP_ADMIN_PASSWORD=请替换为至少12位的长密码
TRAIO_BOOTSTRAP_ADMIN_NAME=Owner
TRAIO_COOKIE_SECURE=false
TRAIO_SESSION_TTL=12h
```

首次启动会创建默认 Workspace Owner，数据库只保存 Argon2id 密码哈希。成功启动后可以从
`.env` 删除 bootstrap 用户名和密码；已有账号仍可正常登录。通过 HTTPS 域名访问时必须设置
`TRAIO_COOKIE_SECURE=true`。也可以用 `TRAIO_BOOTSTRAP_ADMIN_PASSWORD_FILE` 从文件读取密码。

需要企业单点登录时，把认证模式改为 OIDC，并在身份提供商中登记 callback：

```dotenv
TRAIO_AUTH_MODE=oidc
TRAIO_OIDC_ISSUER_URL=https://identity.example.com/application/o/traio/
TRAIO_OIDC_CLIENT_ID=traio
TRAIO_OIDC_CLIENT_SECRET=
TRAIO_OIDC_REDIRECT_URL=https://traio.nite0x.com/auth/callback
TRAIO_SESSION_TTL=12h
```

两种服务端登录都只在浏览器中设置 `HttpOnly` Session Cookie，写请求额外校验 CSRF Token。
OIDC 登录采用 Authorization Code + PKCE；第一个成功登录的 OIDC 账号成为默认 Workspace Owner，
后续账号必须先由 Owner/Admin 在「设置 → 成员」中按邮箱邀请。角色为 Owner、Admin、
Member、Viewer。桌面 `.app` 不走浏览器登录，继续使用运行时生成的本机 API Token。

## 技术栈

- **服务核心**：Go、Gin、SQLite（modernc）、gorilla/websocket
- **辅助服务**：独立部署的 MCP server
- **数据源**：Schwab、SnapTrade、IBKR CPAPI、Finnhub、EDGAR、Claude
