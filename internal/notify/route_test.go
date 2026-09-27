package notify

import (
	"context"
	"errors"
	"testing"
)

func TestParseRoutes(t *testing.T) {
	cases := []struct {
		spec string
		want map[string]string // type → "email,sms"
	}{
		{spec: "", want: map[string]string{"default": "email"}},
		{spec: "absence=email,sms;head-up-rate=email", want: map[string]string{
			"absence": "email/sms", "head-up-rate": "email", "default": "email",
		}},
		{spec: "Absence=EMAIL", want: map[string]string{"absence": "email", "default": "email"}},
		{spec: "default=sms", want: map[string]string{"default": "sms"}},
		{spec: "a=email;;b=sms", want: map[string]string{"a": "email", "b": "sms", "default": "email"}},
	}
	for _, tc := range cases {
		table, err := ParseRoutes(tc.spec)
		if err != nil {
			t.Fatalf("%q 解析失败: %v", tc.spec, err)
		}
		for kind, want := range tc.want {
			got := table.Candidates(kind)
			if joinChannels(got) != want {
				t.Errorf("%q: 类型 %q 期望 %q 实际 %q", tc.spec, kind, want, joinChannels(got))
			}
		}
	}

	for _, bad := range []string{"absence", "absence=", "=email"} {
		if _, err := ParseRoutes(bad); err == nil {
			t.Errorf("%q 应解析失败", bad)
		}
	}
}

func TestRouteCandidatesFallback(t *testing.T) {
	table, err := ParseRoutes("absence=sms,email")
	if err != nil {
		t.Fatal(err)
	}
	if got := joinChannels(table.Candidates("head-up-rate")); got != "email" {
		t.Errorf("未配类型的通知应走 default=email，实际 %q", got)
	}
	if got := joinChannels(table.Candidates("")); got != "email" {
		t.Errorf("type 为空应走 default，实际 %q", got)
	}
	if got := joinChannels(table.Candidates("ABSENCE")); got != "sms/email" {
		t.Errorf("类型匹配应忽略大小写，实际 %q", got)
	}
}

/* ---------- 路由选择的端到端单测 ---------- */

type fakeNotifier struct {
	name      Channel
	ready     bool
	mode      string
	sent      []Message
	delivered []Delivery
	failMsg   error
}

func (f *fakeNotifier) Name() Channel { return f.name }

func (f *fakeNotifier) Describe() Status {
	mode := f.mode
	if mode == "" {
		mode = "live"
	}
	return Status{Channel: f.name, Provider: "fake", Ready: f.ready, Mode: mode}
}

func (f *fakeNotifier) Send(_ context.Context, d Delivery) (Receipt, error) {
	if f.failMsg != nil {
		return Receipt{}, f.failMsg
	}
	f.sent = append(f.sent, d.Message)
	f.delivered = append(f.delivered, d)
	return Receipt{Channel: f.name, Provider: "fake", Accepted: d.Message.To, Detail: "fake ok"}, nil
}

type fakeResolver struct {
	users map[string]User
	err   error
}

func (f *fakeResolver) Describe() string { return "fake 用户目录" }

func (f *fakeResolver) Resolve(_ context.Context, userID string) (User, error) {
	if f.err != nil {
		return User{}, f.err
	}
	user, ok := f.users[userID]
	if !ok {
		return User{}, NotFoundf("用户 %q 不存在", userID)
	}
	return user, nil
}

func newTestService(t *testing.T, routes string, resolver Resolver, notifiers ...Notifier) (*Service, *Templates) {
	t.Helper()
	templates, err := NewTemplates()
	if err != nil {
		t.Fatal(err)
	}
	table, err := ParseRoutes(routes)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, templates, resolver, table, nil)
	for _, n := range notifiers {
		if err := svc.Register(n); err != nil {
			t.Fatal(err)
		}
	}
	return svc, templates
}

func TestSendRoutesUserToEmail(t *testing.T) {
	email := &fakeNotifier{name: ChannelEmail, ready: true}
	sms := &fakeNotifier{name: ChannelSMS, ready: false, mode: "unconfigured"}
	resolver := &fakeResolver{users: map[string]User{
		"202410810316": {ID: "202410810316", Name: "张三", Channels: map[string]string{"email": "zhangsan@qq.com"}},
	}}
	svc, _ := newTestService(t, "absence=email,sms", resolver, email, sms)

	rec, err := svc.Send(context.Background(), Message{
		User: "202410810316", Type: "absence", Body: "本班 **3 人** 缺勤", BodyFormat: BodyFormatMarkdown,
	})
	if err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	if rec.Channel != ChannelEmail || rec.UserID != "202410810316" || rec.UserName != "张三" {
		t.Errorf("记录不对: %+v", rec)
	}
	if len(email.sent) != 1 || email.sent[0].To[0] != "zhangsan@qq.com" {
		t.Fatalf("邮件未按用户地址发出: %+v", email.sent)
	}
	if rec.Status != StatusSent {
		t.Errorf("状态应为 sent，实际 %q", rec.Status)
	}
}

// 路由里的第一个渠道没有地址/不可用时，按顺序往后挑，并在记录里体现最终选择。
func TestSendRouteFallsBackWhenChannelUnavailable(t *testing.T) {
	email := &fakeNotifier{name: ChannelEmail, ready: true}
	sms := &fakeNotifier{name: ChannelSMS, ready: false, mode: "unconfigured"}
	resolver := &fakeResolver{users: map[string]User{
		"u1": {ID: "u1", Channels: map[string]string{"email": "a@qq.com", "sms": "13800000000"}},
	}}
	svc, _ := newTestService(t, "absence=sms,email", resolver, email, sms)

	rec, err := svc.Send(context.Background(), Message{User: "u1", Type: "absence", Body: "缺勤 2 人"})
	if err != nil {
		t.Fatalf("应回退到邮件，实际报错: %v", err)
	}
	if rec.Channel != ChannelEmail || len(sms.sent) != 0 {
		t.Errorf("应选择邮件通道，记录=%+v", rec)
	}
}

// 路由只剩一个不可用通道时，明确报 503 并说明原因，而不是静默发别处。
func TestSendRouteNoUsableChannel(t *testing.T) {
	sms := &fakeNotifier{name: ChannelSMS, ready: false, mode: "unconfigured"}
	resolver := &fakeResolver{users: map[string]User{
		"u1": {ID: "u1", Channels: map[string]string{"sms": "13800000000"}},
	}}
	svc, _ := newTestService(t, "absence=sms", resolver, sms)

	_, err := svc.Send(context.Background(), Message{User: "u1", Type: "absence", Body: "x"})
	var nerr *Error
	if !errors.As(err, &nerr) || nerr.Kind != KindNotReady {
		t.Fatalf("应返回 channel_not_ready，实际 %v", err)
	}
}

// 用户没有该渠道地址时，属于"找不到收件人"，报 404 更准确。
func TestSendRouteUserMissingAddress(t *testing.T) {
	email := &fakeNotifier{name: ChannelEmail, ready: true}
	resolver := &fakeResolver{users: map[string]User{
		"u1": {ID: "u1", Channels: map[string]string{"sms": "13800000000"}},
	}}
	svc, _ := newTestService(t, "default=email", resolver, email)

	_, err := svc.Send(context.Background(), Message{User: "u1", Body: "x"})
	var nerr *Error
	if !errors.As(err, &nerr) || nerr.Kind != KindNotFound {
		t.Fatalf("应返回 not_found，实际 %v", err)
	}
}

// 调用方显式指定 channel 时不走路由；该通道不可用就直接失败，不偷偷换渠道。
func TestSendExplicitChannelSkipsRouting(t *testing.T) {
	email := &fakeNotifier{name: ChannelEmail, ready: true}
	sms := &fakeNotifier{name: ChannelSMS, ready: true}
	resolver := &fakeResolver{users: map[string]User{
		"u1": {ID: "u1", Channels: map[string]string{"email": "a@qq.com", "sms": "13800000000"}},
	}}
	svc, _ := newTestService(t, "default=email", resolver, email, sms)

	rec, err := svc.Send(context.Background(), Message{User: "u1", Channel: ChannelSMS, Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Channel != ChannelSMS || len(sms.sent) != 1 || len(email.sent) != 0 {
		t.Errorf("应只走短信，记录=%+v", rec)
	}

	sms.ready = false
	if _, err := svc.Send(context.Background(), Message{User: "u1", Channel: ChannelSMS, Body: "x"}); err == nil {
		t.Error("指定渠道不可用时应报错，而不是改走邮件")
	}
}

func TestSendRejectsUserWithAddress(t *testing.T) {
	email := &fakeNotifier{name: ChannelEmail, ready: true}
	svc, _ := newTestService(t, "", &fakeResolver{}, email)

	_, err := svc.Send(context.Background(), Message{User: "u1", To: []string{"a@qq.com"}, Body: "x"})
	var nerr *Error
	if !errors.As(err, &nerr) || nerr.Kind != KindInvalid {
		t.Fatalf("user 与 to 同时出现应报 400，实际 %v", err)
	}
}

func TestSendRequiresDirectory(t *testing.T) {
	email := &fakeNotifier{name: ChannelEmail, ready: true}
	svc, _ := newTestService(t, "", nil, email)

	_, err := svc.Send(context.Background(), Message{User: "u1", Body: "x"})
	var nerr *Error
	if !errors.As(err, &nerr) || nerr.Kind != KindNotReady {
		t.Fatalf("未配置用户目录应报 503，实际 %v", err)
	}
}

func TestSendDirectAddressStillWorks(t *testing.T) {
	email := &fakeNotifier{name: ChannelEmail, ready: true}
	svc, _ := newTestService(t, "", nil, email)

	rec, err := svc.Send(context.Background(), Message{Target: &Target{To: []string{"a@qq.com"}}, Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Channel != ChannelEmail || rec.UserID != "" {
		t.Errorf("直接给地址应走邮件且无 user，记录=%+v", rec)
	}
}
