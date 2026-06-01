package storage

import (
	"database/sql"
	"fmt"

	"github.com/JishiTeam-J1wa/AoEo/core"
	_ "modernc.org/sqlite"
)

// NewSQLite opens a SQLite-backed storage.
// Use ":memory:" for an in-memory database (useful for tests).
func NewSQLite(dsn string) (core.Storage, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &sqlStorage{
		db: db,
		placeholder: func(n int) string { return "?" },
	}
	if err := s.createSchema("INTEGER PRIMARY KEY AUTOINCREMENT"); err != nil {
		return nil, err
	}
	return s, nil
}
