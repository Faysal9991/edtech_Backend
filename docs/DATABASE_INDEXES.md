# Database indexes and supported queries

PostgreSQL constraints already create indexes for primary keys and unique keys. The schema does not duplicate those indexes. All list queries use a bounded limit and a deterministic `(timestamp, id)` cursor so equal timestamps cannot skip or duplicate records.

| Index | Predicate/order it supports |
|---|---|
| `users_email_normalized_uidx` | Case-insensitive registration/login lookup and email uniqueness. |
| `users_status_created_idx` | Admin user lists filtered by account state and ordered by creation. |
| `users_search_trgm_idx` | Bounded admin search across email and display name. |
| `user_roles_role_user_idx` | Admin user role filters and RBAC reverse lookup. |
| `refresh_sessions_user_active_idx` | Logout-all, active-session inspection, and cleanup without scanning revoked history. |
| `refresh_sessions_family_idx` | Family-wide revocation after refresh-token reuse. |
| Active verification/reset token partial unique indexes | One outstanding single-use workflow per user. Token hashes have their own unique indexes. |
| `courses_global_catalogue_idx` | Public published-course cursor listing. |
| `courses_org_listing_idx` | Organization/status catalogue and publication ordering. |
| `courses_managed_cursor_idx` | Teacher/admin managed-course cursor list. |
| `courses_search_gin_idx`, `courses_title_trgm_idx` | Full-text catalogue search with typo-tolerant title fallback. |
| `course_instructors_instructor_course_idx` | Teacher-owned course filtering and ownership checks. |
| Unique `(course_id, position)` and `(module_id, position)` | Ordered module/lesson inserts and deterministic content reads. |
| `enrollments_student_listing_idx`, `enrollments_student_cursor_idx` | Student enrollment list by state/update cursor. |
| `enrollments_course_listing_idx`, `enrollments_course_cursor_idx` | Course roster by state/create cursor. |
| `enrollments_org_reporting_idx` | Date-range enrollment and completion aggregates. |
| Unique `(course_id, student_id)` | Concurrency-safe prevention of duplicate enrollment. |
| `lesson_progress_incomplete_idx`, `lesson_progress_enrollment_cursor_idx` | Resume-learning, incomplete progress, and bounded progress history. |
| Unique `(enrollment_id, lesson_id)` | Idempotent per-lesson progress upsert. |
| `quiz_attempts_in_progress_uidx` | One active attempt for a student/quiz under concurrent requests. |
| `quiz_attempts_student_idx`, `quiz_attempts_quiz_student_cursor_idx` | Student attempt history and teacher quiz analytics. |
| `quiz_attempts_graded_report_idx` | Date-bounded teacher quiz averages with percentage available from the index. |
| `assignment_submissions_*_cursor_idx` | Student history and teacher submission queues without N+1 queries. |
| `assignment_submissions_report_idx` | Date-bounded teacher submission and grading counts. |
| `assignment_submissions_one_active_uidx` | Enforces one editable/waiting submission per student and assignment under concurrency. |
| Unique `(assignment_id, student_id, submission_number)` | Submission/resubmission sequencing. |
| `live_sessions_active_idx`, `live_sessions_org_cursor_idx` | Upcoming-live-class jobs and bounded schedule lists. |
| `live_attendance_one_open_uidx` | One open attendance interval per provider participant identity. |
| Certificate enrollment/number/verification unique constraints | At-most-one certificate and constant-time public verification lookup. |
| `course_results_student_created_idx`, `course_results_course_passed_idx` | Student result history and course pass/completion reports. |
| `orders_user_cursor_idx`, `orders_org_reporting_idx` | Student payment history and admin revenue/status/date reports. |
| `outbox_pending_idx`, `outbox_processing_lease_idx` | Due outbox claims and recovery of work abandoned by a crashed worker. |
| Payment intent/transaction/webhook unique indexes | Provider idempotency and replay-safe event processing. |
| `notifications_user_listing_idx`, `notifications_unread_idx` | Notification cursor list and unread count/read operations. |
| `outbox_pending_idx` | `FOR UPDATE SKIP LOCKED` publisher claims by due time; dead-letter rows are excluded. |
| `audit_logs_org_created_idx`, `audit_logs_actor_created_idx` | Organization audit history and security investigation by actor. |

Run representative planner checks against realistic statistics with:

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f docs/query-plans.sql
```

Production plan reviews should use sanitized parameters and `EXPLAIN (ANALYZE, BUFFERS, WAL)` on a replica or staging-sized database. Do not run `ANALYZE` variants for expensive report ranges on a busy primary.
