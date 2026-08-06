\set ON_ERROR_STOP on
ANALYZE;
EXPLAIN (ANALYZE, BUFFERS) SELECT id,title,published_at FROM courses WHERE organization_id=(SELECT id FROM organizations ORDER BY id LIMIT 1) AND status='published' ORDER BY published_at DESC,id DESC LIMIT 26;
EXPLAIN (ANALYZE, BUFFERS) SELECT id,course_id,status,updated_at FROM enrollments WHERE student_id=(SELECT id FROM users ORDER BY id LIMIT 1) ORDER BY updated_at DESC,id DESC LIMIT 26;
EXPLAIN (ANALYZE, BUFFERS) SELECT id FROM outbox_events WHERE status IN ('pending','failed') AND next_attempt_at<=now() ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT 50;
-- Refresh family revocation / active-session lookup.
EXPLAIN (COSTS, VERBOSE)
SELECT id, expires_at FROM refresh_sessions
WHERE user_id = '018f0000-0000-7000-8000-000000000001'
  AND revoked_at IS NULL
ORDER BY expires_at DESC LIMIT 25;

-- Admin user filter with stable cursor and role existence predicate.
EXPLAIN (COSTS, VERBOSE)
SELECT u.id, u.email, u.status
FROM users u
WHERE u.status = 'active'
  AND EXISTS (
    SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id
    WHERE ur.user_id=u.id AND r.code='teacher'
  )
ORDER BY u.created_at DESC, u.id DESC LIMIT 25;

-- Admin payment date/status report.
EXPLAIN (COSTS, VERBOSE)
SELECT status, currency, count(*), sum(amount_minor)
FROM orders
WHERE organization_id = '018f0000-0000-7000-8000-000000000001'
  AND created_at >= now() - interval '30 days'
GROUP BY status, currency;

-- Outbox claim uses the partial due-time index and avoids worker contention.
EXPLAIN (COSTS, VERBOSE)
SELECT id FROM outbox_events
WHERE status IN ('pending','failed') AND next_attempt_at <= now()
ORDER BY next_attempt_at, id
FOR UPDATE SKIP LOCKED LIMIT 50;
