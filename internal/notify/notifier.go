package notify

import "context"

// Delivery 是交给通道投递的内容：原始请求 + 已按 bodyFormat 渲染好的正文。
// 渲染在 Service 里统一做，通道只负责按自己的形态使用：
//   - email 用 HTML（再套邮件外壳）+ Text 作为 text/plain 兜底；
//   - sms 只用 Text（已去掉 Markdown 语法）。
type Delivery struct {
	Message Message
	Text    string
	HTML    string // 内容片段；邮件外壳由 email 通道套
}

// Notifier 是所有通知通道必须实现的统一接口。
// 接入新通道只有两步：实现本接口，然后在 Service 上 Register。
type Notifier interface {
	// Name 返回通道名，与 Message.Channel 对应（"email"、"sms"……）。
	Name() Channel
	// Describe 返回通道当前状态，用于 GET /api/v1/channels。
	Describe() Status
	// Send 执行一次投递。实现必须尊重 ctx 的取消与超时。
	Send(ctx context.Context, d Delivery) (Receipt, error)
}

// Status 描述一个通道是否可用、当前处于什么模式。
type Status struct {
	Channel  Channel           `json:"channel"`
	Provider string            `json:"provider"`
	Ready    bool              `json:"ready"`
	Mode     string            `json:"mode"` // live=真实投递, dev=本地模拟, unconfigured=未配置
	Details  map[string]string `json:"details,omitempty"`
}

// Receipt 是一次成功发送的回执。
type Receipt struct {
	Channel   Channel  `json:"channel"`
	Provider  string   `json:"provider"`
	MessageID string   `json:"messageId,omitempty"`
	Accepted  []string `json:"accepted,omitempty"`
	Simulated bool     `json:"simulated,omitempty"`
	Detail    string   `json:"detail,omitempty"`
}
