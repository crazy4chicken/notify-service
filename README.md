# notify-service

`notify-service` is a standalone notification gateway in Go for service fleets
that need one uniform way to reach users. Callers submit a notification intent
— user id, type, body — and the service owns the rest: resolving the recipient
through a user directory, choosing a channel by type-based routing, rendering
the body for that channel, delivering it, and recording the attempt.

It carries no business vocabulary and no built-in templates: content is supplied
by the caller, so the same endpoint serves any subsystem.

## Features

- One endpoint, `POST /api/v1/notify`. Callers never touch SMTP, phone numbers,
  or channel quirks.
- Recipient resolution by user id, from either a local JSON user table or a
  user-service HTTP contract (`{id}` path template, optional bearer token,
  explicit 404/502 semantics). No directory configured means a clear 503.
- Type-based routing with ordered channel preferences
  (`alert=email,sms;digest=email;default=email`); an explicit `channel` skips
  routing. Unroutable notifications fail with the reason instead of silently
  going elsewhere.
- Two channels. Email delivers over SMTP (implicit TLS, STARTTLS, or plain) with
  PLAIN/LOGIN auth and per-session timeouts. SMS is a provider interface so an
  upstream can be dropped in; it reports "not ready" rather than pretending to
  send.
- Markdown-first bodies. `bodyFormat` is `text` (default, HTML-escaped only) or
  `markdown`: email gets an inline-styled HTML shell plus a `text/plain`
  alternative, SMS gets syntax-stripped plain text. Raw HTML is rejected, and
  content is escaped before parsing, so caller-supplied text cannot inject tags.
- Append-only delivery records in JSONL, queryable by channel, type, status, and
  user id. Failed attempts are recorded with the reason.
- Configuration exclusively through environment variables (a `.env` file is
  read when present; real environment variables win). No config API, no
  database.
- Optional static bearer token for `/api/*`, permissive CORS for browser
  clients, panic-recovering middleware, and graceful shutdown.
- Zero third-party dependencies — Go standard library only — building to a
  single self-contained binary.

## Quickstart: local development

Copy the environment template and fill in the sender mailbox; without it the
email channel stays unavailable.

```sh
cp .env.example .env      # NOTIFY_SMTP_HOST / _USER / _PASS (an app password)
go build -o notify-service .
./notify-service
```

Check the process and the resolved configuration:

```sh
curl -fsS http://127.0.0.1:8090/healthz
curl -fsS http://127.0.0.1:8090/api/v1/channels
```

`/api/v1/channels` reports each channel's mode and readiness plus the active
user directory and routing rules. A sample notification:

```sh
curl -fsS -X POST http://127.0.0.1:8090/api/v1/notify \
  -H 'Content-Type: application/json' \
  -d '{
    "user": "u1001",
    "type": "alert",
    "bodyFormat": "markdown",
    "subject": "Deploy finished",
    "body": "## Deploy finished\n\n- **v1.2.3** is live\n\n> Rollback command is in the runbook"
  }'
```

Both simulation paths are **off by default**, because a notification that looks
sent but never leaves the host is worse than a hard failure: enable
`NOTIFY_DEV_OUTBOX=1` to write email to `data/outbox/*.eml` instead of
delivering, and `NOTIFY_SMS_SIMULATE=1` to log SMS to `data/outbox/sms.log`.
Neither switch changes the API.

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `NOTIFY_BRAND` | `notify-service` | Mail shell header and default sender display name |
| `NOTIFY_MAIL_FOOTER` | generated | Mail shell footer line |
| `NOTIFY_ADDR` | `127.0.0.1:8090` | Listen address; use `0.0.0.0:8090` to expose it |
| `NOTIFY_DATA_DIR` | `data` | Delivery records, default user table, outbox |
| `NOTIFY_TOKEN` | empty | When set, `/api/*` requires `Authorization: Bearer <token>` |
| `NOTIFY_LOG_LEVEL` | `info` | `debug` logs every HTTP request |
| `NOTIFY_SMTP_HOST` | empty | Sender mailbox SMTP host |
| `NOTIFY_SMTP_PORT` | `587` | Use `465` for QQ/163-style providers |
| `NOTIFY_SMTP_TLS` | `auto` | `auto`, `starttls` (587), `implicit` (465), or `none` (local only) |
| `NOTIFY_SMTP_USER` / `NOTIFY_SMTP_PASS` | empty | Mailbox account and its SMTP app password |
| `NOTIFY_SMTP_FROM` | `NOTIFY_SMTP_USER` | Envelope sender; most providers require it to match the account |
| `NOTIFY_SMTP_FROM_NAME` | `NOTIFY_BRAND` | Display name, encoded per RFC 2047 |
| `NOTIFY_SMTP_TIMEOUT` | `15` | Per-session timeout in seconds |
| `NOTIFY_USERS_FILE` | `<data>/users.json` | Local user table |
| `NOTIFY_USER_SERVICE_URL` | empty | User service base URL; takes precedence over the local table |
| `NOTIFY_USER_SERVICE_PATH` | `/api/users/{id}` | User lookup path; must contain `{id}` |
| `NOTIFY_USER_SERVICE_TOKEN` | empty | Bearer token for the user service |
| `NOTIFY_USER_SERVICE_TIMEOUT` | `5` | User service timeout in seconds |
| `NOTIFY_ROUTES` | `default=email` | Type-to-channel preferences |
| `NOTIFY_SMS_SIMULATE` | `0` | `1` uses the local simulated SMS upstream |
| `NOTIFY_DEV_OUTBOX` | `0` | `1` writes email to the outbox instead of delivering |

Command-line flags mirror the common settings: `-brand`, `-addr`, `-data`,
`-token`, and `-log-level`.

## API

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/api/v1/notify` | Send a notification |
| `GET` | `/api/v1/channels` | Channel status, user directory, routing rules |
| `POST` | `/api/v1/channels/email/verify` | Send a self-test mail to `{"to":["me@example.com"]}` |
| `GET` | `/api/v1/notifications` | Delivery records; `?limit=&channel=&type=&status=&userId=` |
| `GET` | `/api/v1/notifications/{id}` | Single delivery record |
| `GET` | `/healthz` | Liveness check; no token required |

The send endpoint accepts exactly one recipient form: `user` (resolved by the
service) or `to` / `target` (an explicit address). Supplying both is a 400.
`target` additionally accepts the shorthand forms `"a@example.com"` and
`["a@example.com","b@example.com"]`, or `{"channel":"sms","to":["138…"]}`.

Responses report the delivery attempt:

```json
{
  "ok": true,
  "record": {
    "id": "ntf_3f9c1a7b2d5e4c80",
    "channel": "email", "provider": "smtp", "status": "sent",
    "type": "alert", "bodyFormat": "markdown",
    "userId": "u1001", "userName": "Zhang San",
    "to": ["zhangsan@example.com"], "subject": "Deploy finished",
    "detail": "delivered via smtp.example.com:465", "durationMs": 812
  }
}
```

Errors are uniform — `{"error":{"kind":"…","message":"…"}}`:

| Kind | HTTP | Meaning |
| --- | --- | --- |
| `invalid_request` | 400 | Malformed parameters, both recipient forms, raw HTML in a Markdown body |
| `not_found` | 404 | Unknown channel or record, or the user has no address on the route |
| `channel_not_ready` | 503 | Channel not configured, no usable channel on the route, no user directory |
| `upstream_failed` | 502 | User service failed or returned an unusable payload |
| `delivery_failed` | 502 | SMTP rejected or unreachable |

### Body format

| `bodyFormat` | Behaviour |
| --- | --- |
| `text` (default) | No Markdown parsing; HTML-escaped, preserving legacy callers |
| `markdown` | Parsed and rendered per channel |

Markdown support is the notification-sized subset: headings, bold, italics,
inline code, links, ordered and unordered lists, blockquotes, thematic breaks,
and fenced code blocks. Only `http`, `https`, and `mailto` links are allowed.
A raw HTML tag in a Markdown body is rejected with 400 — put it in a code span
if it must appear literally. The removed `html` and `markdown` fields return an
explicit 400 telling the caller what to use instead.

## User directory

Two interchangeable implementations; the user service wins when both are set.

The local table (reloaded when the file changes) is a JSON object or array:

```json
{
  "users": [
    {"id": "u1001", "name": "Zhang San", "channels": {"email": "zhangsan@example.com", "sms": "13800000000"}}
  ]
}
```

The user service must answer `GET {URL}{PATH}` with `200` and a user object:

```json
{"id": "u1001", "name": "Zhang San", "channels": {"email": "zhangsan@example.com", "sms": "13800000000"}}
```

Addresses may also be flattened to `email` / `sms` / `phone` at the top level.
`404` means "no such user"; any other non-2xx, malformed payload, or mismatched
`id` is treated as an upstream failure so a wrong integration cannot misdeliver.

## Routing

`NOTIFY_ROUTES` maps a notification type to an ordered preference list. For each
candidate the service checks that the user has an address for that channel *and*
that the channel is ready, then uses the first match. Types without a rule fall
back to `default`. An explicit `channel` on the request bypasses routing
entirely — and fails rather than switching channels silently.

## Delivery records

Every attempt is appended to `data/notifications.jsonl` and the most recent 5000
are kept in memory for queries. Records carry `status` `sent` (actually
delivered), `simulated` (written locally, never delivered), or `failed`, plus
the `userId`, `userName`, resolved channel, and the error for failures — enough
to answer "what was sent, to whom, and why it did not go out".
