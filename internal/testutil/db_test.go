package testutil

import (
	"testing"
)

func TestNewTestDBRunsMigrations(t *testing.T) {
	db := NewTestDB(t)

	// verify all 7 tables exist by querying sqlite_master
	tables := []string{"config", "households", "users", "sessions", "login_attempts", "invites", "audit_log"}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

func TestNewTestDBForeignKeysEnabled(t *testing.T) {
	db := NewTestDB(t)

	var fkEnabled int
	err := db.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled)
	if err != nil {
		t.Fatalf("querying foreign_keys pragma: %v", err)
	}
	if fkEnabled != 1 {
		t.Error("expected foreign_keys to be enabled")
	}
}
