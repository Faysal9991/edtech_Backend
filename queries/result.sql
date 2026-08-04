-- name: MyResults :many
SELECT e.id AS enrollment_id,e.course_id,c.title AS course_title,e.status,e.completion_percentage,e.completed_at,e.updated_at,
 COALESCE((SELECT avg(a.percentage) FROM quiz_attempts a WHERE a.enrollment_id=e.id AND a.status='graded'),0)::numeric(5,2) AS quiz_average,
 COALESCE((SELECT avg(g.percentage) FROM grades g JOIN assignment_submissions s ON s.id=g.assignment_submission_id WHERE s.enrollment_id=e.id),0)::numeric(5,2) AS assignment_average
FROM enrollments e JOIN courses c ON c.id=e.course_id WHERE e.student_id=sqlc.arg(student_id)
 AND (sqlc.narg(cursor_updated_at)::timestamptz IS NULL OR (e.updated_at,e.id)<(sqlc.narg(cursor_updated_at),sqlc.narg(cursor_id)::uuid))
ORDER BY e.updated_at DESC,e.id DESC LIMIT sqlc.arg(page_size);
