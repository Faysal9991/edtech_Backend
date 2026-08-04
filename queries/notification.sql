-- name: RegisterDeviceToken :one
INSERT INTO device_tokens (id,user_id,token_hash,encrypted_token,platform,last_seen_at)
VALUES ($1,$2,$3,$4,$5,now()) ON CONFLICT(token_hash) DO UPDATE SET user_id=EXCLUDED.user_id,encrypted_token=EXCLUDED.encrypted_token,platform=EXCLUDED.platform,last_seen_at=now() RETURNING *;

-- name: RemoveDeviceToken :execrows
DELETE FROM device_tokens WHERE id=$1 AND user_id=$2;

-- name: ListNotifications :many
SELECT * FROM notifications WHERE user_id=sqlc.arg(user_id)
 AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(cursor_created_at),sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_size);

-- name: NotificationUnreadCount :one
SELECT count(*) FROM notifications WHERE user_id=$1 AND read_at IS NULL;

-- name: MarkNotificationRead :one
UPDATE notifications SET read_at=COALESCE(read_at,now()) WHERE id=$1 AND user_id=$2 RETURNING *;

-- name: MarkAllNotificationsRead :execrows
UPDATE notifications SET read_at=now() WHERE user_id=$1 AND read_at IS NULL;

-- name: InsertOutboxEvent :exec
INSERT INTO outbox_events (id,aggregate_type,aggregate_id,event_type,payload,deduplication_key)
VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT(deduplication_key) DO NOTHING;

-- name: ClaimOutboxEvents :many
WITH claimed AS (
 SELECT id FROM outbox_events WHERE status IN ('pending','failed') AND next_attempt_at<=now()
 ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT $1
)
UPDATE outbox_events o SET status='processing',attempts=attempts+1
FROM claimed WHERE o.id=claimed.id RETURNING o.*;

-- name: CreateNotification :one
INSERT INTO notifications (id,user_id,organization_id,type,title,body,data,deduplication_key)
VALUES (sqlc.arg(id),sqlc.arg(user_id),sqlc.narg(organization_id),sqlc.arg(type),sqlc.arg(title),sqlc.arg(body),sqlc.arg(data),sqlc.narg(deduplication_key))
ON CONFLICT(user_id,deduplication_key) WHERE deduplication_key IS NOT NULL DO UPDATE SET title=EXCLUDED.title RETURNING *;

-- name: ListUserDeviceTokens :many
SELECT * FROM device_tokens WHERE user_id=$1 ORDER BY last_seen_at DESC,id;

-- name: CreateNotificationDelivery :one
INSERT INTO notification_deliveries (id,notification_id,device_token_id,status,next_attempt_at)
VALUES ($1,$2,$3,'pending',now()) ON CONFLICT(notification_id,device_token_id) DO UPDATE SET updated_at=now() RETURNING *;

-- name: SetNotificationDeliveryResult :one
UPDATE notification_deliveries SET status=$2,attempts=attempts+1,next_attempt_at=$3,provider_message_id=$4,last_error=$5,sent_at=CASE WHEN $2='sent' THEN now() ELSE sent_at END,updated_at=now() WHERE id=$1 RETURNING *;

-- name: DeleteDeviceTokenByID :execrows
DELETE FROM device_tokens WHERE id=$1;

-- name: SetOutboxPublished :one
UPDATE outbox_events SET status='published',published_at=now(),last_error=NULL WHERE id=$1 RETURNING *;

-- name: SetOutboxFailed :one
UPDATE outbox_events SET status='failed',next_attempt_at=$2,last_error=$3 WHERE id=$1 RETURNING *;

-- name: ListOrganizationStudentIDs :many
SELECT DISTINCT m.user_id FROM organization_memberships m JOIN membership_roles mr ON mr.membership_id=m.id JOIN roles r ON r.id=mr.role_id
WHERE m.organization_id=$1 AND m.status='active' AND r.code='student' ORDER BY m.user_id;

-- name: ListDueLiveReminders :many
SELECT l.id AS live_session_id,l.organization_id,e.student_id AS user_id,l.title,l.scheduled_start_at
FROM live_sessions l JOIN enrollments e ON e.course_id=l.course_id AND e.status='active'
WHERE l.status='scheduled' AND l.scheduled_start_at>now() AND l.scheduled_start_at<=now()+interval '15 minutes'
ORDER BY l.scheduled_start_at,l.id,e.student_id LIMIT $1;

-- name: ListDueAssignmentReminders :many
SELECT a.id AS assignment_id,a.organization_id,e.student_id AS user_id,a.title,a.due_at
FROM assignments a JOIN enrollments e ON e.course_id=a.course_id AND e.status='active'
WHERE a.status='published' AND a.due_at>now() AND a.due_at<=now()+interval '24 hours'
ORDER BY a.due_at,a.id,e.student_id LIMIT $1;
