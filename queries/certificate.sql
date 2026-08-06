-- name: CompletionRequirements :one
SELECT
 (SELECT count(*) FROM lessons l JOIN course_modules m ON m.id=l.module_id WHERE m.course_id=sqlc.arg(course_id) AND l.is_published AND l.is_required) AS required_lessons,
 (SELECT count(*) FROM lessons l JOIN course_modules m ON m.id=l.module_id JOIN lesson_progress p ON p.lesson_id=l.id WHERE m.course_id=sqlc.arg(course_id) AND l.is_published AND l.is_required AND p.enrollment_id=sqlc.arg(enrollment_id) AND p.state='completed') AS completed_lessons,
 (SELECT count(*) FROM quizzes q WHERE q.course_id=sqlc.arg(course_id) AND q.status='published' AND q.is_required) AS required_quizzes,
 (SELECT count(DISTINCT q.id) FROM quizzes q JOIN quiz_attempts a ON a.quiz_id=q.id WHERE q.course_id=sqlc.arg(course_id) AND q.status='published' AND q.is_required AND a.enrollment_id=sqlc.arg(enrollment_id) AND a.status='graded' AND a.passed) AS passed_quizzes,
 (SELECT count(*) FROM assignments a WHERE a.course_id=sqlc.arg(course_id) AND a.status='published' AND a.is_required) AS required_assignments,
 (SELECT count(DISTINCT a.id) FROM assignments a JOIN assignment_submissions s ON s.assignment_id=a.id JOIN grades g ON g.assignment_submission_id=s.id WHERE a.course_id=sqlc.arg(course_id) AND a.status='published' AND a.is_required AND s.enrollment_id=sqlc.arg(enrollment_id) AND s.status='graded' AND g.points>=a.passing_score) AS passed_assignments;

-- name: CreateCompletionSnapshot :one
INSERT INTO course_completion_snapshots (id,enrollment_id,required_lessons,completed_lessons,required_quizzes,passed_quizzes,required_assignments,passed_assignments,percentage,is_complete)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING *;

-- name: CreateCertificate :one
INSERT INTO certificates (id,organization_id,enrollment_id,student_id,course_id,certificate_number,verification_code,status,issued_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8)
ON CONFLICT(enrollment_id) DO UPDATE SET updated_at=now() RETURNING *;

-- name: GetCertificate :one
SELECT * FROM certificates WHERE id=$1;

-- name: GetCertificateForUpdate :one
SELECT * FROM certificates WHERE id=$1 FOR UPDATE;

-- name: ListUserCertificates :many
SELECT * FROM certificates WHERE student_id=sqlc.arg(student_id)
 AND (sqlc.narg(cursor_issued_at)::timestamptz IS NULL OR (issued_at,id)<(sqlc.narg(cursor_issued_at),sqlc.narg(cursor_id)::uuid))
ORDER BY issued_at DESC,id DESC LIMIT sqlc.arg(page_size);

-- name: VerifyCertificate :one
SELECT c.certificate_number,c.status,c.issued_at,u.display_name AS student_name,co.title AS course_title,o.name AS organization_name
FROM certificates c JOIN users u ON u.id=c.student_id JOIN courses co ON co.id=c.course_id JOIN organizations o ON o.id=c.organization_id
WHERE c.verification_code=$1 OR c.certificate_number=$1;

-- name: SetCertificateReady :one
UPDATE certificates SET status='ready',media_asset_id=$2,updated_at=now() WHERE id=$1 AND status='pending' RETURNING *;
