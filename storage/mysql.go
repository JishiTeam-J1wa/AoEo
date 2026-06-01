package storage

import (
	"database/sql"
	"fmt"

	"github.com/JishiTeam-J1wa/AoEo/core"
	_ "github.com/go-sql-driver/mysql"
)

// NewMySQL opens a MySQL-backed storage.
// DSN format: "user:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True"
func NewMySQL(dsn string) (core.Storage, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	s := &sqlStorage{
		db: db,
		placeholder: func(n int) string { return "?" },
	}
	if err := s.createSchema("INT AUTO_INCREMENT PRIMARY KEY"); err != nil {
		return nil, err
	}
	return s, nil
}
