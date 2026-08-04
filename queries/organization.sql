-- name: CreateOrganization :one
INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $3) RETURNING *;

-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = $1 AND status <> 'deleted';

-- name: ListOrganizations :many
SELECT * FROM organizations
WHERE status <> 'deleted'
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(cursor_created_at), sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(page_size);

-- name: UpdateOrganization :one
UPDATE organizations SET name = $2, slug = $3, updated_at = now()
WHERE id = $1 AND status <> 'deleted' RETURNING *;

-- name: CreateMembership :one
INSERT INTO organization_memberships (id, organization_id, user_id, status, joined_at)
VALUES ($1, $2, $3, $4, CASE WHEN $4 = 'active' THEN now() ELSE NULL END)
ON CONFLICT (organization_id, user_id) DO UPDATE SET status = EXCLUDED.status, updated_at = now()
RETURNING *;

-- name: AssignMembershipRole :exec
INSERT INTO membership_roles (membership_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;

-- name: GetRoleByCode :one
SELECT * FROM roles WHERE code = $1;

-- name: ListOrganizationMembers :many
SELECT u.id, u.email, u.display_name, u.status AS user_status, m.id AS membership_id, m.status AS membership_status,
       COALESCE(array_agg(r.code ORDER BY r.code) FILTER (WHERE r.code IS NOT NULL), '{}')::text[] AS roles,
       m.created_at
FROM organization_memberships m
JOIN users u ON u.id = m.user_id
LEFT JOIN membership_roles mr ON mr.membership_id = m.id
LEFT JOIN roles r ON r.id = mr.role_id
WHERE m.organization_id = sqlc.arg(organization_id)
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (m.created_at, m.id) < (sqlc.narg(cursor_created_at), sqlc.narg(cursor_id)::uuid))
GROUP BY u.id, m.id ORDER BY m.created_at DESC, m.id DESC LIMIT sqlc.arg(page_size);

-- name: GetMembershipForUpdate :one
SELECT * FROM organization_memberships WHERE id=sqlc.arg(id) AND organization_id=sqlc.arg(organization_id) FOR UPDATE;

-- name: ListMembershipRoleCodes :many
SELECT r.code FROM membership_roles mr JOIN roles r ON r.id=mr.role_id WHERE mr.membership_id=$1 ORDER BY r.code;

-- name: DeleteMembershipRoles :exec
DELETE FROM membership_roles WHERE membership_id=$1;

-- name: SetMembershipStatus :one
UPDATE organization_memberships SET status=sqlc.arg(status),joined_at=CASE WHEN sqlc.arg(status)::text='active' THEN COALESCE(joined_at,now()) ELSE joined_at END,updated_at=now()
WHERE id=sqlc.arg(id) AND organization_id=sqlc.arg(organization_id) RETURNING *;

-- name: CreateInvitation :one
INSERT INTO user_invitations (id, organization_id, email, role_id, token_hash, invited_by, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(organization_id), lower(sqlc.arg(email)), sqlc.arg(role_id), sqlc.arg(token_hash), sqlc.arg(invited_by), sqlc.arg(expires_at)) RETURNING *;

-- name: GetInvitationByTokenHashForUpdate :one
SELECT * FROM user_invitations WHERE token_hash = $1 FOR UPDATE;

-- name: AcceptInvitation :one
UPDATE user_invitations SET status = 'accepted', accepted_at = now()
WHERE id = $1 AND status = 'pending' RETURNING *;
