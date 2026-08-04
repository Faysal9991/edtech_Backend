-- name: UpsertUserByFirebaseUID :one
INSERT INTO users (id, firebase_uid, email, display_name, status, last_login_at)
VALUES (sqlc.arg(id), sqlc.arg(firebase_uid), lower(sqlc.arg(email)), sqlc.arg(display_name), 'active', now())
ON CONFLICT (firebase_uid) DO UPDATE
SET email = lower(EXCLUDED.email),
    display_name = CASE WHEN EXCLUDED.display_name = '' THEN users.display_name ELSE EXCLUDED.display_name END,
    last_login_at = now(),
    updated_at = now()
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByFirebaseUID :one
SELECT * FROM users WHERE firebase_uid = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower($1);

-- name: ListUserMemberships :many
SELECT m.id, m.organization_id, o.name AS organization_name, o.slug AS organization_slug,
       m.status, COALESCE(array_agg(r.code ORDER BY r.code) FILTER (WHERE r.code IS NOT NULL), '{}')::text[] AS roles
FROM organization_memberships m
JOIN organizations o ON o.id = m.organization_id
LEFT JOIN membership_roles mr ON mr.membership_id = m.id
LEFT JOIN roles r ON r.id = mr.role_id
WHERE m.user_id = $1 AND m.status = 'active' AND o.status = 'active'
GROUP BY m.id, o.id
ORDER BY o.name, m.id;

-- name: GetActiveMembership :one
SELECT m.id, m.organization_id, m.user_id, m.status,
       COALESCE(array_agg(r.code ORDER BY r.code) FILTER (WHERE r.code IS NOT NULL), '{}')::text[] AS roles
FROM organization_memberships m
LEFT JOIN membership_roles mr ON mr.membership_id = m.id
LEFT JOIN roles r ON r.id = mr.role_id
WHERE m.user_id = sqlc.arg(user_id) AND m.organization_id = sqlc.arg(organization_id)
GROUP BY m.id
HAVING m.status = 'active';

-- name: GetSuperAdminRoleForUser :one
SELECT EXISTS (
  SELECT 1 FROM organization_memberships m
  JOIN membership_roles mr ON mr.membership_id = m.id
  JOIN roles r ON r.id = mr.role_id
  WHERE m.user_id = $1 AND m.status = 'active' AND r.code = 'super_admin'
) AS is_super_admin;

-- name: HasOrganizationRole :one
SELECT EXISTS (
  SELECT 1 FROM organization_memberships m
  JOIN membership_roles mr ON mr.membership_id=m.id
  JOIN roles r ON r.id=mr.role_id
  WHERE m.organization_id=sqlc.arg(organization_id) AND m.user_id=sqlc.arg(user_id)
    AND m.status='active' AND r.code=sqlc.arg(role_code)
) AS allowed;

-- name: InsertAuditLog :exec
INSERT INTO audit_logs (id, organization_id, actor_user_id, action, resource_type, resource_id, request_id, before_data, after_data)
VALUES (sqlc.arg(id), sqlc.narg(organization_id), sqlc.narg(actor_user_id), sqlc.arg(action), sqlc.arg(resource_type), sqlc.narg(resource_id), sqlc.narg(request_id), sqlc.narg(before_data), sqlc.narg(after_data));

-- name: ListOrganizationAuditLogs :many
SELECT a.*,u.email AS actor_email
FROM audit_logs a LEFT JOIN users u ON u.id=a.actor_user_id
WHERE a.organization_id=sqlc.arg(organization_id)
 AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (a.created_at,a.id)<(sqlc.narg(cursor_created_at),sqlc.narg(cursor_id)::uuid))
ORDER BY a.created_at DESC,a.id DESC LIMIT sqlc.arg(page_size);
