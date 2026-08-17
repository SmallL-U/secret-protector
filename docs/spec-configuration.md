# Secret Protector YAML 配置规格

解析使用严格模式：未知字段、多个 YAML document、类型错误或下列语义校验失败都会使配置无效。

## 完整示例

```yaml
version: 1
server:
  listen: 127.0.0.1:8080
  reload_interval: 2s
  read_header_timeout: 10s
  idle_timeout: 60s
  shutdown_timeout: 10s
routes:
  - name: example
    upstream:
      url: https://api.example.com/v1
      auth:
        mode: auto
        token: replace-with-real-upstream-secret
        username: service-user
        password: optional-basic-password
        query_param: api_key
    downstream:
      query_params:
        - token
        - api_key
      tokens:
        - name: local-dev
          value: sp_replace_with_cli_generated_value
```

## 字段

### 顶层

| 字段 | 必填 | 约束 |
| --- | --- | --- |
| `version` | 是 | 当前只能为 `1` |
| `server` | 否 | 缺省字段使用下表默认值 |
| `routes` | 否 | 可为空；路由名称必须唯一，所有下游 token 值必须全局唯一 |

### `server`

| 字段 | 默认值 | 约束 |
| --- | --- | --- |
| `listen` | `127.0.0.1:8080` | 必须是有效的 TCP 地址 |
| `reload_interval` | `2s` | Go duration，必须大于 0 |
| `read_header_timeout` | `10s` | Go duration，必须大于 0 |
| `idle_timeout` | `60s` | Go duration，必须大于 0 |
| `shutdown_timeout` | `10s` | Go duration，必须大于 0 |

### `routes[]`

| 字段 | 必填 | 约束 |
| --- | --- | --- |
| `name` | 是 | 非空，在配置中唯一 |
| `upstream` | 是 | 见下表 |
| `downstream` | 是 | 见下表 |

### `upstream`

| 字段 | 必填 | 约束 |
| --- | --- | --- |
| `url` | 是 | 绝对 `http`/`https` URL，不允许 userinfo 和 fragment |
| `auth.mode` | 否 | `auto`（默认）、`follow`、`bearer`、`query`、`basic` |
| `auth.token` | 视 mode | `auto`、`bearer`、`query` 必填 |
| `auth.username` | 视 mode | `basic` 必填；`auto` 可选 |
| `auth.password` | 视 mode | `basic` 必填；`auto` 可选 |
| `auth.query_param` | 否 | 非空合法 Query key；`query` 默认 `token`，`auto` 缺省时沿用下游参数名 |

### `downstream`

| 字段 | 默认值/约束 |
| --- | --- |
| `query_params` | 默认 `[token]`；每项非空且不得重复 |
| `tokens` | 可为空；每项的 `name` 和 `value` 非空，名称在同一路由内唯一，值在所有路由间全局唯一 |

下游 token 用于选择路由，请求 path 不参与路由选择。空 token 列表会让路由拒绝所有客户端，但仍是合法配置，便于紧急吊销。`/healthz` 始终由代理保留，不能转发给任何路由。

配置不提供路径匹配或路径剥离字段；`path_prefix` 和 `strip_prefix` 会作为未知字段被拒绝。

## 运行时可变性

`routes` 中的所有字段均可热更新。`server` 中任一规范化后的字段发生变化时，本次热更新整体失败并保留旧配置；重启后才应用新 server 配置。

服务启动时，完整配置无效但文档可严格解码且 `server` 字段自身有效，则仍使用该组 `server` 设置进入未就绪状态。配置无法读取、无法严格解码或 `server` 无效时使用本节列出的全部默认值。后续配置必须与进程实际采用的规范化 `server` 设置一致，才能发布首个路由快照。
