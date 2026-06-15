// postgres.go 实现 PostgreSQL 存储后端的初始化与连接管理。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package storage

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	_ "github.com/lib/pq"
)

// NewPostgres 创建并返回一个基于 PostgreSQL 的存储实例。
//
// Param:
//   - dsn: string - 格式为 "postgres://user:password@host:port/dbname?sslmode=disable"
//
// Return:
//   - core.Storage: 初始化完成的 PostgreSQL 存储实例
//   - error: 连接或初始化失败时返回错误
//
// 该函数会依次完成以下步骤：
//  1. 打开数据库连接
//  2. 配置连接池参数（最大打开连接数、最大空闲连接数、连接最大生命周期）
//  3. 通过 Ping 验证连接可用性（如果 Ping 失败则关闭 db 防止资源泄露）
//  4. 创建数据库表结构（schema）
func NewPostgres(dsn string) (core.Storage, error) {
	// sql.Open 仅做延迟初始化，不会立即建立连接。
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	// SetMaxOpenConns(25)：设置最大打开连接数为 25，限制并发连接总量，
	// 避免在高并发场景下创建过多连接导致 PostgreSQL 服务端资源耗尽。
	db.SetMaxOpenConns(25)

	// SetMaxIdleConns(5)：设置最大空闲连接数为 5，保持适量的空闲连接
	// 以便复用，减少频繁创建/销毁连接的开销。
	db.SetMaxIdleConns(5)

	// SetConnMaxLifetime(5 * time.Minute)：设置连接的最大生命周期为 5 分钟。
	// PostgreSQL 默认不会主动断开空闲连接，但网络设备（如防火墙、负载均衡器）可能会在
	// 一段时间不活动后中断 TCP 连接。设置合理的生命周期可确保客户端主动回收连接，
	// 避免使用已被中间设备关闭的"死连接"。
	db.SetConnMaxLifetime(5 * time.Minute)

	// SetConnMaxIdleTime(5 * time.Minute)：设置连接的最大空闲时间为 5 分钟。
	// 超过此时间的空闲连接将被自动关闭，避免长期持有已被中间设备关闭的"死连接"。
	db.SetConnMaxIdleTime(5 * time.Minute)

	// 如果 Ping 失败，必须先关闭 db 再返回错误，否则已打开的连接池会造成资源泄露。
	if err := db.Ping(); err != nil {
		db.Close() // Bug S-17 修复：Ping 失败时关闭 db，防止资源泄露
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	// PostgreSQL 使用 "$1", "$2", "$3" ... 形式的编号占位符（positional parameters）。
	s := &sqlStorage{
		db:          db,
		placeholder: func(n int) string { return "$" + strconv.Itoa(n) },
	}

	// 使用 "SERIAL PRIMARY KEY" 作为自增主键语法。
	if err := s.createSchema("SERIAL PRIMARY KEY"); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}
