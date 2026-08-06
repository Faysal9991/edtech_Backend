-- +goose Up
ALTER TABLE users ALTER COLUMN email TYPE citext USING email::citext;

ALTER TABLE courses
    ADD COLUMN passing_percentage numeric(5,2) NOT NULL DEFAULT 60
        CHECK (passing_percentage BETWEEN 0 AND 100);

ALTER TABLE media_assets DROP CONSTRAINT media_assets_status_check;
ALTER TABLE media_assets ADD CONSTRAINT media_assets_status_check
    CHECK (status IN ('pending','uploaded','processing','ready','failed','deleted'));

ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check
    CHECK (status IN ('pending','processing','requires_action','paid','failed','cancelled','refunded'));

ALTER TABLE outbox_events DROP CONSTRAINT outbox_events_status_check;
ALTER TABLE outbox_events ADD CONSTRAINT outbox_events_status_check
    CHECK (status IN ('pending','processing','published','failed','dead_letter'));
ALTER TABLE outbox_events ADD COLUMN dead_lettered_at timestamptz;
ALTER TABLE outbox_events ADD COLUMN claimed_at timestamptz;

CREATE INDEX quiz_attempts_graded_report_idx
    ON quiz_attempts (quiz_id, submitted_at) INCLUDE (percentage)
    WHERE status = 'graded';
CREATE INDEX assignment_submissions_report_idx
    ON assignment_submissions (assignment_id, submitted_at) INCLUDE (status)
    WHERE submitted_at IS NOT NULL;
CREATE UNIQUE INDEX assignment_submissions_one_active_uidx
    ON assignment_submissions (assignment_id, student_id)
    WHERE status IN ('draft','submitted','late','returned');
CREATE INDEX outbox_processing_lease_idx
    ON outbox_events (claimed_at)
    WHERE status = 'processing';

-- +goose Down
DROP INDEX IF EXISTS outbox_processing_lease_idx;
DROP INDEX IF EXISTS assignment_submissions_one_active_uidx;
DROP INDEX IF EXISTS assignment_submissions_report_idx;
DROP INDEX IF EXISTS quiz_attempts_graded_report_idx;
ALTER TABLE outbox_events DROP COLUMN IF EXISTS claimed_at;
ALTER TABLE outbox_events DROP COLUMN IF EXISTS dead_lettered_at;
ALTER TABLE outbox_events DROP CONSTRAINT outbox_events_status_check;
UPDATE outbox_events SET status = 'failed' WHERE status = 'dead_letter';
ALTER TABLE outbox_events ADD CONSTRAINT outbox_events_status_check
    CHECK (status IN ('pending','processing','published','failed'));
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
UPDATE orders SET status = 'processing' WHERE status = 'requires_action';
ALTER TABLE orders ADD CONSTRAINT orders_status_check
    CHECK (status IN ('pending','processing','paid','failed','cancelled','refunded'));
ALTER TABLE media_assets DROP CONSTRAINT media_assets_status_check;
UPDATE media_assets SET status = 'failed' WHERE status = 'deleted';
ALTER TABLE media_assets ADD CONSTRAINT media_assets_status_check
    CHECK (status IN ('pending','uploaded','processing','ready','failed'));
ALTER TABLE courses DROP COLUMN IF EXISTS passing_percentage;
ALTER TABLE users ALTER COLUMN email TYPE text USING email::text;
