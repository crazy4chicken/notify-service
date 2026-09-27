# notify-service · 通用通知服务

一个**通用**的多通道通知服务：任何子系统把「发给谁 + 什么类型 + 正文」交给它，
它自己去解析收件地址、选择渠道、渲染内容并把结果记下来。

服务本身不认识任何业务——没有内置模板、没有业务字段、没有领域词汇。

```json
{"user": "u1001", "type": "alert", "bodyFormat": "markdown",
 "subject": "部署完成", "body": "## 部署完成\n\n- 服务 **v1.2.3** 已上线"}
```

**发到哪个渠道、哪个地址，由本服务决定**：先通过用户目录（本地用户表 / 用户服务）把 user id 解析成地址，
再按 `type` 查路由规则挑一个可用渠道，最后交给对应通道投递。

```
调用方（任意子系统）
        │  POST /api/v1/notify   {user, type, body, bodyFormat, subject}
        ▼
   httpapi ──  鉴权 / CORS / 参数校验 / 错误码
        ▼
   notify.Service ── ① 校验正文（body + bodyFormat）
                     ② 用户目录解析 user → 地址      ← 本地用户表 或 用户服务 HTTP
                     ③ 路由规则选渠道（按 type）      ← NOTIFY_ROUTES
                     ④ 渲染（Markdown → HTML / 纯文本）+ 投递 + 落记录
        ▼
   notify.Notifier（统一接口）
   ├── email  →  SMTP 投递（内联样式邮件外壳 + text/plain 兜底）
   └── sms    →  SMSProvider 抽象；未接入上游时明确报"不可用"，不会假装成功
```

- 零第三方依赖（纯 Go 标准库），单文件可执行；
- 加一个渠道只需实现 `notify.Notifier`，加一个短信上游只需实现 `channel.SMSProvider`。

## 快速开始

所有配置都来自**环境变量**；为方便本地使用，程序启动时会读取同目录的 `.env`（不存在就跳过，
真实环境变量优先于 `.env`）。复制 `.env.example` 改一改即可。

```bat
cd /d <项目目录>
copy .env.example .env      :: 填好 NOTIFY_SMTP_* 才会真实投递
go build -o notify-service.exe .
notify-service.exe
```

或者直接双击 `run.bat`（自动找 go、构建、启动）。

| 地址 | 说明 |
| --- | --- |
| http://127.0.0.1:8090/healthz | 健康检查 |
| http://127.0.0.1:8090/api/v1/channels | 通道状态、用户目录、路由规则 |
| http://127.0.0.1:8090/ | 本地测试页（`web/` 不上传仓库，见文末） |

启动参数（也可用同名环境变量）：`-addr`、`-data`、`-web`、`-token`、`-log-level`、
`-brand`（品牌名，用于邮件外壳与默认发件人显示名）。

启动日志会说明当前是怎么配的：

```
INFO 服务已启动 version=0.3.0 brand=notify-service addr=127.0.0.1:8090 dataDir=... 
INFO 路由规则（type → 渠道顺序） routes="alert=email,sms;default=email;digest=email"
INFO 用户目录 resolver="本地用户表 data/users.json"
INFO 通道状态 channel=email provider=smtp mode=live ready=true
INFO 通道状态 channel=sms provider=none mode=unconfigured ready=false
```

## 调用示例

任意子系统调一次即可（Python）：

```python
import requests

requests.post("http://127.0.0.1:8090/api/v1/notify", json={
    "user": "u1001",                     # 用户 id，地址由通知服务解析
    "type": "alert",                     # 通知类型，决定走哪个渠道
    "subject": "部署完成",
    "bodyFormat": "markdown",
    "body": "## 部署完成\n\n- 服务 **v1.2.3** 已上线\n- 回滚命令见 [运维手册](https://example.com/runbook)",
}, timeout=20).raise_for_status()
```

返回（`record` 就是落库的发送记录）：

```json
{
  "ok": true,
  "record": {
    "id": "ntf_3f9c1a7b2d5e4c80",
    "channel": "email", "provider": "smtp", "status": "sent", "type": "alert",
    "bodyFormat": "markdown",
    "userId": "u1001", "userName": "张三",
    "to": ["zhangsan@example.com"], "subject": "部署完成",
    "detail": "已投递至 smtp.example.com:465", "durationMs": 812
  }
}
```

需要临时指定地址（测试、或确有明确地址的场合）时，用 `target` 代替 `user`：

```jsonc
{"target": "someone@example.com", "type": "system", "subject": "标题", "body": "内容"}
{"target": ["a@example.com", "b@example.com"], "body": "纯文本"}
{"target": {"channel": "sms", "to": ["13800000000"]}, "body": "短信内容"}
```

`user` 与 `to/target` **不能同时出现**（会 400）——给了 user 就由通知服务解析，给了地址就别带 user。

## 正文：body + bodyFormat

正文只有一个来源：`body`（`bodyFormat` 说明它是什么），内容完全由调用方提供。

| bodyFormat | 行为 |
| --- | --- |
| `text`（默认） | 不解析 Markdown，只做 HTML 转义（老调用方只传 body 的行为不变） |
| `markdown` | 按 Markdown 渲染，各通道各取所需 |

| 通道 | 渲染结果 |
| --- | --- |
| email | 渲染成 HTML，套一层**内联样式的邮件外壳**（浅灰底 + 白卡片 + 页眉品牌 + 页脚提示），同时产出 `text/plain` 兜底，组成 `multipart/alternative` |
| sms | 渲染成**去掉语法的纯文本**（`#`、`**`、`` ` ``、`>` 等标记全部剥离），长于 500 字报 400 |

Markdown 支持：`#` 标题、`**粗体**`、`*斜体*`、`` `代码` ``、`[文字](https://链接)`、`- 列表`、
`1. 有序列表`、`> 引用`、`---` 分割线、围栏代码块。链接只放行 `http/https/mailto`。

**裸 HTML 被禁用**：`bodyFormat=markdown` 时正文里出现 `<div>` 这类标签会直接 400，
提示改用 Markdown 语法或把这段放进代码块；渲染器本身也是先整体转义再解析，不存在标签注入。

已移除的字段会给出明确 400，而不是被静默忽略：

| 写法 | 结果 |
| --- | --- |
| `body` + `html` | 400：`html` 字段已移除，改用 `body` + `bodyFormat=markdown` |
| `body` + `markdown` | 400：`markdown` 字段已改名为 `body` |
| `bodyFormat` 其它值 | 400：只能是 `text` 或 `markdown` |

## 用户目录（user id → 地址）

两种实现，配哪个用哪个；两个都配时优先用户服务。

### 方式一：本地用户表文件

`NOTIFY_USERS_FILE`（默认 `data/users.json`），改完即生效（按文件修改时间自动重载）：

```json
{
  "users": [
    {"id": "u1001", "name": "张三", "channels": {"email": "zhangsan@example.com", "sms": "13800000000"}},
    {"id": "u2002", "name": "李四", "channels": {"sms": "13900000000"}}
  ]
}
```

裸数组写法也支持。重复 id、缺少 id 会在读取时报错并说明原因。

### 方式二：用户服务 HTTP 接口

```env
NOTIFY_USER_SERVICE_URL=http://127.0.0.1:8080
NOTIFY_USER_SERVICE_PATH=/api/users/{id}      # 默认值，{id} 会被替换
NOTIFY_USER_SERVICE_TOKEN=                    # 可选，会带 Authorization: Bearer
NOTIFY_USER_SERVICE_TIMEOUT=5
```

用户服务需要提供的契约：

| 情况 | 返回 |
| --- | --- |
| 用户存在 | `200` + `{"id":"u1001","name":"张三","channels":{"email":"zhangsan@example.com","sms":"13800000000"}}` |
| 用户不存在 | `404` |
| 其它错误 | 非 2xx → 通知服务按 502 `upstream_failed` 记错，不会误发 |

也兼容把地址平铺在顶层：`{"email":"...","sms":"...","phone":"..."}`（`phone` 视作 `sms`）。
返回的 `id` 与请求的不一致会直接报错，避免接错接口。

## 路由规则：由通知服务决定往哪发

```env
NOTIFY_ROUTES=alert=email,sms;digest=email;default=email
```

选择逻辑（按顺序取**第一个既有地址、通道又可用的渠道**）：

1. `type` 命中规则 → 用它列出的渠道顺序；没命中 → 用 `default`（缺省为 `email`）；
2. 逐个渠道看：用户在用户目录里有没有该渠道地址、该通道是否就绪；
3. 调用方显式传了 `channel` → 只认它，不做路由、失败也不偷偷换渠道；
4. 都不行 → 返回明确原因，而不是静默发到别处：

```json
{"error": {"kind": "channel_not_ready",
 "message": "用户 \"u2002\" 的类型 \"alert\" 路由（email/sms）暂时没有可用通道（用户在 email 上没有地址；sms 通道当前不可用）"}}
```

已在地址里、但通道不可用（如短信没接上游）→ `503 channel_not_ready`；
用户在该路由里没有地址 → `404 not_found`。

## API 一览

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| POST | `/api/v1/notify` | **统一发送入口** |
| GET | `/api/v1/channels` | 通道状态 + 用户目录 + 路由规则 |
| POST | `/api/v1/channels/email/verify` | 用当前环境变量配置发一封自检邮件，`{"to":["me@example.com"]}` |
| GET | `/api/v1/notifications` | 发送记录，支持 `?limit=&channel=&type=&status=&userId=` |
| GET | `/api/v1/notifications/{id}` | 单条记录详情 |
| GET | `/healthz` | 健康检查（无需 Token） |

错误响应统一 `{"error":{"kind":"...","message":"..."}}`：

| kind | HTTP | 含义 |
| --- | --- | --- |
| `invalid_request` | 400 | 参数/收件人格式、user 与地址同时出现、正文含裸 HTML 等 |
| `not_found` | 404 | 通道/记录不存在，或用户在其路由里没有可用地址 |
| `channel_not_ready` | 503 | 通道没配好（如短信上游未接入）、路由没有可用通道、用户目录未配置 |
| `upstream_failed` | 502 | 用户服务出错或返回非法数据 |
| `delivery_failed` | 502 | SMTP 拒收、连接失败等投递失败 |

## 环境变量

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `NOTIFY_BRAND` | `notify-service` | 邮件外壳页眉、默认发件人显示名 |
| `NOTIFY_MAIL_FOOTER` | 自动生成 | 邮件外壳页脚提示语 |
| `NOTIFY_ADDR` | `127.0.0.1:8090` | 监听地址，改成 `0.0.0.0:8090` 供局域网访问 |
| `NOTIFY_DATA_DIR` | `data` | 数据目录：发送记录、本地用户表默认位置、outbox |
| `NOTIFY_TOKEN` | 空 | 设置后 `/api/*` 需要 `Authorization: Bearer <token>` |
| `NOTIFY_LOG_LEVEL` | `info` | `debug` 会打印每个 HTTP 请求 |
| `NOTIFY_SMTP_HOST` | 空 | 发件邮箱 SMTP 服务器 |
| `NOTIFY_SMTP_PORT` | `587` | QQ/163 用 `465` |
| `NOTIFY_SMTP_TLS` | `auto` | `auto` / `starttls`(587) / `implicit`(465) / `none`(仅本地测试) |
| `NOTIFY_SMTP_USER` | 空 | 邮箱账号 |
| `NOTIFY_SMTP_PASS` | 空 | **SMTP 授权码**，不是登录密码 |
| `NOTIFY_SMTP_FROM` | 取 `USER` | 发件地址，多数邮箱要求与账号一致 |
| `NOTIFY_SMTP_FROM_NAME` | 取 `BRAND` | 发件人显示名（按 RFC 2047 编码，中文正常） |
| `NOTIFY_SMTP_TIMEOUT` | `15` | 单次 SMTP 会话超时（秒） |
| `NOTIFY_USERS_FILE` | `<data>/users.json` | 本地用户表 |
| `NOTIFY_USER_SERVICE_URL` | 空 | 用户服务地址，设置后优先于本地用户表 |
| `NOTIFY_USER_SERVICE_PATH` | `/api/users/{id}` | 用户查询路径，必须含 `{id}` |
| `NOTIFY_USER_SERVICE_TOKEN` | 空 | 访问用户服务的 Bearer Token |
| `NOTIFY_USER_SERVICE_TIMEOUT` | `5` | 用户服务超时（秒） |
| `NOTIFY_ROUTES` | `default=email` | 类型 → 渠道顺序，见上文 |
| `NOTIFY_SMS_SIMULATE` | `0` | `1` = 用本地模拟短信上游（只写日志/`data/outbox/sms.log`） |
| `NOTIFY_DEV_OUTBOX` | `0` | `1` = 未配置 SMTP 时把邮件写进 `data/outbox/*.eml` 而不投递 |
| `NOTIFY_ENV_FILE` | `.env` | 指定要读取的 env 文件路径 |

**两个模拟开关默认都是关的**，这是刻意的：通知"以为发了其实没发"比直接报错危险得多。
所以没配 SMTP 又不开 `NOTIFY_DEV_OUTBOX` 时，邮件通道会明确报 `503 channel_not_ready`；短信同理。

## 通道：邮件

| 邮箱 | HOST | PORT | TLS |
| --- | --- | --- | --- |
| QQ 邮箱 | `smtp.qq.com` | `465` | `implicit`（或 `587` + `starttls`） |
| 163 / 126 | `smtp.163.com` / `smtp.126.com` | `465` | `implicit` |
| 腾讯企业邮 | `smtp.exmail.qq.com` | `465` | `implicit` |
| Gmail | `smtp.gmail.com` | `587` | `starttls`（需应用专用密码） |
| 阿里云邮件推送 | `smtpdm.aliyun.com` | `465` | `implicit` |

授权码在邮箱网页版开启 SMTP 服务后生成；`NOTIFY_SMTP_FROM` 一般要与账号一致，否则会被拒信。
明文连接（`TLS=none`）下只要配了账号，服务会拒绝发送，避免把密码明文送出去。

## 通道：短信

两条路，都不需要改 API 和路由：

1. **本地联调**：`NOTIFY_SMS_SIMULATE=1` → 短信只写日志与 `data/outbox/sms.log`，
   记录里 `status: simulated`、`simulated: true`。
2. **接真实上游**：实现 `SMSProvider` 并在 `main.go` 注册：

```go
type SMSProvider interface {
	Name() string                                                          // "aliyun"
	Mode() string                                                          // "live"
	Ready() bool                                                           // 凭证是否就绪
	Send(ctx context.Context, phone, text string) (messageID string, err error)
}
```

签名、模板、错误码、重试都封在 provider 里；通道层只做号码校验、500 字上限和写记录。
现在是一个号码一次请求，量大时建议改用上游的批量接口。

## 发送记录

- 全量追加在 `data/notifications.jsonl`，内存保留最近 5000 条用于查询；重启自动载入。
- **失败的请求同样记录**（`status: failed` + `error`），能直接看到"谁发的、为什么没发出去"；
  路由失败时记录里也带着 `userId`/`userName`，便于排查用户目录问题。
- `status`：`sent`（真实投递）/ `simulated`（模拟，未真实投递）/ `failed`。
- 按类型查：`GET /api/v1/notifications?type=alert&limit=50`。

## 本地测试页（不上传仓库）

`web/index.html` 是手工发通知、看通道状态的本地工具，**`web/` 已写进 `.gitignore`，不会上传**。
它不参与编译，服务每次请求都从磁盘读，所以改完刷新浏览器即生效；页面缺失只影响 `GET /`，
`/api/*` 一切照常；`-web ""` 可以彻底关掉。

## 目录结构

```
main.go                            启动、环境变量装配、.env 读取、优雅退出
internal/notify/                   统一模型与服务（与通道无关）
  message.go                       Message / Target / bodyFormat / 校验
  user.go                          User 模型与 Resolver 接口
  route.go                         类型 → 渠道路由表
  service.go                       校验 → 路由 → 渲染 → 投递 → 落记录
  markdown.go / text.go            Markdown 渲染（禁裸 HTML）、HTML→纯文本
  notifier.go                      Notifier 接口、Delivery、Status、Receipt
internal/directory/                用户目录实现：本地文件、用户服务 HTTP
internal/channel/email.go          邮件通道（SMTP 会话 + 内联样式邮件外壳）
internal/channel/sms.go            SMSProvider 抽象 + 模拟上游
internal/store/store.go            JSONL 记录存储
internal/httpapi/server.go         HTTP 路由与中间件
web/index.html                     本地测试页（已 gitignore）
data/                              运行期数据（已 gitignore）：记录、用户表、outbox
.env                               环境变量（已 gitignore，可含授权码）
.env.example                       配置模板（可提交）
```

## 已知边界

- 发送是**同步**的：调用方会等到上游（SMTP）返回。要削峰再加队列。
- 没有自动重试；失败会如实写进记录，由调用方决定是否重发。
- 多个收件人时邮件是一封多人邮件，短信按号码逐个发。
- 没有内置模板：文案由调用方提供（保持服务与业务解耦）；需要统一文案就在调用方封装。
- 默认不鉴权（本地部署方便）；对外暴露请设 `NOTIFY_TOKEN`。
