-- name: GetConfig :one
SELECT value FROM config WHERE key = ?;

-- name: SetConfig :exec
INSERT INTO config (key, value, updated_at)
VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;

-- name: DeleteConfig :exec
DELETE FROM config WHERE key = ?;
