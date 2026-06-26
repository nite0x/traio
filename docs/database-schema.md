# Traio 数据库表结构

Traio 使用 **SQLite** 作为本地持久化存储，默认路径为 `{baseDir}/data/traio.db`。

- 引擎：`modernc.org/sqlite`（纯 Go 实现）
- 迁移入口：`internal/store/store.go` → `migrate()`
- 连接配置：`PRAGMA foreign_keys = ON`、`PRAGMA journal_mode = WAL`
- 单连接模式：`MaxOpenConns(1)` / `MaxIdleConns(1)`，确保 PRAGMA 全局生效

---

## 表一览

| 表名 | 用途 |
|------|------|
| `watchlist_groups` | 自选股分组 |
| `watchlist_items` | 自选股条目 |
| `oauth_tokens` | OAuth 访问令牌（按 provider 存储） |
| `app_settings` | 应用配置（单行 JSON） |
| `broker_accounts` | 券商账户投影 |
| `broker_positions` | 券商持仓投影 |
| `broker_position_syncs` | 券商持仓同步状态 |
| `candle_cache` | K 线数据缓存 |

---

## ER 关系图

```mermaid
erDiagram
    watchlist_groups ||--o{ watchlist_items : "group_id"
    broker_accounts ||--o{ broker_positions : "broker, account"

    watchlist_groups {
        INTEGER id PK
        TEXT name UK
        INTEGER sort_order
        TEXT created_at
    }

    watchlist_items {
        INTEGER id PK
        INTEGER group_id FK
        TEXT symbol
        INTEGER conid
        TEXT name
        TEXT sec_type
        TEXT exchange
        TEXT currency
        TEXT tags
        TEXT notes
        TEXT custom_fields
        INTEGER sort_order
        TEXT created_at
        TEXT updated_at
    }

    oauth_tokens {
        TEXT provider PK
        TEXT access_token
        TEXT refresh_token
        TEXT expires_at
        TEXT updated_at
    }

    app_settings {
        INTEGER id PK
        TEXT data
        TEXT updated_at
    }

    broker_accounts {
        TEXT broker PK
        TEXT account PK
        TEXT currency
        TEXT synced_at
    }

    broker_positions {
        TEXT broker PK
        TEXT account PK
        TEXT symbol PK
        INTEGER conid PK
        REAL quantity
        REAL avg_cost
        REAL market_price
        REAL market_value
        REAL unrealized_pnl
        REAL realized_pnl
        TEXT currency
        TEXT synced_at
    }

    broker_position_syncs {
        TEXT broker PK
        TEXT synced_at
        TEXT last_attempt_at
        TEXT last_error
    }

    candle_cache {
        TEXT symbol PK
        TEXT period PK
        TEXT bar PK
        INTEGER conid
        TEXT candles
        INTEGER cached_at
    }
```

---

## watchlist_groups

自选股分组表。

| 列名 | 类型 | 约束 | 默认值 | 说明 |
|------|------|------|--------|------|
| `id` | INTEGER | PRIMARY KEY, AUTOINCREMENT | — | 分组 ID |
| `name` | TEXT | NOT NULL, UNIQUE | — | 分组名称 |
| `sort_order` | INTEGER | NOT NULL | `0` | 排序权重 |
| `created_at` | TEXT | NOT NULL | `datetime('now')` | 创建时间（SQLite datetime 字符串） |

**初始数据：**

```sql
INSERT OR IGNORE INTO watchlist_groups (id, name, sort_order) VALUES (1, '默认', 0);
```

---

## watchlist_items

自选股条目表。基础结构在 `migrate()` 中创建，部分列通过 `ensureWatchlistItemColumns()` 增量迁移添加（兼容旧库）。

| 列名 | 类型 | 约束 | 默认值 | 说明 |
|------|------|------|--------|------|
| `id` | INTEGER | PRIMARY KEY, AUTOINCREMENT | — | 条目 ID |
| `group_id` | INTEGER | NOT NULL, FK → `watchlist_groups(id)` ON DELETE CASCADE | — | 所属分组 |
| `symbol` | TEXT | NOT NULL | — | 标的代码 |
| `conid` | INTEGER | NOT NULL | `0` | 合约 ID（IBKR 等） |
| `name` | TEXT | NOT NULL | `''` | 标的名称 |
| `sec_type` | TEXT | NOT NULL | `''` | 证券类型（如 STK、OPT） |
| `exchange` | TEXT | NOT NULL | `''` | 交易所 |
| `currency` | TEXT | NOT NULL | `''` | 计价货币 |
| `tags` | TEXT | NOT NULL | `'[]'` | 标签 JSON 数组 |
| `notes` | TEXT | NOT NULL | `''` | 备注 |
| `custom_fields` | TEXT | NOT NULL | `'{}'` | 自定义字段 JSON 对象 |
| `sort_order` | INTEGER | NOT NULL | `0` | 组内排序 |
| `created_at` | TEXT | NOT NULL | `datetime('now')` | 创建时间 |
| `updated_at` | TEXT | NOT NULL | `''` | 最后更新时间 |

**唯一约束：** `UNIQUE(group_id, symbol)` — 同一分组内 symbol 不可重复。

**对应 Go 类型：** `store.WatchlistItem`

---

## oauth_tokens

OAuth 令牌存储，按 provider 维度单行保存。

| 列名 | 类型 | 约束 | 默认值 | 说明 |
|------|------|------|--------|------|
| `provider` | TEXT | PRIMARY KEY | — | 提供方标识（如 schwab、ibkr） |
| `access_token` | TEXT | NOT NULL | — | 访问令牌 |
| `refresh_token` | TEXT | 可空 | — | 刷新令牌 |
| `expires_at` | TEXT | 可空 | — | 过期时间（RFC3339Nano） |
| `updated_at` | TEXT | NOT NULL | `datetime('now')` | 最后更新时间 |

**对应 Go 类型：** `store.OAuthToken`

---

## app_settings

应用配置表，固定单行（`id = 1`），`data` 列存储完整 `config.Config` 的 JSON 序列化结果。

| 列名 | 类型 | 约束 | 默认值 | 说明 |
|------|------|------|--------|------|
| `id` | INTEGER | PRIMARY KEY, CHECK (`id = 1`) | — | 固定为 1 |
| `data` | TEXT | NOT NULL | — | 配置 JSON |
| `updated_at` | TEXT | NOT NULL | `datetime('now')` | 最后保存时间 |

**读写入口：** `internal/store/settings.go`、`internal/settings/manager.go`

---

## broker_accounts

券商账户投影，与持仓同步一并更新。

| 列名 | 类型 | 约束 | 默认值 | 说明 |
|------|------|------|--------|------|
| `broker` | TEXT | PRIMARY KEY（复合） | — | 券商名称（大写，如 IBKR） |
| `account` | TEXT | PRIMARY KEY（复合） | `''` | 账户 ID |
| `currency` | TEXT | NOT NULL | `''` | 账户主货币 |
| `synced_at` | TEXT | NOT NULL | — | 最后同步时间（RFC3339 UTC） |

**主键：** `(broker, account)`

---

## broker_positions

券商持仓投影。每次成功同步时，先按 broker 删除旧持仓再全量写入。

| 列名 | 类型 | 约束 | 默认值 | 说明 |
|------|------|------|--------|------|
| `broker` | TEXT | PRIMARY KEY（复合） | — | 券商名称 |
| `account` | TEXT | PRIMARY KEY（复合） | `''` | 账户 ID |
| `symbol` | TEXT | PRIMARY KEY（复合） | — | 标的代码（大写） |
| `conid` | INTEGER | PRIMARY KEY（复合） | `0` | 合约 ID |
| `quantity` | REAL | NOT NULL | — | 持仓数量 |
| `avg_cost` | REAL | NOT NULL | `0` | 平均成本 |
| `market_price` | REAL | NOT NULL | `0` | 市价 |
| `market_value` | REAL | NOT NULL | `0` | 市值 |
| `unrealized_pnl` | REAL | NOT NULL | `0` | 未实现盈亏 |
| `realized_pnl` | REAL | NOT NULL | `0` | 已实现盈亏 |
| `currency` | TEXT | NOT NULL | `''` | 计价货币 |
| `synced_at` | TEXT | NOT NULL | — | 同步时间（RFC3339 UTC） |

**主键：** `(broker, account, symbol, conid)`

**外键：** `(broker, account)` → `broker_accounts(broker, account)` ON DELETE CASCADE

**索引：**

```sql
CREATE INDEX IF NOT EXISTS idx_broker_positions_symbol ON broker_positions (symbol);
```

**对应 Go 类型：** `broker.Position`

---

## broker_position_syncs

各券商持仓同步的状态记录（成功/失败均写入）。

| 列名 | 类型 | 约束 | 默认值 | 说明 |
|------|------|------|--------|------|
| `broker` | TEXT | PRIMARY KEY | — | 券商名称 |
| `synced_at` | TEXT | NOT NULL | `''` | 上次成功同步时间；失败时为空 |
| `last_attempt_at` | TEXT | NOT NULL | — | 上次尝试时间 |
| `last_error` | TEXT | NOT NULL | `''` | 上次错误信息；成功时清空 |

**对应 Go 类型：** `store.BrokerPositionSync`

---

## candle_cache

K 线数据本地缓存，按 `(symbol, period, bar)` 维度存储，TTL 在应用层按 bar 大小判定。

| 列名 | 类型 | 约束 | 默认值 | 说明 |
|------|------|------|--------|------|
| `symbol` | TEXT | PRIMARY KEY（复合） | — | 标的代码 |
| `period` | TEXT | PRIMARY KEY（复合） | — | 时间范围（如 `1d`、`1m`、`1y`） |
| `bar` | TEXT | PRIMARY KEY（复合） | — | K 线周期（如 `1min`、`1h`、`1d`） |
| `conid` | INTEGER | NOT NULL | — | 合约 ID |
| `candles` | TEXT | NOT NULL | — | K 线 JSON 数组 |
| `cached_at` | INTEGER | NOT NULL | — | 缓存写入时间（Unix 秒） |

**主键：** `(symbol, period, bar)`

**`candles` JSON 元素结构（`broker.Candle`）：**

```json
{
  "time": 1719000000,
  "open": 100.0,
  "high": 101.5,
  "low": 99.5,
  "close": 101.0,
  "volume": 1234567
}
```

**缓存 TTL 规则（应用层，`candleTTL()`）：**

| bar 类型 | TTL |
|----------|-----|
| `1d`、`1w`、`1m` | 24 小时 |
| `1h`、`2h`、`4h` | 1 小时 |
| 其他（如 `5min`、`15min`） | 15 分钟 |

**迁移入口：** `internal/store/candle_cache.go` → `ensureCandleCache()`

---

## 完整 DDL

以下为当前代码中的完整建表语句（含索引），`watchlist_items` 已合并增量迁移列。

```sql
-- PRAGMA foreign_keys = ON;
-- PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS watchlist_groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS watchlist_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id INTEGER NOT NULL REFERENCES watchlist_groups(id) ON DELETE CASCADE,
    symbol TEXT NOT NULL,
    conid INTEGER NOT NULL DEFAULT 0,
    name TEXT NOT NULL DEFAULT '',
    sec_type TEXT NOT NULL DEFAULT '',
    exchange TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]',
    notes TEXT NOT NULL DEFAULT '',
    custom_fields TEXT NOT NULL DEFAULT '{}',
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT '',
    UNIQUE(group_id, symbol)
);

CREATE TABLE IF NOT EXISTS oauth_tokens (
    provider TEXT PRIMARY KEY,
    access_token TEXT NOT NULL,
    refresh_token TEXT,
    expires_at TEXT,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS app_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    data TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS broker_accounts (
    broker TEXT NOT NULL,
    account TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL DEFAULT '',
    synced_at TEXT NOT NULL,
    PRIMARY KEY (broker, account)
);

CREATE TABLE IF NOT EXISTS broker_positions (
    broker TEXT NOT NULL,
    account TEXT NOT NULL DEFAULT '',
    symbol TEXT NOT NULL,
    conid INTEGER NOT NULL DEFAULT 0,
    quantity REAL NOT NULL,
    avg_cost REAL NOT NULL DEFAULT 0,
    market_price REAL NOT NULL DEFAULT 0,
    market_value REAL NOT NULL DEFAULT 0,
    unrealized_pnl REAL NOT NULL DEFAULT 0,
    realized_pnl REAL NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT '',
    synced_at TEXT NOT NULL,
    PRIMARY KEY (broker, account, symbol, conid),
    FOREIGN KEY (broker, account) REFERENCES broker_accounts (broker, account)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_broker_positions_symbol
    ON broker_positions (symbol);

CREATE TABLE IF NOT EXISTS broker_position_syncs (
    broker TEXT PRIMARY KEY,
    synced_at TEXT NOT NULL DEFAULT '',
    last_attempt_at TEXT NOT NULL,
    last_error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS candle_cache (
    symbol    TEXT    NOT NULL,
    conid     INTEGER NOT NULL,
    period    TEXT    NOT NULL,
    bar       TEXT    NOT NULL,
    candles   TEXT    NOT NULL,
    cached_at INTEGER NOT NULL,
    PRIMARY KEY (symbol, period, bar)
);

-- 初始种子数据
INSERT OR IGNORE INTO watchlist_groups (id, name, sort_order) VALUES (1, '默认', 0);
```

---

## 源码索引

| 文件 | 内容 |
|------|------|
| `internal/store/store.go` | 主迁移、watchlist CRUD |
| `internal/store/candle_cache.go` | K 线缓存表及读写 |
| `internal/store/settings.go` | `app_settings` 读写 |
| `internal/store/oauth_tokens.go` | `oauth_tokens` 读写 |
| `internal/store/positions.go` | 券商持仓/同步状态读写 |
