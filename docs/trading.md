# 统一交易服务设计与券商能力分析

> 本文描述服务端适配边界。券商最终允许交易的市场、产品、订单类型和时段仍取决于账户权限、所在地、行情订阅及监管要求；提交前必须以券商返回的校验结果为准。

## 1. 为什么以“连接”而不是券商代码路由

一个用户可能有多个 IBKR Gateway、多个 Schwab OAuth 身份或 Alpaca 的 paper/live 账户。统一入口使用 `connection_id + account_id` 定位交易通道，避免把订单误发至同一券商的另一个账户。核心 `TradingProvider` 仅暴露下单、单笔查询、列表和撤单；`TradingService` 保存连接到 adapter 的映射，HTTP、MCP 和自动化策略不依赖券商 SDK。

统一订单保留 `instrument_id`（IBKR conid 等）、`asset_class`、数量/名义金额、开平仓、限价/止损/跟踪参数、有效期、盘前盘后和客户端幂等 ID。并非每家券商都支持每个字段：adapter 只转换可表达的字段，券商拒绝是最终权威。生产系统下一步应增加“按 provider + asset class 声明能力”的预检，以及持久化的订单意图、请求摘要和审计日志。

## 2. 支持标的与订单对比

### IBKR Client Portal API

- **标的范围最广**：股票、ETF、期权、期货、期货期权、外汇、债券、基金、CFD 等，实际可用品种由账户地区、交易权限及 Client Portal API 对该产品的覆盖决定。
- **订单**：市价、限价、止损、止损限价、跟踪止损，以及大量高级订单和算法订单。当前统一 adapter 实现单腿基础订单；组合单、括号单、OCA、算法参数应以后续扩展模型表达，不能用若干独立订单模拟原子组合。
- **关键差异**：下单使用 `conid` 而不是仅用 symbol；Gateway 可能返回风险提示的 `reply_id`。当前服务安全地返回“需要确认”，不会自动确认提示。

### Charles Schwab Trader API

- **标的**：美股/ETF、单腿及多腿期权、共同基金、固定收益等；具体能力受账户类型和 API 产品权限约束。期货与外汇不应假设可通过 Trader API 下单。
- **订单**：市价、限价、止损、止损限价、跟踪止损；API 原生还可表达复杂期权策略、条件单、OCO 和触发链。当前 adapter 实现单腿订单。
- **关键差异**：交易 URL 必须使用 `/accounts/accountNumbers` 返回的 `hashValue`，不可把明文账号放进路径；期权需要明确 `BUY_TO_OPEN` 等开平仓指令。

### Alpaca Trading API

- **标的**：美国股票/ETF、期权和加密货币。产品、可做空状态和零股能力必须结合 assets 端点及账户权限判断。
- **订单**：股票支持市价、限价、止损、止损限价、跟踪止损和多种有效期；加密货币与期权支持的订单类型/有效期是更小的子集。API 还支持 bracket、OCO、OTO 等高级订单类，当前 adapter 实现基础单。
- **关键差异**：股票可按 `qty` 或 `notional` 下单（两者必须二选一）；paper 与 live endpoint 必须由连接配置隔离；扩展时段限制由 Alpaca 校验。

## 3. HTTP 接口

所有入口均受现有鉴权保护；写操作需要 `trade` 权限。

```http
POST /api/v1/orders
Content-Type: application/json

{
  "connection_id": 12,
  "account_id": "ABC123",
  "symbol": "AAPL",
  "instrument_id": "265598",
  "asset_class": "equity",
  "side": "buy",
  "order_type": "limit",
  "quantity": 1,
  "limit_price": 180.25,
  "time_in_force": "day",
  "client_order_id": "strategy-42-20260818"
}
```

- `GET /api/v1/orders?connection_id=12&account_id=ABC123&status=open&limit=100`
- `GET /api/v1/orders/{order_id}?connection_id=12&account_id=ABC123`
- `DELETE /api/v1/orders/{order_id}?connection_id=12&account_id=ABC123`

返回的统一状态为 `open`、`filled`、`canceled` 或 `rejected`；同时保留 `raw_status` 便于排障。HTTP `201` 只表示券商接受了请求，不表示成交。

## 4. 上线安全清单

1. 默认使用 paper/sandbox 连接；live 连接必须有醒目标识和二次开关。
2. 在 adapter 前增加最大单笔名义金额、持仓上限、价格偏离、重复 client ID、市场时段与账户白名单风控。
3. 订单请求和券商响应写入只追加审计记录，密钥、OAuth token 和 IBKR reply 内容需脱敏。
4. 超时不能直接重试下单：先按 client ID/订单列表对账，避免网络超时导致重复成交。
5. Webhook/stream 只用于加速状态更新，定时以 REST 查询完成最终对账；部分成交必须累计处理。
6. 撤单响应不等于已经撤销，继续查询直到终态；改单应建模为 replace，而不是无条件“撤后重下”。
7. 金额和价格后续应从 `float64` 迁移到十进制定点类型，并依据标的 tick size/quantity increment 做舍入。
