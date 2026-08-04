# PostgreSQL indexing

Indexes follow concrete filters and sort orders. Cursor predicates use the same ordered columns and UUID tie-breaker.

| Endpoint/query | Filter and ordering | Supporting index |
|---|---|---|
| user bootstrap | `firebase_uid`, normalized email | `users_firebase_uid_key`, `users_email_normalized_uidx` |
| membership authorization | `user_id, status`; unique org/user | `organization_memberships_user_status_idx`, unique constraint |
| organization member pages | `organization_id, created_at DESC, id` | `organization_memberships_org_cursor_idx` |
| course categories | `organization_id, created_at DESC, id` | `course_categories_org_cursor_idx` |
| published catalogue | `organization_id, status, published_at DESC, id` | `courses_org_listing_idx` |
| cross-organization catalogue | `status, published_at DESC, id` | `courses_global_catalogue_idx` |
| managed courses | `organization_id, created_at DESC, id` | `courses_managed_cursor_idx` |
| course search | weighted `search_vector`; typo title | `courses_search_gin_idx`, `courses_title_trgm_idx` |
| instructor courses | `instructor_id, course_id` | `course_instructors_instructor_course_idx` |
| ordered content | unique course/module positions | unique constraints on `(course_id, position)`, `(module_id, position)` |
| student enrollments | `student_id, status, updated_at DESC, id` | `enrollments_student_listing_idx` |
| course students | `course_id, status, created_at DESC, id` | `enrollments_course_listing_idx` |
| incomplete progress | enrollment and lesson where incomplete | `lesson_progress_incomplete_idx` |
| progress history | `enrollment_id, updated_at DESC, id` | `lesson_progress_enrollment_cursor_idx` |
| current quiz attempt | quiz/student where in progress | `quiz_attempts_in_progress_uidx` |
| quiz and attempt pages | course or quiz/student with stable created-time cursor | `quizzes_course_cursor_idx`, `quiz_attempts_quiz_student_cursor_idx` |
| assignment definitions | `course_id, created_at DESC, id` | `assignments_course_cursor_idx` |
| assignment history | assignment or assignment/student with stable created-time cursor | `assignment_submissions_course_cursor_idx`, `assignment_submissions_student_cursor_idx` |
| assignment attachment authorization | media/assignment | `assignment_assets_media_idx`, assignment-asset primary key |
| upcoming live classes | course/scheduled start and active partial | `live_sessions_course_schedule_idx`, `live_sessions_active_idx` |
| organization live-session pages | organization/status/scheduled start/id | `live_sessions_org_cursor_idx` |
| open attendance interval | one open room/participant interval | `live_attendance_one_open_uidx` |
| order history | user/status/created descending | `orders_user_listing_idx` |
| all-status order pages | user/created descending/id | `orders_user_cursor_idx` |
| certificate pages | student/issued descending/id | `certificates_student_cursor_idx` |
| payment history | order/created descending/id after user-order lookup | `payment_transactions_order_cursor_idx` |
| Stripe idempotency | provider event and PaymentIntent | payment event unique constraint, `orders_payment_intent_uidx` |
| unread notifications | user/created where unread | `notifications_unread_idx` |
| outbox worker | due rows where pending/failed | `outbox_pending_idx` |
| audit history | organization/created descending/id | `audit_logs_org_created_idx` |
| reports | organization/date/status | `enrollments_org_reporting_idx`, `orders_org_reporting_idx` |

Use realistic data before interpreting a plan. Examples:

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, title, published_at
FROM courses
WHERE organization_id = $1 AND status = 'published'
  AND (published_at, id) < ($2, $3)
ORDER BY published_at DESC, id DESC
LIMIT 26;

EXPLAIN (ANALYZE, BUFFERS)
SELECT id, course_id, status, updated_at
FROM enrollments
WHERE student_id = $1 AND status = 'active'
ORDER BY updated_at DESC, id DESC
LIMIT 26;

EXPLAIN (ANALYZE, BUFFERS)
SELECT id FROM outbox_events
WHERE status IN ('pending','failed') AND next_attempt_at <= now()
ORDER BY next_attempt_at, id
FOR UPDATE SKIP LOCKED
LIMIT 50;
```

Development plan check:

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f docs/query-plans.sql
```

For production, capture plans with representative parameters and compare actual rows, buffer hits, and execution time. Do not disable sequential scans to manufacture index use; small tables correctly prefer sequential scans.
