// Package notify 定义统一通知模型：一份 Message 描述"要发什么"，
// 一个 Notifier 接口描述"用什么通道发"。通道实现放在 internal/channel，
// 新增通道（短信、钉钉、企业微信……）不需要改动本包。
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Channel 是通道标识。内置 email / sms。
type Channel string

const (
	ChannelEmail Channel = "email"
	ChannelSMS   Channel = "sms"
)

// maxBodyBytes 单条通知正文上限，防止误用（例如把整段视频 base64 塞进来）。
const maxBodyBytes = 1 << 20

// maxTypeRunes 通知类型长度上限。
const maxTypeRunes = 40

// Target 是"发给谁、走哪个渠道"的简写形式，方便其它服务一行调用。
//
// 三种写法都接受：
//
//	"target": "teacher@qq.com"
//	"target": ["a@qq.com", "b@qq.com"]
//	"target": {"channel": "sms", "to": ["13800000000"]}
//
// 与顶层 channel / to 同时存在时会合并（target 里的 channel 只在顶层没写时生效）。
type Target struct {
	Channel Channel  `json:"channel,omitempty"`
	To      []string `json:"to,omitempty"`
}

// UnmarshalJSON 实现上面三种写法的解析。
func (t *Target) UnmarshalJSON(raw []byte) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	switch trimmed[0] {
	case '"':
		var single string
		if err := json.Unmarshal(raw, &single); err != nil {
			return Invalidf("target 字符串解析失败: %v", err)
		}
		t.To = []string{single}
		return nil
	case '[':
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil {
			return Invalidf("target 数组解析失败: %v", err)
		}
		t.To = list
		return nil
	case '{':
		type plain Target // 避免递归调用本方法
		var parsed plain
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&parsed); err != nil {
			return Invalidf("target 对象解析失败: %v", err)
		}
		*t = Target(parsed)
		return nil
	default:
		return Invalidf(`target 只能是字符串、字符串数组，或 {"channel":"...","to":[...]} 对象`)
	}
}

// BodyFormat 说明正文该怎么解释。
type BodyFormat string

const (
	// BodyFormatText 把正文当纯文本（默认，兼容旧调用）。
	BodyFormatText BodyFormat = "text"
	// BodyFormatMarkdown 把正文当 Markdown：邮件渲染成 HTML，短信去掉语法。
	BodyFormatMarkdown BodyFormat = "markdown"
)

// Message 是统一通知请求。
//
// 收件人有两条路径，二选一：
//   - User（推荐）：其它服务只给 user id，由通知服务自己解析地址并决定渠道；
//   - To / Target：直接给地址，用于测试或确有明确地址的场合。
//
// 正文只有一个来源：Body（配合 BodyFormat），或者由 Template + Data 生成。
// 正文统一按 Markdown 的思路处理——email 通道渲染成 HTML（带 text/plain 兜底），
// sms 通道渲染成去掉语法的纯文本；bodyFormat=text 时不做 Markdown 解析，只做转义。
type Message struct {
	User       string            `json:"user,omitempty"`
	Channel    Channel           `json:"channel,omitempty"`
	To         []string          `json:"to,omitempty"`
	Target     *Target           `json:"target,omitempty"`
	Type       string            `json:"type,omitempty"`
	Subject    string            `json:"subject,omitempty"`
	Body       string            `json:"body,omitempty"`
	BodyFormat BodyFormat        `json:"bodyFormat,omitempty"`
	Template   string            `json:"template,omitempty"`
	Data       map[string]any    `json:"data,omitempty"`
	Meta       map[string]string `json:"meta,omitempty"`

	// resolved 由服务内部在"地址已从用户目录解析出来"后置位：
	// 此时 To 是解析结果而不是调用方给的，user 与 to 同时非空属于正常情况。
	resolved bool
}

// Normalize 做无损清理：合并 target、通道小写、去空白、收件人去重（忽略大小写）。
// 这里不填默认通道——通道要么由调用方指定，要么由路由策略决定，见 Service.Send。
func (m *Message) Normalize() {
	m.User = strings.TrimSpace(m.User)
	if m.Target != nil {
		if strings.TrimSpace(string(m.Target.Channel)) != "" && strings.TrimSpace(string(m.Channel)) == "" {
			m.Channel = m.Target.Channel
		}
		m.To = append(m.To, m.Target.To...)
	}
	m.Channel = Channel(strings.ToLower(strings.TrimSpace(string(m.Channel))))
	m.Subject = strings.TrimSpace(m.Subject)
	m.Template = strings.TrimSpace(m.Template)
	m.Type = strings.ToLower(strings.TrimSpace(m.Type))
	m.BodyFormat = BodyFormat(strings.ToLower(strings.TrimSpace(string(m.BodyFormat))))

	seen := make(map[string]struct{}, len(m.To))
	kept := m.To[:0]
	for _, rcpt := range m.To {
		rcpt = strings.TrimSpace(rcpt)
		if rcpt == "" {
			continue
		}
		key := strings.ToLower(rcpt)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		kept = append(kept, rcpt)
	}
	m.To = kept
}

// PreValidate 做不依赖收件地址的校验，用于在解析用户之前挡住明显非法的请求
// （这样错误请求不会打到用户服务）。
func (m Message) PreValidate() error {
	if m.User != "" && len(m.To) > 0 && !m.resolved {
		return Invalidf("user 与 to/target 不能同时使用：给 user 就让通知服务自己解析地址，给地址就不要带 user")
	}
	if err := validateType(m.Type); err != nil {
		return err
	}
	switch m.BodyFormat {
	case "", BodyFormatText, BodyFormatMarkdown:
	default:
		return Invalidf("bodyFormat 只能是 text 或 markdown，收到 %q", m.BodyFormat)
	}
	if strings.TrimSpace(m.Body) == "" && m.Template == "" {
		return Invalidf("正文不能为空：直接给 body（配 bodyFormat），或用 template + data 生成")
	}
	// 渲染器不解释裸 HTML（整体转义），这里让调用方早点发现自己用错了格式。
	if m.BodyFormat == BodyFormatMarkdown && strings.TrimSpace(m.Template) == "" {
		if snippet, found := ContainsRawHTML(m.Body); found {
			return Invalidf("正文里检测到裸 HTML %q：请改用 Markdown 语法，或把这段放进代码块（反引号）里", snippet)
		}
	}
	return nil
}

// Validate 是发送前的完整校验；通道相关的格式校验由各 Notifier 负责。
func (m Message) Validate() error {
	if err := m.PreValidate(); err != nil {
		return err
	}
	if m.User == "" && len(m.To) == 0 {
		return Invalidf("需要收件人：传 user（由通知服务解析），或直接传 to/target 地址")
	}
	for _, rcpt := range m.To {
		if strings.ContainsAny(rcpt, "\r\n") {
			return Invalidf("接收方 %q 含非法字符（回车/换行）", rcpt)
		}
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return Invalidf("subject 不能包含回车/换行")
	}
	if len(m.Body) > maxBodyBytes {
		return Invalidf("正文过长：%d 字节，上限 %d 字节", len(m.Body), maxBodyBytes)
	}
	return nil
}

// validateType 允许中英文类型名，但不允许空白与控制字符，免得把记录写成一行乱码。
func validateType(kind string) error {
	if kind == "" {
		return nil
	}
	if runes := []rune(kind); len(runes) > maxTypeRunes {
		return Invalidf("type 过长：%d 字，上限 %d 字", len(runes), maxTypeRunes)
	}
	for _, r := range kind {
		if r <= ' ' || r == 0x7f {
			return Invalidf("type %q 不能包含空白或控制字符（建议用 absence、head-up-rate 这类写法）", kind)
		}
	}
	return nil
}

// Kind 是错误类别，HTTP 层据此选择状态码。
type Kind string

const (
	KindInvalid  Kind = "invalid_request"   // 400 请求本身有问题
	KindNotFound Kind = "not_found"         // 404 通道/模板/用户/记录不存在
	KindNotReady Kind = "channel_not_ready" // 503 通道未配置好，或路由没有可用通道
	KindDelivery Kind = "delivery_failed"   // 502 上游投递失败（SMTP 拒收等）
	KindUpstream Kind = "upstream_failed"   // 502 依赖的服务出错（用户服务不可用等）
)

// Error 是带类别的通知错误。
type Error struct {
	Kind Kind
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

// Invalidf 构造 400 类错误。
func Invalidf(format string, args ...any) *Error {
	return &Error{Kind: KindInvalid, Msg: fmt.Sprintf(format, args...)}
}

// NotFoundf 构造 404 类错误。
func NotFoundf(format string, args ...any) *Error {
	return &Error{Kind: KindNotFound, Msg: fmt.Sprintf(format, args...)}
}

// NotReadyf 构造 503 类错误。
func NotReadyf(format string, args ...any) *Error {
	return &Error{Kind: KindNotReady, Msg: fmt.Sprintf(format, args...)}
}

// Deliveryf 构造 502 类错误。
func Deliveryf(format string, args ...any) *Error {
	return &Error{Kind: KindDelivery, Msg: fmt.Sprintf(format, args...)}
}

// Upstreamf 构造 502 类错误（依赖服务出错）。
func Upstreamf(format string, args ...any) *Error {
	return &Error{Kind: KindUpstream, Msg: fmt.Sprintf(format, args...)}
}
