# 部署

`nsc-msghub` 是单个静态可执行文件，配置全部来自环境变量，没有数据库；唯一的持久状态是数据目录。
本文覆盖发布产物契约、systemd 直接运行、svchost 托管、teamusers 鉴权、升级/回滚与自检。
svchost 侧的字段语义与校验以官方文档为准：[Compose 配置参考](https://github.com/crazy4chicken/nekostick-svchost/blob/main/docs/compose.md)、
[Microservice 发布指南](https://github.com/crazy4chicken/nekostick-svchost/blob/main/docs/publishing.md)。

## 发布产物契约

CI 在 `v*` tag 上构建并发布 linux 静态二进制（见 `.github/workflows/release.yml`）：

- 构建命令固定为 `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$TAG"`，产物无 cgo、无动态库依赖。
- 版本号通过 `-X main.version` 注入完整 tag（含 `v` 前缀），出现在启动日志与 `/healthz` 的 `version` 字段；不用 ldflags 直接构建时是源码里的默认值（`0.3.0`）。
- svchost release asset 命名为 `msghub_<version>_<arch>.zip`：`<version>` 是去掉 `v` 前缀的 tag（`v0.3.0` → `msghub_0.3.0_x64.zip`），`<arch>` 取 `x64` 或 `arm64`。
- ZIP 根目录下只有一个入口文件，文件名恰好是 `msghub`（即 serviceId）；sha256 针对 ZIP 文件本身，不是解压后的二进制。

在 [Releases](https://github.com/crazy4chicken/nsc-msghub/releases) 页面下载对应架构的 ZIP；release notes 里会链接本文。

## 直接运行

命令行参数 `-brand`、`-addr`、`-data`、`-token`、`-log-level`、`-web` 可覆盖对应环境变量。
工作目录下的 `.env` 也会被读取（真实环境变量优先，路径可用 `NOTIFY_ENV_FILE` 改写）；
systemd / svchost 下请用各自的环境变量注入方式，不要依赖 `.env`。

### systemd

安装二进制与数据目录（属主为运行用户）：

```sh
install -m 0755 msghub /usr/local/bin/msghub
install -d -o nsc-msghub -g nsc-msghub /var/lib/nsc-msghub
```

写 `/etc/nsc-msghub.env`（权限 0600，内含 SMTP 授权码与 teamusers 服务 Token；值含空格或 `#` 时用双引号）：

```sh
NOTIFY_ADDR=0.0.0.0:8090
NOTIFY_DATA_DIR=/var/lib/nsc-msghub
NOTIFY_LOG_LEVEL=info

# 通知类型 → 渠道优先级
NOTIFY_ROUTES=alert=email,sms;digest=email;default=email
NOTIFY_USERS_FILE=/var/lib/nsc-msghub/users.json

# 发件邮箱
NOTIFY_SMTP_HOST=smtp.qq.com
NOTIFY_SMTP_PORT=465
NOTIFY_SMTP_TLS=implicit
NOTIFY_SMTP_USER=you@qq.com
NOTIFY_SMTP_PASS=授权码

# teamusers 鉴权（可选；配置后静态 NOTIFY_TOKEN 退场）
# NOTIFY_TEAMUSERS_URL=http://127.0.0.1:8080
# NOTIFY_TEAMUSERS_SERVICE_TOKEN=
```

`NOTIFY_ADDR=0.0.0.0:8090` 才对其他主机开放；默认 `127.0.0.1:8090` 只监听本机。

unit `/etc/systemd/system/nsc-msghub.service`：

```ini
[Unit]
Description=nsc-msghub 通知网关
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=nsc-msghub
Group=nsc-msghub
WorkingDirectory=/var/lib/nsc-msghub
EnvironmentFile=/etc/nsc-msghub.env
ExecStart=/usr/local/bin/msghub
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/nsc-msghub

[Install]
WantedBy=multi-user.target
```

```sh
systemctl daemon-reload && systemctl enable --now nsc-msghub
```

`NOTIFY_DATA_DIR` 必须是持久且运行用户可写的绝对路径，里面放 `notifications.jsonl`（投递记录）、
`users.json`（本地用户表）与 `outbox/`（仅开 `NOTIFY_DEV_OUTBOX` / `NOTIFY_SMS_SIMULATE` 时写入）。
测试页面由 `NOTIFY_WEB_DIR` 指定（目录需含 `index.html`），生产环境一般留空关闭。

## svchost 部署

把下面内容存为 `svchost.compose.yaml`。service key 必须是 `msghub`：它同时是 asset 名前缀，
启动后还要在解压根目录找到同名入口 `msghub`。

```yaml
strictSources: false # 默认值；多架构 ZIP 摘要不同时不要填单一 sha256
serviceScope: global
services:
  msghub:
    source:
      release: "github:crazy4chicken/nsc-msghub@v0.3.0" # SemVer ref：release tag 与 asset version 按 SemVer identity 匹配
      # sha256: "…64 位十六进制…" # 单架构部署可填所选 ZIP 的真实摘要；多架构共享本配置时保持注释，交给 GitHub asset digest 校验
    env:
      NOTIFY_ADDR: "0.0.0.0:8090" # 需要 Host 分配端口时用 "${HOST}:${PORT}"
      NOTIFY_DATA_DIR: /var/lib/nsc-msghub # 必须持久；不要指向 artifacts/ 或 tmp/
      NOTIFY_ROUTES: "alert=email,sms;digest=email;default=email"
      NOTIFY_USERS_FILE: /var/lib/nsc-msghub/users.json
      NOTIFY_SMTP_HOST: smtp.qq.com
      NOTIFY_SMTP_PORT: "465"
      NOTIFY_SMTP_TLS: implicit
      NOTIFY_SMTP_USER: you@qq.com
      NOTIFY_SMTP_PASS: "${HOST:MSGHUB_SMTP_PASS}" # 授权码走 Host 环境，不写进配置文件
      NOTIFY_TEAMUSERS_URL: "http://127.0.0.1:8080"
      NOTIFY_TEAMUSERS_SERVICE_TOKEN: "${HOST:MSGHUB_TEAMUSERS_SERVICE_TOKEN}" # 缺失时服务拒绝启动
      NOTIFY_TEAMUSERS_AUDIENCE: teamusers
      NOTIFY_TEAMUSERS_PERMISSION_SEND: "msghub:send:any"
      NOTIFY_TEAMUSERS_PERMISSION_READ: "msghub:read:any"
    start: eager
    restart: on-failure
    health:
      type: http
      path: /healthz
      timeout: 5s
```

说明：

- `env` value 里的 `${...}` 是 svchost 的 launch template，不是 shell：`${HOST:X}` 透传 Host 环境变量 `X`，
  `${HOST}`/`${PORT}` 取本次启动的动态值；svchost 只改写 `env` 的 value 与 `args` 字符串，不要按 shell 语义写别的表达式。
- `NOTIFY_DATA_DIR` 与 `NOTIFY_WEB_DIR` 要写绝对路径：svchost 进程 CWD 是 service root（`global` 时为
  `<data>/svchost/global`），升级只替换 `artifacts/` 下的 bundle 与 `tmp/`，数据目录自管且必须可写。
- `health` 用 `http /healthz`，该接口不需要 Token；默认的 `type: process` 发现不了“进程在、监听没起来”的情况。
- 服务名 `msghub` 在 `serviceScope: global` 的全局命名空间里去重，其他配置已声明同名服务会让整份配置失败；
  同机多实例请用 `serviceScope: document`，服务名仍保持 `msghub`。
- 需要经 Host 暴露时再按 compose.md 加 `route` 段（`prefix` 必须以 `/` 开头，`strip: true` 会在转发前去掉前缀）。

## teamusers 鉴权

- 配置 `NOTIFY_TEAMUSERS_URL` 后 `/api/*` 只认 teamusers 签发的 JWT（仍放 `Authorization: Bearer <JWT>`）：
  缺失或校验失败返回 401 `unauthorized`，权限不足返回 403 `forbidden`；静态 `NOTIFY_TOKEN` 同时被忽略（启动日志会警告）。
- `NOTIFY_TEAMUSERS_SERVICE_TOKEN` 是 msghub 查询用户权限用的服务 Token，配了 URL 却缺这个值服务拒绝启动。
- `NOTIFY_TEAMUSERS_AUDIENCE`（默认 `teamusers`）必须与签发方写入 JWT 的 `aud` 一致，否则所有请求 401。
- 权限名默认 `msghub:send:any`（发送、邮件自检）与 `msghub:read:any`（通道状态、投递记录查询），
  需要在 teamusers 侧授予调用方；签发方若用别的名字，用 `NOTIFY_TEAMUSERS_PERMISSION_SEND` / `_READ` 对齐。
- msghub 要能访问 `NOTIFY_TEAMUSERS_URL`（JWKS 与权限接口，超时 `NOTIFY_TEAMUSERS_TIMEOUT`，默认 5 秒）。
- `/healthz` 与测试页面始终公开，不走这套鉴权。

## 升级与回滚

- svchost：改 `source.release` 的 ref（例如 `@v0.3.1`）并触发 reconcile；svchost 重新下载并校验对应架构的 ZIP，
  替换 bundle 后按 `restart` 策略重启进程。回滚就是把 ref 改回旧 tag 再 reconcile。ref 用 `vX.Y.Z` 最省事；
  想用 commit 前缀 pin 版本时，需 release 的 `target_commitish` 与 asset version 都以该前缀开头，否则会直接拒绝（见发布指南的 ref 规则）。
- systemd：停服务，替换 `/usr/local/bin/msghub`，再启动；环境文件与数据目录不动。
- 数据兼容：`notifications.jsonl` 是追加写的 JSONL，启动时加载最近 5000 条到内存供查询，升级/回滚不迁移也不清空；
  `users.json` 变更后自动重载；`outbox/` 只是模拟投递的落盘目录。
- 回滚后核对 `/healthz` 的 `version` 与目标版本一致，并确认没有残留旧进程（端口占用会直接启动失败）。

## 健康检查与冒烟

```sh
# 存活 + 版本注入是否生效（不需要 Token）
curl -fsS http://127.0.0.1:8090/healthz
# → {"status":"ok","version":"v0.3.1",...}

# 通道就绪、用户目录与生效路由（需要读权限的凭证：静态 Token 或带 msghub:read:any 的 JWT）
curl -fsS http://127.0.0.1:8090/api/v1/channels -H "Authorization: Bearer $TOKEN"

# 邮件通道自检（需要发送权限）
curl -fsS -X POST http://127.0.0.1:8090/api/v1/channels/email/verify \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"to":["you@qq.com"]}'
```

`/api/v1/channels` 的 `channels` 给出每个通道的模式与可用性，`directory` 说明用户目录来自本地表还是用户服务，
`routes` 是实际生效的 type → 渠道顺序——部署后先看这里，再发真实通知。
未配置 SMTP 且没开 `NOTIFY_DEV_OUTBOX` / `NOTIFY_SMS_SIMULATE` 时通道会明确报“不可用”，不会假装发送成功；
模拟开关只写 `outbox/`，记录状态是 `simulated`，与真实投递的 `sent` 可区分。
