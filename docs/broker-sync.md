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

## 手动触发接口

```http
POST /api/v1/brokers/sync
```

除 `/health` 和无副作用的 Gateway 登录跳转外，所有 `/api/v1` 接口都需要
本地 API Token。开发模式下 Token 位于项目根目录 `api-token`，打包应用位于
`~/Library/Application Support/Traio/api-token`，文件权限为 `0600`。

```bash
TOKEN="$(tr -d '\n' < api-token)"
curl -sS -i -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  http://127.0.0.1:38181/api/v1/brokers/sync
```

该接口复用后台定时任务的 `Sync` 流程，并在同步完成后返回：

```json
{"status":"synced"}
```

同步服务不可用时返回 `503`；任一券商资源同步失败时返回 `502` 和错误信息。
`broker_sync.enabled` 关闭时不会访问券商，接口仍返回成功。

前端可通过 `api.brokers.sync()` 主动触发，并通过 `api.brokers.status()`
查询各券商、账户和数据类型最近一次同步状态。

Gateway 的规范操作顺序是：先携带 Token 调用
`POST /api/v1/ibkr/gateway/start`，然后浏览器访问
`GET /api/v1/ibkr/gateway/login`。登录入口只重定向至本地 Gateway 的 IBKR
官方页面，不接收、保存或代理用户名、密码和 2FA。前端可使用
`api.ibkr.loginUrl()` 获取该入口地址。

Traio 只会停止同时满足 PID 文件、Gateway 命令行和 Gateway 工作目录校验的
进程；配置端口若被其他进程占用会报错，不会自动杀进程。Gateway 目录权限为
`0700`，配置、PID、证书缓存与日志为 `0600`，超过 14 天的 Gateway `.log`
文件会自动清理。

## Gateway 安装、升级与回滚

网络安装只接受 Traio 内置发布清单中的 Gateway ZIP。当前清单固定版本、文件
大小和 SHA-256；下载、大小或摘要任一不匹配都会终止安装，现有 Gateway 不会
被替换。安装先在同一文件系统的私有临时目录完成解压、结构校验、配置写入与
权限收紧，再通过目录重命名原子切换。

```bash
TOKEN="$(tr -d '\n' < api-token)"

curl -sS -i -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  http://127.0.0.1:38181/api/v1/ibkr/gateway/upgrade

curl -sS -i -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  http://127.0.0.1:38181/api/v1/ibkr/gateway/rollback
```

升级前的目录保留为 `<gateway_dir>.rollback`。新版本无法启动时 Traio 会自动
恢复上一版；手动回滚会交换当前版和上一版，因此可再次调用实现受控前滚。
`GET /api/v1/ibkr/gateway/status` 额外返回：

- `state`、`last_error`、`state_updated_at`
- `installed_version`、`pinned_version`、`install_verified`
- `rollback_available`

启动、停止、重连、升级和回滚接口现在会等待操作完成，并直接返回具体错误，
不再只返回后台任务已受理。Traio 服务通过 `traio-server.lock` 保证同一运行目录
只有一个实例；Gateway 通过 `<gateway_dir>.manager.lock` 防止跨服务重复管理。

Gateway 管理审计写入 `<gateway_dir>.audit.jsonl`，权限为 `0600`。审计内容只
包含时间、事件、结果和脱敏详情，不记录 Authorization、Cookie、密码、TOTP
或 Token 值。文件达到 5 MiB 时轮转为 `.audit.jsonl.1`。

## 接入新券商

新券商必须实现完整的 `broker.Broker` 最小能力集合，并在
`runtime.BuildBrokers` 注册为 `portfolio.Source`。

读接口和前端不得直接依赖具体券商客户端。

当前同步任务只注册 IBKR。

本地数据库与其他设备之间的端到端加密同步方案见
[e2ee-device-sync.md](e2ee-device-sync.md)。
