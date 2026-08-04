-- name: CreateMediaAsset :one
INSERT INTO media_assets (id,organization_id,owner_user_id,kind,status,storage_key,original_filename,content_type,size_bytes,checksum_sha256)
VALUES (sqlc.arg(id),sqlc.arg(organization_id),sqlc.arg(owner_user_id),sqlc.arg(kind),'pending',sqlc.arg(storage_key),sqlc.arg(original_filename),sqlc.arg(content_type),sqlc.arg(size_bytes),sqlc.narg(checksum_sha256)) RETURNING *;

-- name: CreateUploadIntent :one
INSERT INTO upload_intents (id,media_asset_id,owner_user_id,expected_size_bytes,expected_content_type,expected_checksum_sha256,expires_at)
VALUES (sqlc.arg(id),sqlc.arg(media_asset_id),sqlc.arg(owner_user_id),sqlc.arg(expected_size_bytes),sqlc.arg(expected_content_type),sqlc.narg(expected_checksum_sha256),sqlc.arg(expires_at)) RETURNING *;

-- name: GetUploadIntentWithAsset :one
SELECT i.id,i.media_asset_id,i.owner_user_id,i.expected_size_bytes,i.expected_content_type,i.expected_checksum_sha256,i.status,i.expires_at,i.completed_at,
 a.organization_id,a.kind,a.status AS asset_status,a.storage_key,a.content_type,a.size_bytes,a.checksum_sha256
FROM upload_intents i JOIN media_assets a ON a.id=i.media_asset_id WHERE i.id=$1;

-- name: GetUploadIntentForUpdate :one
SELECT * FROM upload_intents WHERE id=$1 FOR UPDATE;

-- name: CompleteUploadIntent :one
UPDATE upload_intents SET status='completed',completed_at=COALESCE(completed_at,now()) WHERE id=$1 AND status IN ('pending','completed') RETURNING *;

-- name: SetMediaAssetStatus :one
UPDATE media_assets SET status=$2,failure_reason=$3,updated_at=now() WHERE id=$1 RETURNING *;

-- name: SetMediaAssetProcessed :one
UPDATE media_assets SET status='ready',duration_seconds=$2,width=$3,height=$4,metadata=$5,failure_reason=NULL,updated_at=now() WHERE id=$1 RETURNING *;

-- name: GetMediaAsset :one
SELECT * FROM media_assets WHERE id=$1;

-- name: CanUserAccessMedia :one
SELECT EXISTS(
 SELECT 1 FROM media_assets a WHERE a.id=sqlc.arg(media_asset_id) AND a.owner_user_id=sqlc.arg(user_id)
 UNION ALL
 SELECT 1 FROM lessons l JOIN course_modules m ON m.id=l.module_id JOIN enrollments e ON e.course_id=m.course_id
 WHERE l.media_asset_id=sqlc.arg(media_asset_id) AND e.student_id=sqlc.arg(user_id) AND e.status IN ('active','completed')
 UNION ALL
 SELECT 1 FROM lessons l JOIN course_modules m ON m.id=l.module_id
 WHERE l.media_asset_id=sqlc.arg(media_asset_id) AND l.is_preview AND l.is_published
 UNION ALL
 SELECT 1 FROM courses c WHERE c.thumbnail_asset_id=sqlc.arg(media_asset_id) AND c.status='published'
 UNION ALL
 SELECT 1 FROM lessons l JOIN course_modules m ON m.id=l.module_id JOIN course_instructors ci ON ci.course_id=m.course_id
 WHERE l.media_asset_id=sqlc.arg(media_asset_id) AND ci.instructor_id=sqlc.arg(user_id)
 UNION ALL
 SELECT 1 FROM assignment_submission_assets sa JOIN assignment_submissions s ON s.id=sa.submission_id
 WHERE sa.media_asset_id=sqlc.arg(media_asset_id) AND s.student_id=sqlc.arg(user_id)
 UNION ALL
 SELECT 1 FROM assignment_submission_assets sa JOIN assignment_submissions s ON s.id=sa.submission_id
 JOIN assignments x ON x.id=s.assignment_id JOIN course_instructors ci ON ci.course_id=x.course_id
 WHERE sa.media_asset_id=sqlc.arg(media_asset_id) AND ci.instructor_id=sqlc.arg(user_id)
 UNION ALL
 SELECT 1 FROM assignment_assets aa JOIN assignments x ON x.id=aa.assignment_id
 JOIN enrollments e ON e.course_id=x.course_id
 WHERE aa.media_asset_id=sqlc.arg(media_asset_id) AND e.student_id=sqlc.arg(user_id) AND e.status IN ('active','completed') AND x.status='published'
 UNION ALL
 SELECT 1 FROM assignment_assets aa JOIN assignments x ON x.id=aa.assignment_id JOIN course_instructors ci ON ci.course_id=x.course_id
 WHERE aa.media_asset_id=sqlc.arg(media_asset_id) AND ci.instructor_id=sqlc.arg(user_id)
 UNION ALL
 SELECT 1 FROM certificates c WHERE c.media_asset_id=sqlc.arg(media_asset_id) AND c.student_id=sqlc.arg(user_id)
) AS allowed;
