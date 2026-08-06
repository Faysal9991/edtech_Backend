-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

ALTER TABLE users DROP CONSTRAINT users_status_check;
UPDATE users SET status = 'disabled' WHERE status = 'deleted';
ALTER TABLE users
    ADD COLUMN password_hash text NOT NULL DEFAULT '',
    ADD COLUMN email_verified_at timestamptz,
    ADD COLUMN failed_login_count integer NOT NULL DEFAULT 0 CHECK (failed_login_count >= 0),
    ADD COLUMN locked_until timestamptz,
    ADD CONSTRAINT users_status_check CHECK (status IN ('pending','active','suspended','disabled'));

ALTER TABLE roles DROP CONSTRAINT roles_code_check;
ALTER TABLE roles ADD CONSTRAINT roles_code_check CHECK (code IN (
    'super_admin','organization_admin','instructor','admin','teacher','student'
));

INSERT INTO roles (id, code, name) VALUES
('018f0000-0000-7000-8000-000000000005', 'admin', 'Administrator'),
('018f0000-0000-7000-8000-000000000006', 'teacher', 'Teacher')
ON CONFLICT (code) DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p WHERE r.code = 'admin'
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p ON p.code IN (
    'courses.manage','assessments.manage','submissions.grade','reports.view'
) WHERE r.code = 'teacher'
ON CONFLICT DO NOTHING;

CREATE TABLE user_roles (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles(id),
    assigned_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role_id)
);
CREATE INDEX user_roles_role_user_idx ON user_roles (role_id, user_id);

CREATE TABLE user_profiles (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    first_name text NOT NULL DEFAULT '',
    last_name text NOT NULL DEFAULT '',
    phone text,
    timezone text NOT NULL DEFAULT 'UTC',
    locale text NOT NULL DEFAULT 'en',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE teacher_profiles (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    biography text NOT NULL DEFAULT '',
    expertise text[] NOT NULL DEFAULT '{}',
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','suspended')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE student_profiles (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    headline text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE refresh_sessions (
    id uuid PRIMARY KEY,
    family_id uuid NOT NULL,
    parent_id uuid REFERENCES refresh_sessions(id),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    last_used_at timestamptz,
    rotated_at timestamptz,
    revoked_at timestamptz,
    reuse_detected_at timestamptz,
    ip_address inet,
    user_agent text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);
CREATE INDEX refresh_sessions_user_active_idx ON refresh_sessions (user_id, expires_at DESC)
    WHERE revoked_at IS NULL;
CREATE INDEX refresh_sessions_family_idx ON refresh_sessions (family_id, created_at);

CREATE TABLE email_verification_tokens (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);
CREATE UNIQUE INDEX email_verification_tokens_active_user_uidx
    ON email_verification_tokens (user_id) WHERE used_at IS NULL;

CREATE TABLE password_reset_tokens (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);
CREATE UNIQUE INDEX password_reset_tokens_active_user_uidx
    ON password_reset_tokens (user_id) WHERE used_at IS NULL;

ALTER TABLE audit_logs
    ADD COLUMN metadata jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN user_agent text NOT NULL DEFAULT '';
CREATE INDEX audit_logs_actor_created_idx ON audit_logs (actor_user_id, created_at DESC, id)
    WHERE actor_user_id IS NOT NULL;

CREATE TABLE course_results (
    id uuid PRIMARY KEY,
    enrollment_id uuid NOT NULL UNIQUE REFERENCES enrollments(id) ON DELETE CASCADE,
    student_id uuid NOT NULL REFERENCES users(id),
    course_id uuid NOT NULL REFERENCES courses(id),
    quiz_percentage numeric(5,2) NOT NULL DEFAULT 0 CHECK (quiz_percentage BETWEEN 0 AND 100),
    assignment_percentage numeric(5,2) NOT NULL DEFAULT 0 CHECK (assignment_percentage BETWEEN 0 AND 100),
    completion_percentage numeric(5,2) NOT NULL DEFAULT 0 CHECK (completion_percentage BETWEEN 0 AND 100),
    final_percentage numeric(5,2) NOT NULL CHECK (final_percentage BETWEEN 0 AND 100),
    passing_percentage numeric(5,2) NOT NULL CHECK (passing_percentage BETWEEN 0 AND 100),
    passed boolean NOT NULL,
    components jsonb NOT NULL DEFAULT '{}',
    calculated_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX course_results_student_created_idx ON course_results (student_id, created_at DESC, id);
CREATE INDEX course_results_course_passed_idx ON course_results (course_id, passed, created_at DESC);

CREATE INDEX users_status_created_idx ON users (status, created_at DESC, id);
CREATE INDEX users_search_trgm_idx ON users USING gin ((email || ' ' || display_name) gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS users_search_trgm_idx;
DROP INDEX IF EXISTS users_status_created_idx;
DROP TABLE IF EXISTS course_results;
DROP INDEX IF EXISTS audit_logs_actor_created_idx;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS user_agent, DROP COLUMN IF EXISTS metadata;
DROP TABLE IF EXISTS password_reset_tokens;
DROP TABLE IF EXISTS email_verification_tokens;
DROP TABLE IF EXISTS refresh_sessions;
DROP TABLE IF EXISTS student_profiles;
DROP TABLE IF EXISTS teacher_profiles;
DROP TABLE IF EXISTS user_profiles;
DROP TABLE IF EXISTS user_roles;
DELETE FROM role_permissions WHERE role_id IN (SELECT id FROM roles WHERE code IN ('admin','teacher'));
DELETE FROM roles WHERE code IN ('admin','teacher');
ALTER TABLE roles DROP CONSTRAINT roles_code_check;
ALTER TABLE roles ADD CONSTRAINT roles_code_check CHECK (code IN (
    'super_admin','organization_admin','instructor','student'
));
ALTER TABLE users DROP CONSTRAINT users_status_check;
UPDATE users SET status = 'active' WHERE status IN ('pending','disabled');
ALTER TABLE users
    DROP COLUMN IF EXISTS locked_until,
    DROP COLUMN IF EXISTS failed_login_count,
    DROP COLUMN IF EXISTS email_verified_at,
    DROP COLUMN IF EXISTS password_hash,
    ADD CONSTRAINT users_status_check CHECK (status IN ('active','suspended','deleted'));
DROP EXTENSION IF EXISTS citext;
