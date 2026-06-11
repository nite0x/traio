# 券商持仓同步架构

## 目标

前端持仓读取与券商 API 解耦：

```text
IBKR / Schwab / Alpaca / Binance
            |
            v
   broker.PortfolioProvider
            |
            v
 portfolio.SyncPositions
            |
            v
 SQLite broker_accounts / broker_positions
            |
            v
 GET /api/v1/positions
```

`GET /api/v1/positions` 只读取 SQLite，不调用任何券商接口。券商连接失败时，
继续返回该券商上一次成功同步的持仓。

## 同步机制

- 服务启动时立即尝试同步。
- 默认每 30 秒后台同步一次。
- `POST /api/v1/positions/sync` 可触发同步。
- `GET /api/v1/positions/sync-status` 返回每家券商的最近成功时间和错误。
- 每家券商独立替换自己的持仓投影，单家失败不影响其他券商。

## 接入新券商

新券商适配器实现 `broker.PortfolioProvider`，把原始字段清洗为
`broker.Position`，然后在 `runtime.BuildBrokers` 注册为一个
`portfolio.Source`。

读接口和前端不得直接依赖具体券商客户端。

当前代码已注册 IBKR 和 SnapTrade。Schwab、Alpaca、Binance 需要各自完成
持仓适配器后注册到同步源。

本地数据库与其他设备之间的端到端加密同步方案见
[e2ee-device-sync.md](e2ee-device-sync.md)。
