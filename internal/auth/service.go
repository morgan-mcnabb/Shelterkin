package auth

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/shelterkin/shelterkin/internal/apperror"
	"github.com/shelterkin/shelterkin/internal/crypto"
	"github.com/shelterkin/shelterkin/internal/db/dbgen"
	"github.com/shelterkin/shelterkin/internal/ulid"
	"golang.org/x/crypto/bcrypt"
)

const (
	maxFailedLoginsByEmail = 5
	maxFailedLoginsByIP    = 20
	rateLimitWindow        = "-15 minutes"
	rateLimitRetryAfter    = 15 * time.Minute
	sessionDuration        = 30 * 24 * time.Hour
	bcryptCost             = bcrypt.DefaultCost
)

type Service struct {
	queries *dbgen.Queries
	db      *sql.DB
	enc     *crypto.Encryptor
	hmac    *crypto.HMACHasher
}

func NewService(db *sql.DB, enc *crypto.Encryptor, hmac *crypto.HMACHasher) *Service {
	return &Service{
		queries: dbgen.New(db),
		db:      db,
		enc:     enc,
		hmac:    hmac,
	}
}

type RegisterInput struct {
	Email         string
	Password      string
	DisplayName   string
	InviteToken   string
	HouseholdName string
	IPAddress     string
	UserAgent     string
}

func (s *Service) Login(ctx context.Context, email, password, ipAddress, userAgent string) (*dbgen.Session, *apperror.Error) {
	ve := &apperror.ValidationErrors{}
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		ve.Add("email", "Email is required")
	}
	if password == "" {
		ve.Add("password", "Password is required")
	}
	if ve.HasErrors() {
		return nil, ve.ToError()
	}

	emailHash := s.hmac.Hash(email)

	if appErr := s.checkRateLimits(ctx, emailHash, ipAddress); appErr != nil {
		return nil, appErr
	}

	user, err := s.queries.GetUserByEmailHash(ctx, emailHash)
	if err != nil {
		s.recordLoginAttempt(ctx, emailHash, ipAddress, false)
		return nil, apperror.Unauthorized("Invalid email or password")
	}

	if !user.PasswordHash.Valid {
		s.recordLoginAttempt(ctx, emailHash, ipAddress, false)
		return nil, apperror.Unauthorized("Invalid email or password")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash.String), []byte(password)); err != nil {
		s.recordLoginAttempt(ctx, emailHash, ipAddress, false)
		return nil, apperror.Unauthorized("Invalid email or password")
	}

	s.recordLoginAttempt(ctx, emailHash, ipAddress, true)

	session, appErr := s.createSession(ctx, user.ID, user.HouseholdID, ipAddress, userAgent)
	if appErr != nil {
		return nil, appErr
	}

	if err := s.queries.UpdateUserLastLogin(ctx, user.ID); err != nil {
		slog.Error("failed to update last login", "user_id", user.ID, "error", err)
	}

	return &session, nil
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (*dbgen.Session, *apperror.Error) {
	ve := &apperror.ValidationErrors{}
	input.Email = strings.TrimSpace(strings.ToLower(input.Email))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.HouseholdName = strings.TrimSpace(input.HouseholdName)

	if input.Email == "" {
		ve.Add("email", "Email is required")
	}
	if len(input.Password) < 8 {
		ve.Add("password", "Password must be at least 8 characters")
	}
	if input.DisplayName == "" {
		ve.Add("display_name", "Display name is required")
	}
	if input.InviteToken == "" && input.HouseholdName == "" {
		ve.Add("household_name", "Household name is required")
	}
	if ve.HasErrors() {
		return nil, ve.ToError()
	}

	emailHash := s.hmac.Hash(input.Email)
	_, err := s.queries.GetUserByEmailHash(ctx, emailHash)
	if err == nil {
		return nil, apperror.Conflict("An account with this email already exists")
	}

	if input.InviteToken != "" {
		return s.registerViaInvite(ctx, input, emailHash)
	}
	return s.registerFirstUser(ctx, input, emailHash)
}

func (s *Service) registerFirstUser(ctx context.Context, input RegisterInput, emailHash string) (*dbgen.Session, *apperror.Error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, apperror.Internal("Failed to start transaction", err)
	}
	defer tx.Rollback()

	qtx := s.queries.WithTx(tx)

	encHouseholdName, err := s.enc.Encrypt(input.HouseholdName)
	if err != nil {
		return nil, apperror.Internal("Failed to encrypt household name", err)
	}

	household, err := qtx.CreateHousehold(ctx, dbgen.CreateHouseholdParams{
		ID:                 ulid.New(),
		NameEnc:            encHouseholdName,
		EncryptionSalt:     "",
		OnboardingProgress: `{"step":"profile"}`,
		Settings:           "{}",
	})
	if err != nil {
		return nil, apperror.Internal("Failed to create household", err)
	}

	user, appErr := s.createUser(ctx, qtx, input, emailHash, household.ID, "admin")
	if appErr != nil {
		return nil, appErr
	}

	session, appErr := s.createSessionTx(ctx, qtx, user.ID, household.ID, input.IPAddress, input.UserAgent)
	if appErr != nil {
		return nil, appErr
	}

	if err := tx.Commit(); err != nil {
		return nil, apperror.Internal("Failed to commit registration", err)
	}

	return &session, nil
}

func (s *Service) registerViaInvite(ctx context.Context, input RegisterInput, emailHash string) (*dbgen.Session, *apperror.Error) {
	tokenHash := s.hmac.Hash(input.InviteToken)
	invite, err := s.queries.GetInviteByToken(ctx, tokenHash)
	if err != nil {
		return nil, apperror.Validation("invite_token", "Invalid or expired invite link")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, apperror.Internal("Failed to start transaction", err)
	}
	defer tx.Rollback()

	qtx := s.queries.WithTx(tx)

	user, appErr := s.createUser(ctx, qtx, input, emailHash, invite.HouseholdID, invite.Role)
	if appErr != nil {
		return nil, appErr
	}

	if err := qtx.AcceptInvite(ctx, invite.ID); err != nil {
		return nil, apperror.Internal("Failed to accept invite", err)
	}

	session, appErr := s.createSessionTx(ctx, qtx, user.ID, invite.HouseholdID, input.IPAddress, input.UserAgent)
	if appErr != nil {
		return nil, appErr
	}

	if err := tx.Commit(); err != nil {
		return nil, apperror.Internal("Failed to commit registration", err)
	}

	return &session, nil
}

func (s *Service) ValidateSession(ctx context.Context, sessionID string) (*AuthUser, *apperror.Error) {
	row, err := s.queries.GetSessionWithUser(ctx, sessionID)
	if err != nil {
		return nil, apperror.Unauthorized("Session expired")
	}

	if row.UserDeletedAt.Valid {
		s.queries.DeleteSession(ctx, sessionID)
		return nil, apperror.Unauthorized("Account deactivated")
	}

	if err := s.queries.UpdateSessionLastActive(ctx, sessionID); err != nil {
		slog.Error("failed to update session last active", "session_id", sessionID, "error", err)
	}

	return &AuthUser{
		ID:          row.UserID,
		HouseholdID: row.HouseholdID,
		Role:        row.Role,
		SessionID:   row.SessionID,
	}, nil
}

func (s *Service) Logout(ctx context.Context, sessionID string) *apperror.Error {
	if err := s.queries.DeleteSession(ctx, sessionID); err != nil {
		return apperror.Internal("Failed to delete session", err)
	}
	return nil
}

func (s *Service) createUser(ctx context.Context, q *dbgen.Queries, input RegisterInput, emailHash, householdID, role string) (dbgen.User, *apperror.Error) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcryptCost)
	if err != nil {
		return dbgen.User{}, apperror.Internal("Failed to hash password", err)
	}

	encEmail, err := s.enc.Encrypt(input.Email)
	if err != nil {
		return dbgen.User{}, apperror.Internal("Failed to encrypt email", err)
	}

	encDisplayName, err := s.enc.Encrypt(input.DisplayName)
	if err != nil {
		return dbgen.User{}, apperror.Internal("Failed to encrypt display name", err)
	}

	user, err := q.CreateUser(ctx, dbgen.CreateUserParams{
		ID:             ulid.New(),
		HouseholdID:    householdID,
		EmailEnc:       encEmail,
		EmailHash:      emailHash,
		PasswordHash:   sql.NullString{String: string(passwordHash), Valid: true},
		DisplayNameEnc: encDisplayName,
		Role:           role,
		AuthProvider:   "local",
		Timezone:       "America/New_York",
	})
	if err != nil {
		if apperror.IsUniqueConstraintViolation(err) {
			return dbgen.User{}, apperror.Conflict("An account with this email already exists")
		}
		return dbgen.User{}, apperror.Internal("Failed to create user", err)
	}

	return user, nil
}

func (s *Service) createSession(ctx context.Context, userID, householdID, ipAddress, userAgent string) (dbgen.Session, *apperror.Error) {
	return s.createSessionTx(ctx, s.queries, userID, householdID, ipAddress, userAgent)
}

func (s *Service) createSessionTx(ctx context.Context, q *dbgen.Queries, userID, householdID, ipAddress, userAgent string) (dbgen.Session, *apperror.Error) {
	expiresAt := time.Now().UTC().Add(sessionDuration).Format(time.RFC3339)
	session, err := q.CreateSession(ctx, dbgen.CreateSessionParams{
		ID:          ulid.New(),
		UserID:      userID,
		HouseholdID: householdID,
		IpAddress:   sql.NullString{String: ipAddress, Valid: ipAddress != ""},
		UserAgent:   sql.NullString{String: userAgent, Valid: userAgent != ""},
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		return dbgen.Session{}, apperror.Internal("Failed to create session", err)
	}
	return session, nil
}

func (s *Service) checkRateLimits(ctx context.Context, emailHash, ipAddress string) *apperror.Error {
	emailCount, err := s.queries.CountRecentFailedByEmail(ctx, dbgen.CountRecentFailedByEmailParams{
		EmailHash: emailHash,
		Datetime:  rateLimitWindow,
	})
	if err == nil && emailCount >= maxFailedLoginsByEmail {
		return apperror.RateLimited("Too many login attempts. Please try again later.", rateLimitRetryAfter)
	}

	ipCount, err := s.queries.CountRecentFailedByIP(ctx, dbgen.CountRecentFailedByIPParams{
		IpAddress: ipAddress,
		Datetime:  rateLimitWindow,
	})
	if err == nil && ipCount >= maxFailedLoginsByIP {
		return apperror.RateLimited("Too many login attempts from this location. Please try again later.", rateLimitRetryAfter)
	}

	return nil
}

func (s *Service) recordLoginAttempt(ctx context.Context, emailHash, ipAddress string, succeeded bool) {
	successInt := int64(0)
	if succeeded {
		successInt = 1
	}
	err := s.queries.CreateLoginAttempt(ctx, dbgen.CreateLoginAttemptParams{
		ID:        ulid.New(),
		EmailHash: emailHash,
		IpAddress: ipAddress,
		Succeeded: successInt,
	})
	if err != nil {
		slog.Error("failed to record login attempt", "error", err)
	}
}

func ClientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.SplitN(forwarded, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
