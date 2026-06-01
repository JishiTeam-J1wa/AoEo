package storage

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/JishiTeam-J1wa/AoEo/core"
	_ "github.com/lib/pq"
)

// NewPostgres opens a PostgreSQL-backed storage.
// DSN format: "postgres://user:password@host:port/dbname?sslmode=disable"
func NewPostgres(dsn string) (core.Storage, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	s := &sqlStorage{
		db: db,
		placeholder: func(n int) string { return "$" + strconv.Itoa(n) },
	}
	if err := s.createSchema("SERIAL PRIMARY KEY"); err != nil {
		return nil, err
	}
	return s, nil
}
