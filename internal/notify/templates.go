package notify

import (
	"sort"
	"strings"
	"sync"
	texttpl "text/template"
)

// Template 是一个内置通知模板：只产出 Markdown（正文单一来源），
// 具体渲染成邮件的 HTML 还是短信的纯文本，由通道决定。
// 调用方按 Fields 准备 data 即可，缺字段会直接报 400（而不是渲染出空白）。
type Template struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Fields      []string `json:"fields"`
	Subject     string   `json:"subjectTemplate"`
	Markdown    string   `json:"markdownTemplate"`
}

// Rendered 是模板渲染结果：一个主题 + 一段 Markdown。
type Rendered struct {
	Subject  string
	Markdown string
}

type compiledTemplate struct {
	def     Template
	subject *texttpl.Template
	body    *texttpl.Template
}

// Templates 保存全部内置模板；编译一次，之后并发只读。
type Templates struct {
	mu     sync.RWMutex
	list   []Template
	byName map[string]*compiledTemplate
}

// NewTemplates 编译内置模板。出错说明模板字面量写错了，启动即失败。
func NewTemplates() (*Templates, error) {
	defs := []Template{
		{
			Name:        "absence-alert",
			Title:       "缺勤告警",
			Description: "某节课出现缺勤时发送给辅导员/任课教师",
			Fields:      []string{"ClassName", "CourseName", "TimeRange", "StudentNames", "AbsentCount", "TotalCount"},
			Subject:     "【缺勤告警】{{.ClassName}} {{.CourseName}} {{.TimeRange}}",
			Markdown: `## {{.ClassName}} 课堂考勤异常

- 课程：{{.CourseName}}
- 时间：{{.TimeRange}}
- 应到：{{.TotalCount}} 人
- **缺勤：{{.AbsentCount}} 人**
- 缺勤名单：{{.StudentNames}}`,
		},
		{
			Name:        "low-head-up-rate",
			Title:       "抬头率不达标提醒",
			Description: "抬头率低于阈值时提醒任课教师",
			Fields:      []string{"ClassName", "CourseName", "TimeRange", "HeadUpRate", "Threshold", "Suggestion"},
			Subject:     "【抬头率提醒】{{.ClassName}} {{.CourseName}} 抬头率 {{.HeadUpRate}}",
			Markdown: `## {{.ClassName}} 抬头率低于阈值

- 课程：{{.CourseName}}
- 时间：{{.TimeRange}}
- 当前抬头率：**{{.HeadUpRate}}**（阈值 {{.Threshold}}）
- 建议：{{.Suggestion}}`,
		},
		{
			Name:        "attendance-summary",
			Title:       "考勤与抬头率日报",
			Description: "一节课结束后的汇总，可发给任课教师或教学管理方",
			Fields:      []string{"ClassName", "CourseName", "Date", "TotalCount", "PresentCount", "AbsentCount", "AttendanceRate", "HeadUpRate"},
			Subject:     "【课堂日报】{{.ClassName}} {{.CourseName}} {{.Date}}",
			Markdown: `## {{.ClassName}} 课堂状态日报

- 日期：{{.Date}}
- 课程：{{.CourseName}}
- 应到 / 实到 / 缺勤：{{.TotalCount}} / {{.PresentCount}} / **{{.AbsentCount}}** 人
- 出勤率：{{.AttendanceRate}}
- 平均抬头率：{{.HeadUpRate}}`,
		},
	}

	t := &Templates{byName: make(map[string]*compiledTemplate, len(defs))}
	for _, def := range defs {
		subject, err := texttpl.New(def.Name + ":subject").Option("missingkey=error").Parse(def.Subject)
		if err != nil {
			return nil, err
		}
		body, err := texttpl.New(def.Name + ":body").Option("missingkey=error").Parse(def.Markdown)
		if err != nil {
			return nil, err
		}
		t.byName[def.Name] = &compiledTemplate{def: def, subject: subject, body: body}
		t.list = append(t.list, def)
	}
	sort.Slice(t.list, func(i, j int) bool { return t.list[i].Name < t.list[j].Name })
	return t, nil
}

// List 返回全部模板定义（按名称排序）。
func (t *Templates) List() []Template {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Template, len(t.list))
	copy(out, t.list)
	return out
}

// Render 用 data 渲染指定模板，得到主题与 Markdown 正文。
func (t *Templates) Render(name string, data map[string]any) (Rendered, error) {
	t.mu.RLock()
	item, ok := t.byName[name]
	t.mu.RUnlock()
	if !ok {
		return Rendered{}, NotFoundf("模板 %q 不存在，可用模板见 GET /api/v1/templates", name)
	}
	if data == nil {
		data = map[string]any{}
	}

	var subject, body strings.Builder
	if err := item.subject.Execute(&subject, data); err != nil {
		return Rendered{}, renderError(item.def, err)
	}
	if err := item.body.Execute(&body, data); err != nil {
		return Rendered{}, renderError(item.def, err)
	}
	return Rendered{
		Subject:  strings.TrimSpace(subject.String()),
		Markdown: strings.TrimSpace(body.String()),
	}, nil
}

func renderError(def Template, err error) error {
	return Invalidf("模板 %q 渲染失败：%v；该模板需要的字段：%s",
		def.Name, err, strings.Join(def.Fields, ", "))
}
