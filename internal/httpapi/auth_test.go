package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notify-service/internal/directory"
	"notify-service/internal/notify"
	"notify-service/internal/store"
)

const (
	testServiceToken = "svc-token"
	testKeyID        = "auth-test-key"
	testAudience     = "teamusers"
)

// teamusersStub 模拟 teamusers 的 JWKS、权限查询与远程授权检查接口。
type teamusersStub struct {
	*httptest.Server
	privateKey ed25519.PrivateKey
}

func newTeamusersStub(t *testing.T) *teamusersStub {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 ed25519 密钥失败: %v", err)
	}
	stub := &teamusersStub{privateKey: privateKey}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "OKP",
				"crv": "Ed25519",
				"alg": "EdDSA",
				"use": "sig",
				"kid": testKeyID,
				"x":   base64.RawURLEncoding.EncodeToString(publicKey),
			}},
		})
	})
	mux.HandleFunc("GET /authz/permissions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testServiceToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var grants []map[string]string
		switch r.PathValue("id") {
		case "granted":
			grants = append(grants, map[string]string{"key": "msghub:send:any"}, map[string]string{"key": "msghub:read:any"})
		case "denied":
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_id":  r.PathValue("id"),
			"perm_ver": 0,
			"grants":   grants,
		})
	})
	mux.HandleFunc("POST /authz/check", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testServiceToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"allow": false, "reason": "stub 不做远程放行"})
	})

	stub.Server = httptest.NewServer(mux)
	t.Cleanup(stub.Close)
	return stub
}

// sign 用测试私钥签一个 EdDSA JWT；claims 按 SDK 的校验要求给全。
func (s *teamusersStub) sign(t *testing.T, subject string) string {
	t.Helper()
	headerJSON, err := json.Marshal(map[string]any{"alg": "EdDSA", "kid": testKeyID, "typ": "JWT"})
	if err != nil {
		t.Fatalf("编码 JWT 头失败: %v", err)
	}
	claimsJSON, err := json.Marshal(map[string]any{
		"iss":      testAudience,
		"aud":      testAudience,
		"exp":      time.Now().Add(5 * time.Minute).Unix(),
		"sub":      subject,
		"kind":     "user",
		"perm_ver": 0,
	})
	if err != nil {
		t.Fatalf("编码 JWT 声明失败: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := ed25519.Sign(s.privateKey, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// newTestServer 构造一个可直接打请求的 Handler：真实 store/用户目录/服务，日志丢弃。
func newTestServer(t *testing.T, opt Options) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	records, _, err := store.Open(filepath.Join(t.TempDir(), "notifications.jsonl"), 100)
	if err != nil {
		t.Fatalf("打开记录库失败: %v", err)
	}
	usersFile := filepath.Join(t.TempDir(), "users.json")
	users := []byte(`{"users":[{"id":"granted","name":"已授权","channels":{"email":"granted@example.com"}}]}`)
	if err := os.WriteFile(usersFile, users, 0o600); err != nil {
		t.Fatalf("写入测试用户表失败: %v", err)
	}
	resolver, err := directory.New("", directory.DefaultUserServicePath, "", 5*time.Second, usersFile)
	if err != nil {
		t.Fatalf("构造用户目录失败: %v", err)
	}
	routes, err := notify.ParseRoutes("default=email")
	if err != nil {
		t.Fatalf("解析路由失败: %v", err)
	}
	opt.Service = notify.NewService(records, resolver, routes, logger)
	opt.Store = records
	opt.Logger = logger
	return NewServer(opt).Handler()
}

type httpResult struct {
	status  int
	payload map[string]any
}

func (r httpResult) kind(t *testing.T) string {
	t.Helper()
	body, _ := r.payload["error"].(map[string]any)
	kind, _ := body["kind"].(string)
	return kind
}

func call(t *testing.T, handler http.Handler, method, path, body string, headers map[string]string) httpResult {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var payload map[string]any
	if strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s %s: 解析响应失败: %v（%s）", method, path, err, rec.Body.String())
		}
	}
	return httpResult{status: rec.Code, payload: payload}
}

// tamperSignature 改写签名段的首字符（参与真实比特编码），保证验签必然失败。
func tamperSignature(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[2] == "" {
		return token + "x"
	}
	signature := parts[2]
	replacement := "A"
	if signature[0] == 'A' {
		replacement = "B"
	}
	parts[2] = replacement + signature[1:]
	return strings.Join(parts, ".")
}

// TestTeamusersAuth 覆盖启用 teamusers 后的可观察契约：
// 无/坏 JWT 401、权限不足 403、有权限时到达处理器、未知 /api/ 路径只认证、/healthz 公开。
func TestTeamusersAuth(t *testing.T) {
	stub := newTeamusersStub(t)
	handler := newTestServer(t, Options{
		Auth: NewTeamusersAuth(TeamusersOptions{
			BaseURL:        stub.URL,
			Audience:       testAudience,
			ServiceToken:   testServiceToken,
			Timeout:        5 * time.Second,
			PermissionSend: "msghub:send:any",
			PermissionRead: "msghub:read:any",
		}),
	})
	granted := stub.sign(t, "granted")
	denied := stub.sign(t, "denied")

	cases := []struct {
		name       string
		method     string
		path       string
		auth       string
		body       string
		wantStatus int
		wantKind   string
	}{
		{name: "无 JWT 返回 401", method: "GET", path: "/api/v1/notifications", wantStatus: 401, wantKind: "unauthorized"},
		{name: "坏 JWT 返回 401", method: "GET", path: "/api/v1/notifications", auth: "Bearer not-a-jwt", wantStatus: 401, wantKind: "unauthorized"},
		{name: "签名被篡改返回 401", method: "GET", path: "/api/v1/notifications", auth: "Bearer " + tamperSignature(granted), wantStatus: 401, wantKind: "unauthorized"},
		{name: "无发送权限返回 403", method: "POST", path: "/api/v1/notify", auth: "Bearer " + denied, body: `{"html":"x"}`, wantStatus: 403, wantKind: "forbidden"},
		{name: "有读权限放行到处理器", method: "GET", path: "/api/v1/notifications", auth: "Bearer " + granted, wantStatus: 200},
		{name: "有发送权限放行到处理器", method: "POST", path: "/api/v1/notify", auth: "Bearer " + granted, body: `{"html":"x"}`, wantStatus: 400, wantKind: "invalid_request"},
		{name: "未知 /api/ 路径认证后 404", method: "GET", path: "/api/v1/nope", auth: "Bearer " + granted, wantStatus: 404, wantKind: "not_found"},
		{name: "未知 /api/ 路径仍要认证", method: "GET", path: "/api/v1/nope", wantStatus: 401, wantKind: "unauthorized"},
		{name: "/healthz 保持公开", method: "GET", path: "/healthz", wantStatus: 200},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{}
			if tc.auth != "" {
				headers["Authorization"] = tc.auth
			}
			got := call(t, handler, tc.method, tc.path, tc.body, headers)
			if got.status != tc.wantStatus {
				t.Fatalf("状态码 = %d，期望 %d，响应 %v", got.status, tc.wantStatus, got.payload)
			}
			if tc.wantKind != "" && got.kind(t) != tc.wantKind {
				t.Fatalf("kind = %q，期望 %q，响应 %v", got.kind(t), tc.wantKind, got.payload)
			}
		})
	}

	t.Run("403 消息带权限与 SDK 原因", func(t *testing.T) {
		got := call(t, handler, "POST", "/api/v1/notify", `{"html":"x"}`, map[string]string{"Authorization": "Bearer " + denied})
		body, _ := got.payload["error"].(map[string]any)
		message, _ := body["message"].(string)
		if !strings.Contains(message, "msghub:send:any") || !strings.Contains(message, "no matching grant") {
			t.Fatalf("message = %q，应包含权限与 SDK 原因", message)
		}
	})

	t.Run("有权限读接口返回空记录", func(t *testing.T) {
		got := call(t, handler, "GET", "/api/v1/notifications", "", map[string]string{"Authorization": "Bearer " + granted})
		if got.payload["records"] == nil || got.payload["total"].(float64) != 0 {
			t.Fatalf("响应 = %v，期望空记录列表", got.payload)
		}
	})
}

// TestStaticTokenAuthUnchanged 确认未配置 teamusers 时静态 Token（或完全不鉴权）的行为不变。
func TestStaticTokenAuthUnchanged(t *testing.T) {
	handler := newTestServer(t, Options{Token: "s3cret"})

	cases := []struct {
		name       string
		headers    map[string]string
		wantStatus int
		wantKind   string
	}{
		{name: "无 Token 返回 401", wantStatus: 401, wantKind: "unauthorized"},
		{name: "错误 Token 返回 401", headers: map[string]string{"Authorization": "Bearer wrong"}, wantStatus: 401, wantKind: "unauthorized"},
		{name: "正确 Bearer 放行", headers: map[string]string{"Authorization": "Bearer s3cret"}, wantStatus: 200},
		{name: "X-Notify-Token 回退仍有效", headers: map[string]string{"X-Notify-Token": "s3cret"}, wantStatus: 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := call(t, handler, "GET", "/api/v1/notifications", "", tc.headers)
			if got.status != tc.wantStatus {
				t.Fatalf("状态码 = %d，期望 %d，响应 %v", got.status, tc.wantStatus, got.payload)
			}
			if tc.wantKind != "" && got.kind(t) != tc.wantKind {
				t.Fatalf("kind = %q，期望 %q", got.kind(t), tc.wantKind)
			}
		})
	}

	t.Run("未配置鉴权时保持公开", func(t *testing.T) {
		open := newTestServer(t, Options{})
		if got := call(t, open, "GET", "/api/v1/notifications", "", nil); got.status != 200 {
			t.Fatalf("状态码 = %d，期望 200", got.status)
		}
	})
}
