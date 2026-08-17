# Secret Protector 产品与行为规格

## 1. 目标与术语

Secret Protector 是一个小型 HTTP 反向代理。它让调用方只持有代理签发的短 token，并在请求转发时将其替换为真正的上游凭证。

- **下游（client）**：调用 Secret Protector 的客户端。
- **代理（proxy）**：Secret Protector 自身。
- **上游（upstream）**：代理最终访问的 HTTP 服务。
- **下游 token**：代理签发并保存在 YAML 中、供客户端访问代理的 token。
- **上游凭证**：保存在 YAML 中、由代理注入上游请求的真实凭证。

本版本不使用数据库。配置和凭证均保存在单个 YAML 文件中。

## 2. 请求处理流程

每个请求必须依次经过以下步骤：

1. 以最长路径前缀匹配路由。前缀按路径段匹配：`/api` 匹配 `/api` 和 `/api/users`，不匹配 `/apix`。
2. 自动识别下游鉴权方式。
3. 使用常量时间比较校验下游 token。
4. 移除下游凭证，避免泄漏给上游。
5. 按路由配置选择鉴权注入策略并写入上游凭证。
6. 将请求转发到上游，并把上游响应原样返回。

未匹配路由返回 `404`。凭证缺失或 token 错误返回 `401`。凭证格式错误、方式不支持或同时提供多份凭证返回 `400`。上游网络错误返回 `502`。

错误响应使用 JSON，且不得包含任何 token 或上游凭证。

## 3. 下游鉴权自动识别

每条路由都支持以下三种方式，不需要客户端额外声明：

| 方式 | 下游请求格式 | 用作下游 token 的值 |
| --- | --- | --- |
| Query | `?token=<value>`；参数名由路由配置 | 参数值 |
| Bearer | `Authorization: Bearer <value>` | Bearer 值 |
| Basic | HTTP Basic Auth | 密码；用户名只作为 Basic 用户名元数据 |

规则：

- Query 参数默认只识别 `token`，可为路由配置多个候选参数名。
- 同一个请求只能出现一份代理凭证。Header 与 Query 同时存在、多个候选 Query 参数同时存在、或同一 Query 参数有多个值，均视为歧义并返回 `400`。
- 非 Bearer/Basic 的 `Authorization` 方式返回 `400`，不会透传。
- 空 Bearer、空 Basic 密码和空 Query 值均为格式错误。
- Basic 客户端示例为 `curl -u any-user:<downstream-token>`。

## 4. 上游鉴权注入策略

上游 `auth.mode` 支持 `auto`、`bearer`、`query`、`basic`。`follow` 是 `auto` 的兼容别名，读取配置后统一规范为 `auto`。未设置时默认 `auto`。

| mode | 注入行为 |
| --- | --- |
| `bearer` | 写入 `Authorization: Bearer <auth.token>` |
| `query` | 写入 `<auth.query_param>=<auth.token>`；参数名默认 `token` |
| `basic` | 使用 `auth.username` 与 `auth.password` 写入 HTTP Basic Auth |
| `auto` | 跟随本次下游请求实际使用的 Query/Bearer/Basic 方式 |

`auto` 的细则：

- 跟随 Bearer 时使用 `auth.token`。
- 跟随 Query 时使用 `auth.query_param`；若未设置，则沿用下游实际参数名。值使用 `auth.token`。
- 跟随 Basic 时，用户名优先使用 `auth.username`，否则沿用下游 Basic 用户名；密码优先使用 `auth.password`，否则使用 `auth.token`。

所有策略都必须先删除下游 `Authorization` 或实际承载 token 的 Query 参数，再注入上游凭证。

鉴权注入必须通过独立策略接口实现，使新增方式无需修改路由认证与转发主流程。

## 5. 路径与代理行为

- 仅支持 `http` 和 `https` 上游。
- 上游 URL 不允许包含 userinfo 或 fragment。
- `strip_prefix: true` 时，匹配到的路由前缀会在转发前移除；空路径按 `/` 转发。
- `strip_prefix: false` 时保留原请求路径。
- 移除前缀时必须保留合法的百分号编码语义，例如路径段中的 `%2F` 不得被改写为新的 `/` 分隔符。
- 上游 URL 自带的基础路径和 Query 参数必须保留。
- 转发时 `Host` 使用上游 URL 的 host。
- 响应状态、Header 和 body 由标准库反向代理处理。

## 6. 配置生命周期与不可变更新

### 启动

进程启动时必须完整读取、严格解析并校验 YAML，然后构建全部路由和注入策略。任一步失败都必须拒绝监听端口并以非零状态退出。

### 运行时更新

进程按 `server.reload_interval` 轮询配置文件内容：

1. 对候选文件完整读取、严格解析和校验。
2. 在独立对象中构建完整的新路由快照。
3. 只有所有步骤成功后，才以一次原子指针切换发布新快照。
4. 失败时保留最后一个可用快照，并输出不含秘密的 `WARN` 日志。
5. 相同的失败内容只警告一次，避免日志刷屏；文件内容再次变化后重新尝试。

监听地址、重载间隔及 HTTP 超时属于进程级配置，运行中修改这些字段必须拒绝该次重载并保留旧快照。重启进程后新值生效。

正在处理的请求可以继续使用其开始时取得的旧快照；新请求使用切换后的新快照。

## 7. 下游 token

- token 使用 `crypto/rand` 生成 18 个随机字节，以无填充 Base64URL 编码并加 `sp_` 前缀。
- CLI 每次签发的 token 必须唯一；配置中同一路由不得出现重复 token 名称或值。
- token 在签发命令的标准输出中完整显示一次；列表命令只显示不可逆短指纹。
- YAML 含明文秘密，CLI 新建和重写配置文件时使用 `0600` 权限。

## 8. 可观测性与退出

- 使用结构化日志记录启动、成功重载、失败重载和上游错误。
- 日志不得输出下游 token、上游 token、密码或完整 `Authorization`。
- 收到 `SIGINT` 或 `SIGTERM` 后，在 `server.shutdown_timeout` 内优雅关闭。

## 9. 验收场景

自动化测试至少覆盖：

1. Query、Bearer、Basic 三种下游识别与 token 校验。
2. 四种上游 mode，包括 `auto` 对三种下游方式的跟随。
3. 下游凭证不会泄漏，上游收到的是配置凭证。
4. 最长前缀路由与 `strip_prefix`。
5. 启动配置错误被拒绝。
6. 有效配置热更新原子生效；无效更新和进程级字段更新保留旧快照。
7. 参数式和交互式 CLI 均可完成初始化、校验、路由管理、token 签发和吊销。
