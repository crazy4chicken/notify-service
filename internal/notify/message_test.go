package notify

import (
	"errors"
	"strings"
	"testing"
)

// bodyFormat 决定正文怎么解释：不写按纯文本（兼容旧调用），模板产出的是 Markdown。
func TestBodyFormatDefaults(t *testing.T) {
	msg := Message{Body: "磁盘使用率 < 60% 无需处理"}
	msg.Normalize()
	if msg.BodyFormat != "" {
		t.Fatalf("Normalize 不应替调用方决定格式，实际 %q", msg.BodyFormat)
	}
	if err := msg.PreValidate(); err != nil {
		t.Fatalf("纯文本正文应通过（正文里的 < 不算裸 HTML）: %v", err)
	}
	if got := TextToHTML(msg.Body); !strings.Contains(got, "&lt; 60%") {
		t.Errorf("纯文本正文进邮件时应转义，实际 %q", got)
	}
}

func TestBodyFormatInvalid(t *testing.T) {
	msg := Message{Body: "x", BodyFormat: "html"}
	msg.Normalize()
	var nerr *Error
	if err := msg.PreValidate(); !errors.As(err, &nerr) || nerr.Kind != KindInvalid {
		t.Fatalf("非法 bodyFormat 应报 400，实际 %v", err)
	}
}

// 裸 HTML 在 Markdown 正文里被拒（渲染器不解释标签）；放进代码块则允许。
func TestMarkdownRawHTMLRejected(t *testing.T) {
	cases := []struct {
		body    string
		wantErr bool
	}{
		{body: "<div>部署完成</div>", wantErr: true},
		{body: "磁盘 **42%**\n\n<img src=\"x.png\">", wantErr: true},
		{body: "看这个例子：`<div>` 会被转义显示", wantErr: false},
		{body: "```html\n<div>示例</div>\n```", wantErr: false},
		{body: "磁盘 < 60% 时提醒", wantErr: false},
		{body: "参考 <https://example.com/report>", wantErr: false},
		{body: "## 正常 Markdown\n\n- 列表项", wantErr: false},
	}
	for _, tc := range cases {
		msg := Message{Body: tc.body, BodyFormat: BodyFormatMarkdown}
		msg.Normalize()
		err := msg.PreValidate()
		if tc.wantErr && err == nil {
			t.Errorf("%q 应被拒绝", tc.body)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%q 应通过，实际 %v", tc.body, err)
		}
	}
}

func TestPreValidateRequiresBody(t *testing.T) {
	msg := Message{}
	msg.Normalize()
	if err := msg.PreValidate(); err == nil {
		t.Fatal("没有正文应报错")
	}

	// 正文由调用方提供：长度上限、CRLF 等规则在 Validate 里继续生效。
	tooLong := Message{Body: strings.Repeat("x", 1<<20+1)}
	tooLong.Normalize()
	if err := tooLong.Validate(); err == nil {
		t.Fatal("超长正文应报错")
	}
}

func TestTextToHTML(t *testing.T) {
	html := TextToHTML("第一段 <b>原样</b>\n第二行\n\n第二段")
	for _, want := range []string{"第一段 &lt;b&gt;原样&lt;/b&gt;<br>第二行", "<p>第二段</p>"} {
		if !strings.Contains(html, want) {
			t.Errorf("缺少 %q，实际 %s", want, html)
		}
	}
	if strings.Contains(html, "<b>") {
		t.Errorf("不应出现未转义标签: %s", html)
	}
	if got := TextToHTML("   \n\n "); got != "" {
		t.Errorf("空内容应返回空，实际 %q", got)
	}
}
