-- +goose Up
CREATE UNIQUE INDEX live_attendance_one_open_uidx
    ON live_attendance_sessions (live_session_id, participant_identity)
    WHERE left_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS live_attendance_one_open_uidx;
