-- name: CreateLiveSession :one
INSERT INTO live_sessions (id,organization_id,course_id,title,description,room_name,scheduled_start_at,scheduled_end_at,created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *;

-- name: GetLiveSession :one
SELECT * FROM live_sessions WHERE id=$1;

-- name: GetLiveSessionByRoom :one
SELECT * FROM live_sessions WHERE room_name=$1;

-- name: ListLiveSessions :many
SELECT * FROM live_sessions
WHERE organization_id=sqlc.arg(organization_id) AND status=ANY(sqlc.arg(statuses)::text[])
 AND (sqlc.narg(cursor_scheduled_at)::timestamptz IS NULL OR (scheduled_start_at,id)>(sqlc.narg(cursor_scheduled_at),sqlc.narg(cursor_id)::uuid))
ORDER BY scheduled_start_at,id LIMIT sqlc.arg(page_size);

-- name: UpdateLiveSession :one
UPDATE live_sessions SET title=$2,description=$3,scheduled_start_at=$4,scheduled_end_at=$5,updated_at=now()
WHERE id=$1 AND status='scheduled' RETURNING *;

-- name: SetLiveSessionStatus :one
UPDATE live_sessions SET status=$2,started_at=CASE WHEN $2='live' THEN COALESCE(started_at,now()) ELSE started_at END,ended_at=CASE WHEN $2='ended' THEN COALESCE(ended_at,now()) ELSE ended_at END,updated_at=now() WHERE id=$1 RETURNING *;

-- name: CreateLiveWebhookEvent :execrows
INSERT INTO live_webhook_events (id,provider_event_id,event_type,payload,processed_at) VALUES ($1,$2,$3,$4,now()) ON CONFLICT(provider_event_id) DO NOTHING;

-- name: StartAttendanceInterval :execrows
INSERT INTO live_attendance_sessions (id,live_session_id,user_id,participant_identity,joined_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING;

-- name: GetOpenAttendanceIntervalForUpdate :one
SELECT * FROM live_attendance_sessions WHERE live_session_id=$1 AND participant_identity=$2 AND left_at IS NULL ORDER BY joined_at DESC LIMIT 1 FOR UPDATE;

-- name: CloseAttendanceInterval :one
UPDATE live_attendance_sessions SET left_at=$2,duration_seconds=GREATEST(0,extract(epoch FROM ($2-joined_at))::integer) WHERE id=$1 RETURNING *;
