# Traio MCP（Model Context Protocol）

Traio 通过 **stdio MCP 子进程** 对外提供工具，供 Cursor / Claude Desktop 等 MCP 客户端调用。

## 架构

```
Cursor / Claude Desktop
        │  stdio (JSON-RPC)
        ▼
   traio-mcp          ← 独立二进制，内置于 Traio.app/Contents/Resources/
        │  HTTP（默认 http://127.0.0.1:38180）
        ▼
   traio-server       ← Flutter 启动时自动拉起（固定本机端口）
        │
        ▼
   IBKR Gateway / 券商 API / SQLite
```

**前提**：`traio-server` 必须在运行（打开 Traio 桌面 App 即可自动启动），默认地址是 `http://127.0.0.1:38180`。

## Cursor 配置

Settings → MCP → 编辑 `mcp.json`：

```json
{
  "mcpServers": {
    "traio": {
      "command": "/Applications/Traio.app/Contents/Resources/traio-mcp"
    }
  }
}
```

开发阶段（未打包 .app）：

```json
{
  "mcpServers": {
    "traio": {
      "command": "/Users/you/open/traio/bin/traio-mcp"
    }
  }
}
```

构建 MCP 二进制：

```bash
make build-mcp
```

## 可用工具

| 工具 | 说明 |
|------|------|
| `traio_health` | 后端健康检查 |
| `traio_ibkr_gateway_status` | IBKR Gateway 状态 |
| `traio_settings_get` | 读取全部设置 |
| `traio_watchlist_groups` | 自选分组 |
| `traio_quote` | 行情（symbol 参数） |
| `traio_positions` | 持仓 |

## 其他 MCP 接入方式（可选扩展）

| 方式 | 适用 | 说明 |
|------|------|------|
| **stdio 子进程**（当前） | Cursor、Claude Desktop | 最通用，已实现 |
| **HTTP/SSE MCP** | Web 客户端 | 可在 Go 后端加 `/mcp/sse` 端点 |
| **REST 直连** | 自定义 Agent | 跳过 MCP，直接调 `/api/v1/*` |

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `TRAIO_API` | `http://127.0.0.1:38180` | 可选，覆盖 backend 地址 |
