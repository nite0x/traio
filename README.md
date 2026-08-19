# Traio Service

Traio 的核心服务仓库，负责行情、账户、持仓、券商接入、数据存储以及本地 API。

## 仓库边界

- `cmd/server` 和 `internal/` 是服务核心。
- `cmd/mcp` 是开发期的 stdio MCP 适配器，不被服务核心反向依赖；本地安装包与标准服务构建都不会携带或启动它。
- 服务架构、API 规格和接入文档统一保存在 [`traio-doc`](https://github.com/nite0x/traio-doc/tree/main/docs/traio)。
- Tauri 桌面客户端位于独立的 `traio-desktop` 仓库；移动客户端位于独立的 `traio-app` 仓库。
- 本地数据库、配置、编译产物和 IBKR Gateway 安装目录均已忽略，不属于 Git 仓库内容。

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
GET  /admin/gateways                     内嵌 Gateway 管理页面
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
GET  /api/v1/ibkr/gateways
POST /api/v1/ibkr/gateways
GET  /api/v1/ibkr/gateways/:id/status
POST /api/v1/ibkr/gateways/:id/start
POST /api/v1/ibkr/gateways/:id/stop
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

浏览器打开 `http://127.0.0.1:38181/admin/gateways` 可使用内嵌的 IBKR Gateway
管理页面。页面使用运行目录中的 API Token 连接服务；Token 只保存在当前标签页的
`sessionStorage`，关闭标签页后清除。

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
- [IBKR Client Portal Gateway 管理手册](https://github.com/nite0x/traio-doc/blob/main/docs/traio/ibkr-gateway-management.md)
- [端到端加密设备同步架构](https://github.com/nite0x/traio-doc/blob/main/docs/traio/e2ee-device-sync.md)

## IBKR Gateway

IBKR connection 与 Gateway 生命周期管理是两个独立概念。connection 的
`config.gateway_url` 只保存可访问的 Gateway origin，例如
`https://localhost:5680` 或 `https://gateway.example.com`；这个地址可以来自
当前 Traio 管理的本机实例，也可以来自另一台服务器。

Traio 管理的本机 Gateway 是独立资源，可以创建多个实例。每个实例拥有自己
的目录、端口和生命周期：

```http
POST /api/v1/ibkr/gateways
Content-Type: application/json

{
  "gateway_key": "paper-local",
  "name": "Paper Gateway",
  "gateway_url": "https://localhost:5680",
  "gateway_port": 5680,
  "lifecycle": "managed",
  "enabled": true
}
```

`gateway_dir` 可以省略：服务端模式默认使用
`/var/lib/traio/ibkr-gateways/<gateway_key>`，macOS 桌面版默认使用
`~/Library/Application Support/Traio/ibkr-gateways/<gateway_key>`。需要使用已有安装或
挂载卷时仍可传入其他绝对路径。所有实例统一保存在 `ibkr_gateways` 表中。

本机 Gateway 有三种安装来源：

| 方式 | 适用场景 |
|------|----------|
| 项目内捆绑 | 离线分发：`make bundle-ibkr-gateway IBKR_SRC=/path/to/clientportal.gw` |
| 指定本地目录 | 已自行解压，在 `config.yaml` 设置 `gateway_dir` |
| 自动下载 | 能访问 IBKR CDN，留空 `bundled_gateway_dir` |

登录：服务端开发会在 `5680–5699` 中自动分配端口；打包桌面端使用独立的
`5780–5799` 端口段，因此两边的多个 Gateway 可以同时运行。创建时会跳过当前数据库
已配置和系统正在监听的端口。`TRAIO_IBKR_GATEWAY_PORT` 可覆盖自动分配范围的起始端口；
已保存实例仍以数据库配置为准。

### 通过服务域名登录 IBKR

服务端或 Docker 部署可以让 Go 服务代理 connection 所指向的 loopback Gateway。配置后，本地 connection 的登录接口返回一分钟有效、单次使用的浏览器 URL。connection 指向远端 Gateway 时不会经过这个本地代理，而是保留远端 Gateway 自己的登录 URL。

```bash
TRAIO_LISTEN_ADDR=0.0.0.0:8080
TRAIO_ALLOWED_API_HOSTS=alice.traio.example.com
TRAIO_IBKR_PROXY_URL=https://alice-ibkr.traio.example.com
```

外层 Nginx 将两个域名都转发到同一个 Traio 容器并保留原始 `Host`。不要把 Gateway 的 `5680` 端口发布到宿主机或公网。

```text
alice.traio.example.com      -> Traio API
alice-ibkr.traio.example.com -> Traio Go -> 容器内 https://localhost:5680
```

调用流程：

```text
POST /api/v1/broker-connections/{id}/login
  -> { "url": "https://alice-ibkr.../login?ticket=..." }
  -> 浏览器打开 url
  -> Go 验证 Ticket 并代理 /sso/Login
GET /api/v1/broker-connections/{id}/auth/status
  -> authenticated=true 后调用连接同步接口
POST /api/v1/broker-connections/{id}/sync
```

代理只接受从 connection 地址解析出的 HTTPS loopback Gateway，Ticket 单次有效，代理 Session 使用 HttpOnly Cookie。`TRAIO_IBKR_PROXY_URL` 必须是独立 Origin，不能包含路径。

### Gateway 生命周期

Traio 支持两种 Gateway 生命周期：

| 模式 | 适用场景 | Traio 退出时 |
|------|----------|--------------|
| `managed` | 服务端、Docker、本地后端开发 | 停止该受管 Gateway；下次启动需要重新登录 |
| `persistent` | Tauri 桌面端 | 保留 Gateway 和登录会话；下次启动校验后重新接管 |

服务端和 Docker 默认使用 `managed`，打包在 macOS `.app` 中的 sidecar 默认使用 `persistent`。可以通过环境变量覆盖：

```bash
TRAIO_IBKR_GATEWAY_LIFECYCLE=persistent
```

每个 `ibkr_gateways` 资源可以用 `lifecycle` 覆盖默认值；connection 不包含生命周期设置。Traio 会记录实际监听端口的 Java PID、启动时间、Gateway 目录和端口；所有信息匹配后才会接管或终止进程，不会扫描并关闭机器上的其他 Java 服务。用户通过 Gateway 停止接口明确退出时，仍可选择是否保留 Session。

没有域名时可用两个宿主机端口映射到同一个 Go 端口：API 使用 `8080`，IBKR 代理使用 `8081`。手机测试时将 `127.0.0.1` 替换为电脑局域网 IP；HTTP 模式仅用于可信局域网开发，不能暴露到公网。

> Gateway 版权归 Interactive Brokers。**不要将 `third_party/clientportal.gw/` 提交到 git。**

## Docker 部署（包含 Web 前端）

Docker 镜像使用 `traio-desktop` 作为额外构建上下文：Node 阶段执行
`npm run build:web`，Go 阶段构建 `traio-server`，最终镜像保留 Go
二进制、Java 运行时、必要系统工具和编译后的前端文件。前端位于 `/opt/traio/web`，由 Go
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
不要改成 `0.0.0.0:8080` 或在安全组中开放该端口。SQLite、API Token 和
IBKR Gateway 运行目录保存在 `traio-data` volume 中，重新创建容器不会丢失。

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

配置公网域名后，在 `.env` 中设置独立的 API 和 IBKR Proxy host：

```dotenv
TRAIO_ALLOWED_API_HOSTS=traio.nite0x.com
TRAIO_IBKR_PROXY_URL=https://traio-ibkr.nite0x.com
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
