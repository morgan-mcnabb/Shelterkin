-- name: CreateHousehold :one
INSERT INTO households (id, name_enc, encryption_salt, onboarding_progress, settings)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetHouseholdByID :one
SELECT * FROM households WHERE id = ?;

-- name: UpdateHouseholdName :exec
UPDATE households SET name_enc = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE id = ?;

-- name: UpdateHouseholdOnboarding :exec
UPDATE households SET onboarding_progress = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE id = ?;

-- name: UpdateHouseholdSettings :exec
UPDATE households SET settings = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
WHERE id = ?;
