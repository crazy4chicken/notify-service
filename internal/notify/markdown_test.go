package notify

import (
	"encoding/json"
	"strings"
	"testing"
)

// Markdown 渲染是纯函数且有安全边界（转义、链接协议），值得固定住行为。
func TestRenderMarkdown(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantHTML []string // 必须全部出现
		notHTML  []string // 必须一个都不出现
		wantText string
	}{
		{
			name:     "标题与粗体",
			src:      "# 缺勤告警\n\n本班 **3 人** 缺勤",
			wantHTML: []string{"<h1>缺勤告警</h1>", "<strong>3 人</strong>", "<p>本班 "},
			wantText: "缺勤告警\n本班 3 人 缺勤",
		},
		{
			name:     "斜体不误伤下划线标识符",
			src:      "*提醒* head_up_rate 不变",
			wantHTML: []string{"<em>提醒</em>", "head_up_rate"},
			notHTML:  []string{"head<em>up</em>rate"},
			wantText: "提醒 head_up_rate 不变",
		},
		{
			name:     "无序与有序列表",
			src:      "- 张三\n- 李四\n\n1. 第一步\n2. 第二步",
			wantHTML: []string{"<ul>", "<li>张三</li>", "</ul>", "<ol>", "<li>第一步</li>", "</ol>"},
			wantText: "- 张三\n- 李四\n- 第一步\n- 第二步",
		},
		{
			name:     "引用与分割线",
			src:      "> 抬头率偏低\n\n---",
			wantHTML: []string{"<blockquote", "抬头率偏低</blockquote>", "<hr>"},
			wantText: "抬头率偏低\n----------",
		},
		{
			name:     "代码块内容不会被解析成标签",
			src:      "```go\nx := \"<b>raw</b>\"\n```",
			wantHTML: []string{"<pre", "<code", "&lt;b&gt;raw&lt;/b&gt;"},
			notHTML:  []string{"<b>raw</b>"},
			wantText: `x := "<b>raw</b>"`,
		},
		{
			name:     "行内代码里的星号保持原样",
			src:      "用 `a*b*c` 表示",
			wantHTML: []string{"<code>a*b*c</code>"},
			notHTML:  []string{"<em>"},
			wantText: "用 a*b*c 表示",
		},
		{
			name:     "注入的标签被转义",
			src:      "<script>alert(1)</script> **粗**",
			wantHTML: []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "<strong>粗</strong>"},
			notHTML:  []string{"<script>"},
			wantText: "<script>alert(1)</script> 粗",
		},
		{
			name:     "危险协议的链接降级成纯文字",
			src:      "[点我](javascript:alert)",
			wantHTML: []string{"点我"},
			notHTML:  []string{"javascript:", "<a "},
			wantText: "点我",
		},
		{
			name:     "正常链接保留",
			src:      "[考勤表](https://example.com/report?a=1&b=2)",
			wantHTML: []string{`<a href="https://example.com/report?a=1&amp;b=2">考勤表</a>`},
			wantText: "考勤表",
		},
		{
			name:     "空内容返回空",
			src:      "   \n\n  ",
			wantHTML: nil,
			wantText: "",
		},
		{
			name:     "CRLF 与段内换行",
			src:      "第一行\r\n第二行\r\n",
			wantHTML: []string{"<p>第一行<br>第二行</p>"},
			wantText: "第一行 第二行",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			htmlOut, textOut := RenderMarkdown(tc.src)
			for _, want := range tc.wantHTML {
				if !strings.Contains(htmlOut, want) {
					t.Errorf("HTML 缺少 %q\n实际: %s", want, htmlOut)
				}
			}
			for _, bad := range tc.notHTML {
				if strings.Contains(htmlOut, bad) {
					t.Errorf("HTML 不应包含 %q\n实际: %s", bad, htmlOut)
				}
			}
			if textOut != tc.wantText {
				t.Errorf("纯文本不符\n期望: %q\n实际: %q", tc.wantText, textOut)
			}
		})
	}
}

// target 允许三种写法，别的服务调用时不用关心我们的内部结构。
func TestTargetUnmarshal(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		channel Channel
		to      []string
		wantErr bool
	}{
		{name: "字符串", raw: `"teacher@qq.com"`, to: []string{"teacher@qq.com"}},
		{name: "数组", raw: `["a@qq.com","b@qq.com"]`, to: []string{"a@qq.com", "b@qq.com"}},
		{name: "对象", raw: `{"channel":"sms","to":["13800000000"]}`, channel: "sms", to: []string{"13800000000"}},
		{name: "null", raw: `null`},
		{name: "对象字段写错", raw: `{"chanel":"sms"}`, wantErr: true},
		{name: "非法类型", raw: `123`, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var msg Message
			err := json.Unmarshal([]byte(`{"target":`+tc.raw+`}`), &msg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际通过：%+v", msg.Target)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if msg.Target == nil {
				if len(tc.to) != 0 || tc.channel != "" {
					t.Fatalf("target 不应为 nil")
				}
				return
			}
			if msg.Target.Channel != tc.channel {
				t.Errorf("channel 期望 %q 实际 %q", tc.channel, msg.Target.Channel)
			}
			if strings.Join(msg.Target.To, ",") != strings.Join(tc.to, ",") {
				t.Errorf("to 期望 %v 实际 %v", tc.to, msg.Target.To)
			}
		})
	}
}

// target 里的渠道只在顶层没写时生效；收件人合并去重。
func TestMessageNormalizeMergesTarget(t *testing.T) {
	msg := Message{
		Target: &Target{Channel: "sms", To: []string{"13800000000"}},
		To:     []string{"13800000000", "13900000000"},
	}
	msg.Normalize()
	if msg.Channel != ChannelSMS {
		t.Errorf("通道应取自 target，实际 %q", msg.Channel)
	}
	if len(msg.To) != 2 {
		t.Errorf("收件人应合并去重为 2 个，实际 %v", msg.To)
	}

	explicit := Message{Channel: ChannelEmail, Target: &Target{Channel: "sms", To: []string{"13800000000"}}}
	explicit.Normalize()
	if explicit.Channel != ChannelEmail {
		t.Errorf("顶层 channel 优先，实际 %q", explicit.Channel)
	}
}

// type 里出现空白/控制字符会被拒，避免记录被写乱。
func TestValidateType(t *testing.T) {
	ok := []string{"", "absence", "head-up-rate", "系统告警"}
	for _, kind := range ok {
		if err := validateType(kind); err != nil {
			t.Errorf("%q 应通过，实际 %v", kind, err)
		}
	}
	bad := []string{"two words", "line\nbreak", strings.Repeat("太", maxTypeRunes+1)}
	for _, kind := range bad {
		if err := validateType(kind); err == nil {
			t.Errorf("%q 应被拒绝", kind)
		}
	}
}
