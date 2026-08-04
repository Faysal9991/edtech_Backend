# Notifications and outbox

Business transactions insert `outbox_events` with an aggregate, type, JSON payload, and unique deduplication key. The worker claims due rows with `FOR UPDATE SKIP LOCKED`, creates the in-app notification, creates one delivery per device, calls FCM, then marks the outbox row published.

At-least-once execution is safe because notification/user deduplication and notification/device delivery are unique. Failed outbox events and FCM deliveries use bounded exponential backoff. Permanently invalid registration tokens are removed.

Device tokens are never logged or returned by list endpoints. PostgreSQL stores a SHA-256 lookup hash and AES-256-GCM ciphertext. `DEVICE_TOKEN_ENCRYPTION_KEY` must be at least 32 random characters, supplied by a secret manager, and rotated with an explicit re-encryption migration.

Supported event copy covers invitation, enrollment, payment success/failure/refund, course publication, live reminders, assignment deadlines/grades, quiz results, completion, and certificate readiness. Event payloads may override copy and carry string-only navigation data for Flutter.

Recovery:

```sql
SELECT id,event_type,attempts,next_attempt_at,last_error
FROM outbox_events
WHERE status='failed'
ORDER BY next_attempt_at;
```

Fix the downstream issue, then move only the selected event's `next_attempt_at` to `now()`. Do not create a duplicate row with a new key.
