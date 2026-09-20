# 每日账户价值与收益曲线

Traio 服务端从规范化账户活动重建每日现金和持仓，再以统一的日终价格与历史 FX 计算账户价值。浏览器和客户端只展示结果，不重复计算收益。

## 计算口径

- `total_value = cash_value + holdings_value`
- `daily_pnl = total_value - previous_total_value - external_cash_flow`
- `daily_return = daily_pnl / previous_total_value`
- `cumulative_twr = product(1 + daily_return) - 1`

入金、出金、现金转入转出和证券转入转出属于外部流量，不计入投资收益。交易、分红、利息、费用、税费和拆股仍通过各自的现金或持仓腿改变账户状态。

只有日期、没有可靠盘中时间的外部流量采用日末流量口径；这一约定固定在 `daily-performance-v1` 中，避免同一历史区间因客户端实现不同而产生不同收益率。

历史证券价格固定按 Tiingo 主源、Yahoo 回退选择；UTC 当日才会在持仓所属连接明确时优先尝试该券商报价。历史 FX 使用 Yahoo 的标准货币对适配器；请求失败时返回 `missing_fx`，不会假设 1:1。

缺少价格或 FX 时，该日的相关合计、PnL 和收益率返回 `null`，并在 `missing_prices` / `missing_fx` 中列明原因，不会按零估值。价格和 FX 最多跨普通周末/假日沿用七个日历日，之后标记为 `partial`。活动覆盖为 `partial` / `unknown` 时，估值点也不能标记为 `official`。缺少成本证据只写入 `missing_cost_basis`，不阻塞市值曲线。历史日期标记为 `eod`；UTC 当日标记为 `intraday`。

活动覆盖不是 `complete` 时仍可展示可解释的现金、持仓和总价值，但 `daily_pnl`、`daily_return` 与 `cumulative_twr` 必须为 `null`，并中断 TWR 链。`source_mix` 只记录估值行情和 FX 来源；Flex、Gateway 等活动来源保留在 `coverage.activity_windows`。

## 数据保留模式

环境变量 `TRAIO_PERFORMANCE_RETENTION_MODE` 控制派生结果是否落库：

- `transient`：默认值。请求时计算，不持久化每日结果，也不持久化行情原始或规范化 OHLC。
- `licensed_persistence`：仅在明确拥有相应许可并同时设置 `TRAIO_MARKET_DATA_PERSISTENCE_LICENSED=true` 时启用。保存每日派生结果；仍不保存提供商原始或规范化 OHLC。

交易修订会记录最早受影响日期。历史同步提交活动与覆盖后会自动触发 `licensed_persistence` 重算；`transient` 仍保留 dirty 边界但不会落库。重新计算只替换受影响日期之后的结果。

## API

```text
GET /api/v1/performance/accounts/:account_id/daily?from=2026-01-01&to=2026-09-20&base_currency=USD
POST /api/v1/performance/accounts/:account_id/recalculate?from=2026-01-01&to=2026-09-20&base_currency=USD
```

响应包含覆盖窗口、估值质量、缺失价格、缺失 FX、行情来源组合、活动代次和 `daily-performance-v1` 计算版本。
