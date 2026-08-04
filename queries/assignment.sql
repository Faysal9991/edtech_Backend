-- name: CreateAssignment :one
INSERT INTO assignments (id,organization_id,course_id,lesson_id,title,instructions,due_at,maximum_score,passing_score,allowed_file_types,maximum_submissions,is_required)
VALUES (sqlc.arg(id),sqlc.arg(organization_id),sqlc.arg(course_id),sqlc.narg(lesson_id),sqlc.arg(title),sqlc.arg(instructions),sqlc.narg(due_at),sqlc.arg(maximum_score),sqlc.arg(passing_score),sqlc.arg(allowed_file_types),sqlc.arg(maximum_submissions),sqlc.arg(is_required)) RETURNING *;

-- name: GetAssignment :one
SELECT * FROM assignments WHERE id=$1;

-- name: AddAssignmentAsset :exec
INSERT INTO assignment_assets (assignment_id,media_asset_id) VALUES ($1,$2) ON CONFLICT DO NOTHING;

-- name: DeleteAssignmentAssets :exec
DELETE FROM assignment_assets WHERE assignment_id=$1;

-- name: ListAssignmentAssetIDs :many
SELECT media_asset_id FROM assignment_assets WHERE assignment_id=$1 ORDER BY media_asset_id;

-- name: ListCourseAssignments :many
SELECT * FROM assignments WHERE course_id=sqlc.arg(course_id)
 AND (sqlc.arg(include_unpublished)::boolean OR status='published')
 AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(cursor_created_at),sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_size);

-- name: UpdateAssignment :one
UPDATE assignments SET title=sqlc.arg(title),instructions=sqlc.arg(instructions),due_at=sqlc.narg(due_at),maximum_score=sqlc.arg(maximum_score),passing_score=sqlc.arg(passing_score),allowed_file_types=sqlc.arg(allowed_file_types),maximum_submissions=sqlc.arg(maximum_submissions),is_required=sqlc.arg(is_required),updated_at=now() WHERE id=sqlc.arg(id) RETURNING *;

-- name: DeleteAssignment :execrows
DELETE FROM assignments WHERE id=$1 AND status='draft';

-- name: SetAssignmentStatus :one
UPDATE assignments SET status=$2,updated_at=now() WHERE id=$1 RETURNING *;

-- name: NextAssignmentSubmissionNumber :one
SELECT COALESCE(max(submission_number),0)::integer+1 FROM assignment_submissions WHERE assignment_id=$1 AND student_id=$2;

-- name: CreateAssignmentSubmission :one
INSERT INTO assignment_submissions (id,assignment_id,enrollment_id,student_id,submission_number,text_content)
VALUES ($1,$2,$3,$4,$5,$6) RETURNING *;

-- name: AddSubmissionAsset :exec
INSERT INTO assignment_submission_assets (submission_id,media_asset_id) VALUES ($1,$2) ON CONFLICT DO NOTHING;

-- name: DeleteSubmissionAssets :exec
DELETE FROM assignment_submission_assets WHERE submission_id=$1;

-- name: ListSubmissionAssetIDs :many
SELECT media_asset_id FROM assignment_submission_assets WHERE submission_id=$1 ORDER BY media_asset_id;

-- name: GetAssignmentSubmission :one
SELECT * FROM assignment_submissions WHERE id=$1;

-- name: GetAssignmentSubmissionForUpdate :one
SELECT * FROM assignment_submissions WHERE id=$1 FOR UPDATE;

-- name: ListAssignmentSubmissions :many
SELECT * FROM assignment_submissions
WHERE assignment_id=sqlc.arg(assignment_id) AND (sqlc.narg(student_id)::uuid IS NULL OR student_id=sqlc.narg(student_id))
 AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(cursor_created_at),sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_size);

-- name: UpdateAssignmentSubmissionDraft :one
UPDATE assignment_submissions SET text_content=$2,updated_at=now() WHERE id=$1 AND status IN ('draft','returned') RETURNING *;

-- name: SubmitAssignmentSubmission :one
UPDATE assignment_submissions SET status=$2,submitted_at=COALESCE(submitted_at,now()),updated_at=now() WHERE id=$1 AND status IN ('draft','returned') RETURNING *;

-- name: UpsertAssignmentGrade :one
INSERT INTO grades (id,assignment_submission_id,student_id,graded_by,points,percentage,feedback)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT(assignment_submission_id) DO UPDATE SET graded_by=EXCLUDED.graded_by,points=EXCLUDED.points,percentage=EXCLUDED.percentage,feedback=EXCLUDED.feedback,updated_at=now() RETURNING *;

-- name: SetAssignmentSubmissionStatus :one
UPDATE assignment_submissions SET status=$2,updated_at=now() WHERE id=$1 RETURNING *;
