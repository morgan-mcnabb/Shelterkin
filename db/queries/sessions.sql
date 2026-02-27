-- name: CreateSession :one
INSERT INTO sessions (id, user_id, household_id, ip_address, user_agent, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetSessionByID :one
SELECT * FROM sessions WHERE id = ? AND expires_at > strftime('%Y-%m-%dT%H:%M:%SZ', 'now');

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= strftime('%Y-%m-%dT%H:%M:%SZ', 'now');

-- name: DeleteSessionsByUser :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: UpdateSessionLastActive :exec
UPDATE sessions SET last_active_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE id = ?;
