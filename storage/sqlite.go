package storage

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/JishiTeam-J1wa/AoEo/core"
	_ "modernc.org/sqlite"
)

// NewSQLite 创建并返回一个基于 SQLite 的存储实例。
// dsn 参数为 SQLite 的数据源名称（Data Source Name），可以是文件路径或 ":memory:"（内存数据库）。
// 该函数会依次完成以下步骤：
//  1. 打开数据库连接
//  2. 对内存数据库限制最大连接数为 1（避免因多连接导致数据不一致）
//  3. 设置 PRAGMA 优化参数（WAL 日志模式 + busy_timeout 超时）
//  4. 通过 Ping 验证连接可用性（如果 Ping 失败则关闭 db 防止资源泄露）
//  5. 创建数据库表结构（schema）
func NewSQLite(dsn string) (core.Storage, error) {
	// 第一步：打开数据库连接。sql.Open 仅做延迟初始化，不会立即建立连接。
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// 第二步：如果是内存数据库（":memory:"），必须将最大连接数限制为 1。
	// 原因：SQLite 的 :memory: 数据库是每个连接独立的，多个连接会各自拥有独立的数据库实例，
	// 导致写入和读取可能不在同一个数据库上，造成数据不一致。
	if strings.Contains(dsn, ":memory:") {
		db.SetMaxOpenConns(1)
	}

	// 第三步：设置 PRAGMA 优化参数。
	// journal_mode=WAL：启用 Write-Ahead Logging 模式，允许读写并发执行，显著提升并发性能。
	// busy_timeout=5000：当数据库被锁定时，最多等待 5000 毫秒（5秒），避免立即返回 SQLITE_BUSY 错误。
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		// 如果设置 PRAGMA 失败，需要关闭 db 以避免资源泄露。
		db.Close()
		return nil, fmt.Errorf("set pragma journal_mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set pragma busy_timeout: %w", err)
	}

	// 第四步：通过 Ping 验证数据库连接是否真正可用。
	// 如果 Ping 失败，必须先关闭 db 再返回错误，否则已打开的连接池会造成资源泄露。
	if err := db.Ping(); err != nil {
		db.Close() // Bug S-10 修复：Ping 失败时关闭 db，防止资源泄露
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	// 第五步：构建 sqlStorage 实例，设置 SQLite 专用的占位符函数（SQLite 使用 "?" 作为占位符）。
	s := &sqlStorage{
		db:          db,
		placeholder: func(n int) string { return "?" },
	}

	// 第六步：创建数据库表结构和索引。使用 "INTEGER PRIMARY KEY AUTOINCREMENT" 作为自增主键语法。
	if err := s.createSchema("INTEGER PRIMARY KEY AUTOINCREMENT"); err != nil {
		// 创建表结构失败也需要关闭 db，避免资源泄露。
		db.Close()
		return nil, err
	}

	return s, nil
}
