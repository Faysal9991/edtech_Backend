-- name: CreatePasswordUser :one
INSERT INTO users (
    id, firebase_uid, email, display_name, password_hash, status
) VALUES (
    sqlc.arg(id), 'local:' || (sqlc.arg(id)::uuid)::text, lower(sqlc.arg(email)),
    sqlc.arg(display_name), sqlc.arg(password_hash), 'pending'
)
RETURNING *;

-- name: GetOrganizationBySlug :one
SELECT * FROM organizations WHERE slug = sqlc.arg(slug) AND status = 'active';

-- name: SetUserPassword :one
UPDATE users
SET password_hash = sqlc.arg(password_hash), failed_login_count = 0,
    locked_until = NULL, updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: RecordSuccessfulLogin :one
UPDATE users
SET failed_login_count = 0, locked_until = NULL, last_login_at = now(), updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: RecordFailedLogin :one
UPDATE users
SET failed_login_count = failed_login_count + 1,
    locked_until = CASE
        WHEN failed_login_count + 1 >= sqlc.arg(lock_after)
        THEN now() + sqlc.arg(lock_duration)::interval
        ELSE locked_until
    END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING failed_login_count, locked_until;

-- name: AssignGlobalRole :exec
INSERT INTO user_roles (user_id, role_id, assigned_by)
VALUES (sqlc.arg(user_id), sqlc.arg(role_id), sqlc.narg(assigned_by))
ON CONFLICT DO NOTHING;

-- name: RemoveGlobalRole :execrows
DELETE FROM user_roles
WHERE user_id = sqlc.arg(user_id) AND role_id = sqlc.arg(role_id);

-- name: DeleteGlobalRoles :exec
DELETE FROM user_roles WHERE user_id = sqlc.arg(user_id);

-- name: ListGlobalRoleCodes :many
SELECT r.code
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id
WHERE ur.user_id = sqlc.arg(user_id)
ORDER BY r.code;

-- name: UserHasGlobalRole :one
SELECT EXISTS (
    SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id
    WHERE ur.user_id = sqlc.arg(user_id) AND r.code = sqlc.arg(role_code)
) AS allowed;

-- name: CreateUserProfile :exec
INSERT INTO user_profiles (user_id, first_name, last_name)
VALUES (sqlc.arg(user_id), sqlc.arg(first_name), sqlc.arg(last_name))
ON CONFLICT (user_id) DO NOTHING;

-- name: CreateStudentProfile :exec
INSERT INTO student_profiles (user_id) VALUES (sqlc.arg(user_id))
ON CONFLICT (user_id) DO NOTHING;

-- name: CreateTeacherProfile :exec
INSERT INTO teacher_profiles (user_id) VALUES (sqlc.arg(user_id))
ON CONFLICT (user_id) DO NOTHING;

-- name: DeleteActiveEmailVerificationTokens :exec
DELETE FROM email_verification_tokens WHERE user_id = sqlc.arg(user_id) AND used_at IS NULL;

-- name: CreateEmailVerificationToken :exec
INSERT INTO email_verification_tokens (id, user_id, token_hash, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(token_hash), sqlc.arg(expires_at));

-- name: ConsumeEmailVerificationToken :one
UPDATE email_verification_tokens
SET used_at = now()
WHERE token_hash = sqlc.arg(token_hash) AND used_at IS NULL AND expires_at > now()
RETURNING user_id;

-- name: VerifyUserEmail :one
UPDATE users
SET email_verified_at = COALESCE(email_verified_at, now()),
    status = CASE WHEN status = 'pending' THEN 'active' ELSE status END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteActivePasswordResetTokens :exec
DELETE FROM password_reset_tokens WHERE user_id = sqlc.arg(user_id) AND used_at IS NULL;

-- name: CreatePasswordResetToken :exec
INSERT INTO password_reset_tokens (id, user_id, token_hash, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(token_hash), sqlc.arg(expires_at));

-- name: ConsumePasswordResetToken :one
UPDATE password_reset_tokens
SET used_at = now()
WHERE token_hash = sqlc.arg(token_hash) AND used_at IS NULL AND expires_at > now()
RETURNING user_id;

-- name: CreateRefreshSession :one
INSERT INTO refresh_sessions (
    id, family_id, parent_id, user_id, token_hash, expires_at, ip_address, user_agent
) VALUES (
    sqlc.arg(id), sqlc.arg(family_id), sqlc.narg(parent_id), sqlc.arg(user_id),
    sqlc.arg(token_hash), sqlc.arg(expires_at),
    nullif(sqlc.arg(ip_address)::text, '')::inet, sqlc.arg(user_agent)
)
RETURNING *;

-- name: GetRefreshSessionForUpdate :one
SELECT * FROM refresh_sessions WHERE token_hash = sqlc.arg(token_hash) FOR UPDATE;

-- name: MarkRefreshSessionRotated :exec
UPDATE refresh_sessions
SET rotated_at = now(), last_used_at = now()
WHERE id = sqlc.arg(id) AND rotated_at IS NULL AND revoked_at IS NULL;

-- name: RevokeRefreshFamilyForReuse :exec
UPDATE refresh_sessions
SET revoked_at = COALESCE(revoked_at, now()), reuse_detected_at = now()
WHERE family_id = sqlc.arg(family_id);

-- name: RevokeRefreshSession :execrows
UPDATE refresh_sessions SET revoked_at = COALESCE(revoked_at, now())
WHERE token_hash = sqlc.arg(token_hash) AND revoked_at IS NULL;

-- name: RevokeAllRefreshSessions :execrows
UPDATE refresh_sessions SET revoked_at = COALESCE(revoked_at, now())
WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

-- name: InsertSecurityAudit :exec
INSERT INTO audit_logs (
    id, actor_user_id, action, resource_type, resource_id, request_id,
    ip_address, user_agent, metadata
) VALUES (
    sqlc.arg(id), sqlc.narg(actor_user_id), sqlc.arg(action),
    sqlc.arg(resource_type), sqlc.narg(resource_id), sqlc.narg(request_id),
    nullif(sqlc.arg(ip_address)::text, '')::inet, sqlc.arg(user_agent),
    sqlc.arg(metadata)
);

-- name: ListAdminUsers :many
SELECT u.id, u.email, u.display_name, u.status, u.email_verified_at,
       u.created_at, u.updated_at,
       COALESCE(array_agg(DISTINCT r.code ORDER BY r.code)
           FILTER (WHERE r.code IS NOT NULL), '{}')::text[] AS roles
FROM users u
LEFT JOIN user_roles ur ON ur.user_id = u.id
LEFT JOIN roles r ON r.id = ur.role_id
WHERE (sqlc.narg(status)::text IS NULL OR u.status = sqlc.narg(status))
  AND (sqlc.narg(role_code)::text IS NULL OR EXISTS (
      SELECT 1 FROM user_roles ur2 JOIN roles r2 ON r2.id = ur2.role_id
      WHERE ur2.user_id = u.id AND r2.code = sqlc.narg(role_code)
  ))
  AND (sqlc.arg(search)::text = '' OR
       u.email ILIKE '%' || sqlc.arg(search) || '%' OR
       u.display_name ILIKE '%' || sqlc.arg(search) || '%')
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR
       (u.created_at, u.id) < (sqlc.narg(cursor_created_at), sqlc.narg(cursor_id)::uuid))
GROUP BY u.id
ORDER BY u.created_at DESC, u.id DESC
LIMIT sqlc.arg(page_size);

-- name: GetAdminUser :one
SELECT u.id, u.email, u.display_name, u.status, u.email_verified_at,
       u.created_at, u.updated_at,
       COALESCE(array_agg(DISTINCT r.code ORDER BY r.code)
           FILTER (WHERE r.code IS NOT NULL), '{}')::text[] AS roles
FROM users u
LEFT JOIN user_roles ur ON ur.user_id = u.id
LEFT JOIN roles r ON r.id = ur.role_id
WHERE u.id = sqlc.arg(id)
GROUP BY u.id;

-- name: SetUserStatus :one
UPDATE users SET status = sqlc.arg(status), updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetUserProfile :one
SELECT u.id, u.email, u.display_name, u.status, u.avatar_url, u.email_verified_at,
       p.first_name, p.last_name, p.phone, p.timezone, p.locale,
       t.biography, t.expertise, t.status AS teacher_status,
       s.headline AS student_headline,
       COALESCE(array_agg(DISTINCT r.code ORDER BY r.code)
           FILTER (WHERE r.code IS NOT NULL), '{}')::text[] AS roles
FROM users u
LEFT JOIN user_profiles p ON p.user_id = u.id
LEFT JOIN teacher_profiles t ON t.user_id = u.id
LEFT JOIN student_profiles s ON s.user_id = u.id
LEFT JOIN user_roles ur ON ur.user_id = u.id
LEFT JOIN roles r ON r.id = ur.role_id
WHERE u.id = sqlc.arg(id)
GROUP BY u.id, p.user_id, t.user_id, s.user_id;

-- name: UpdateUserProfile :exec
UPDATE user_profiles
SET first_name = sqlc.arg(first_name), last_name = sqlc.arg(last_name),
    phone = sqlc.narg(phone), timezone = sqlc.arg(timezone),
    locale = sqlc.arg(locale), updated_at = now()
WHERE user_id = sqlc.arg(user_id);

-- name: UpdateUserDisplayName :exec
UPDATE users SET display_name = sqlc.arg(display_name), updated_at = now()
WHERE id = sqlc.arg(id);

-- name: UpsertTeacherProfile :exec
INSERT INTO teacher_profiles (user_id, biography, expertise)
VALUES (sqlc.arg(user_id), sqlc.arg(biography), sqlc.arg(expertise))
ON CONFLICT (user_id) DO UPDATE SET biography = EXCLUDED.biography,
    expertise = EXCLUDED.expertise, updated_at = now();
