-- name: UpsertBuiltinModule :execrows
INSERT INTO modules(id, name, version, contract_version, enabled, installed_at, updated_at, kind)
VALUES (?, ?, ?, ?, 1, ?, ?, 'builtin')
ON CONFLICT(id) DO UPDATE SET
    name = excluded.name,
    version = excluded.version,
    contract_version = excluded.contract_version,
    updated_at = excluded.updated_at
WHERE modules.kind = 'builtin';

-- name: ListModuleStates :many
SELECT id, enabled, kind FROM modules ORDER BY id;

-- name: SetModuleEnabled :execrows
UPDATE modules SET enabled = ?, updated_at = ? WHERE id = ?;

-- name: GetModule :one
SELECT id, name, version, contract_version, enabled, installed_at, updated_at, kind
FROM modules WHERE id = ?;

-- name: InsertModule :exec
INSERT INTO modules(id, name, version, contract_version, enabled, installed_at, updated_at, kind)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateModuleIdentity :exec
UPDATE modules SET name = ?, version = ?, updated_at = ? WHERE id = ? AND kind = ?;

-- name: DeleteExternalModuleRow :execrows
DELETE FROM modules WHERE id = ? AND kind = 'external';

-- name: InsertExternalModule :exec
INSERT INTO external_modules(
    module_id, registration_id, connection_revision, base_url, allow_non_local, service_token,
    protocol_version, manifest_json, generation, observed_generation, observed_enabled, health,
    last_error, last_checked_at, last_success_at, instance_id, connection_note, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListExternalModules :many
SELECT
    m.id, m.name, m.version, m.enabled, m.installed_at, m.updated_at,
    e.registration_id, e.connection_revision, e.base_url, e.allow_non_local, e.service_token,
    e.protocol_version, e.manifest_json, e.generation, e.observed_generation, e.observed_enabled,
    e.health, e.last_error, e.last_checked_at, e.last_success_at, e.instance_id, e.connection_note,
    e.created_at, e.updated_at
FROM external_modules e
JOIN modules m ON m.id = e.module_id
ORDER BY m.id;

-- name: GetExternalModule :one
SELECT
    m.id, m.name, m.version, m.enabled, m.installed_at, m.updated_at,
    e.registration_id, e.connection_revision, e.base_url, e.allow_non_local, e.service_token,
    e.protocol_version, e.manifest_json, e.generation, e.observed_generation, e.observed_enabled,
    e.health, e.last_error, e.last_checked_at, e.last_success_at, e.instance_id, e.connection_note,
    e.created_at, e.updated_at
FROM external_modules e
JOIN modules m ON m.id = e.module_id
WHERE m.id = ?;

-- name: UpdateExternalConnection :exec
UPDATE external_modules SET
    connection_revision = ?,
    base_url = ?,
    allow_non_local = ?,
    service_token = ?,
    protocol_version = ?,
    manifest_json = ?,
    observed_generation = NULL,
    observed_enabled = NULL,
    health = 'unknown',
    last_error = NULL,
    instance_id = NULL,
    connection_note = ?,
    updated_at = ?
WHERE module_id = ?;

-- name: UpdateExternalGeneration :exec
UPDATE external_modules SET generation = ?, updated_at = ? WHERE module_id = ?;

-- name: UpdateExternalObserved :exec
UPDATE external_modules SET
    observed_generation = ?,
    observed_enabled = ?,
    health = ?,
    last_error = ?,
    last_checked_at = ?,
    last_success_at = ?,
    instance_id = ?,
    updated_at = ?
WHERE module_id = ?;

-- name: UpdateExternalManifest :exec
UPDATE external_modules SET
    protocol_version = ?,
    manifest_json = ?,
    health = ?,
    last_error = ?,
    last_checked_at = ?,
    last_success_at = ?,
    updated_at = ?
WHERE module_id = ?;
