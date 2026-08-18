# Secret Protector 产品与行为规格

## 1. 目标与术语

Secret Protector 是一个小型 HTTP 反向代理。它让调用方只持有代理签发的下游 token，并在请求转发时将其替换为真正的上游凭证。

- **下游（client）**：调用 Secret Protector 的客户端。
- **代理（proxy）**：Secret Protector 自身。
- **上游（upstream）**：代理最终访问的 HTTP 服务。
- **下游 token**：代理签发并保存在 YAML 中、供客户端访问代理的 token。
- **上游凭证**：保存在 YAML 中、由代理注入上游请求的真实凭证。

本版本不使用数据库。配置和凭证均保存在单个 YAML 文件中。

## 2. 请求处理流程

`/healthz` 是代理保留的健康检查路径，不参与路由选择、下游鉴权或上游转发。其他请求必须依次经过以下步骤：

1. 自动识别下游鉴权方式。
2. 使用常量时间比较校验下游 token，并由 token 选择唯一的路由。
3. 移除下游凭证，避免泄漏给上游。
4. 按路由配置选择鉴权注入策略并写入上游凭证。
5. 将请求按下游提供的 path 转发到上游，并把上游响应原样返回。

凭证缺失、token 错误或 Query 参数不被 token 所属路由接受时返回 `401`。凭证格式错误、方式不支持或同时提供多份凭证返回 `400`。上游网络错误返回 `502`。

错误响应使用 JSON，且不得包含任何 token 或上游凭证。

### 健康检查

- `GET /healthz` 和 `HEAD /healthz` 无需鉴权。
- 已发布至少一个完整、有效的路由快照时返回 `200`；GET 的 JSON 状态为 `ok`，HEAD 不返回 body。
- 启动后尚无有效快照时返回 `503`；GET 的 JSON 状态为 `unavailable`，HEAD 不返回 body；其他代理请求同样返回 `503`。
- 其他 HTTP method 返回 `405` 并设置 `Allow: GET, HEAD`。
- 无效的运行时候选配置若被拒绝且仍有旧快照可用，健康检查保持 `200`。

## 3. 下游鉴权自动识别

每条路由都支持以下四种方式，不需要客户端额外声明：

| 方式 | 下游请求格式 | 用作下游 token 的值 |
| --- | --- | --- |
| Query | `?token=<value>`；参数名由路由配置 | 参数值 |
| Bearer | `Authorization: Bearer <value>` | Bearer 值 |
| Basic | HTTP Basic Auth | 密码；用户名只作为 Basic 用户名元数据 |
| Header | `X-API-Key: <value>`；Header 名称由路由配置 | Header 值 |

规则：

- Query 参数默认只识别 `token`，可为路由配置多个候选参数名。
- 代理汇总所有路由的候选 Query 参数进行凭证识别；token 命中路由后，该参数名还必须属于该路由。
- 自定义 Header 默认不启用，可为路由配置多个候选名称；匹配不区分大小写，`Authorization` 和 `Host` 不能作为自定义凭证 Header。
- 代理汇总所有路由的候选 Header 名称进行凭证识别；token 命中路由后，该 Header 名称还必须属于该路由。
- 同一个请求只能出现一份代理凭证。Authorization、自定义 Header 与 Query 同时存在，多个候选 Header/Query 同时存在，或同一候选项有多个值，均视为歧义并返回 `400`。
- 非 Bearer/Basic 的 `Authorization` 方式返回 `400`，不会透传。
- 空 Bearer、空 Basic 密码、空 Query 值和空自定义 Header 值均为格式错误。
- Basic 客户端示例为 `curl -u any-user:<downstream-token>`。

## 4. 上游鉴权注入策略

上游 `auth.mode` 支持 `auto`、`bearer`、`query`、`header`、`basic`。`follow` 是 `auto` 的兼容别名，读取配置后统一规范为 `auto`。未设置时默认 `auto`。

| mode | 注入行为 |
| --- | --- |
| `bearer` | 写入 `Authorization: Bearer <auth.token>` |
| `query` | 写入 `<auth.query_param>=<auth.token>`；参数名默认 `token` |
| `header` | 写入 `<auth.header_name>: <auth.token>` |
| `basic` | 使用 `auth.username` 与 `auth.password` 写入 HTTP Basic Auth |
| `auto` | 跟随本次下游请求实际使用的 Query/Bearer/Header/Basic 方式 |

`auto` 的细则：

- 跟随 Bearer 时使用 `auth.token`。
- 跟随 Query 时使用 `auth.query_param`；若未设置，则沿用下游实际参数名。值使用 `auth.token`。
- 跟随 Header 时使用 `auth.header_name`；若未设置，则沿用下游实际 Header 名称。值使用 `auth.token`。
- 跟随 Basic 时，用户名优先使用 `auth.username`，否则沿用下游 Basic 用户名；密码优先使用 `auth.password`，否则使用 `auth.token`。

所有策略都必须先删除下游 `Authorization` 或实际承载 token 的 Query/Header，再注入上游凭证。

鉴权注入必须通过独立策略接口实现，使新增方式无需修改路由认证与转发主流程。

## 5. 路径与代理行为

- 仅支持 `http` 和 `https` 上游。
- 上游 URL 不允许包含 userinfo 或 fragment。
- 下游请求 path 不参与路由选择，也不添加或移除任何代理前缀。
- 下游请求 path 及其合法百分号编码语义必须保留，例如路径段中的 `%2F` 不得被改写为新的 `/` 分隔符。
- `/healthz` 由代理独占，任何路由和有效下游 token 都不能使其转发到上游。
- 上游 URL 自带的基础路径和 Query 参数必须保留；例如上游基础路径为 `/v1`、下游请求 path 为 `/users` 时，上游收到 `/v1/users`。
- 转发时 `Host` 使用上游 URL 的 host。
- 响应状态、Header 和 body 由标准库反向代理处理。

## 6. 配置生命周期与不可变更新

### 启动

进程启动时读取配置并尝试严格解析、完整校验及构建全部路由和注入策略：

1. 全部成功时，以配置中的 `server` 设置启动并立即发布路由快照。
2. 完整配置无效，但 YAML 可严格解码且规范化后的 `server` 字段自身有效时，使用这些 `server` 设置启动，不发布路由快照。
3. 配置无法读取、无法严格解码或 `server` 字段无效时，使用 `server` 的全部默认值启动，不发布路由快照。
4. 没有有效快照时必须输出不含秘密的 `WARN`，`/healthz` 与所有代理请求返回 `503`，并继续轮询配置文件。
5. 后续候选配置完整有效且其规范化后的 `server` 设置与进程实际使用的设置相同时，原子发布首个快照并恢复健康。

监听地址无法绑定、HTTP server 异常退出等进程级错误仍必须以非零状态退出，因为此时无法提供健康检查。

### 运行时更新

进程按 `server.reload_interval` 轮询配置文件内容：

1. 对候选文件完整读取、严格解析和校验。
2. 在独立对象中构建完整的新路由快照。
3. 只有所有步骤成功后，才以一次原子指针切换发布新快照。
4. 失败时保留最后一个可用快照；若尚无可用快照则保持未就绪，并输出不含秘密的 `WARN` 日志。
5. 相同的失败内容只警告一次，避免日志刷屏；文件内容再次变化后重新尝试。

监听地址、重载间隔及 HTTP 超时属于进程级配置，运行中修改这些字段必须拒绝该次重载并保留旧快照。重启进程后新值生效。

正在处理的请求可以继续使用其开始时取得的旧快照；新请求使用切换后的新快照。

## 7. 下游 token

- token 使用 `crypto/rand` 生成 18 个随机字节，以无填充 Base64URL 编码并加 `sp_` 前缀。
- CLI 每次签发的 token 必须全局唯一；配置中同一路由不得出现重复 token 名称，token 值在所有路由间不得重复。
- token 在签发命令的标准输出中完整显示一次；列表命令只显示不可逆短指纹。
- YAML 含明文秘密，CLI 新建和重写配置文件时使用 `0600` 权限。

## 8. 可观测性与退出

- 使用结构化日志记录启动、成功重载、失败重载和上游错误。
- 日志不得输出下游 token、上游 token、密码或完整 `Authorization`。
- 启动日志必须标明当前是否已就绪，但不得包含配置错误的原始 YAML 内容。
- 收到 `SIGINT` 或 `SIGTERM` 后，在 `server.shutdown_timeout` 内优雅关闭。

## 9. 验收场景

自动化测试至少覆盖：

1. Query、Bearer、Header、Basic 四种下游识别与 token 校验。
2. 五种上游 mode，包括 `auto` 对四种下游方式的跟随。
3. 下游凭证不会泄漏，上游收到的是配置凭证。
4. 下游 token 选择路由，请求 path 原样转发且保留百分号编码语义。
5. `/healthz` 在有有效快照时返回 `200`，无有效快照时返回 `503`，且不需要鉴权、不能转发到上游。
6. 启动配置无效时进程以可探测的未就绪状态运行，并能在配置修复后恢复。
7. 有效配置热更新原子生效；无效更新和进程级字段更新保留旧快照。
8. 参数式和交互式 CLI 均可完成初始化、校验、路由管理、token 签发和吊销。
