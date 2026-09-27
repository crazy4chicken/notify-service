package notify

import (
	"context"
	"strings"
)

// User 是通知服务视角下的一个用户：id + 各渠道的地址。
// 其它服务只把 user id 发过来，具体地址由 Resolver 解析。
type User struct {
	ID       string            `json:"id"`
	Name     string            `json:"name,omitempty"`
	Channels map[string]string `json:"channels,omitempty"`
}

// Address 返回某渠道的地址；没有配置返回 ok=false。
func (u User) Address(channel Channel) (string, bool) {
	addr, ok := u.Channels[string(channel)]
	addr = strings.TrimSpace(addr)
	if !ok || addr == "" {
		return "", false
	}
	return addr, true
}

// Resolver 把 user id 解析成 User。实现有两种：本地用户表文件、用户服务 HTTP 接口。
// 接口定义在使用方（本包），实现放在 internal/directory，避免循环依赖。
type Resolver interface {
	// Describe 用于启动日志与 /api/v1/channels 展示。
	Describe() string
	// Resolve 解析用户；用户不存在应返回 NotFound 类错误。
	Resolve(ctx context.Context, userID string) (User, error)
}
