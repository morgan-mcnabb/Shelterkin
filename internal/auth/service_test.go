package auth

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/shelterkin/shelterkin/internal/apperror"
	"github.com/shelterkin/shelterkin/internal/db/dbgen"
	"github.com/shelterkin/shelterkin/internal/testutil"
	"github.com/shelterkin/shelterkin/internal/ulid"
	"golang.org/x/crypto/bcrypt"
)

func setupService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db := testutil.NewTestDB(t)
	enc := testutil.NewTestEncryptor(t)
	hmac := testutil.NewTestHMAC(t)
	svc := NewService(db, enc, hmac)
	return svc, db
}

func registerFirstUser(t *testing.T, svc *Service) *dbgen.Session {
	t.Helper()
	session, appErr := svc.Register(context.Background(), RegisterInput{
		Email:         "admin@test.com",
		Password:      "password123",
		DisplayName:   "Admin User",
		HouseholdName: "Test Household",
		IPAddress:     "127.0.0.1",
		UserAgent:     "test-agent",
	})
	if appErr != nil {
		t.Fatalf("registering first user: %v", appErr)
	}
	return session
}

// --- Login Tests ---

func TestLogin_HappyPath(t *testing.T) {
	svc, _ := setupService(t)
	registerFirstUser(t, svc)

	session, appErr := svc.Login(context.Background(), "admin@test.com", "password123", "127.0.0.1", "test-agent")
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
	if session == nil {
		t.Fatal("expected session, got nil")
	}
	if session.UserID == "" {
		t.Fatal("expected non-empty user ID in session")
	}
}

func TestLogin_InvalidEmail(t *testing.T) {
	svc, _ := setupService(t)

	_, appErr := svc.Login(context.Background(), "nonexistent@test.com", "password123", "127.0.0.1", "test-agent")
	if appErr == nil {
		t.Fatal("expected error, got nil")
	}
	if appErr.Type != apperror.TypeUnauthorized {
		t.Fatalf("expected Unauthorized, got %v", appErr.Type)
	}
	if appErr.Message != "Invalid email or password" {
		t.Fatalf("expected generic error message, got %q", appErr.Message)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	svc, _ := setupService(t)
	registerFirstUser(t, svc)

	_, appErr := svc.Login(context.Background(), "admin@test.com", "wrongpassword", "127.0.0.1", "test-agent")
	if appErr == nil {
		t.Fatal("expected error, got nil")
	}
	if appErr.Type != apperror.TypeUnauthorized {
		t.Fatalf("expected Unauthorized, got %v", appErr.Type)
	}
}

func TestLogin_EmptyFields(t *testing.T) {
	svc, _ := setupService(t)

	_, appErr := svc.Login(context.Background(), "", "", "127.0.0.1", "test-agent")
	if appErr == nil {
		t.Fatal("expected error, got nil")
	}
	if appErr.Type != apperror.TypeValidation {
		t.Fatalf("expected Validation, got %v", appErr.Type)
	}
}

func TestLogin_RateLimitByEmail(t *testing.T) {
	svc, _ := setupService(t)
	registerFirstUser(t, svc)

	for i := 0; i < maxFailedLoginsByEmail; i++ {
		svc.Login(context.Background(), "admin@test.com", "wrong", fmt.Sprintf("192.168.1.%d", i), "test-agent")
	}

	_, appErr := svc.Login(context.Background(), "admin@test.com", "password123", "10.0.0.1", "test-agent")
	if appErr == nil {
		t.Fatal("expected rate limit error, got nil")
	}
	if appErr.Type != apperror.TypeRateLimited {
		t.Fatalf("expected RateLimited, got %v", appErr.Type)
	}
}

func TestLogin_RateLimitByIP(t *testing.T) {
	svc, _ := setupService(t)

	for i := 0; i < maxFailedLoginsByIP; i++ {
		email := ulid.New() + "@test.com"
		svc.Login(context.Background(), email, "wrong", "10.0.0.1", "test-agent")
	}

	_, appErr := svc.Login(context.Background(), "any@test.com", "any", "10.0.0.1", "test-agent")
	if appErr == nil {
		t.Fatal("expected rate limit error, got nil")
	}
	if appErr.Type != apperror.TypeRateLimited {
		t.Fatalf("expected RateLimited, got %v", appErr.Type)
	}
}

func TestLogin_SoftDeletedUser(t *testing.T) {
	svc, db := setupService(t)
	session := registerFirstUser(t, svc)

	q := dbgen.New(db)
	q.SoftDeleteUser(context.Background(), dbgen.SoftDeleteUserParams{
		ID:          session.UserID,
		HouseholdID: session.HouseholdID,
	})

	_, appErr := svc.Login(context.Background(), "admin@test.com", "password123", "127.0.0.1", "test-agent")
	if appErr == nil {
		t.Fatal("expected error for soft-deleted user, got nil")
	}
	if appErr.Type != apperror.TypeUnauthorized {
		t.Fatalf("expected Unauthorized, got %v", appErr.Type)
	}
}

// --- Registration Tests ---

func TestRegister_FirstUser_HappyPath(t *testing.T) {
	svc, db := setupService(t)

	session, appErr := svc.Register(context.Background(), RegisterInput{
		Email:         "admin@test.com",
		Password:      "password123",
		DisplayName:   "Admin User",
		HouseholdName: "Test Household",
		IPAddress:     "127.0.0.1",
		UserAgent:     "test-agent",
	})
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
	if session == nil {
		t.Fatal("expected session, got nil")
	}

	// verify user was created as admin
	q := dbgen.New(db)
	user, err := q.GetUserByEmailHash(context.Background(), svc.hmac.Hash("admin@test.com"))
	if err != nil {
		t.Fatalf("fetching user: %v", err)
	}
	if user.Role != "admin" {
		t.Fatalf("expected role admin, got %q", user.Role)
	}

	// verify household was created
	household, err := q.GetHouseholdByID(context.Background(), user.HouseholdID)
	if err != nil {
		t.Fatalf("fetching household: %v", err)
	}
	if household.ID == "" {
		t.Fatal("expected household to exist")
	}
}

func TestRegister_ViaInvite_HappyPath(t *testing.T) {
	svc, db := setupService(t)
	registerFirstUser(t, svc)

	// find the admin user to get household ID
	q := dbgen.New(db)
	admin, _ := q.GetUserByEmailHash(context.Background(), svc.hmac.Hash("admin@test.com"))

	// create an invite
	inviteToken := "test-invite-token-123"
	q.CreateInvite(context.Background(), dbgen.CreateInviteParams{
		ID:          ulid.New(),
		HouseholdID: admin.HouseholdID,
		InvitedBy:   admin.ID,
		TokenHash:   svc.hmac.Hash(inviteToken),
		Role:        "caregiver",
		ExpiresAt:   time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
	})

	session, appErr := svc.Register(context.Background(), RegisterInput{
		Email:       "caregiver@test.com",
		Password:    "password123",
		DisplayName: "Caregiver User",
		InviteToken: inviteToken,
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	})
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
	if session.HouseholdID != admin.HouseholdID {
		t.Fatalf("expected household %q, got %q", admin.HouseholdID, session.HouseholdID)
	}

	// verify user has correct role
	user, _ := q.GetUserByEmailHash(context.Background(), svc.hmac.Hash("caregiver@test.com"))
	if user.Role != "caregiver" {
		t.Fatalf("expected role caregiver, got %q", user.Role)
	}
}

func TestRegister_DuplicateEmail(t *testing.T) {
	svc, _ := setupService(t)
	registerFirstUser(t, svc)

	_, appErr := svc.Register(context.Background(), RegisterInput{
		Email:         "admin@test.com",
		Password:      "password123",
		DisplayName:   "Another User",
		HouseholdName: "Another Household",
		IPAddress:     "127.0.0.1",
		UserAgent:     "test-agent",
	})
	if appErr == nil {
		t.Fatal("expected error for duplicate email, got nil")
	}
	if appErr.Type != apperror.TypeConflict {
		t.Fatalf("expected Conflict, got %v", appErr.Type)
	}
}

func TestRegister_InvalidInviteToken(t *testing.T) {
	svc, _ := setupService(t)

	_, appErr := svc.Register(context.Background(), RegisterInput{
		Email:       "new@test.com",
		Password:    "password123",
		DisplayName: "New User",
		InviteToken: "bogus-token",
		IPAddress:   "127.0.0.1",
		UserAgent:   "test-agent",
	})
	if appErr == nil {
		t.Fatal("expected error for invalid invite, got nil")
	}
	if appErr.Type != apperror.TypeValidation {
		t.Fatalf("expected Validation, got %v", appErr.Type)
	}
}

func TestRegister_ValidationErrors(t *testing.T) {
	svc, _ := setupService(t)

	_, appErr := svc.Register(context.Background(), RegisterInput{
		Email:       "",
		Password:    "short",
		DisplayName: "",
		IPAddress:   "127.0.0.1",
	})
	if appErr == nil {
		t.Fatal("expected validation error, got nil")
	}
	if appErr.Type != apperror.TypeValidation {
		t.Fatalf("expected Validation, got %v", appErr.Type)
	}
}

func TestRegister_EncryptionRoundTrip(t *testing.T) {
	svc, db := setupService(t)
	registerFirstUser(t, svc)

	q := dbgen.New(db)
	user, _ := q.GetUserByEmailHash(context.Background(), svc.hmac.Hash("admin@test.com"))

	decryptedEmail, err := svc.enc.Decrypt(user.EmailEnc)
	if err != nil {
		t.Fatalf("decrypting email: %v", err)
	}
	if decryptedEmail != "admin@test.com" {
		t.Fatalf("expected email %q, got %q", "admin@test.com", decryptedEmail)
	}

	decryptedName, err := svc.enc.Decrypt(user.DisplayNameEnc)
	if err != nil {
		t.Fatalf("decrypting display name: %v", err)
	}
	if decryptedName != "Admin User" {
		t.Fatalf("expected display name %q, got %q", "Admin User", decryptedName)
	}
}

// --- Session Tests ---

func TestValidateSession_HappyPath(t *testing.T) {
	svc, _ := setupService(t)
	session := registerFirstUser(t, svc)

	authUser, appErr := svc.ValidateSession(context.Background(), session.ID)
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
	if authUser.ID != session.UserID {
		t.Fatalf("expected user ID %q, got %q", session.UserID, authUser.ID)
	}
	if authUser.Role != "admin" {
		t.Fatalf("expected role admin, got %q", authUser.Role)
	}
	if authUser.SessionID != session.ID {
		t.Fatalf("expected session ID %q, got %q", session.ID, authUser.SessionID)
	}
}

func TestValidateSession_Expired(t *testing.T) {
	svc, db := setupService(t)
	session := registerFirstUser(t, svc)

	// manually expire the session
	db.ExecContext(context.Background(),
		"UPDATE sessions SET expires_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-1 hour') WHERE id = ?",
		session.ID)

	_, appErr := svc.ValidateSession(context.Background(), session.ID)
	if appErr == nil {
		t.Fatal("expected error for expired session, got nil")
	}
	if appErr.Type != apperror.TypeUnauthorized {
		t.Fatalf("expected Unauthorized, got %v", appErr.Type)
	}
}

func TestValidateSession_NonExistent(t *testing.T) {
	svc, _ := setupService(t)

	_, appErr := svc.ValidateSession(context.Background(), "nonexistent-session-id")
	if appErr == nil {
		t.Fatal("expected error, got nil")
	}
	if appErr.Type != apperror.TypeUnauthorized {
		t.Fatalf("expected Unauthorized, got %v", appErr.Type)
	}
}

func TestValidateSession_SoftDeletedUser(t *testing.T) {
	svc, db := setupService(t)
	session := registerFirstUser(t, svc)

	q := dbgen.New(db)
	q.SoftDeleteUser(context.Background(), dbgen.SoftDeleteUserParams{
		ID:          session.UserID,
		HouseholdID: session.HouseholdID,
	})

	_, appErr := svc.ValidateSession(context.Background(), session.ID)
	if appErr == nil {
		t.Fatal("expected error for deactivated user, got nil")
	}
	if appErr.Type != apperror.TypeUnauthorized {
		t.Fatalf("expected Unauthorized, got %v", appErr.Type)
	}
}

func TestLogout_DeletesSession(t *testing.T) {
	svc, _ := setupService(t)
	session := registerFirstUser(t, svc)

	appErr := svc.Logout(context.Background(), session.ID)
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}

	_, appErr = svc.ValidateSession(context.Background(), session.ID)
	if appErr == nil {
		t.Fatal("expected error after logout, got nil")
	}
}

// --- Household Isolation ---

func TestLogin_ReturnsCorrectHousehold(t *testing.T) {
	svc, db := setupService(t)

	// register household A
	sessionA, _ := svc.Register(context.Background(), RegisterInput{
		Email:         "usera@test.com",
		Password:      "password123",
		DisplayName:   "User A",
		HouseholdName: "Household A",
		IPAddress:     "127.0.0.1",
		UserAgent:     "test-agent",
	})

	// register household B
	sessionB, _ := svc.Register(context.Background(), RegisterInput{
		Email:         "userb@test.com",
		Password:      "password123",
		DisplayName:   "User B",
		HouseholdName: "Household B",
		IPAddress:     "127.0.0.1",
		UserAgent:     "test-agent",
	})

	if sessionA.HouseholdID == sessionB.HouseholdID {
		t.Fatal("expected different household IDs")
	}

	// login as user A, verify correct household
	loginA, appErr := svc.Login(context.Background(), "usera@test.com", "password123", "127.0.0.1", "test-agent")
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
	if loginA.HouseholdID != sessionA.HouseholdID {
		t.Fatalf("expected household %q, got %q", sessionA.HouseholdID, loginA.HouseholdID)
	}

	// login as user B, verify correct household
	loginB, appErr := svc.Login(context.Background(), "userb@test.com", "password123", "127.0.0.1", "test-agent")
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
	if loginB.HouseholdID != sessionB.HouseholdID {
		t.Fatalf("expected household %q, got %q", sessionB.HouseholdID, loginB.HouseholdID)
	}

	// verify no cross-contamination in DB
	q := dbgen.New(db)
	usersA, _ := q.ListUsersByHousehold(context.Background(), sessionA.HouseholdID)
	usersB, _ := q.ListUsersByHousehold(context.Background(), sessionB.HouseholdID)
	if len(usersA) != 1 || len(usersB) != 1 {
		t.Fatalf("expected 1 user per household, got %d and %d", len(usersA), len(usersB))
	}
}

// --- Password Hashing ---

func TestRegister_PasswordIsBcryptHashed(t *testing.T) {
	svc, db := setupService(t)
	registerFirstUser(t, svc)

	q := dbgen.New(db)
	user, _ := q.GetUserByEmailHash(context.Background(), svc.hmac.Hash("admin@test.com"))

	if !user.PasswordHash.Valid {
		t.Fatal("expected password hash to be set")
	}

	// verify it's a valid bcrypt hash
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash.String), []byte("password123"))
	if err != nil {
		t.Fatalf("password hash verification failed: %v", err)
	}

	// verify plaintext is not stored
	if user.PasswordHash.String == "password123" {
		t.Fatal("password stored in plaintext")
	}
}
