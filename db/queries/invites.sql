-- name: CreateInvite :one
INSERT INTO invites (id, household_id, invited_by, email_enc, email_hash, token_hash, role, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetInviteByToken :one
SELECT * FROM invites
WHERE token_hash = ? AND accepted_at IS NULL
AND expires_at > strftime('%Y-%m-%dT%H:%M:%SZ', 'now');

-- name: AcceptInvite :exec
UPDATE invites SET accepted_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE id = ?;

-- name: ListPendingInvitesByHousehold :many
SELECT * FROM invites
WHERE household_id = ? AND accepted_at IS NULL
AND expires_at > strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
ORDER BY created_at;
