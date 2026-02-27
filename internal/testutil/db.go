package testutil

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/shelterkin/shelterkin/db"
	_ "modernc.org/sqlite"
)

func NewTestDB(t *testing.T) *sql.DB {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening test database: %v", err)
	}

	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		sqlDB.Close()
		t.Fatalf("enabling foreign keys: %v", err)
	}

	goose.SetBaseFS(db.MigrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		sqlDB.Close()
		t.Fatalf("setting goose dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		sqlDB.Close()
		t.Fatalf("running migrations: %v", err)
	}

	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}
