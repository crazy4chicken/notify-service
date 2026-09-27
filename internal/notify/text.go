package notify

import "strings"

// htmlEntities 常见实体的还原表，够覆盖我们自己的模板输出。
var htmlEntities = strings.NewReplacer(
	"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'",
)

// StripTags 把 HTML 粗略转成纯文本：去标签、还原常见实体、压缩空白。
// 用途有两个：邮件 multipart 的 text/plain 兜底内容；短信通道从 HTML 正文取文本。
func StripTags(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
			b.WriteByte(' ')
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(htmlEntities.Replace(b.String())), " ")
}
