# 端到端加密设备同步架构

## 决策摘要

Traio 采用同一个 Go 仓库、两个独立服务入口：

```text
go run ./cmd/server        # 用户设备上的本地服务
go run ./cmd/cloud-server  # 云端密文同步服务
```

两个入口共享同步协议、加密格式和纯逻辑，但拥有不同的运行权限、数据库和职责。
它们是同一仓库内的两个服务，不是同一个进程，也不共享数据库。

核心安全目标：

- 券商凭据和 OAuth Token 仅保存在本地设备。
- 云端服务只保存密文，不能读取持仓、账户、交易和自选内容。
- 本地前端只访问本地 Go 服务，不直接访问云端同步服务。
- 多设备之间通过云端密文中继同步。

## 总体架构

```text
桌面端 / 移动端前端
         |
         v
本地 Go 服务 cmd/server
  - 连接券商
  - 维护本地明文投影
  - 合并同步记录
  - 加密与解密
         |
         | HTTPS，仅传输密文
         v
云端 Go 服务 cmd/cloud-server
  - 用户与设备认证
  - 验证签名
  - 保存密文记录
  - 分配同步游标
  - 不持有解密密钥
         ^
         |
其他已授权设备
```

现有券商持仓同步仍然先写入本地 SQLite，再由设备同步模块选取允许同步的数据上传。
详见 [portfolio-sync.md](portfolio-sync.md)。

## 推荐目录结构

```text
traio/
├── cmd/
│   ├── server/              # 本地 Traio 服务入口
│   ├── cloud-server/        # 云端密文同步服务入口
│   └── sync-cli/            # 可选：同步协议调试工具
├── internal/
│   ├── broker/              # 仅本地：券商连接
│   ├── portfolio/           # 仅本地：明文持仓投影
│   ├── store/               # 仅本地：业务数据库
│   ├── localsync/           # 仅本地：加密、解密、合并、同步任务
│   └── cloudsync/           # 仅云端：认证、游标、密文存储
└── pkg/
    └── sync/
        ├── protocol/        # 双端共享：DTO、版本、游标
        ├── cryptobox/       # 双端共享：密文信封与签名验证
        └── merge/           # 双端共享：不依赖明文的纯合并规则
```

共享包不得依赖 `internal/broker`、`internal/store` 或具体云端数据库实现。

## 职责边界

### 双端可以复用

- 同步 API 请求与响应结构体
- 密文信封格式
- Ed25519 签名与验证
- 版本、游标、幂等键规则
- 记录类型与 schema 版本定义
- 不需要读取明文的冲突检测逻辑
- 协议兼容性测试工具

### 仅本地服务负责

- 连接 IBKR、Schwab、Alpaca、Binance
- 保存和读取业务明文
- 数据加密、解密与明文合并
- 管理设备私钥、用户数据主密钥
- 决定哪些数据允许同步
- 为本地前端提供业务 API

### 仅云端服务负责

- 用户认证、设备注册与撤销
- 验证设备签名和请求权限
- 保存密文、版本、游标和删除墓碑
- 租户隔离、限流、审计和备份
- 防止重放和非法版本覆盖

云端服务不得依赖本地业务表结构，也不得拥有用户数据解密能力。

## 密钥与加密模型

每台设备首次启动时生成：

- Ed25519 密钥对：设备身份和请求签名。
- X25519 密钥对：设备授权和密钥封装。

每个用户生成随机数据主密钥 `DEK`：

- 使用 `XChaCha20-Poly1305` 加密同步 payload。
- 每条记录使用独立随机 nonce。
- 使用 `user_id + record_type + record_id + version` 作为 AAD。
- `DEK` 分别使用每台授权设备的 X25519 公钥封装。

私钥必须保存在系统安全存储：

- macOS / iOS：Keychain
- Windows：Credential Manager 或 DPAPI
- Android：Android Keystore

券商 OAuth Token 默认不参与多设备同步。

## 同步记录模型

不要同步整个 SQLite 文件。按逻辑记录同步：

```go
type EncryptedRecord struct {
    UserID       string
    RecordID     string
    RecordType   string
    SchemaVersion int
    Version      int64
    DeviceID     string
    Nonce        []byte
    Ciphertext   []byte
    Signature    []byte
    Deleted      bool
}
```

云端仅存储密文信封及同步元数据。推荐云端表：

```text
users
devices
device_key_envelopes
encrypted_records
sync_changes
sync_cursors
```

第一阶段建议同步：

- watchlists / watchlist_items
- 用户设置中的非敏感部分
- 用户手工维护的交易和备注

券商实时持仓属于可重建投影，默认不必跨设备同步；需要离线展示时再选择性同步。

## 同步流程

```text
1. 本地业务变更写入 SQLite，并记录 outbox。
2. 本地同步任务序列化、加密并签名 outbox 记录。
3. 客户端通过 HTTPS 上传密文批次。
4. 云端验证设备签名、版本和幂等键，原子提交并返回 cursor。
5. 客户端拉取 cursor 之后的密文变更。
6. 本地验证签名、解密、合并，并更新本地 cursor。
7. 确认成功后清理已提交 outbox。
```

网络重试必须幂等。服务端使用 `user_id + record_id + version` 防止重复写入。

## 冲突策略

| 数据类型 | 推荐策略 |
|---|---|
| 自选列表、备注、普通设置 | Last-Write-Wins，保留冲突元数据 |
| 手工交易流水 | 不可变追加，修改生成新版本 |
| 券商持仓投影 | 以券商来源和最新同步版本覆盖 |
| 删除操作 | 使用墓碑，延迟清理 |
| 设备授权与密钥 | 服务端版本检查，不允许自动覆盖 |

涉及资产和交易的数据应使用稳定 UUID，不能依赖本地 SQLite 自增 ID。

## 安全约束

- E2EE 不替代 HTTPS，传输层仍必须使用 TLS。
- 所有上传批次必须签名，并包含时间窗口、nonce 或请求序号防重放。
- 设备撤销后，云端拒绝其后续写入；高安全场景需要轮换 `DEK`。
- 丢失全部授权设备和恢复密钥后，密文不可恢复。
- 日志、错误信息和审计记录不得包含明文 payload、密钥或券商 Token。
- 云端无法执行依赖明文的搜索、统计、告警和 AI 分析。

## 分阶段实施

### 第一阶段：协议与本地 outbox

- 建立 `pkg/sync/protocol` 和协议版本。
- 为需要同步的业务记录引入稳定 UUID、版本和更新时间。
- 新增本地 `sync_outbox` 与 `sync_state`。
- 完成密文信封和加密单元测试。

### 第二阶段：云端密文服务

- 新增 `cmd/cloud-server`。
- 实现用户、设备、密钥信封和密文记录 API。
- 实现签名验证、幂等写入、增量 cursor 和设备撤销。

### 第三阶段：多设备闭环

- 本地后台推送与拉取。
- 实现冲突处理、删除墓碑和失败重试。
- 完成双设备、离线编辑、重复上传和设备撤销的端到端测试。

### 第四阶段：恢复与运维

- 恢复密钥和新设备授权流程。
- 密钥轮换、数据导出、云端备份和审计。
- 协议版本升级与兼容策略。

## 验收标准

- 关闭所有券商连接时，本地业务读取和设备同步仍可正常运行。
- 云端数据库泄露时，攻击者无法还原用户业务明文。
- `GET /api/v1/positions` 等本地业务接口不依赖云端同步服务。
- 同一批次重复上传不会产生重复数据。
- 单设备离线修改后可以增量同步到另一设备。
- 被撤销设备不能继续上传或获取新的密钥信封。
