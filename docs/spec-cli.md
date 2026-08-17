# Secret Protector Cobra CLI 规格

可执行文件名为 `secret-protector`，使用 Cobra 实现。所有管理命令通过 `--config`（默认 `config.yml`）操作同一个 YAML 文件。

```text
secret-protector serve
secret-protector manage
secret-protector config init [--listen ADDRESS] [--force]
secret-protector config validate
secret-protector route list
secret-protector route add --name NAME --prefix PATH --upstream-url URL [auth flags]
secret-protector route remove NAME
secret-protector token issue ROUTE --name NAME
secret-protector token list ROUTE
secret-protector token revoke ROUTE TOKEN_NAME
```

## 管理行为

- `config init` 创建 version 1 的空路由配置；目标已存在时默认拒绝覆盖，`--force` 可显式覆盖。
- `config validate` 使用与服务启动相同的严格解析和校验逻辑。
- `route add` 默认 `--auth-mode auto`、`--query-param token`，并自动签发一个下游 token。完整 token 只在本命令输出一次。
- `route list` 不显示任何秘密。
- `route remove` 删除指定路由。
- `token issue` 使用安全随机数生成新 token，并在输出中显示一次。
- `token list` 只显示 token 名称和 SHA-256 短指纹。
- `token revoke` 按名称删除 token。

所有写命令必须先在内存副本中完成修改并校验整个候选配置，再通过同目录临时文件、`fsync` 和原子 rename 替换目标。失败时原文件保持不变。配置文件权限设置为 `0600`。

## 路由鉴权 flags

```text
--auth-mode auto|bearer|query|basic
--upstream-token VALUE
--upstream-username VALUE
--upstream-password VALUE
--query-param NAME
--downstream-query-param NAME   # 可重复
--token-name NAME               # route add 自动签发的下游 token 名称
--strip-prefix
```

缺少当前 mode 必需的上游字段时，候选配置校验失败且文件不变。

## 交互式管理

`secret-protector manage` 使用 `charm.land/huh/v2` 表单提供不依赖 flags 的交互式配置管理入口。交互入口使用 `--config` 指定同一个 YAML 文件，并提供以下主菜单：

```text
1  List routes
2  Add route
3  Remove route
4  Issue downstream token
5  List downstream tokens
6  Revoke downstream token
7  Validate configuration
0  Exit
```

行为要求：

- 配置文件不存在时，先询问是否创建，并提供监听地址默认值 `127.0.0.1:8080`。
- Add route 以向导形式收集路由、上游鉴权、下游 Query 参数和初始 token 名称；根据 auth mode 只要求对应的必填凭证。
- `auto` 向导允许配置可选的上游 Basic 用户名和密码；空密码表示使用 `auth.token`。
- 选择、确认和普通字段使用 `huh` 的 Select、Confirm 和 Input 控件，并支持直接接受默认值。
- 删除路由和吊销 token 前必须二次确认，默认答案为否。
- 在真实 TTY 上输入上游 token 或密码时关闭回显；从 pipe 或测试 reader 输入时按普通行读取。
- 非 TTY 输入自动使用 accessible 行模式；`SECRET_PROTECTOR_ACCESSIBLE` 非空时也强制使用该模式，方便屏幕阅读器且避免终端重绘。
- List route 不显示秘密；List token 只显示名称和短指纹；签发的完整下游 token 仍只显示一次。
- 单个操作输入或校验失败时打印错误并回到主菜单，不终止会话，也不修改原文件。
- 收到 EOF 或选择 Exit 时正常退出。
- 所有交互式写操作必须复用参数式命令相同的全量校验和原子替换路径，不得维护第二套持久化逻辑。
