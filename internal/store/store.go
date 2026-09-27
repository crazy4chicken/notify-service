// Package store 把发送记录追加写入 JSONL 文件，并在内存里保留最近 N 条用于查询。
// 单文件、零外部依赖：本地部署不需要装数据库，出了问题 tail/grep 就能查。
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"notify-service/internal/notify"
)

// Store 是发送记录存储。
type Store struct {
	path string
	max  int

	mu   sync.RWMutex
	recs []notify.Record // 时间升序
}

// Filter 是记录查询条件。
type Filter struct {
	Channel string
	Type    string
	UserID  string
	Status  string
	Limit   int
}

// Open 打开（必要时创建）记录文件，并把历史记录载入内存。
// 返回的 skipped 是读取时跳过的损坏行数，仅用于启动日志。
func Open(path string, max int) (s *Store, skipped int, err error) {
	if max <= 0 {
		max = 5000
	}
	s = &Store{path: path, max: max}
	if path == "" {
		return s, 0, nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, 0, fmt.Errorf("创建数据目录 %s 失败: %w", dir, err)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, 0, nil
		}
		return nil, 0, fmt.Errorf("打开记录文件 %s 失败: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec notify.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			skipped++
			continue
		}
		s.recs = append(s.recs, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, skipped, fmt.Errorf("读取记录文件 %s 失败: %w", path, err)
	}
	s.trim()
	return s, skipped, nil
}

// Save 实现 notify.Recorder：先落盘再入内存，落盘失败则由调用方决定是否忽略。
func (s *Store) Save(rec notify.Record) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if s.path != "" {
		f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("写入记录文件失败: %w", err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			f.Close()
			return fmt.Errorf("写入记录文件失败: %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("写入记录文件失败: %w", err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, rec)
	s.trim()
	return nil
}

// Get 按 ID 取一条记录。
func (s *Store) Get(id string) (notify.Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.recs) - 1; i >= 0; i-- {
		if s.recs[i].ID == id {
			return s.recs[i], true
		}
	}
	return notify.Record{}, false
}

// List 返回按时间倒序排列的记录。
func (s *Store) List(f Filter) []notify.Record {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := f.Limit
	if limit <= 0 || limit > s.max {
		limit = 100
	}
	out := make([]notify.Record, 0, limit)
	for i := len(s.recs) - 1; i >= 0 && len(out) < limit; i-- {
		rec := s.recs[i]
		if f.Channel != "" && string(rec.Channel) != f.Channel {
			continue
		}
		if f.Type != "" && rec.Type != f.Type {
			continue
		}
		if f.UserID != "" && rec.UserID != f.UserID {
			continue
		}
		if f.Status != "" && rec.Status != f.Status {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// Count 返回内存中保留的记录总数。
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.recs)
}

// trim 只保留最近 max 条（调用方须持有写锁或处于初始化阶段）。
func (s *Store) trim() {
	if len(s.recs) > s.max {
		s.recs = append([]notify.Record(nil), s.recs[len(s.recs)-s.max:]...)
	}
}
