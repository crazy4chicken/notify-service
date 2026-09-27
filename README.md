# notify-service · 统一通知服务

课堂状态监控系统的通知出口。**系统里所有子系统的所有通知都走这一个接口。**

调用方只给三样东西：

```json
{"user": "202410810316", "type": "absence", "bodyFormat": "markdown",
 "subject": "缺勤告警", "body": "## 考勤异常\n\n本节课 **2 人** 缺勤"}
```

**发到哪个渠道、哪个地址，由本服务自己决定**：先通过用户目录（本地用户表 / 用户服务）把 user id 解析成地址，
再按 `type` 查路由规则挑一个可用渠道，最后交给对应通道投递。

```
调用方（监控后端 / 考勤服务 / 教务模块 …）
        │  POST /api/v1/notify   {user, type, 内容}
        ▼
   httpapi ──  鉴权 / CORS / 参数校验 / 错误码
        ▼
   notify.Service ── ① 解析正文（body + bodyFormat / template）
                     ② 用户目录解析 user → 地址      ← 本地用户表 或 用户服务 HTTP
                     ③ 路由规则选渠道（按 type）      ← NOTIFY_ROUTES
                     ④ 投递 + 落发送记录
        ▼
   notify.Notifier（统一接口）
   ├── email  →  真实 SMTP 投递（配置全部来自环境变量）
   └── sms    →  SMSProvider 抽象；未接入上游时明确报"不可用"，不会假装成功
```

- 零第三方依赖（纯 Go 标准库），单文件可执行；
- 邮件是当前唯一会真实投递的通道，短信上游留了可插拔接口。

## 快速开始

所有配置都来自**环境变量**；为方便本地使用，程序启动时会读取同目录的 `.env`（不存在就跳过，
真实环境变量优先于 `.env`）。复制 `.env.example` 改一改即可。

```bat
cd /d D:\notify-service
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

启动参数（也可用同名环境变量）：`-addr`（默认 `127.0.0.1:8090`）、`-data`（默认 `data`）、
`-web`（默认 `web`，留空则关闭测试页）、`-token`（默认空＝不鉴权）、`-log-level`。

启动日志会直接告诉你当前是怎么配的：

```
INFO 路由规则（type → 渠道顺序） routes="absence=email,sms;default=email"
INFO 用户目录 resolver="本地用户表 data/users.json"
INFO 通道状态 channel=email provider=smtp mode=live ready=true
INFO 通道状态 channel=sms provider=none mode=unconfigured ready=false
```

## 调用示例

监控后端在检测到缺勤时调一次即可（Python）：

```python
import requests

requests.post("http://127.0.0.1:8090/api/v1/notify", json={
    "user": "202410810316",              # 用户 id，地址由通知服务解析
    "type": "absence",                   # 通知类型，决定走哪个渠道
    "subject": "【缺勤告警】24大数据三班 大数据采集",
    "bodyFormat": "markdown",
    "body": "## 考勤异常\n\n本节课 **2 人** 缺勤：\n\n- 张三\n- 李四\n\n> 请辅导员核实。",
}, timeout=20).raise_for_status()
```

返回（`record` 就是落库的发送记录）：

```json
{
  "ok": true,
  "record": {
    "id": "ntf_33686a3eb2fdc82b",
    "channel": "email", "provider": "smtp", "status": "sent", "type": "absence",
    "userId": "202410810316", "userName": "薛涣铄",
    "to": ["arrowxue@qq.com"], "subject": "【缺勤告警】…",
    "detail": "已投递至 smtp.qq.com:465", "durationMs": 2961
  }
}
```

需要临时指定地址（测试、或有明确地址的场合）时，用 `target` 代替 `user`：

```jsonc
{"target": "teacher@qq.com", "type": "system", "subject": "标题", "bodyFormat": "markdown", "body": "内容"}
{"target": ["a@qq.com", "b@qq.com"], "body": "纯文本"}
{"target": {"channel": "sms", "to": ["13800000000"]}, "body": "短信内容"}
```

`user` 与 `to/target` **不能同时出现**（会 400）——给了 user 就由通知服务解析，给了地址就别带 user。

## 用户目录（user id → 地址）

两种实现，配哪个用哪个；两个都配时优先用户服务。

### 方式一：本地用户表文件

`NOTIFY_USERS_FILE`（默认 `data/users.json`），改完即生效（按文件修改时间自动重载）：

```json
{
  "users": [
    {"id": "202410810316", "name": "薛涣铄", "channels": {"email": "a@qq.com", "sms": "13800000000"}},
    {"id": "u2002",        "name": "只有手机的学生", "channels": {"sms": "13800000000"}}
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
| 用户存在 | `200` + `{"id":"202410810316","name":"张三","channels":{"email":"a@qq.com","sms":"13800000000"}}` |
| 用户不存在 | `404` |
| 其它错误 | 非 2xx → 通知服务按 502 `upstream_failed` 记错，不会误发 |

也兼容把地址平铺在顶层：`{"email":"...","sms":"...","phone":"..."}`（`phone` 视作 `sms`）。
返回的 `id` 与请求的不一致会直接报错，避免接错接口。

## 路由规则：由通知服务决定往哪发

```env
NOTIFY_ROUTES=absence=email,sms;head-up-rate=email;attendance-summary=email;default=email
```

选择逻辑（按顺序取**第一个既有地址、通道又可用的渠道**）：

1. `type` 命中规则 → 用它列出的渠道顺序；没命中 → 用 `default`（缺省为 `email`）；
2. 逐个渠道看：用户在用户目录里有没有该渠道地址、该通道是否就绪；
3. 调用方显式传了 `channel` → 只认它，不做路由、失败也不偷偷换渠道；
4. 都不行 → 返回明确原因，而不是静默发到别处：

```json
{"error": {"kind": "channel_not_ready",
 "message": "用户 \"u2002\" 的类型 \"absence\" 路由（email/sms）暂时没有可用通道（用户在 email 上没有地址；sms 通道当前不可用）"}}
```

已在地址里、但通道不可用（如短信没接上游）→ `503 channel_not_ready`；
用户在该路由里没有地址 → `404 not_found`。

## API 一览

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| POST | `/api/v1/notify` | **统一发送入口** |
| GET | `/api/v1/channels` | 通道状态 + 用户目录 + 路由规则 |
| GET | `/api/v1/templates` | 内置模板及其所需字段 |
| POST | `/api/v1/channels/email/verify` | 用当前环境变量配置发一封自检邮件，`{"to":["me@qq.com"]}` |
| GET | `/api/v1/notifications` | 发送记录，支持 `?limit=&channel=&type=&status=&userId=` |
| GET | `/api/v1/notifications/{id}` | 单条记录详情 |
| GET | `/healthz` | 健康检查（无需 Token） |

错误响应统一 `{"error":{"kind":"...","message":"..."}}`：

| kind | HTTP | 含义 |
| --- | --- | --- |
| `invalid_request` | 400 | 参数/模板字段/收件人格式、user 与地址同时出现 |
| `not_found` | 404 | 通道/模板/记录不存在，或用户在其路由里没有可用地址 |
| `channel_not_ready` | 503 | 通道没配好（如短信上游未接入）、路由没有可用通道、用户目录未配置 |
| `upstream_failed` | 502 | 用户服务出错或返回非法数据 |
| `delivery_failed` | 502 | SMTP 拒收、连接失败等投递失败 |

## 正文：Markdown 单源 + bodyFormat

正文**只有一个来源**，二选一（同时给会 400）：

```jsonc
// ① 直接给正文，用 bodyFormat 声明它是什么
{"user":"u1","type":"system","subject":"选课结果更新",
 "bodyFormat":"markdown",                                  // text(默认) | markdown
 "body":"## 选课结果\n\n- 大数据采集\n\n> 24 小时内确认。"}

// ② 由内置模板生成（模板内部产出 Markdown，字段缺失会 400 并列出所需字段）
{"user":"u1","type":"absence","template":"absence-alert",
 "data":{"ClassName":"24大数据三班","CourseName":"大数据采集","TimeRange":"2026-09-26 08:00-09:40",
         "StudentNames":"张三、李四","AbsentCount":2,"TotalCount":46}}
```

`bodyFormat` **默认 `text`**，所以老调用方只传 `body`（纯文本）仍然能跑，行为不变。
显式写 `markdown` 时正文按 Markdown 渲染：

| 通道 | 渲染结果 |
| --- | --- |
| email | 渲染成 HTML，套一层**内联样式的邮件外壳**（浅灰底 + 白卡片 + 页眉署名 + 页脚提示），同时产出 `text/plain` 兜底，组成 `multipart/alternative` |
| sms | 渲染成**去掉语法的纯文本**（`#`、`**`、`` ` ``、`>` 等标记全部剥离），长于 500 字报 400 |

**裸 HTML 被禁用**：`bodyFormat=markdown` 时正文里出现 `<div>` 这类标签会直接 400，
提示改用 Markdown 语法或把这段放进代码块（用反引号包起来）；渲染器本身也是先整体转义再解析，
不存在标签注入。`bodyFormat=text` 不解析 Markdown，只做转义，所以 `抬头率 < 60%` 这类正文照常可发。

已移除的字段会给出明确 400，而不是被静默忽略：

| 写法 | 结果 |
| --- | --- |
| `body` + `html` | 400：`html` 字段已移除，改用 `body` + `bodyFormat=markdown` |
| `body` + `markdown` | 400：`markdown` 字段已改名为 `body` |
| `body` + `template` | 400：正文要么直接给 `body`，要么用 `template` + `data` |
| `bodyFormat` 其它值 | 400：只能是 `text` 或 `markdown` |

| 模板名 | 用途 | 必需字段 |
| --- | --- | --- |
| `absence-alert` | 缺勤告警 | ClassName, CourseName, TimeRange, StudentNames, AbsentCount, TotalCount |
| `low-head-up-rate` | 抬头率不达标提醒 | ClassName, CourseName, TimeRange, HeadUpRate, Threshold, Suggestion |
| `attendance-summary` | 考勤与抬头率日报 | ClassName, CourseName, Date, TotalCount, PresentCount, AbsentCount, AttendanceRate, HeadUpRate |

Markdown 支持：`#` 标题、`**粗体**`、`*斜体*`、`` `代码` ``、`[文字](https://链接)`、`- 列表`、
`1. 有序列表`、`> 引用`、`---` 分割线、围栏代码块。链接只放行 `http/https/mailto`，
`javascript:` 之类会降级成纯文字。

## 环境变量

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `NOTIFY_ADDR` | `127.0.0.1:8090` | 监听地址，改成 `0.0.0.0:8090` 供局域网访问 |
| `NOTIFY_DATA_DIR` | `data` | 数据目录：发送记录、本地用户表默认位置、outbox |
| `NOTIFY_TOKEN` | 空 | 设置后 `/api/*` 需要 `Authorization: Bearer <token>` |
| `NOTIFY_LOG_LEVEL` | `info` | `debug` 会打印每个 HTTP 请求 |
| `NOTIFY_SMTP_HOST` | 空 | 发件邮箱 SMTP 服务器，如 `smtp.qq.com` |
| `NOTIFY_SMTP_PORT` | `587` | QQ/163 用 `465` |
| `NOTIFY_SMTP_TLS` | `auto` | `auto` / `starttls`(587) / `implicit`(465) / `none`(仅本地测试) |
| `NOTIFY_SMTP_USER` | 空 | 邮箱账号 |
| `NOTIFY_SMTP_PASS` | 空 | **SMTP 授权码**，不是登录密码 |
| `NOTIFY_SMTP_FROM` | 取 `USER` | 发件地址，QQ 要求与账号一致 |
| `NOTIFY_SMTP_FROM_NAME` | `课堂状态监控系统` | 发件人显示名（按 RFC 2047 编码，中文正常） |
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

**两个模拟开关默认都是关的**，这是刻意的：整套系统的通知都会经过本服务，
"以为发了其实没发"比直接报错危险得多。所以没配 SMTP 又不开 `NOTIFY_DEV_OUTBOX` 时，
邮件通道会明确报 `503 channel_not_ready`；短信没接上游同理。

## 常见邮箱配置

| 邮箱 | HOST | PORT | TLS |
| --- | --- | --- | --- |
| QQ 邮箱 | `smtp.qq.com` | `465` | `implicit`（或 `587` + `starttls`） |
| 163 / 126 | `smtp.163.com` / `smtp.126.com` | `465` | `implicit` |
| 腾讯企业邮 | `smtp.exmail.qq.com` | `465` | `implicit` |
| Gmail | `smtp.gmail.com` | `587` | `starttls`（需应用专用密码） |
| 阿里云邮件推送 | `smtpdm.aliyun.com` | `465` | `implicit` |

授权码在邮箱网页版开启 SMTP 服务后生成；`NOTIFY_SMTP_FROM` 一般要与账号一致，否则会被拒信。
明文连接（`TLS=none`）下只要配了账号，服务会拒绝发送，避免把密码明文送出去。

## 短信通道

现在有两条路，都不需要改 API 和路由：

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

阿里云要准备：AccessKeyId/Secret、已审核的签名 `SignName`、已审核的模板 `TemplateCode`
（模板变量用 JSON 传）。这些细节都封在你的 provider 里，通道层只做号码校验、500 字上限和写记录。
现在是一个号码一次请求，真实上游下建议改用批量接口。

## 发送记录

- 全量追加在 `data/notifications.jsonl`，内存保留最近 5000 条用于查询；重启自动载入。
- **失败的请求同样记录**（`status: failed` + `error`），能直接看到"谁发的、为什么没发出去"；
  路由失败时记录里也带着 `userId`/`userName`，便于排查用户目录问题。
- `status`：`sent`（真实投递）/ `simulated`（模拟，未真实投递）/ `failed`。
- 按类型查：`GET /api/v1/notifications?type=absence&limit=50`。

## 本地测试页（不上传仓库）

`web/index.html` 是手工发通知、看通道状态的本地工具，**`web/` 已写进 `.gitignore`，不会上传**。
它不参与编译，服务每次请求都从磁盘读，所以改完刷新浏览器即生效；页面缺失只影响 `GET /`，
`/api/*` 一切照常；`-web ""` 可以彻底关掉。页面上没有邮箱配置入口——配置只认环境变量。

## 目录结构

```
main.go                            启动、环境变量装配、.env 读取、优雅退出
internal/notify/                   统一模型与服务（与通道无关）
  message.go                       Message / Target / 校验 / 错误类别
  user.go                          User 模型与 Resolver 接口
  route.go                         类型 → 渠道路由表
  service.go                       解析 → 路由 → 投递 → 落记录
  templates.go / markdown.go / text.go
  notifier.go                      Notifier 接口、Status、Receipt
internal/directory/                用户目录实现：本地文件、用户服务 HTTP
internal/channel/email.go          邮件通道（环境变量配置 + SMTP 会话）
internal/channel/sms.go            SMSProvider 抽象 + 模拟上游
internal/store/store.go            JSONL 记录存储
internal/httpapi/server.go         HTTP 路由与中间件
web/index.html                     本地测试页（已 gitignore）
data/                              运行期数据（已 gitignore）：记录、用户表、outbox
.env                               环境变量（已 gitignore，可含授权码）
.env.example                       配置模板（可提交）
```

## 已知边界

- 发送是**同步**的：调用方会等到 SMTP 返回（目前几十毫秒到几秒）。要削峰再加队列。
- 没有自动重试；失败会如实写进记录，由调用方决定是否重发。
- 多个收件人时邮件是一封多人邮件，短信按号码逐个发。
- 默认不鉴权（本地部署方便）；对外暴露请设 `NOTIFY_TOKEN`。
