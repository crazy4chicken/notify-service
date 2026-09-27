package notify

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// 行内语法只支持这三个；刻意不支持 `_下划线斜体_`，
// 否则像 head_up_rate 这种标识符会被误当成斜体。
var (
	reBold   = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	reItalic = regexp.MustCompile(`\*([^*\n]+)\*`)
	reLink   = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)\)`)
)

// RenderMarkdown 把一小段 Markdown 渲染成 (邮件用 HTML, 纯文本)。
//
// 支持的部分是通知场景够用的子集：标题、段落、无序/有序列表、引用、分割线、
// 围栏代码块，行内 **粗体** / *斜体* / `代码` / [文字](链接)。
// 其它调用方可以放心传用户产出的内容——HTML 先行整体转义，无法注入标签；
// 链接只允许 http/https/mailto，其它协议会被降级成纯文字。
//
// 纯文本结果给短信通道与 text/plain 兜底用，因此会把实体还原回未转义字符。
func RenderMarkdown(src string) (htmlOut, textOut string) {
	if strings.TrimSpace(src) == "" {
		return "", ""
	}
	lines := splitMDLines(src)

	var h, t strings.Builder
	writeBlank := func() {
		if h.Len() > 0 {
			h.WriteString("\n")
		}
	}

	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		switch {
		case trimmed == "":
			i++
			writeBlank()

		case isFence(trimmed):
			lang := strings.TrimSpace(strings.TrimLeft(trimmed, "`~"))
			i++
			var code []string
			for i < len(lines) && !isFence(strings.TrimSpace(lines[i])) {
				code = append(code, lines[i])
				i++
			}
			i++ // 跳过收尾的 ```
			h.WriteString(`<pre style="background:#f5f6f8;padding:10px 12px;border-radius:6px;overflow:auto"><code`)
			if lang != "" {
				h.WriteString(` class="language-` + escapeMD(lang) + `"`)
			}
			h.WriteString(`>` + escapeMD(strings.Join(code, "\n")) + "</code></pre>\n")
			t.WriteString(strings.Join(code, "\n") + "\n")

		case isThematicBreak(trimmed):
			h.WriteString("<hr>\n")
			t.WriteString("----------\n")
			i++

		case headingLevel(trimmed) > 0:
			level := headingLevel(trimmed)
			content := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			h.WriteString("<h" + itoa(level) + ">" + renderInline(escapeMD(content)) + "</h" + itoa(level) + ">\n")
			t.WriteString(inlineText(escapeMD(content)) + "\n")
			i++

		case strings.HasPrefix(trimmed, ">"):
			var quoted []string
			for i < len(lines) {
				q := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(q, ">") {
					break
				}
				quoted = append(quoted, strings.TrimSpace(strings.TrimPrefix(q, ">")))
				i++
			}
			h.WriteString(`<blockquote style="margin:8px 0;padding:4px 12px;border-left:3px solid #d8dde6;color:#5b6270">`)
			h.WriteString(renderInline(escapeMD(strings.Join(quoted, " "))))
			h.WriteString("</blockquote>\n")
			// 纯文本版本不带 "> " 标记：短信要的是"去语法"的正文。
			t.WriteString(inlineText(escapeMD(strings.Join(quoted, " "))) + "\n")

		case unorderedItem(trimmed) >= 0:
			h.WriteString("<ul>\n")
			for i < len(lines) {
				item := strings.TrimSpace(lines[i])
				marker := unorderedItem(item)
				if marker < 0 {
					break
				}
				content := strings.TrimSpace(item[marker:])
				h.WriteString("<li>" + renderInline(escapeMD(content)) + "</li>\n")
				t.WriteString("- " + inlineText(escapeMD(content)) + "\n")
				i++
			}
			h.WriteString("</ul>\n")

		case orderedItem(trimmed) >= 0:
			h.WriteString("<ol>\n")
			for i < len(lines) {
				item := strings.TrimSpace(lines[i])
				marker := orderedItem(item)
				if marker < 0 {
					break
				}
				content := strings.TrimSpace(item[marker:])
				h.WriteString("<li>" + renderInline(escapeMD(content)) + "</li>\n")
				t.WriteString("- " + inlineText(escapeMD(content)) + "\n")
				i++
			}
			h.WriteString("</ol>\n")

		default:
			// 段落：直到空行或下一个块级标记为止，段内换行按 <br> 处理
			var para []string
			for i < len(lines) {
				cur := strings.TrimSpace(lines[i])
				if cur == "" || isFence(cur) || isThematicBreak(cur) || headingLevel(cur) > 0 ||
					strings.HasPrefix(cur, ">") || unorderedItem(cur) >= 0 || orderedItem(cur) >= 0 {
					break
				}
				para = append(para, cur)
				i++
			}
			h.WriteString("<p>" + renderInline(escapeMD(strings.Join(para, "\n"))) + "</p>\n")
			t.WriteString(inlineText(escapeMD(strings.Join(para, " "))) + "\n")
			continue // i 已在上面推进
		}
	}

	// 只返回内容片段：邮件外壳（内联样式的容器/页眉/页脚）由 email 通道套上。
	return strings.TrimSpace(h.String()), html.UnescapeString(strings.TrimSpace(t.String()))
}

func splitMDLines(src string) []string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	src = strings.ReplaceAll(src, "\t", "    ")
	return strings.Split(src, "\n")
}

func escapeMD(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(s)
}

// renderInline 处理行内语法。代码段先按反引号切出来单独成 <code>，
// 避免后面的强调/链接规则改动代码里的内容。
func renderInline(escaped string) string {
	var out strings.Builder
	parts := strings.Split(escaped, "`")
	for idx, part := range parts {
		if idx%2 == 1 {
			out.WriteString("<code>" + part + "</code>")
			continue
		}
		out.WriteString(emphasis(part))
	}
	return strings.ReplaceAll(out.String(), "\n", "<br>")
}

func emphasis(s string) string {
	s = reBold.ReplaceAllString(s, "<strong>$1</strong>")
	s = reItalic.ReplaceAllString(s, "<em>$1</em>")
	return reLink.ReplaceAllStringFunc(s, func(m string) string {
		groups := reLink.FindStringSubmatch(m)
		if !allowedURL(groups[2]) {
			return groups[1]
		}
		return `<a href="` + groups[2] + `">` + groups[1] + `</a>`
	})
}

func inlineText(escaped string) string {
	var out strings.Builder
	parts := strings.Split(escaped, "`")
	for idx, part := range parts {
		if idx%2 == 1 {
			out.WriteString(part)
			continue
		}
		part = reBold.ReplaceAllString(part, "$1")
		part = reItalic.ReplaceAllString(part, "$1")
		part = reLink.ReplaceAllString(part, "$1")
		out.WriteString(part)
	}
	return out.String()
}

// allowedURL 只放行 http/https/mailto，挡掉 javascript: 之类。
func allowedURL(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "mailto:")
}

func isFence(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func isThematicBreak(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	for _, ch := range []string{"-", "*", "_"} {
		if strings.Trim(trimmed, ch) == "" {
			return true
		}
	}
	return false
}

func headingLevel(trimmed string) int {
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level < 1 || level > 6 || level >= len(trimmed) {
		return 0
	}
	if trimmed[level] != ' ' {
		return 0
	}
	return level
}

// unorderedItem 返回标记长度（含空格），不是列表项时返回 -1。
func unorderedItem(trimmed string) int {
	if len(trimmed) < 2 {
		return -1
	}
	switch trimmed[0] {
	case '-', '*', '+':
		if trimmed[1] == ' ' {
			return 2
		}
	}
	return -1
}

// orderedItem 返回标记长度（例如 "12. " 返回 4），不是列表项时返回 -1。
func orderedItem(trimmed string) int {
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(trimmed) {
		return -1
	}
	switch trimmed[i] {
	case '.', ')':
	default:
		return -1
	}
	if i+1 >= len(trimmed) || trimmed[i+1] != ' ' {
		return -1
	}
	return i + 2
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// reHTMLTag 匹配裸 HTML 标签，例如 <div>、</p>、<img src="x">。
var reHTMLTag = regexp.MustCompile(`</?[a-zA-Z][a-zA-Z0-9-]*([ \t][^<>]*)?/?>`)

// ContainsRawHTML 检查正文里的裸 HTML 标签，返回第一个命中的片段。
// 围栏代码块与行内代码里的标签不算（那里本来就是代码，会被转义显示）。
// 渲染器本身也不会解释裸标签（先整体转义），这里只是让调用方早点发现用错了格式。
func ContainsRawHTML(src string) (string, bool) {
	stripped := stripCodeSpans(src)
	if match := reHTMLTag.FindString(stripped); match != "" {
		return match, true
	}
	return "", false
}

// stripCodeSpans 去掉围栏代码块与行内代码，避免把代码里的标签误判成裸 HTML。
func stripCodeSpans(src string) string {
	var out strings.Builder
	inFence := false
	for _, line := range splitMDLines(src) {
		trimmed := strings.TrimSpace(line)
		if isFence(trimmed) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for i, part := range strings.Split(line, "`") {
			if i%2 == 0 {
				out.WriteString(part)
			}
		}
		out.WriteString("\n")
	}
	return out.String()
}

// TextToHTML 把纯文本正文转成邮件用的 HTML 片段：整体转义 + 空行分段 + 段内换行 <br>。
// 用于 bodyFormat=text 的旧调用，保证邮件里既有 HTML 版本也有纯文本版本。
func TextToHTML(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	paragraphs := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n\n")
	var out strings.Builder
	for _, para := range paragraphs {
		para = strings.Trim(para, "\n")
		if strings.TrimSpace(para) == "" {
			continue
		}
		out.WriteString("<p>" + strings.ReplaceAll(escapeMD(para), "\n", "<br>") + "</p>\n")
	}
	return strings.TrimSpace(out.String())
}
