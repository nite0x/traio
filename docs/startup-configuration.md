# 启动配置与 Infisical

Traio 可在启动时通过 Infisical Secrets Management 获取数据库和认证配置。使用 Go SDK，不需要安装 CLI。Infisical 是可替换的配置来源，业务服务只接收最终配置。

本功能不加密数据库字段，不迁移券商 Token，也不会因为在 Infisical 保存了 AES 密钥而自动启用加密。

## 来源与优先级

1. 显式环境变量或对应 Secret 文件。
2. 已启用的 Infisical 配置来源，仅补齐缺失值。
3. 配置允许的默认值。
4. 校验必填项与格式；失败时不开放 HTTP 服务。

空字符串视为未提供。敏感值保留首尾空格；Secret 文件只去掉尾部 CR/LF。非敏感值去掉首尾空白。`false` 是显式值，不会被覆盖。值与对应 `_FILE` 同时非空时直接报错。文件读失败或显式值无效时不回退到远程来源。

默认值在合并后应用。PostgreSQL 必须有 DSN；连接失败不回退 SQLite。未指定数据库类型时保留原有 SQLite 默认，但远程选择项或 DSN 获取失败不能被当成“不存在”并创建新的默认数据库。

修改 Secret 后需重启服务。运行期间不轮询 Infisical，不刷新认证 Token，不保存本地缓存，不修改进程环境变量。Infisical 故障不会中断已启动的业务服务。撤销平台权限也不会清除已启动进程里的值；需要重启或停止该进程。

## 支持的配置

Infisical Secret 名称与下表一致。只加载白名单中的名称；远程 `_FILE`、运行目录、部署模式、Provider 设置和未列出的键均不生效。

| 配置 | 规则 |
| --- | --- |
| `TRAIO_DATABASE_DRIVER` | `sqlite` / `postgres` / `postgresql`；默认 SQLite |
| `TRAIO_DATABASE_DSN` | PostgreSQL 必填；SQLite 默认使用运行目录中的数据库 |
| `TRAIO_AUTH_MODE` | `local` / `password` / `oidc` / `disabled-dev`；保留原有部署模式默认值和限制 |
| `TRAIO_OIDC_ISSUER_URL` | OIDC 必填，HTTP(S) URL |
| `TRAIO_OIDC_CLIENT_ID` | OIDC 必填 |
| `TRAIO_OIDC_CLIENT_SECRET` | 按身份提供方需要配置；远程读取失败时不降级为无 Secret 客户端 |
| `TRAIO_OIDC_REDIRECT_URL` | OIDC 必填，HTTP(S) URL |
| `TRAIO_BOOTSTRAP_ADMIN_USERNAME` | password 模式首次创建管理员时必填 |
| `TRAIO_BOOTSTRAP_ADMIN_PASSWORD` | 与用户名成对提供；已有管理员时可同时省略 |
| `TRAIO_BOOTSTRAP_ADMIN_EMAIL` | 可选 |
| `TRAIO_BOOTSTRAP_ADMIN_NAME` | 可选 |
| `TRAIO_COOKIE_SECURE` | 可选，未提供时从 OIDC redirect URL 推导 |
| `TRAIO_SESSION_TTL` | 可选，默认 `12h`，必须为正时长 |

DSN、OIDC Client Secret、管理员密码支持本地对应的 `_FILE` 环境变量。数据库/认证选择项先解析，再加载该模式适用的配置；未启用券商的凭据不属于启动必填项。

管理员存在与否需要连接数据库后判断。已有账号不会因为更改启动密码而被自动重置；用户名密码只提供一项仍视为配置错误。

## 启用 Infisical

在 Infisical 创建项目、环境和专用目录（例如 `/traio`），创建 Machine Identity 并开启 Universal Auth。授权该身份读取指定环境和目录的 Secrets，不授予写入或管理员权限。

启动环境提供以下信息；这些值不能再从 Infisical 自身读取：

```dotenv
TRAIO_CONFIG_PROVIDER=infisical
TRAIO_INFISICAL_SITE_URL=https://app.infisical.com
TRAIO_INFISICAL_PROJECT_ID=your-project-id
TRAIO_INFISICAL_ENVIRONMENT=prod
TRAIO_INFISICAL_SECRET_PATH=/traio
INFISICAL_UNIVERSAL_AUTH_CLIENT_ID=your-machine-client-id
INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET_FILE=/run/secrets/infisical-client-secret
```

站点必须是 HTTPS origin，请使用实际云区域或自托管地址，不要带 `/api`、用户信息、查询参数。项目、环境和机器凭据均必填；目录默认 `/`。自托管证书必须由系统信任。未设置 `TRAIO_CONFIG_PROVIDER` 或设为 `none` 时完全跳过平台，不初始化 SDK。

也可通过 `INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET` 直接注入凭据，不能与 `_FILE` 同时配置。使用文件时应由部署平台以只读 Secret 挂载提供；不会把凭据复制进镜像。

在 Infisical `/traio` 目录中保存，例如：

```text
TRAIO_DATABASE_DRIVER = postgres
TRAIO_DATABASE_DSN = <完整 PostgreSQL DSN>
TRAIO_AUTH_MODE = password
TRAIO_BOOTSTRAP_ADMIN_USERNAME = <初始管理员名>
TRAIO_BOOTSTRAP_ADMIN_PASSWORD = <初始管理员密码>
TRAIO_COOKIE_SECURE = true
```

`compose.yaml` 已透传 Provider 和数据库配置。Compose 保留现有 `TRAIO_AUTH_MODE=password` 默认值，这属于显式启动配置，会覆盖远程模式；如要由 Infisical 决定模式，在 Compose 的环境文件里明确设置 `TRAIO_AUTH_MODE=`。Cookie 和 Session TTL 不再由 Compose 注入默认值，交给应用在合并后推导。

Docker Secret 示例，作为额外的 Compose override 文件使用：

```yaml
services:
  traio:
    environment:
      INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET_FILE: /run/secrets/infisical-client-secret
    secrets:
      - infisical-client-secret
secrets:
  infisical-client-secret:
    file: ./private/infisical-client-secret
```

文件不要加入版本控制。Railway 等部署环境可直接提供同名环境变量，继续使用原有服务二进制入口，不需要 CLI 包装。

## 网络和失败行为

整个配置加载共享 15 秒预算，单次 HTTP 请求最多 5 秒。网络错误、429 和 500/502/503/504 最多重试两次；401/403/404 不重试。重试也受总预算约束。禁止重定向，避免向其他地址转发认证信息。

适配器使用固定版本 `github.com/infisical/go-sdk v0.8.0` 的官方认证和 Secret 请求模块，并自行配置 HTTP transport。该 SDK 顶层客户端未暴露请求 context/timeout 控制，因此不使用其后台生命周期。SDK 的公共工具包仍会带入云认证相关的间接依赖。

第一次需要远程值时，认证后读取指定目录的非递归 Secret 快照，不包含导入目录；仅保留白名单值，按请求键返回，不设置全局环境变量。同一启动过程使用同一快照。这样能把“成功读取目录但缺少某个键”和“项目/环境/目录错误导致 404”分开，避免错误默认值。该身份需要目录读取权限；建议给 Traio 使用专用目录。

| 场景 | 行为 |
| --- | --- |
| 所有适用配置均已显式提供 | 不认证、不发请求 |
| 仅未提供可选项 | 可尝试读取；平台不可用时告警并使用允许的默认值 |
| 数据库类型、认证模式或 DSN 读取失败 | 停止启动 |
| OIDC 必填项缺失或 Client Secret 无法读取 | 停止启动 |
| 初始管理员凭据缺失 | 已建号可启动；新库停止启动 |
| Secret 成功获取但格式错误 | 停止启动，不回退其他来源 |

日志只包含键名、错误类别和必要状态，不打印值、认证凭据、远程错误正文或 DSN。数据库与认证初始化失败使用固定提示，避免底层驱动将连接信息写入日志。可选项失败告警不能替代最终必填校验。

## 数据边界与平台迁移

启动配置独立于页面使用的 `config.Config`；不写入 `app_settings`，不加入设置 API、MCP 设置响应或配置导出。数据库中的现有业务设置保持原有管理方式。

新平台实现 `bootstrap.Provider`，返回按键的值、缺失状态及脱敏错误，在 Provider 工厂中注册即可。合并和校验逻辑不需要改变。未来如托管 AES 密钥，保持密钥字节和稳定编号可避免因平台更换而重加密；此版本尚未加入数据库加密模块。

## 验收

自动测试覆盖本地覆盖远程、空值/false/文件冲突、模式选择、必填校验、SDK 实际请求路径、权限错误、有限重试、HTTP 取消、错误脱敏及设置隔离。

真实环境需另行确认：

1. 使用测试项目和测试数据库，显式配置仅提供 Infisical 启动信息，确认数据库及认证启动成功。
2. 显式覆盖一项远程配置，重启确认显式值生效。
3. 修改远程值，确认运行中不自动变化，重启后生效。
4. 撤销测试身份权限，确认依赖远程配置的新进程失败；已有进程仍运行。
5. 直接提供全部适用配置，确认无需联系平台也能启动。
6. 检查日志和设置响应不含测试 Secret。

本地模拟服务验证不等于真实 Infisical、真实 PostgreSQL 或公网部署验收。
