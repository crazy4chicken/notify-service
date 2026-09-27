package notify

import (
	"errors"
	"strings"
	"testing"
)

// bodyFormat 决定正文怎么解释：不写按纯文本（兼容旧调用），模板产出的是 Markdown。
func TestBodyFormatDefaults(t *testing.T) {
	msg := Message{Body: "抬头率 < 60% 需要提醒"}
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
		{body: "<div>考勤异常</div>", wantErr: true},
		{body: "抬头率 **42%**\n\n<img src=\"x.png\">", wantErr: true},
		{body: "看这个例子：`<div>` 会被转义显示", wantErr: false},
		{body: "```html\n<div>示例</div>\n```", wantErr: false},
		{body: "抬头率 < 60% 时提醒", wantErr: false},
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

func TestPreValidateRequiresBodyOrTemplate(t *testing.T) {
	msg := Message{}
	msg.Normalize()
	if err := msg.PreValidate(); err == nil {
		t.Fatal("既没有 body 也没有 template 应报错")
	}
	withTemplate := Message{Template: "absence-alert", Data: map[string]any{}}
	withTemplate.Normalize()
	if err := withTemplate.PreValidate(); err != nil {
		t.Fatalf("只用模板时正文由模板生成，不应报错: %v", err)
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

// 模板产出 Markdown，渲染后邮件里应是 HTML 结构。
func TestTemplateProducesMarkdown(t *testing.T) {
	templates, err := NewTemplates()
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := templates.Render("absence-alert", map[string]any{
		"ClassName": "计算机2301班", "CourseName": "大数据采集", "TimeRange": "08:00-09:40",
		"StudentNames": "张三、李四", "AbsentCount": 2, "TotalCount": 46,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rendered.Markdown, "## ") {
		t.Errorf("模板正文应是 Markdown，实际 %q", rendered.Markdown[:min(20, len(rendered.Markdown))])
	}
	html, text := RenderMarkdown(rendered.Markdown)
	if !strings.Contains(html, "<h2>") || !strings.Contains(html, "<strong>") {
		t.Errorf("Markdown 应渲染出标题与粗体: %s", html)
	}
	if strings.Contains(text, "**") || strings.Contains(text, "##") {
		t.Errorf("纯文本版本应去掉语法标记: %s", text)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
