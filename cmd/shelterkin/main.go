package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shelterkin/shelterkin/db"
	"github.com/shelterkin/shelterkin/internal/config"
	"github.com/shelterkin/shelterkin/internal/crypto"
	"github.com/shelterkin/shelterkin/internal/database"
	"github.com/shelterkin/shelterkin/internal/db/dbgen"
	"github.com/shelterkin/shelterkin/internal/server"
	"github.com/shelterkin/shelterkin/static"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logLevel := parseLogLevel(cfg.LogLevel)
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

	slog.Info("starting shelterkin", "version", version, "port", cfg.Port)

	if err := os.MkdirAll(cfg.DataDir, 0750); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	sqlDB, err := database.Open(cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer sqlDB.Close()

	if err := database.RunMigrations(sqlDB, db.MigrationsFS, "migrations"); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	salt, err := getOrCreateEncryptionSalt(sqlDB)
	if err != nil {
		return fmt.Errorf("initializing encryption salt: %w", err)
	}

	key := crypto.DeriveKey(cfg.EncryptionSecret, salt)
	enc, err := crypto.NewEncryptor(key)
	if err != nil {
		return fmt.Errorf("initializing encryptor: %w", err)
	}

	// derive a separate key for hmac lookups
	hmacKey := crypto.DeriveKey(cfg.EncryptionSecret+"-hmac", salt)
	hmac := crypto.NewHMAC(hmacKey)

	if err := verifyEncryptionKey(sqlDB, enc); err != nil {
		return fmt.Errorf("encryption key verification failed: %w", err)
	}

	srv := server.New(cfg, sqlDB, enc, hmac, static.FS)

	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server listening", "addr", fmt.Sprintf("http://localhost:%d", cfg.Port))
		errCh <- srv.Start()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case sig := <-shutdownCh:
		slog.Info("shutdown signal received", "signal", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

const (
	configKeyEncryptionSalt = "encryption_salt"
	configKeyEncryptionTest = "encryption_test_value"
	encryptionTestPlaintext = "shelterkin-encryption-verify"
)

func getOrCreateEncryptionSalt(sqlDB *sql.DB) ([]byte, error) {
	queries := dbgen.New(sqlDB)
	ctx := context.Background()

	saltB64, err := queries.GetConfig(ctx, configKeyEncryptionSalt)
	if err == nil {
		return base64.StdEncoding.DecodeString(saltB64)
	}

	// first run: generate and store a new salt
	salt, err := crypto.GenerateSalt()
	if err != nil {
		return nil, fmt.Errorf("generating salt: %w", err)
	}

	encoded := base64.StdEncoding.EncodeToString(salt)
	if err := queries.SetConfig(ctx, dbgen.SetConfigParams{Key: configKeyEncryptionSalt, Value: encoded}); err != nil {
		return nil, fmt.Errorf("storing salt: %w", err)
	}

	slog.Info("generated new encryption salt")
	return salt, nil
}

func verifyEncryptionKey(sqlDB *sql.DB, enc *crypto.Encryptor) error {
	queries := dbgen.New(sqlDB)
	ctx := context.Background()

	stored, err := queries.GetConfig(ctx, configKeyEncryptionTest)
	if err != nil {
		// first run: encrypt the test value and store it
		encrypted, encErr := enc.Encrypt(encryptionTestPlaintext)
		if encErr != nil {
			return fmt.Errorf("encrypting test value: %w", encErr)
		}
		if storeErr := queries.SetConfig(ctx, dbgen.SetConfigParams{Key: configKeyEncryptionTest, Value: encrypted}); storeErr != nil {
			return fmt.Errorf("storing test value: %w", storeErr)
		}
		slog.Info("stored encryption verification token")
		return nil
	}

	// subsequent runs: verify we can still decrypt
	decrypted, err := enc.Decrypt(stored)
	if err != nil {
		return fmt.Errorf("ENCRYPTION_SECRET appears to have changed — cannot decrypt existing data: %w", err)
	}
	if decrypted != encryptionTestPlaintext {
		return fmt.Errorf("encryption verification failed: decrypted value does not match expected")
	}

	return nil
}
