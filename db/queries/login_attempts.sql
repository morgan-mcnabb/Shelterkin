-- name: CreateLoginAttempt :exec
INSERT INTO login_attempts (id, email_hash, ip_address, succeeded)
VALUES (?, ?, ?, ?);

-- name: CountRecentFailedByEmail :one
SELECT COUNT(*) FROM login_attempts
WHERE email_hash = ? AND succeeded = 0
AND attempted_at > strftime('%Y-%m-%dT%H:%M:%SZ', datetime('now', ?));

-- name: CountRecentFailedByIP :one
SELECT COUNT(*) FROM login_attempts
WHERE ip_address = ? AND succeeded = 0
AND attempted_at > strftime('%Y-%m-%dT%H:%M:%SZ', datetime('now', ?));
