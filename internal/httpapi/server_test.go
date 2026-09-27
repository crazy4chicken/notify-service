package httpapi

import (
	"errors"
	"testing"

	"notify-service/internal/notify"
)

// 入口层只保证"正文只有一个来源"：body（+ bodyFormat）或 template+data。
// 已移除的 html 字段、改名的 markdown 字段都要给出明确 400，而不是被静默忽略。
func TestValidateBody(t *testing.T) {
	htmlField := "<p>x</p>"
	mdField := "**x**"

	cases := []struct {
		name    string
		req     notifyRequest
		wantErr bool
	}{
		{name: "body 纯文本", req: notifyRequest{Body: "x", BodyFormat: "text"}},
		{name: "body markdown", req: notifyRequest{Body: "## x", BodyFormat: "markdown"}},
		{name: "bodyFormat 留空", req: notifyRequest{Body: "x"}},
		{name: "模板生成", req: notifyRequest{Template: "absence-alert"}},
		{name: "两个都写：body+html", req: notifyRequest{Body: "x", HTML: &htmlField}, wantErr: true},
		{name: "两个都写：body+markdown", req: notifyRequest{Body: "x", Markdown: &mdField}, wantErr: true},
		{name: "两个都写：body+template", req: notifyRequest{Body: "x", Template: "absence-alert"}, wantErr: true},
		{name: "只给 html", req: notifyRequest{HTML: &htmlField}, wantErr: true},
		{name: "非法 bodyFormat", req: notifyRequest{Body: "x", BodyFormat: "html"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.validateBody()
			if tc.wantErr && err == nil {
				t.Fatal("应报错")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if err != nil {
				var nerr *notify.Error
				if !errors.As(err, &nerr) || nerr.Kind != notify.KindInvalid {
					t.Fatalf("应返回 400 类错误，实际 %v", err)
				}
			}
		})
	}
}
