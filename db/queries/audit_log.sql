-- name: CreateAuditLog :exec
INSERT INTO audit_log (id, household_id, user_id, action, entity_type, entity_id, metadata, ip_address)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAuditLogByHousehold :many
SELECT * FROM audit_log WHERE household_id = ?
ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListAuditLogByUser :many
SELECT * FROM audit_log WHERE user_id = ?
ORDER BY created_at DESC LIMIT ? OFFSET ?;
