-- name: UpsertModule :exec
INSERT INTO modules(id, name, version, contract_version, enabled, installed_at, updated_at)
VALUES (?, ?, ?, ?, 1, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    version = excluded.version,
    contract_version = excluded.contract_version,
    updated_at = excluded.updated_at;

-- name: ListModuleStates :many
SELECT id, enabled FROM modules ORDER BY id;

-- name: SetModuleEnabled :execrows
UPDATE modules SET enabled = ?, updated_at = ? WHERE id = ?;

