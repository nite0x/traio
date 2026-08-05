# 券商账户同步架构

## 目标

前端持仓读取与券商 API 解耦：

```text
              IBKR broker.Broker
                        |
                        v
              portfolio.SyncService
                        |
                        v
 SQLite broker_accounts / broker_asset_positions
        / broker_account_balances / broker_account_performance
            |
            v
 GET /api/v1/positions
```

`GET /api/v1/positions` 只读取 SQLite，不调用任何券商接口。券商连接失败时，
继续返回该券商上一次成功同步的持仓。

## 同步机制

- 服务启动时立即尝试同步。
- 默认每 30 秒后台同步一次。
- `POST /api/v1/brokers/sync` 可触发同步。
- `GET /api/v1/brokers/sync-status` 按券商、账户和数据类型返回最近成功时间、尝试时间、错误与条数。
- 每个账户的账户详情、现金、持仓和当日盈亏分别提交事务、分别记录状态。
- IBKR 使用 `broker.Broker` 完整能力集：先读取账户列表，再为每个账户同步账户详情、多币种现金、完整持仓和当日盈亏。
- 单项同步失败只保留该账户该数据类型的上一版数据；同账户及其他账户的其他数据仍可成功更新。
- 登录不在 30 秒数据循环内执行；Gateway 启动、session tickle 和健康检查由 Gateway 生命周期任务负责。
- `broker_sync.enabled` 是唯一的同步开关；当前只注册 IBKR。

## 接入新券商

新券商必须实现完整的 `broker.Broker` 最小能力集合，并在
`runtime.BuildBrokers` 注册为 `portfolio.Source`。

读接口和前端不得直接依赖具体券商客户端。

当前同步任务只注册 IBKR。

本地数据库与其他设备之间的端到端加密同步方案见
[e2ee-device-sync.md](e2ee-device-sync.md)。
