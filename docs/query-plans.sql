\set ON_ERROR_STOP on
ANALYZE;
EXPLAIN (ANALYZE, BUFFERS) SELECT id,title,published_at FROM courses WHERE organization_id=(SELECT id FROM organizations ORDER BY id LIMIT 1) AND status='published' ORDER BY published_at DESC,id DESC LIMIT 26;
EXPLAIN (ANALYZE, BUFFERS) SELECT id,course_id,status,updated_at FROM enrollments WHERE student_id=(SELECT id FROM users ORDER BY id LIMIT 1) ORDER BY updated_at DESC,id DESC LIMIT 26;
EXPLAIN (ANALYZE, BUFFERS) SELECT id FROM outbox_events WHERE status IN ('pending','failed') AND next_attempt_at<=now() ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT 50;
