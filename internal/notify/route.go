package notify

import (
	"fmt"
	"sort"
	"strings"
)

// RouteTable 决定"某类型的通知优先走哪些渠道"。这是通知服务自己的策略，
// 调用方只给 type，不需要关心走邮件还是短信。
//
// 配置形如：`alert=email,sms;digest=email;default=email`
// 没有命中类型的规则时用 default；default 缺失时兜底为 email。
type RouteTable struct {
	rules map[string][]Channel
}

// ParseRoutes 解析路由配置文本。空文本等价于 `default=email`。
func ParseRoutes(spec string) (*RouteTable, error) {
	table := &RouteTable{rules: map[string][]Channel{}}
	for _, part := range strings.Split(spec, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, found := strings.Cut(part, "=")
		if !found {
			return nil, Invalidf("路由规则 %q 格式不对，应为 type=渠道,渠道", part)
		}
		kind := strings.ToLower(strings.TrimSpace(key))
		if kind == "" {
			return nil, Invalidf("路由规则 %q 缺少类型名", part)
		}
		channels := make([]Channel, 0, 2)
		for _, raw := range strings.Split(value, ",") {
			name := strings.ToLower(strings.TrimSpace(raw))
			if name == "" {
				continue
			}
			channels = append(channels, Channel(name))
		}
		if len(channels) == 0 {
			return nil, Invalidf("路由规则 %q 没有指定渠道", part)
		}
		table.rules[kind] = channels
	}
	if _, ok := table.rules["default"]; !ok {
		table.rules["default"] = []Channel{ChannelEmail}
	}
	return table, nil
}

// Candidates 返回该类型通知的渠道优先顺序；类型为空或没配规则时用 default。
func (t *RouteTable) Candidates(kind string) []Channel {
	if t == nil {
		return []Channel{ChannelEmail}
	}
	if list, ok := t.rules[strings.ToLower(strings.TrimSpace(kind))]; ok {
		return append([]Channel(nil), list...)
	}
	if list, ok := t.rules["default"]; ok {
		return append([]Channel(nil), list...)
	}
	return []Channel{ChannelEmail}
}

// Rules 返回全部规则，用于启动日志与状态展示。
func (t *RouteTable) Rules() map[string][]Channel {
	out := make(map[string][]Channel, len(t.rules))
	for k, v := range t.rules {
		out[k] = append([]Channel(nil), v...)
	}
	return out
}

// String 输出稳定顺序的可读规则，便于写日志。
func (t *RouteTable) String() string {
	if t == nil {
		return ""
	}
	kinds := make([]string, 0, len(t.rules))
	for kind := range t.rules {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names := make([]string, 0, len(t.rules[kind]))
		for _, ch := range t.rules[kind] {
			names = append(names, string(ch))
		}
		parts = append(parts, kind+"="+strings.Join(names, ","))
	}
	return strings.Join(parts, ";")
}

// routeError 组装"为什么发不出去"的可读说明，让调用方能一眼看出缺哪一环。
func routeError(userID, kind string, candidates []Channel, missing, notReady []Channel) error {
	var reasons []string
	if len(missing) > 0 {
		reasons = append(reasons, "用户在 "+joinChannels(missing)+" 上没有地址")
	}
	if len(notReady) > 0 {
		reasons = append(reasons, joinChannels(notReady)+" 通道当前不可用")
	}
	detail := strings.Join(reasons, "；")
	if detail != "" {
		detail = "（" + detail + "）"
	}
	route := joinChannels(candidates)
	if kind == "" {
		kind = "default"
	}
	if len(missing) > 0 && len(notReady) == 0 {
		return NotFoundf("用户 %q 在类型 %q 的路由（%s）里没有可用的收件地址%s", userID, kind, route, detail)
	}
	return NotReadyf("用户 %q 的类型 %q 路由（%s）暂时没有可用通道%s", userID, kind, route, detail)
}

func joinChannels(channels []Channel) string {
	names := make([]string, 0, len(channels))
	for _, ch := range channels {
		names = append(names, string(ch))
	}
	return strings.Join(names, "/")
}

// describeRoute 用于日志：把最终选择与原因说清楚。
func describeRoute(kind string, candidates []Channel, chosen Channel) string {
	if kind == "" {
		kind = "default"
	}
	return fmt.Sprintf("type=%s route=%s chosen=%s", kind, joinChannels(candidates), chosen)
}
