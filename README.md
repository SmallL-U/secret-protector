# Secret Protector

Secret Protector 是一个用 Go 编写的小型反向代理：客户端只拿到代理签发的短 token，代理校验后再把真正的上游凭证注入请求。它支持 Query token、Bearer token 和 Basic Auth，并可让上游方式固定或自动跟随客户端方式。

项目以 [`docs`](docs/README.md) 为权威规格，当前不使用数据库，所有配置保存在 YAML 中。

## 快速开始

环境要求：Go 1.26 或更高版本。

```bash
make build

# 交互式初始化和管理（推荐）
./bin/secret-protector --config config.yml manage

# 或使用等价的参数式命令
./bin/secret-protector --config config.yml config init

./bin/secret-protector --config config.yml route add \
  --name local-api \
  --upstream-url http://127.0.0.1:9000 \
  --auth-mode bearer \
  --upstream-token 'real-upstream-secret'

./bin/secret-protector --config config.yml serve

# 有效配置已发布时为 200，否则为 503
curl -i http://127.0.0.1:8080/healthz
```

`route add` 会输出一次自动生成的下游 token。假设它保存在 `DOWNSTREAM_TOKEN`：

```bash
# Bearer
curl -H "Authorization: Bearer ${DOWNSTREAM_TOKEN}" http://127.0.0.1:8080/users

# Query（默认参数名 token）
curl "http://127.0.0.1:8080/users?token=${DOWNSTREAM_TOKEN}"

# Basic：下游 token 放在密码位
curl -u "client:${DOWNSTREAM_TOKEN}" http://127.0.0.1:8080/users
```

下游 token 决定请求使用哪条路由，请求 path 会原样转发并拼接到上游 URL 的基础路径；`/healthz` 为代理保留，不会转发。上例固定使用 Bearer 访问上游，因此三种下游请求都会被转换为上游 Bearer。若 `--auth-mode auto`，代理会跟随每次下游请求实际使用的方式。完整配置示例见 [`examples/config.yml`](examples/config.yml)。

## 管理命令

```text
secret-protector serve
secret-protector manage
secret-protector config init|validate
secret-protector route add|list|remove
secret-protector token issue|list|revoke
```

查看某个命令的 flags：

```bash
./bin/secret-protector route add --help
```

CLI 的写操作先校验完整的内存副本，再以 `0600` 权限原子替换 YAML。服务运行时会轮询配置；新文件只有在严格解析、完整校验和路由构建全部成功后才会切换。无效更新会打印 `WARN` 并继续使用最后一个有效快照。启动配置缺失或无效时服务保持运行，`/healthz` 返回 `503`；配置修复并成功发布后自动恢复为 `200`。监听地址无法绑定等进程级错误仍会退出。

`manage` 使用 `huh` v2 表单：TTY 中提供可选择、可返回的终端界面并隐藏敏感输入；pipe 中自动退化为逐行提示。设置 `SECRET_PROTECTOR_ACCESSIBLE=1` 可强制启用适合屏幕阅读器的无重绘模式。

## Docker

镜像默认以非 root 用户执行 `secret-protector --config /config/config.yml serve`。先将配置放入单独目录，并把 `server.listen` 设置为 `0.0.0.0:8080`；上游地址需要能从容器内部访问。

```bash
mkdir -p docker-config
cp examples/config.yml docker-config/config.yml

# 编辑 docker-config/config.yml，替换示例凭证并修改监听地址
make docker-build

docker run --rm \
  --name secret-protector \
  --read-only \
  --user "$(id -u):$(id -g)" \
  --publish 127.0.0.1:8080:8080 \
  --mount type=bind,src="$PWD/docker-config",dst=/config,readonly \
  secret-protector:local
```

挂载整个配置目录可以让宿主机上的原子配置更新继续被容器内的热重载检测到。`--user` 让容器能够读取 CLI 以 `0600` 权限创建的配置文件。

GitHub Actions 会在每次 push 和 pull request 中执行 `make verify`、race test，并构建和启动检查 Docker 镜像；当前不会向镜像仓库推送。

## 开发

```bash
make verify
```

`make verify` 会执行格式化、单元/集成测试、`go vet` 和构建。

## 安全提示

- YAML 包含明文上游凭证和下游 token，请限制文件读取权限并避免提交真实配置。
- token 列表只显示 SHA-256 短指纹；完整 token 仅在签发时输出一次。
- 同一请求不要同时提交 Header 和 Query 凭证，歧义请求会被拒绝。
- 建议只监听可信网络接口，并在跨主机使用时通过受信任的 TLS 入口暴露代理。
