-- name: CreateUser :one
INSERT INTO users (id, household_id, email_enc, email_hash, password_hash, display_name_enc, role, auth_provider, timezone)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ? AND household_id = ? AND deleted_at IS NULL;

-- name: GetUserByEmailHash :one
SELECT * FROM users WHERE email_hash = ? AND deleted_at IS NULL;

-- name: ListUsersByHousehold :many
SELECT * FROM users WHERE household_id = ? AND deleted_at IS NULL ORDER BY created_at;

-- name: UpdateUserLastLogin :exec
UPDATE users SET last_login_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE id = ?;

-- name: UpdateUserRole :exec
UPDATE users SET role = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE id = ? AND household_id = ?;

-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE id = ? AND household_id = ?;
