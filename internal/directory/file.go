// Package directory 提供两种用户目录实现：本地用户表文件、用户服务 HTTP 接口。
// 通知服务只依赖 notify.Resolver 接口，换实现不需要改动发送逻辑。
package directory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"notify-service/internal/notify"
)

// FileResolver 从本地 JSON 文件读取用户表，文件被修改后自动重载（按修改时间判断），
// 适合后端用户服务还没做好、或小规模固定名单的场景。
//
// 文件格式（两种都支持）：
//
//	{"users": [{"id":"202410810316","name":"张三","channels":{"email":"a@qq.com","sms":"13800000000"}}]}
//	[{"id":"...","channels":{...}}]
type FileResolver struct {
	path string

	mu      sync.RWMutex
	modTime time.Time
	users   map[string]notify.User
}

// NewFileResolver 创建本地用户表解析器，path 为空时返回 nil。
func NewFileResolver(path string) *FileResolver {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return &FileResolver{path: path}
}

// Describe 实现 notify.Resolver。
func (r *FileResolver) Describe() string {
	return "本地用户表 " + r.path
}

// Resolve 实现 notify.Resolver。
func (r *FileResolver) Resolve(_ context.Context, userID string) (notify.User, error) {
	users, err := r.load()
	if err != nil {
		return notify.User{}, err
	}
	user, ok := users[userID]
	if !ok {
		return notify.User{}, notify.NotFoundf("用户 %q 不在 %s 中", userID, r.path)
	}
	return user, nil
}

func (r *FileResolver) load() (map[string]notify.User, error) {
	info, err := os.Stat(r.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, notify.NotReadyf("用户表文件 %s 不存在", r.path)
		}
		return nil, notify.Upstreamf("读取用户表 %s 失败: %v", r.path, err)
	}

	r.mu.RLock()
	cached, modTime := r.users, r.modTime
	r.mu.RUnlock()
	if cached != nil && info.ModTime().Equal(modTime) {
		return cached, nil
	}

	raw, err := os.ReadFile(r.path)
	if err != nil {
		return nil, notify.Upstreamf("读取用户表 %s 失败: %v", r.path, err)
	}
	users, err := ParseUsers(raw)
	if err != nil {
		return nil, notify.Invalidf("解析用户表 %s 失败: %v", r.path, err)
	}

	r.mu.Lock()
	r.users, r.modTime = users, info.ModTime()
	r.mu.Unlock()
	return users, nil
}

// ParseUsers 解析用户表 JSON，兼容 {"users":[...]} 与裸数组两种写法（空表也合法）。
func ParseUsers(raw []byte) (map[string]notify.User, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return map[string]notify.User{}, nil
	}

	var list []notify.User
	if trimmed[0] == '{' {
		var wrapped struct {
			Users []notify.User `json:"users"`
		}
		if err := json.Unmarshal(trimmed, &wrapped); err != nil {
			return nil, fmt.Errorf("不是合法的 {\"users\":[...]} 结构: %w", err)
		}
		list = wrapped.Users
	} else {
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, fmt.Errorf("不是合法的用户数组: %w", err)
		}
	}

	users := make(map[string]notify.User, len(list))
	for _, user := range list {
		id := strings.TrimSpace(user.ID)
		if id == "" {
			return nil, errors.New("存在缺少 id 的用户记录")
		}
		if users[id].ID != "" {
			return nil, fmt.Errorf("用户 id %q 重复", id)
		}
		if user.Channels == nil {
			user.Channels = map[string]string{}
		}
		user.ID = id
		users[id] = user
	}
	return users, nil
}
