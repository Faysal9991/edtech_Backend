-- name: CreateEnrollment :one
INSERT INTO enrollments (id,organization_id,course_id,student_id,status,source,price_minor_snapshot,currency_snapshot,enrolled_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,CASE WHEN $5='active' THEN now() ELSE NULL END)
ON CONFLICT (course_id,student_id) DO UPDATE SET updated_at=now()
RETURNING *;

-- name: GetEnrollment :one
SELECT * FROM enrollments WHERE id=$1;

-- name: GetEnrollmentForUpdate :one
SELECT * FROM enrollments WHERE id=$1 FOR UPDATE;

-- name: GetCourseEnrollment :one
SELECT * FROM enrollments WHERE course_id=$1 AND student_id=$2;

-- name: ListStudentEnrollments :many
SELECT e.*,c.title AS course_title,c.slug AS course_slug,c.thumbnail_asset_id
FROM enrollments e JOIN courses c ON c.id=e.course_id
WHERE e.student_id=sqlc.arg(student_id)
 AND (sqlc.narg(cursor_updated_at)::timestamptz IS NULL OR (e.updated_at,e.id)<(sqlc.narg(cursor_updated_at),sqlc.narg(cursor_id)::uuid))
ORDER BY e.updated_at DESC,e.id DESC LIMIT sqlc.arg(page_size);

-- name: ListCourseEnrollments :many
SELECT e.*,u.email,u.display_name FROM enrollments e JOIN users u ON u.id=e.student_id
WHERE e.course_id=sqlc.arg(course_id)
 AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (e.created_at,e.id)<(sqlc.narg(cursor_created_at),sqlc.narg(cursor_id)::uuid))
ORDER BY e.created_at DESC,e.id DESC LIMIT sqlc.arg(page_size);

-- name: SetEnrollmentStatus :one
UPDATE enrollments SET status=$2,enrolled_at=CASE WHEN $2='active' THEN COALESCE(enrolled_at,now()) ELSE enrolled_at END,completed_at=CASE WHEN $2='completed' THEN COALESCE(completed_at,now()) ELSE completed_at END,updated_at=now() WHERE id=$1 RETURNING *;

-- name: ExpireDueEnrollments :many
WITH due AS (
 SELECT id FROM enrollments WHERE status='active' AND expires_at<=now() ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT $1
)
UPDATE enrollments e SET status='expired',updated_at=now() FROM due WHERE e.id=due.id RETURNING e.id;

-- name: UpsertLessonProgress :one
INSERT INTO lesson_progress (id,enrollment_id,lesson_id,state,last_position_seconds,total_watched_seconds,completed_at)
VALUES (sqlc.arg(id),sqlc.arg(enrollment_id),sqlc.arg(lesson_id),sqlc.arg(state),sqlc.arg(last_position_seconds),sqlc.arg(total_watched_seconds),CASE WHEN sqlc.arg(state)::text='completed' THEN now() ELSE NULL END)
ON CONFLICT (enrollment_id,lesson_id) DO UPDATE SET
 state=CASE WHEN lesson_progress.state='completed' THEN 'completed' ELSE EXCLUDED.state END,
 last_position_seconds=EXCLUDED.last_position_seconds,
 total_watched_seconds=LEAST(sqlc.arg(duration_cap),GREATEST(lesson_progress.total_watched_seconds,LEAST(EXCLUDED.total_watched_seconds,lesson_progress.total_watched_seconds+120))),
 completed_at=CASE WHEN lesson_progress.completed_at IS NOT NULL THEN lesson_progress.completed_at WHEN EXCLUDED.state='completed' THEN now() ELSE NULL END,
 updated_at=now()
RETURNING *;

-- name: GetLessonProgress :one
SELECT * FROM lesson_progress WHERE enrollment_id=$1 AND lesson_id=$2;

-- name: ListEnrollmentProgress :many
SELECT p.*,l.title AS lesson_title,l.lesson_type,l.duration_seconds
FROM lesson_progress p JOIN lessons l ON l.id=p.lesson_id WHERE p.enrollment_id=sqlc.arg(enrollment_id)
 AND (sqlc.narg(cursor_updated_at)::timestamptz IS NULL OR (p.updated_at,p.id)<(sqlc.narg(cursor_updated_at),sqlc.narg(cursor_id)::uuid))
ORDER BY p.updated_at DESC,p.id DESC LIMIT sqlc.arg(page_size);

-- name: CalculateLessonCompletion :one
SELECT count(*) FILTER(WHERE l.is_required) AS required_count,
 count(*) FILTER(WHERE l.is_required AND p.state='completed') AS completed_count
FROM lessons l JOIN course_modules m ON m.id=l.module_id
LEFT JOIN lesson_progress p ON p.lesson_id=l.id AND p.enrollment_id=sqlc.arg(enrollment_id)
WHERE m.course_id=sqlc.arg(course_id) AND l.is_published;

-- name: UpdateEnrollmentCompletionPercentage :one
UPDATE enrollments SET completion_percentage=$2,updated_at=now() WHERE id=$1 RETURNING *;

-- name: ResumeLearning :one
SELECT e.id AS enrollment_id,c.id AS course_id,c.title AS course_title,l.id AS lesson_id,l.title AS lesson_title,p.last_position_seconds,p.updated_at
FROM enrollments e JOIN courses c ON c.id=e.course_id
JOIN lesson_progress p ON p.enrollment_id=e.id
JOIN lessons l ON l.id=p.lesson_id
WHERE e.student_id=$1 AND e.status='active' AND p.state<>'completed'
ORDER BY p.updated_at DESC,p.id DESC LIMIT 1;
