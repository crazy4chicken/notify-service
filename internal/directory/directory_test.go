package directory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notify-service/internal/notify"
)

func TestParseUsers(t *testing.T) {
	wrapped := []byte(`{"users":[{"id":"u1","name":"张三","channels":{"email":"a@qq.com","sms":"13800000000"}}]}`)
	bare := []byte(`[{"id":"u2","channels":{"email":"b@qq.com"}}]`)

	for name, raw := range map[string][]byte{"wrapped": wrapped, "bare": bare} {
		users, err := ParseUsers(raw)
		if err != nil {
			t.Fatalf("%s 解析失败: %v", name, err)
		}
		if len(users) != 1 {
			t.Fatalf("%s 应有 1 个用户，实际 %d", name, len(users))
		}
	}

	for name, raw := range map[string]string{
		"重复 id":   `[{"id":"u1"},{"id":"u1"}]`,
		"缺少 id":   `[{"channels":{"email":"a@qq.com"}}]`,
		"非法 JSON": `not-json`,
	} {
		if _, err := ParseUsers([]byte(raw)); err == nil {
			t.Errorf("%s 应解析失败", name)
		}
	}
}

func TestFileResolver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	write := func(content string) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"users":[{"id":"u1","channels":{"email":"a@qq.com"}}]}`)

	resolver := NewFileResolver(path)
	if resolver == nil {
		t.Fatal("resolver 不应为 nil")
	}
	user, err := resolver.Resolve(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if addr, ok := user.Address(notify.ChannelEmail); !ok || addr != "a@qq.com" {
		t.Errorf("解析结果不对: %+v", user)
	}

	if _, err := resolver.Resolve(context.Background(), "nobody"); err == nil {
		t.Error("未知用户应报错")
	}

	// 文件内容更新后（修改时间变化）应能重新加载，便于运维直接改用户表。
	write(`{"users":[{"id":"u1","channels":{"email":"a@qq.com"}},{"id":"u2","channels":{"sms":"13900000000"}}]}`)
	bumpModTime(t, path)
	if _, err := resolver.Resolve(context.Background(), "u2"); err != nil {
		t.Errorf("应能读到新增用户: %v", err)
	}
}

func TestFileResolverMissingFile(t *testing.T) {
	resolver := NewFileResolver(filepath.Join(t.TempDir(), "nope.json"))
	_, err := resolver.Resolve(context.Background(), "u1")
	var nerr *notify.Error
	if err == nil || !asNotify(err, &nerr) || nerr.Kind != notify.KindNotReady {
		t.Fatalf("文件不存在应报 channel_not_ready，实际 %v", err)
	}
}

func bumpModTime(t *testing.T, path string) {
	t.Helper()
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
}

func asNotify(err error, target **notify.Error) bool {
	return errors.As(err, target)
}

func TestHTTPResolverShapes(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		switch strings.TrimPrefix(r.URL.Path, "/users/") {
		case "u1": // 规范形状
			writeJSON(w, 200, map[string]any{
				"id": "u1", "name": "张三",
				"channels": map[string]string{"email": "a@qq.com", "sms": "13800000000"},
			})
		case "u2": // 兼容平铺形状
			writeJSON(w, 200, map[string]any{"email": "b@qq.com", "phone": "13900000000"})
		case "missing":
			w.WriteHeader(http.StatusNotFound)
		case "broken":
			_, _ = w.Write([]byte("not json"))
		case "wrong-id":
			writeJSON(w, 200, map[string]any{"id": "other", "channels": map[string]string{"email": "x@qq.com"}})
		case "boom":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("user service exploded"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	resolver, err := NewHTTPResolver(server.URL, "/users/{id}", "secret-token", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	user, err := resolver.Resolve(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if user.Name != "张三" || user.Channels["sms"] != "13800000000" {
		t.Errorf("解析结果不对: %+v", user)
	}
	if gotPath != "/users/u1" || gotAuth != "Bearer secret-token" {
		t.Errorf("请求不对：path=%q auth=%q", gotPath, gotAuth)
	}

	flat, err := resolver.Resolve(ctx, "u2")
	if err != nil {
		t.Fatal(err)
	}
	if flat.Channels["email"] != "b@qq.com" || flat.Channels["sms"] != "13900000000" {
		t.Errorf("平铺形状解析不对: %+v", flat)
	}

	expectKind := map[string]notify.Kind{
		"missing":  notify.KindNotFound,
		"broken":   notify.KindUpstream,
		"wrong-id": notify.KindUpstream,
		"boom":     notify.KindUpstream,
	}
	for id, kind := range expectKind {
		_, err := resolver.Resolve(ctx, id)
		var nerr *notify.Error
		if err == nil || !asNotify(err, &nerr) || nerr.Kind != kind {
			t.Errorf("%s 期望 %s，实际 %v", id, kind, err)
		}
	}
}

func TestHTTPResolverConfigValidation(t *testing.T) {
	if r, err := NewHTTPResolver("", "", "", 0); err != nil || r != nil {
		t.Errorf("URL 为空应返回 nil,nil，实际 %v %v", r, err)
	}
	if _, err := NewHTTPResolver("http://x", "/users", "", 0); err == nil {
		t.Error("路径缺少 {id} 占位符应报错")
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
