package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	SessionCookieName = "shelterkin_session"
	cookieMaxAge      = 30 * 24 * 60 * 60 // 30 days
)

func signSessionID(sessionID string, secret string) string {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	payload := sessionID + "|" + timestamp
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	signature := hex.EncodeToString(mac.Sum(nil))
	return payload + "|" + signature
}

func VerifyAndExtractSessionID(cookieValue string, secret string) (string, error) {
	parts := strings.SplitN(cookieValue, "|", 3)
	if len(parts) != 3 {
		return "", fmt.Errorf("malformed session cookie")
	}
	sessionID, timestamp, signature := parts[0], parts[1], parts[2]

	payload := sessionID + "|" + timestamp
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expectedSig)) {
		return "", fmt.Errorf("invalid session cookie signature")
	}

	return sessionID, nil
}

func SetSessionCookie(w http.ResponseWriter, sessionID string, secret string, secure bool) {
	signed := signSessionID(sessionID, secret)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    signed,
		Path:     "/",
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func GetSessionCookie(r *http.Request) (string, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return "", err
	}
	return cookie.Value, nil
}

