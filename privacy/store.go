// store.go 基于 SQLite 的进程内映射存储实现，用于早期版本的兼容保留。
// 新代码应使用 store/ 子包中的 store.MappingStore 接口及其 Pebble 实现。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package privacy

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// MappingStore 在本地 SQLite 数据库中持久化 original-to-fake 映射关系。
// 支持并发安全访问。
type MappingStore struct {
	db *sql.DB
	mu sync.RWMutex
}

// OpenMappingStore 在指定路径打开或创建 SQLite 数据库。
// 如果路径为 ":memory:"，则使用内存数据库（适用于测试场景）。
//
// Param:
//   - path: string - 数据库文件路径，":memory:" 表示内存数据库
//
// Return:
//   - *MappingStore: 初始化完成的映射存储实例
//   - error: 数据库打开、连接或迁移失败时返回错误
func OpenMappingStore(path string) (*MappingStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open mapping store: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping mapping store: %w", err)
	}
	store := &MappingStore{db: db}
	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("migrate mapping store: %w", err)
	}
	return store, nil
}

// Close 关闭底层 SQLite 数据库连接，释放文件锁和内存缓存。
//
// Return:
//   - error: 关闭数据库失败时返回错误
func (s *MappingStore) Close() error {
	return s.db.Close()
}

func (s *MappingStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS mappings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			original TEXT NOT NULL,
			fake TEXT NOT NULL,
			type TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_session_original ON mappings(session_id, original);
		CREATE INDEX IF NOT EXISTS idx_session_fake ON mappings(session_id, fake);
	`)
	return err
}

// FindFake 在指定会话中查找原始值对应的伪造值。
func (s *MappingStore) FindFake(sessionID, original string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var fake string
	err := s.db.QueryRow(
		"SELECT fake FROM mappings WHERE session_id = ? AND original = ?",
		sessionID, original,
	).Scan(&fake)
	if err != nil {
		return "", false
	}
	return fake, true
}

// FindOriginal 在指定会话中查找伪造值对应的原始值，用于响应还原阶段。
func (s *MappingStore) FindOriginal(sessionID, fake string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var original string
	err := s.db.QueryRow(
		"SELECT original FROM mappings WHERE session_id = ? AND fake = ?",
		sessionID, fake,
	).Scan(&original)
	if err != nil {
		return "", false
	}
	return original, true
}

// ExistsFake 检查指定会话中是否已存在该伪造值的映射，用于碰撞检测。
func (s *MappingStore) ExistsFake(sessionID, fake string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM mappings WHERE session_id = ? AND fake = ?",
		sessionID, fake,
	).Scan(&count)
	return err == nil && count > 0
}

// Create 插入一条新的映射记录，包含会话标识、原始值、伪造值、实体类型和创建时间。
//
// Param:
//   - sessionID: string - 会话标识符
//   - original: string - 原始敏感值
//   - fake: string - 生成的伪造替换值
//   - typ: EntityType - 实体类型分类
//
// Return:
//   - error: 数据库写入失败时返回错误
func (s *MappingStore) Create(sessionID, original, fake string, typ EntityType) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		"INSERT INTO mappings (session_id, original, fake, type, created_at) VALUES (?, ?, ?, ?, ?)",
		sessionID, original, fake, string(typ), time.Now().Unix(),
	)
	return err
}

// GetSessionMappings 返回指定会话的全部映射，按伪造值长度降序排列（最长的在前）。
// 此排序对还原阶段至关重要，可避免短伪造值的部分匹配导致错误还原。
func (s *MappingStore) GetSessionMappings(sessionID string) ([]MappingEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		"SELECT id, session_id, original, fake, type, created_at FROM mappings WHERE session_id = ? ORDER BY LENGTH(fake) DESC",
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []MappingEntry
	for rows.Next() {
		var e MappingEntry
		var createdUnix int64
		var typeStr string
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Original, &e.Fake, &typeStr, &createdUnix); err != nil {
			return nil, err
		}
		e.Type = EntityType(typeStr)
		e.CreatedAt = time.Unix(createdUnix, 0)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Cleanup 删除创建时间早于指定时刻的过期映射，用于会话生命周期管理。
func (s *MappingStore) Cleanup(before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		"DELETE FROM mappings WHERE created_at < ?",
		before.Unix(),
	)
	return err
}
