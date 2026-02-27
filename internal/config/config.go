package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Port             int
	DatabasePath     string
	SessionSecret    string
	EncryptionSecret string
	CSRFKey          string
	DataDir          string
	LogLevel         string
	BaseURL          string
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:         envInt("PORT", 8080),
		DatabasePath: envString("DATABASE_PATH", "data/shelterkin.db"),
		DataDir:      envString("DATA_DIR", "data"),
		LogLevel:     envString("LOG_LEVEL", "info"),
		BaseURL:      envString("BASE_URL", "http://localhost:8080"),
	}

	var missing []string

	cfg.SessionSecret = os.Getenv("SESSION_SECRET")
	if len(cfg.SessionSecret) < 32 {
		missing = append(missing, "SESSION_SECRET (must be at least 32 characters)")
	}

	cfg.EncryptionSecret = os.Getenv("ENCRYPTION_SECRET")
	if len(cfg.EncryptionSecret) < 16 {
		missing = append(missing, "ENCRYPTION_SECRET (must be at least 16 characters)")
	}

	cfg.CSRFKey = os.Getenv("CSRF_KEY")
	if len(cfg.CSRFKey) != 32 {
		missing = append(missing, "CSRF_KEY (must be exactly 32 characters)")
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing or invalid environment variables: %v", missing)
	}

	return cfg, nil
}

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
