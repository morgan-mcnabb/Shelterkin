package config

import (
	"os"
	"testing"
)

func setTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SESSION_SECRET", "test-session-secret-that-is-long-enough!!")
	t.Setenv("ENCRYPTION_SECRET", "test-encryption-secret")
	t.Setenv("CSRF_KEY", "exactly-32-characters-long!!!!!!")
}

func TestLoadDefaults(t *testing.T) {
	setTestEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Port)
	}
	if cfg.DatabasePath != "data/shelterkin.db" {
		t.Errorf("expected default database path, got %q", cfg.DatabasePath)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected log level 'info', got %q", cfg.LogLevel)
	}
}

func TestLoadCustomPort(t *testing.T) {
	setTestEnv(t)
	t.Setenv("PORT", "9090")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Port)
	}
}

func TestLoadMissingSessionSecret(t *testing.T) {
	t.Setenv("SESSION_SECRET", "")
	t.Setenv("ENCRYPTION_SECRET", "test-encryption-secret")
	t.Setenv("CSRF_KEY", "exactly-32-characters-long!!!!!!")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing SESSION_SECRET")
	}
}

func TestLoadMissingEncryptionSecret(t *testing.T) {
	t.Setenv("SESSION_SECRET", "test-session-secret-that-is-long-enough!!")
	t.Setenv("ENCRYPTION_SECRET", "")
	t.Setenv("CSRF_KEY", "exactly-32-characters-long!!!!!!")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing ENCRYPTION_SECRET")
	}
}

func TestLoadInvalidCSRFKey(t *testing.T) {
	t.Setenv("SESSION_SECRET", "test-session-secret-that-is-long-enough!!")
	t.Setenv("ENCRYPTION_SECRET", "test-encryption-secret")
	t.Setenv("CSRF_KEY", "too-short")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid CSRF_KEY length")
	}
}

func TestLoadAllMissing(t *testing.T) {
	os.Clearenv()

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when all secrets missing")
	}
}
