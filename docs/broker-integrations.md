# 券商接入架构与开发指南

Traio 以“券商类型”和“用户连接”两个维度建模。`ProviderDefinition` 描述一种券商支持的认证方式、配置字段和能力；数据库中的 broker connection 是用户实际配置的一条连接；`BrokerSession` 则是该连接在当前进程中的运行时实例。

```text
ProviderDefinition
        │ 由 ProviderFactory 注册和创建
        ▼
ProviderFactory.Open(ConnectionConfig)
        ▼
BrokerSession（连接身份、健康状态、关闭）
        │ 通过 Go 小接口声明可选能力
        ├── AuthenticationProvider
        ├── PortfolioProvider
        ├── Instrument / Quote / Candle providers
        ├── TradingProvider
        └── AccountEquityProvider
                ▼
ConnectionManager → Portfolio / MarketData / Trading / Account services
```

## 设计边界

- `ProviderDefinition` 是静态元数据，不保存用户凭证，也不代表网络连接。
- `ProviderFactory` 负责把 provider 级配置与 connection 级配置转换成一个 Session。它不依赖 HTTP API 或同步服务。
- `BrokerSession` 只强制提供连接 ID、provider code、健康状态和关闭操作。认证、持仓、行情与交易均为可选能力，不能通过一个大而全的 Broker 接口强制组合。
- `ConnectionManager` 负责加载配置、复用或替换 Session、关闭旧 Session，并把能力注册到服务。这里不维护 IBKR、Schwab、Alpaca 等具体客户端 map，也不通过 provider `switch` 选择实现。
- `MarketDataService` 支持按 `connection_id` 路由；未显式指定时，使用对应行情能力的默认连接并按确定性顺序回退。`TradingService` 始终要求明确的 `connection_id`。
- IBKR Client Portal Gateway 的安装与生命周期完全在 Traio 之外管理。Traio 不接触
  安装目录或 Java 进程；IBKR provider 保存 Gateway Manager origin 与管理 Token，
  connection 必须选择 Manager 返回的实例，并保存该实例的 `gateway_id`、代理
  `gateway_url` 与只写的 `gateway_token`。

Session 可以嵌入或共享同一个底层 HTTP client、OAuth token、限流器和 WebSocket 管理器。拆分能力接口并不要求创建多套网络客户端。

## 新增券商

建议目录：

```text
internal/broker/<provider>/
├── factory.go
├── session.go（规模较小时可与 factory.go 合并）
├── authentication.go
├── portfolio.go
├── market_data.go
└── trading.go
```

### 1. 定义 Provider

实现 `ProviderFactory.Definition()`，使用稳定的大写 code，声明真实支持的 `AuthModes`、`CapabilitySet` 和 `ConfigSchema`。能力声明必须与 Session 实际实现一致；不要为了复用 UI 而声明尚未实现的能力。

provider 级字段适合所有连接共享的应用配置，例如 OAuth client ID；connection 级字段适合账户身份、环境、API key 或 Gateway URL。秘密字段必须标为 `Secret`，且不得写入错误或日志。

### 2. 打开 Session

实现：

```go
type ProviderFactory interface {
    Definition() ProviderDefinition
    Open(context.Context, ConnectionConfig) (BrokerSession, error)
}
```

`Open` 应校验必填配置，创建连接专属状态，并保证返回 Session 的 `ConnectionID()` 和 `ProviderCode()` 与输入一致。`Close` 必须可重复调用，并释放 HTTP idle connections、WebSocket、后台 goroutine 等资源。

在 `internal/runtime/connections.go` 的 composition root 注册 Factory。新增 Factory 只增加一条注册项；`ConnectionManager.Reload`、同步、行情和交易路由不应出现新的 provider 分支。

若该 provider 还没有数据库 catalog 定义，需要同步增加 store 的 provider seed/迁移数据，但不得修改现有 schema 或已有 connection 配置语义。

### 3. 认证能力

所有可认证 Session 实现：

- `AuthenticationProvider`：开始认证及读取状态；
- `AuthenticationCallbackHandler`：仅 OAuth callback 类连接实现；
- `AuthenticationRefresher`：仅支持主动刷新凭证时实现；
- `AuthenticationRevoker`：仅能实际撤销凭证时实现。

API key 可以在 Begin/Status 中验证密钥；OAuth Begin 返回授权 URL；本机或远程 Gateway 可返回登录 URL。不要为了接口完整度提供空实现，可选能力缺失时运行时会返回统一的 unavailable 错误。

### 4. 持仓同步

优先实现 `PortfolioProvider`，一次返回账户快照。若现有客户端已分别实现 `AccountProvider`、`PositionProvider` 和 `PerformanceProvider`，可用 `AsPortfolioProvider` 生成组合适配器。`portfolio.SyncService` 只依赖 consumer-owned 的 `PortfolioProvider` lease，不会识别具体券商。lease 会覆盖完整同步，包括组合适配器延迟执行的 snapshot `Resolve`，防止 Reload 在资源读取或写入投影期间关闭 Session。

快照中的 provider account ID 和永久 instrument ID 必须稳定；币种、时间和数值要在 adapter 边界归一化。一个连接失败不能阻断其他连接同步。

### 5. 行情能力

按真实支持范围实现以下一个或多个小接口：

- `InstrumentProvider`
- `MarketDataProvider`（symbol quote）
- `BatchMarketDataProvider`（永久 instrument/conid quote）
- `CandleProvider`

`MarketDataService.Replace` 会从当前 Sessions 自动建立能力索引。provider-specific 的 streaming 或旧 API 如必须使用额外方法，应在消费包中定义最小接口，并通过通用 Session lease resolver 做 type assertion，不能向 runtime 添加具体客户端 map。调用方必须在操作结束后释放 lease，避免 Reload 并发关闭正在使用的 Session；WebSocket 在完成订阅注册后即可释放，Session 关闭会结束订阅 channel。

### 6. 交易与账户净值

交易实现 `TradingProvider`，所有请求都以 `connection_id` 路由。保留 provider 原始订单状态供排障，但不要泄露凭证。若能提供实时账户汇总及历史净值，实现 `AccountEquityProvider`；账户服务会自动发现该能力。

## 测试清单

每个新 provider 至少覆盖：

- Definition code、认证方式、配置 schema 和能力矩阵；
- Factory 配置校验、Session 身份及重复 Close；
- 启用、禁用、配置不变复用、配置变化替换及旧 Session 关闭；
- 认证 Begin/Status，以及适用时 callback、refresh、revoke；
- 错误和日志不包含 API key、client secret、access/refresh token；
- 持仓快照归一化、部分接口组合适配及单连接失败隔离；
- 行情各能力的显式连接路由、默认选择、回退和缺能力错误；
- 交易按 connection 路由和 provider 错误映射；
- provider-specific HTTP 兼容接口在 Reload 后解析到新 Session。

合并前运行：

```bash
gofmt -w <changed-go-files>
go vet ./...
go test ./...
go test -race ./internal/runtime ./internal/portfolio ./internal/broker/... ./internal/api
git diff --check
```
