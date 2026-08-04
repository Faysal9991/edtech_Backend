-- +goose Up
CREATE INDEX organization_memberships_org_cursor_idx ON organization_memberships (organization_id, created_at DESC, id DESC);
CREATE INDEX course_categories_org_cursor_idx ON course_categories (organization_id, created_at DESC, id DESC);
CREATE INDEX courses_managed_cursor_idx ON courses (organization_id, created_at DESC, id DESC);
CREATE INDEX lesson_progress_enrollment_cursor_idx ON lesson_progress (enrollment_id, updated_at DESC, id DESC);
CREATE INDEX quizzes_course_cursor_idx ON quizzes (course_id, created_at DESC, id DESC);
CREATE INDEX quiz_attempts_quiz_student_cursor_idx ON quiz_attempts (quiz_id, student_id, created_at DESC, id DESC);
CREATE INDEX assignments_course_cursor_idx ON assignments (course_id, created_at DESC, id DESC);
CREATE INDEX assignment_submissions_course_cursor_idx ON assignment_submissions (assignment_id, created_at DESC, id DESC);
CREATE INDEX assignment_submissions_student_cursor_idx ON assignment_submissions (assignment_id, student_id, created_at DESC, id DESC);
CREATE INDEX enrollments_student_cursor_idx ON enrollments (student_id, updated_at DESC, id DESC);
CREATE INDEX enrollments_course_cursor_idx ON enrollments (course_id, created_at DESC, id DESC);
CREATE INDEX live_sessions_org_cursor_idx ON live_sessions (organization_id, status, scheduled_start_at, id);
CREATE INDEX certificates_student_cursor_idx ON certificates (student_id, issued_at DESC, id DESC);
CREATE INDEX orders_user_cursor_idx ON orders (user_id, created_at DESC, id DESC);
CREATE INDEX payment_transactions_order_cursor_idx ON payment_transactions (order_id, created_at DESC, id DESC);
CREATE INDEX courses_global_catalogue_idx ON courses (status, published_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS courses_global_catalogue_idx;
DROP INDEX IF EXISTS payment_transactions_order_cursor_idx;
DROP INDEX IF EXISTS orders_user_cursor_idx;
DROP INDEX IF EXISTS certificates_student_cursor_idx;
DROP INDEX IF EXISTS live_sessions_org_cursor_idx;
DROP INDEX IF EXISTS enrollments_course_cursor_idx;
DROP INDEX IF EXISTS enrollments_student_cursor_idx;
DROP INDEX IF EXISTS assignment_submissions_student_cursor_idx;
DROP INDEX IF EXISTS assignment_submissions_course_cursor_idx;
DROP INDEX IF EXISTS assignments_course_cursor_idx;
DROP INDEX IF EXISTS quiz_attempts_quiz_student_cursor_idx;
DROP INDEX IF EXISTS quizzes_course_cursor_idx;
DROP INDEX IF EXISTS lesson_progress_enrollment_cursor_idx;
DROP INDEX IF EXISTS courses_managed_cursor_idx;
DROP INDEX IF EXISTS course_categories_org_cursor_idx;
DROP INDEX IF EXISTS organization_memberships_org_cursor_idx;
