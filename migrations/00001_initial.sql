-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE organizations (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 2 AND 200),
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$'),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','deleted')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (slug)
);

CREATE TABLE users (
    id uuid PRIMARY KEY,
    firebase_uid text NOT NULL,
    email text NOT NULL,
    display_name text NOT NULL DEFAULT '',
    avatar_url text,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','deleted')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    UNIQUE (firebase_uid)
);
CREATE UNIQUE INDEX users_email_normalized_uidx ON users (lower(email));

CREATE TABLE roles (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE CHECK (code IN ('super_admin','organization_admin','instructor','student')),
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE permissions (
    id uuid PRIMARY KEY,
    code text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE role_permissions (
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id uuid NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE organization_memberships (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    user_id uuid NOT NULL REFERENCES users(id),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('invited','active','suspended','removed')),
    joined_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, user_id)
);
CREATE INDEX organization_memberships_user_status_idx ON organization_memberships (user_id, status);

CREATE TABLE membership_roles (
    membership_id uuid NOT NULL REFERENCES organization_memberships(id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (membership_id, role_id)
);

CREATE TABLE user_invitations (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    email text NOT NULL,
    role_id uuid NOT NULL REFERENCES roles(id),
    token_hash text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','revoked','expired')),
    invited_by uuid NOT NULL REFERENCES users(id),
    expires_at timestamptz NOT NULL,
    accepted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX user_invitations_org_status_idx ON user_invitations (organization_id, status, created_at DESC, id);
CREATE UNIQUE INDEX user_invitations_pending_email_uidx ON user_invitations (organization_id, lower(email)) WHERE status = 'pending';

CREATE TABLE audit_logs (
    id uuid PRIMARY KEY,
    organization_id uuid REFERENCES organizations(id),
    actor_user_id uuid REFERENCES users(id),
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id uuid,
    request_id text,
    ip_address inet,
    before_data jsonb,
    after_data jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_org_created_idx ON audit_logs (organization_id, created_at DESC, id);

CREATE TABLE course_categories (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 2 AND 100),
    slug text NOT NULL,
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, slug),
    UNIQUE (organization_id, name)
);

CREATE TABLE media_assets (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    owner_user_id uuid NOT NULL REFERENCES users(id),
    kind text NOT NULL CHECK (kind IN ('video','pdf','image','assignment','certificate')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','uploaded','processing','ready','failed')),
    storage_key text NOT NULL UNIQUE CHECK (storage_key !~ '\.\.' AND storage_key !~ '^/'),
    original_filename text NOT NULL,
    content_type text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    checksum_sha256 text,
    duration_seconds integer CHECK (duration_seconds IS NULL OR duration_seconds >= 0),
    width integer CHECK (width IS NULL OR width > 0),
    height integer CHECK (height IS NULL OR height > 0),
    metadata jsonb NOT NULL DEFAULT '{}',
    failure_reason text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX media_assets_owner_idx ON media_assets (owner_user_id, created_at DESC, id);

CREATE TABLE upload_intents (
    id uuid PRIMARY KEY,
    media_asset_id uuid NOT NULL UNIQUE REFERENCES media_assets(id) ON DELETE CASCADE,
    owner_user_id uuid NOT NULL REFERENCES users(id),
    expected_size_bytes bigint NOT NULL CHECK (expected_size_bytes > 0),
    expected_content_type text NOT NULL,
    expected_checksum_sha256 text,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','completed','expired')),
    expires_at timestamptz NOT NULL,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE courses (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    category_id uuid REFERENCES course_categories(id),
    thumbnail_asset_id uuid REFERENCES media_assets(id),
    title text NOT NULL,
    slug text NOT NULL,
    description text NOT NULL DEFAULT '',
    language text NOT NULL DEFAULT 'en',
    level text NOT NULL DEFAULT 'beginner' CHECK (level IN ('beginner','intermediate','advanced','all')),
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','review','published','archived')),
    is_free boolean NOT NULL DEFAULT true,
    price_minor bigint NOT NULL DEFAULT 0 CHECK (price_minor >= 0),
    currency char(3) NOT NULL DEFAULT 'BDT' CHECK (currency = upper(currency)),
    completion_video_threshold numeric(5,2) NOT NULL DEFAULT 90 CHECK (completion_video_threshold BETWEEN 1 AND 100),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    published_at timestamptz,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, slug),
    CHECK ((is_free AND price_minor = 0) OR (NOT is_free AND price_minor > 0))
);
ALTER TABLE courses ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
    setweight(to_tsvector('simple', coalesce(description, '')), 'B')
) STORED;
CREATE INDEX courses_org_listing_idx ON courses (organization_id, status, published_at DESC, id);
CREATE INDEX courses_search_gin_idx ON courses USING gin (search_vector);
CREATE INDEX courses_title_trgm_idx ON courses USING gin (title gin_trgm_ops);

CREATE TABLE course_instructors (
    course_id uuid NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    instructor_id uuid NOT NULL REFERENCES users(id),
    assigned_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (course_id, instructor_id)
);
CREATE INDEX course_instructors_instructor_course_idx ON course_instructors (instructor_id, course_id);

CREATE TABLE course_modules (
    id uuid PRIMARY KEY,
    course_id uuid NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    position integer NOT NULL CHECK (position > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (course_id, position)
);

CREATE TABLE lessons (
    id uuid PRIMARY KEY,
    module_id uuid NOT NULL REFERENCES course_modules(id) ON DELETE CASCADE,
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    lesson_type text NOT NULL CHECK (lesson_type IN ('video','pdf','text','live','quiz','assignment')),
    media_asset_id uuid REFERENCES media_assets(id),
    body text NOT NULL DEFAULT '',
    position integer NOT NULL CHECK (position > 0),
    is_preview boolean NOT NULL DEFAULT false,
    is_required boolean NOT NULL DEFAULT true,
    is_published boolean NOT NULL DEFAULT false,
    duration_seconds integer CHECK (duration_seconds IS NULL OR duration_seconds >= 0),
    metadata jsonb NOT NULL DEFAULT '{"subtitle_tracks":[],"alternate_audio_tracks":[]}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (module_id, position)
);

CREATE TABLE enrollments (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    course_id uuid NOT NULL REFERENCES courses(id),
    student_id uuid NOT NULL REFERENCES users(id),
    status text NOT NULL CHECK (status IN ('pending_payment','active','completed','expired','cancelled')),
    source text NOT NULL CHECK (source IN ('free','payment','admin')),
    price_minor_snapshot bigint NOT NULL CHECK (price_minor_snapshot >= 0),
    currency_snapshot char(3) NOT NULL CHECK (currency_snapshot = upper(currency_snapshot)),
    completion_percentage numeric(5,2) NOT NULL DEFAULT 0 CHECK (completion_percentage BETWEEN 0 AND 100),
    enrolled_at timestamptz,
    completed_at timestamptz,
    expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (course_id, student_id)
);
CREATE INDEX enrollments_student_listing_idx ON enrollments (student_id, status, updated_at DESC, id);
CREATE INDEX enrollments_course_listing_idx ON enrollments (course_id, status, created_at DESC, id);
CREATE INDEX enrollments_org_reporting_idx ON enrollments (organization_id, created_at, status);

CREATE TABLE lesson_progress (
    id uuid PRIMARY KEY,
    enrollment_id uuid NOT NULL REFERENCES enrollments(id) ON DELETE CASCADE,
    lesson_id uuid NOT NULL REFERENCES lessons(id),
    state text NOT NULL DEFAULT 'not_started' CHECK (state IN ('not_started','started','completed')),
    last_position_seconds integer NOT NULL DEFAULT 0 CHECK (last_position_seconds >= 0),
    total_watched_seconds integer NOT NULL DEFAULT 0 CHECK (total_watched_seconds >= 0),
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (enrollment_id, lesson_id)
);
CREATE INDEX lesson_progress_incomplete_idx ON lesson_progress (enrollment_id, lesson_id) WHERE state <> 'completed';

CREATE TABLE course_completion_snapshots (
    id uuid PRIMARY KEY,
    enrollment_id uuid NOT NULL REFERENCES enrollments(id) ON DELETE CASCADE,
    required_lessons integer NOT NULL,
    completed_lessons integer NOT NULL,
    required_quizzes integer NOT NULL,
    passed_quizzes integer NOT NULL,
    required_assignments integer NOT NULL,
    passed_assignments integer NOT NULL,
    percentage numeric(5,2) NOT NULL CHECK (percentage BETWEEN 0 AND 100),
    is_complete boolean NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX course_completion_snapshots_enrollment_idx ON course_completion_snapshots (enrollment_id, created_at DESC);

CREATE TABLE quizzes (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    course_id uuid NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    lesson_id uuid UNIQUE REFERENCES lessons(id) ON DELETE SET NULL,
    title text NOT NULL,
    instructions text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','archived')),
    time_limit_seconds integer CHECK (time_limit_seconds IS NULL OR time_limit_seconds > 0),
    attempt_limit integer NOT NULL DEFAULT 1 CHECK (attempt_limit > 0),
    pass_percentage numeric(5,2) NOT NULL DEFAULT 60 CHECK (pass_percentage BETWEEN 0 AND 100),
    randomize_questions boolean NOT NULL DEFAULT false,
    randomize_options boolean NOT NULL DEFAULT false,
    available_from timestamptz,
    available_until timestamptz,
    results_visibility text NOT NULL DEFAULT 'immediate' CHECK (results_visibility IN ('immediate','after_close','manual')),
    is_required boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (available_until IS NULL OR available_from IS NULL OR available_until > available_from)
);

CREATE TABLE quiz_questions (
    id uuid PRIMARY KEY,
    quiz_id uuid NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
    question_type text NOT NULL CHECK (question_type IN ('single','multiple','true_false','short_answer')),
    prompt text NOT NULL,
    points numeric(10,2) NOT NULL CHECK (points > 0),
    position integer NOT NULL CHECK (position > 0),
    explanation text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (quiz_id, position)
);

CREATE TABLE quiz_question_options (
    id uuid PRIMARY KEY,
    question_id uuid NOT NULL REFERENCES quiz_questions(id) ON DELETE CASCADE,
    text text NOT NULL,
    is_correct boolean NOT NULL DEFAULT false,
    position integer NOT NULL CHECK (position > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (question_id, position)
);

CREATE TABLE quiz_attempts (
    id uuid PRIMARY KEY,
    quiz_id uuid NOT NULL REFERENCES quizzes(id),
    enrollment_id uuid NOT NULL REFERENCES enrollments(id),
    student_id uuid NOT NULL REFERENCES users(id),
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    status text NOT NULL DEFAULT 'in_progress' CHECK (status IN ('in_progress','submitted','graded','expired')),
    question_snapshot jsonb NOT NULL,
    question_order uuid[] NOT NULL DEFAULT '{}',
    started_at timestamptz NOT NULL,
    expires_at timestamptz,
    submitted_at timestamptz,
    graded_at timestamptz,
    score_points numeric(10,2),
    max_points numeric(10,2) NOT NULL CHECK (max_points >= 0),
    percentage numeric(5,2) CHECK (percentage BETWEEN 0 AND 100),
    passed boolean,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (quiz_id, student_id, attempt_number)
);
CREATE UNIQUE INDEX quiz_attempts_in_progress_uidx ON quiz_attempts (quiz_id, student_id) WHERE status = 'in_progress';
CREATE INDEX quiz_attempts_student_idx ON quiz_attempts (student_id, created_at DESC, id);

CREATE TABLE quiz_answers (
    id uuid PRIMARY KEY,
    attempt_id uuid NOT NULL REFERENCES quiz_attempts(id) ON DELETE CASCADE,
    question_id uuid NOT NULL,
    selected_option_ids uuid[] NOT NULL DEFAULT '{}',
    text_answer text,
    awarded_points numeric(10,2),
    is_correct boolean,
    grader_feedback text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (attempt_id, question_id)
);

CREATE TABLE assignments (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    course_id uuid NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    lesson_id uuid UNIQUE REFERENCES lessons(id) ON DELETE SET NULL,
    title text NOT NULL,
    instructions text NOT NULL DEFAULT '',
    due_at timestamptz,
    maximum_score numeric(10,2) NOT NULL CHECK (maximum_score > 0),
    passing_score numeric(10,2) NOT NULL CHECK (passing_score >= 0 AND passing_score <= maximum_score),
    allowed_file_types text[] NOT NULL DEFAULT '{}',
    maximum_submissions integer NOT NULL DEFAULT 1 CHECK (maximum_submissions > 0),
    is_required boolean NOT NULL DEFAULT false,
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','archived')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE assignment_submissions (
    id uuid PRIMARY KEY,
    assignment_id uuid NOT NULL REFERENCES assignments(id),
    enrollment_id uuid NOT NULL REFERENCES enrollments(id),
    student_id uuid NOT NULL REFERENCES users(id),
    submission_number integer NOT NULL CHECK (submission_number > 0),
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','submitted','late','graded','returned')),
    text_content text NOT NULL DEFAULT '',
    submitted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (assignment_id, student_id, submission_number)
);
CREATE INDEX assignment_submissions_lookup_idx ON assignment_submissions (assignment_id, student_id, submitted_at DESC);

CREATE TABLE assignment_submission_assets (
    submission_id uuid NOT NULL REFERENCES assignment_submissions(id) ON DELETE CASCADE,
    media_asset_id uuid NOT NULL REFERENCES media_assets(id),
    PRIMARY KEY (submission_id, media_asset_id)
);

CREATE TABLE grades (
    id uuid PRIMARY KEY,
    assignment_submission_id uuid UNIQUE REFERENCES assignment_submissions(id),
    quiz_attempt_id uuid UNIQUE REFERENCES quiz_attempts(id),
    student_id uuid NOT NULL REFERENCES users(id),
    graded_by uuid NOT NULL REFERENCES users(id),
    points numeric(10,2) NOT NULL CHECK (points >= 0),
    percentage numeric(5,2) NOT NULL CHECK (percentage BETWEEN 0 AND 100),
    feedback text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((assignment_submission_id IS NOT NULL)::integer + (quiz_attempt_id IS NOT NULL)::integer = 1)
);

CREATE TABLE live_sessions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    course_id uuid NOT NULL REFERENCES courses(id),
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    room_name text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled','live','ended','cancelled')),
    scheduled_start_at timestamptz NOT NULL,
    scheduled_end_at timestamptz NOT NULL,
    started_at timestamptz,
    ended_at timestamptz,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (scheduled_end_at > scheduled_start_at)
);
CREATE INDEX live_sessions_course_schedule_idx ON live_sessions (course_id, scheduled_start_at);
CREATE INDEX live_sessions_active_idx ON live_sessions (scheduled_start_at, id) WHERE status IN ('scheduled','live');

CREATE TABLE live_attendance_sessions (
    id uuid PRIMARY KEY,
    live_session_id uuid NOT NULL REFERENCES live_sessions(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id),
    participant_identity text NOT NULL,
    joined_at timestamptz NOT NULL,
    left_at timestamptz,
    duration_seconds integer NOT NULL DEFAULT 0 CHECK (duration_seconds >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX live_attendance_open_idx ON live_attendance_sessions (live_session_id, participant_identity) WHERE left_at IS NULL;

CREATE TABLE live_webhook_events (
    id uuid PRIMARY KEY,
    provider_event_id text NOT NULL UNIQUE,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    processed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE certificate_templates (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    name text NOT NULL,
    background_asset_id uuid REFERENCES media_assets(id),
    config jsonb NOT NULL DEFAULT '{}',
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX certificate_templates_default_uidx ON certificate_templates (organization_id) WHERE is_default;

CREATE TABLE certificates (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    enrollment_id uuid NOT NULL UNIQUE REFERENCES enrollments(id),
    student_id uuid NOT NULL REFERENCES users(id),
    course_id uuid NOT NULL REFERENCES courses(id),
    template_id uuid REFERENCES certificate_templates(id),
    certificate_number text NOT NULL UNIQUE,
    verification_code text NOT NULL UNIQUE,
    media_asset_id uuid UNIQUE REFERENCES media_assets(id),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','ready','failed','revoked')),
    issued_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE orders (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id),
    user_id uuid NOT NULL REFERENCES users(id),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','paid','failed','cancelled','refunded')),
    amount_minor bigint NOT NULL CHECK (amount_minor >= 0),
    currency char(3) NOT NULL CHECK (currency = upper(currency)),
    idempotency_key text NOT NULL,
    provider_payment_intent_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    paid_at timestamptz,
    UNIQUE (user_id, idempotency_key)
);
CREATE UNIQUE INDEX orders_payment_intent_uidx ON orders (provider_payment_intent_id) WHERE provider_payment_intent_id IS NOT NULL;
CREATE INDEX orders_user_listing_idx ON orders (user_id, status, created_at DESC, id);
CREATE INDEX orders_org_reporting_idx ON orders (organization_id, created_at, status);

CREATE TABLE order_items (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    course_id uuid NOT NULL REFERENCES courses(id),
    course_title text NOT NULL,
    amount_minor bigint NOT NULL CHECK (amount_minor >= 0),
    currency char(3) NOT NULL CHECK (currency = upper(currency)),
    UNIQUE (order_id, course_id)
);

CREATE TABLE payment_transactions (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id),
    provider text NOT NULL CHECK (provider IN ('stripe')),
    provider_transaction_id text NOT NULL UNIQUE,
    kind text NOT NULL CHECK (kind IN ('payment','refund')),
    status text NOT NULL CHECK (status IN ('pending','succeeded','failed','cancelled')),
    amount_minor bigint NOT NULL CHECK (amount_minor >= 0),
    currency char(3) NOT NULL CHECK (currency = upper(currency)),
    failure_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE payment_webhook_events (
    id uuid PRIMARY KEY,
    provider text NOT NULL CHECK (provider IN ('stripe')),
    provider_event_id text NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    processed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_event_id)
);

CREATE TABLE refunds (
    id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES orders(id),
    payment_transaction_id uuid NOT NULL REFERENCES payment_transactions(id),
    provider_refund_id text NOT NULL UNIQUE,
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    currency char(3) NOT NULL CHECK (currency = upper(currency)),
    status text NOT NULL CHECK (status IN ('pending','succeeded','failed','cancelled')),
    reason text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE device_tokens (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    encrypted_token text NOT NULL,
    platform text NOT NULL CHECK (platform IN ('android','ios','web')),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX device_tokens_user_idx ON device_tokens (user_id, last_seen_at DESC);

CREATE TABLE notifications (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id uuid REFERENCES organizations(id),
    type text NOT NULL,
    title text NOT NULL,
    body text NOT NULL,
    data jsonb NOT NULL DEFAULT '{}',
    deduplication_key text,
    read_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notifications_user_listing_idx ON notifications (user_id, created_at DESC, id);
CREATE INDEX notifications_unread_idx ON notifications (user_id, created_at DESC, id) WHERE read_at IS NULL;
CREATE UNIQUE INDEX notifications_dedupe_uidx ON notifications (user_id, deduplication_key) WHERE deduplication_key IS NOT NULL;

CREATE TABLE notification_deliveries (
    id uuid PRIMARY KEY,
    notification_id uuid NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    device_token_id uuid REFERENCES device_tokens(id) ON DELETE SET NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz,
    provider_message_id text,
    last_error text,
    sent_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (notification_id, device_token_id)
);

CREATE TABLE outbox_events (
    id uuid PRIMARY KEY,
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    deduplication_key text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','published','failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_pending_idx ON outbox_events (next_attempt_at) WHERE status IN ('pending','failed');

CREATE TABLE idempotency_records (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    scope text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    response_status integer,
    response_body jsonb,
    resource_id uuid,
    locked_until timestamptz,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, scope, idempotency_key)
);

-- +goose Down
DROP TABLE IF EXISTS idempotency_records;
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS notification_deliveries;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS device_tokens;
DROP TABLE IF EXISTS refunds;
DROP TABLE IF EXISTS payment_webhook_events;
DROP TABLE IF EXISTS payment_transactions;
DROP TABLE IF EXISTS order_items;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS certificates;
DROP TABLE IF EXISTS certificate_templates;
DROP TABLE IF EXISTS live_webhook_events;
DROP TABLE IF EXISTS live_attendance_sessions;
DROP TABLE IF EXISTS live_sessions;
DROP TABLE IF EXISTS grades;
DROP TABLE IF EXISTS assignment_submission_assets;
DROP TABLE IF EXISTS assignment_submissions;
DROP TABLE IF EXISTS assignments;
DROP TABLE IF EXISTS quiz_answers;
DROP TABLE IF EXISTS quiz_attempts;
DROP TABLE IF EXISTS quiz_question_options;
DROP TABLE IF EXISTS quiz_questions;
DROP TABLE IF EXISTS quizzes;
DROP TABLE IF EXISTS course_completion_snapshots;
DROP TABLE IF EXISTS lesson_progress;
DROP TABLE IF EXISTS enrollments;
DROP TABLE IF EXISTS lessons;
DROP TABLE IF EXISTS course_modules;
DROP TABLE IF EXISTS course_instructors;
DROP TABLE IF EXISTS courses;
DROP TABLE IF EXISTS upload_intents;
DROP TABLE IF EXISTS media_assets;
DROP TABLE IF EXISTS course_categories;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS user_invitations;
DROP TABLE IF EXISTS membership_roles;
DROP TABLE IF EXISTS organization_memberships;
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;
DROP EXTENSION IF EXISTS pg_trgm;
