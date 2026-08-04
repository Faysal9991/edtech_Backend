-- name: CreateQuiz :one
INSERT INTO quizzes (id,organization_id,course_id,lesson_id,title,instructions,time_limit_seconds,attempt_limit,pass_percentage,randomize_questions,randomize_options,available_from,available_until,results_visibility,is_required)
VALUES (sqlc.arg(id),sqlc.arg(organization_id),sqlc.arg(course_id),sqlc.narg(lesson_id),sqlc.arg(title),sqlc.arg(instructions),sqlc.narg(time_limit_seconds),sqlc.arg(attempt_limit),sqlc.arg(pass_percentage),sqlc.arg(randomize_questions),sqlc.arg(randomize_options),sqlc.narg(available_from),sqlc.narg(available_until),sqlc.arg(results_visibility),sqlc.arg(is_required)) RETURNING *;

-- name: GetQuiz :one
SELECT * FROM quizzes WHERE id=$1;

-- name: ListCourseQuizzes :many
SELECT * FROM quizzes WHERE course_id=sqlc.arg(course_id)
 AND (sqlc.arg(include_unpublished)::boolean OR status='published')
 AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(cursor_created_at),sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_size);

-- name: UpdateQuiz :one
UPDATE quizzes SET title=sqlc.arg(title),instructions=sqlc.arg(instructions),time_limit_seconds=sqlc.narg(time_limit_seconds),attempt_limit=sqlc.arg(attempt_limit),pass_percentage=sqlc.arg(pass_percentage),randomize_questions=sqlc.arg(randomize_questions),randomize_options=sqlc.arg(randomize_options),available_from=sqlc.narg(available_from),available_until=sqlc.narg(available_until),results_visibility=sqlc.arg(results_visibility),is_required=sqlc.arg(is_required),updated_at=now() WHERE id=sqlc.arg(id) RETURNING *;

-- name: DeleteQuiz :execrows
DELETE FROM quizzes WHERE id=$1 AND status='draft';

-- name: CreateQuizQuestion :one
INSERT INTO quiz_questions (id,quiz_id,question_type,prompt,points,position) VALUES ($1,$2,$3,$4,$5,$6) RETURNING *;

-- name: GetQuizQuestion :one
SELECT * FROM quiz_questions WHERE id=$1;

-- name: UpdateQuizQuestion :one
UPDATE quiz_questions SET question_type=sqlc.arg(question_type),prompt=sqlc.arg(prompt),points=sqlc.arg(points),position=sqlc.arg(position),updated_at=now()
WHERE id=sqlc.arg(id) RETURNING *;

-- name: DeleteQuizQuestionOptions :exec
DELETE FROM quiz_question_options WHERE question_id=$1;

-- name: DeleteQuizQuestion :execrows
DELETE FROM quiz_questions q USING quizzes z WHERE q.id=sqlc.arg(id) AND z.id=q.quiz_id AND z.status='draft';

-- name: CreateQuizOption :one
INSERT INTO quiz_question_options (id,question_id,text,is_correct,position) VALUES ($1,$2,$3,$4,$5) RETURNING *;

-- name: ListQuizQuestionBank :many
SELECT q.id,q.question_type,q.prompt,q.points,q.position,q.explanation,
 o.id AS option_id,o.text AS option_text,o.is_correct,o.position AS option_position
FROM quiz_questions q LEFT JOIN quiz_question_options o ON o.question_id=q.id
WHERE q.quiz_id=$1 ORDER BY q.position,q.id,o.position,o.id;

-- name: NextQuizAttemptNumber :one
SELECT COALESCE(max(attempt_number),0)::integer+1 FROM quiz_attempts WHERE quiz_id=$1 AND student_id=$2;

-- name: CreateQuizAttempt :one
INSERT INTO quiz_attempts (id,quiz_id,enrollment_id,student_id,attempt_number,question_snapshot,question_order,started_at,expires_at,max_points)
VALUES (sqlc.arg(id),sqlc.arg(quiz_id),sqlc.arg(enrollment_id),sqlc.arg(student_id),sqlc.arg(attempt_number),sqlc.arg(question_snapshot),sqlc.arg(question_order),sqlc.arg(started_at),sqlc.narg(expires_at),sqlc.arg(max_points)) RETURNING *;

-- name: GetQuizAttempt :one
SELECT * FROM quiz_attempts WHERE id=$1;

-- name: GetQuizAttemptForUpdate :one
SELECT * FROM quiz_attempts WHERE id=$1 FOR UPDATE;

-- name: ListQuizAttempts :many
SELECT * FROM quiz_attempts WHERE quiz_id=sqlc.arg(quiz_id) AND student_id=sqlc.arg(student_id)
 AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(cursor_created_at),sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_size);

-- name: UpsertQuizAnswer :one
INSERT INTO quiz_answers (id,attempt_id,question_id,selected_option_ids,text_answer)
VALUES (sqlc.arg(id),sqlc.arg(attempt_id),sqlc.arg(question_id),sqlc.arg(selected_option_ids),sqlc.narg(text_answer))
ON CONFLICT(attempt_id,question_id) DO UPDATE SET selected_option_ids=EXCLUDED.selected_option_ids,text_answer=EXCLUDED.text_answer,updated_at=now() RETURNING *;

-- name: ListQuizAnswers :many
SELECT * FROM quiz_answers WHERE attempt_id=$1 ORDER BY created_at,id;

-- name: GetQuizAnswerByQuestion :one
SELECT * FROM quiz_answers WHERE attempt_id=$1 AND question_id=$2;

-- name: GradeQuizAnswer :one
UPDATE quiz_answers SET awarded_points=sqlc.arg(awarded_points),is_correct=sqlc.arg(is_correct),grader_feedback=sqlc.narg(grader_feedback),updated_at=now() WHERE id=sqlc.arg(id) RETURNING *;

-- name: SubmitQuizAttempt :one
UPDATE quiz_attempts SET status=sqlc.arg(status),submitted_at=COALESCE(submitted_at,now()),graded_at=CASE WHEN sqlc.arg(status)::text='graded' THEN now() ELSE graded_at END,score_points=sqlc.arg(score_points),percentage=sqlc.arg(percentage),passed=sqlc.arg(passed),updated_at=now() WHERE id=sqlc.arg(id) RETURNING *;

-- name: SetQuizStatus :one
UPDATE quizzes SET status=$2,updated_at=now() WHERE id=$1 RETURNING *;
