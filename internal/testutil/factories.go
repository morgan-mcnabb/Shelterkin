package testutil

import (
	"context"
	"database/sql"
	"testing"

	"github.com/shelterkin/shelterkin/internal/crypto"
	"github.com/shelterkin/shelterkin/internal/db/dbgen"
	"github.com/shelterkin/shelterkin/internal/ulid"
	"golang.org/x/crypto/bcrypt"
)

type householdDefaults struct {
	name string
}

type HouseholdOption func(*householdDefaults)

func WithHouseholdName(name string) HouseholdOption {
	return func(d *householdDefaults) { d.name = name }
}

func CreateTestHousehold(t *testing.T, db *sql.DB, enc *crypto.Encryptor, opts ...HouseholdOption) dbgen.Household {
	t.Helper()

	defaults := householdDefaults{
		name: "Test Household",
	}
	for _, opt := range opts {
		opt(&defaults)
	}

	params := dbgen.CreateHouseholdParams{
		ID:                 ulid.New(),
		NameEnc:            mustEncrypt(t, enc, defaults.name),
		EncryptionSalt:     "dGVzdC1zYWx0",
		OnboardingProgress: "{}",
		Settings:           "{}",
	}
	q := dbgen.New(db)
	household, err := q.CreateHousehold(context.Background(), params)
	if err != nil {
		t.Fatalf("creating test household: %v", err)
	}
	return household
}

type userDefaults struct {
	email       string
	password    string
	displayName string
	role        string
}

type UserOption func(*userDefaults)

func WithRole(role string) UserOption {
	return func(d *userDefaults) { d.role = role }
}

func WithPassword(password string) UserOption {
	return func(d *userDefaults) { d.password = password }
}

func WithEmail(email string) UserOption {
	return func(d *userDefaults) { d.email = email }
}

func WithDisplayName(name string) UserOption {
	return func(d *userDefaults) { d.displayName = name }
}

func CreateTestUser(t *testing.T, db *sql.DB, enc *crypto.Encryptor, hmac *crypto.HMACHasher, householdID string, opts ...UserOption) dbgen.User {
	t.Helper()

	defaults := userDefaults{
		email:       ulid.New() + "@test.com",
		password:    "testpassword123",
		displayName: "Test User",
		role:        "member",
	}
	for _, opt := range opts {
		opt(&defaults)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(defaults.password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hashing test password: %v", err)
	}

	params := dbgen.CreateUserParams{
		ID:             ulid.New(),
		HouseholdID:    householdID,
		EmailEnc:       mustEncrypt(t, enc, defaults.email),
		EmailHash:      hmac.Hash(defaults.email),
		PasswordHash:   sql.NullString{String: string(hash), Valid: true},
		DisplayNameEnc: mustEncrypt(t, enc, defaults.displayName),
		Role:           defaults.role,
		AuthProvider:   "local",
		Timezone:       "America/New_York",
	}

	q := dbgen.New(db)
	user, err := q.CreateUser(context.Background(), params)
	if err != nil {
		t.Fatalf("creating test user: %v", err)
	}
	return user
}

func mustEncrypt(t *testing.T, enc *crypto.Encryptor, plaintext string) string {
	t.Helper()
	encrypted, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypting test data: %v", err)
	}
	return encrypted
}
