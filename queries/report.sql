-- name: OrganizationOverview :one
SELECT
 (SELECT count(DISTINCT m.user_id) FROM organization_memberships m JOIN membership_roles mr ON mr.membership_id=m.id JOIN roles r ON r.id=mr.role_id JOIN users u ON u.id=m.user_id WHERE m.organization_id=sqlc.arg(organization_id) AND m.status='active' AND u.status='active' AND r.code='student') AS total_students,
 (SELECT count(DISTINCT m.user_id) FROM organization_memberships m JOIN membership_roles mr ON mr.membership_id=m.id JOIN roles r ON r.id=mr.role_id JOIN users u ON u.id=m.user_id WHERE m.organization_id=sqlc.arg(organization_id) AND m.status='active' AND u.status='active' AND r.code='instructor') AS total_instructors,
 (SELECT count(*) FROM courses c WHERE c.organization_id=sqlc.arg(organization_id) AND c.status='published') AS published_courses,
 (SELECT count(*) FROM enrollments e WHERE e.organization_id=sqlc.arg(organization_id)) AS total_enrollments,
 (SELECT count(*) FROM enrollments e WHERE e.organization_id=sqlc.arg(organization_id) AND e.status='active') AS active_enrollments,
 (SELECT count(*) FROM enrollments e WHERE e.organization_id=sqlc.arg(organization_id) AND e.status='completed') AS completed_enrollments,
 (SELECT count(*) FROM orders o WHERE o.organization_id=sqlc.arg(organization_id) AND o.status IN ('pending','processing','requires_action') AND o.created_at>=sqlc.arg(from_time) AND o.created_at<sqlc.arg(to_time)) AS pending_payments,
 (SELECT count(*) FROM orders o WHERE o.organization_id=sqlc.arg(organization_id) AND o.status='paid' AND o.created_at>=sqlc.arg(from_time) AND o.created_at<sqlc.arg(to_time)) AS paid_payments,
 (SELECT count(*) FROM orders o WHERE o.organization_id=sqlc.arg(organization_id) AND o.status='failed' AND o.created_at>=sqlc.arg(from_time) AND o.created_at<sqlc.arg(to_time)) AS failed_payments,
 (SELECT count(*) FROM orders o WHERE o.organization_id=sqlc.arg(organization_id) AND o.status='refunded' AND o.created_at>=sqlc.arg(from_time) AND o.created_at<sqlc.arg(to_time)) AS refunded_payments,
 (SELECT COALESCE(sum(o.amount_minor),0)::bigint FROM orders o WHERE o.organization_id=sqlc.arg(organization_id) AND o.status IN ('paid','refunded') AND o.created_at>=sqlc.arg(from_time) AND o.created_at<sqlc.arg(to_time)) AS gross_revenue_minor,
 (SELECT COALESCE(sum(r.amount_minor),0)::bigint FROM refunds r JOIN orders o ON o.id=r.order_id WHERE o.organization_id=sqlc.arg(organization_id) AND r.status='succeeded' AND r.created_at>=sqlc.arg(from_time) AND r.created_at<sqlc.arg(to_time)) AS refund_amount_minor;

-- name: TeacherOverview :one
SELECT
 count(DISTINCT c.id) AS total_courses,
 count(DISTINCT c.id) FILTER (WHERE c.status='published') AS published_courses,
 count(DISTINCT e.student_id) AS total_students,
 count(e.id) FILTER (WHERE e.status='active') AS active_enrollments,
 count(e.id) FILTER (WHERE e.status='completed') AS completed_enrollments,
 COALESCE(round(avg(e.completion_percentage),2),0)::numeric AS average_completion_percentage,
 COALESCE((SELECT round(avg(qa.percentage),2)
   FROM quiz_attempts qa
   JOIN quizzes q ON q.id=qa.quiz_id
   JOIN course_instructors qci ON qci.course_id=q.course_id
   JOIN courses qc ON qc.id=q.course_id
   WHERE qci.instructor_id=sqlc.arg(instructor_id)
     AND qc.organization_id=sqlc.arg(organization_id)
     AND qa.status='graded'
     AND qa.submitted_at>=sqlc.arg(from_time)
     AND qa.submitted_at<sqlc.arg(to_time)),0)::numeric AS average_quiz_percentage,
 (SELECT count(*)
   FROM assignment_submissions sub
   JOIN assignments a ON a.id=sub.assignment_id
   JOIN course_instructors aci ON aci.course_id=a.course_id
   JOIN courses ac ON ac.id=a.course_id
   WHERE aci.instructor_id=sqlc.arg(instructor_id)
     AND ac.organization_id=sqlc.arg(organization_id)
     AND sub.submitted_at>=sqlc.arg(from_time)
     AND sub.submitted_at<sqlc.arg(to_time)) AS assignment_submissions,
 (SELECT count(g.id)
   FROM grades g
   JOIN assignment_submissions sub ON sub.id=g.assignment_submission_id
   JOIN assignments a ON a.id=sub.assignment_id
   JOIN course_instructors aci ON aci.course_id=a.course_id
   JOIN courses ac ON ac.id=a.course_id
   WHERE aci.instructor_id=sqlc.arg(instructor_id)
     AND ac.organization_id=sqlc.arg(organization_id)
     AND sub.submitted_at>=sqlc.arg(from_time)
     AND sub.submitted_at<sqlc.arg(to_time)) AS graded_assignments
FROM course_instructors ci
JOIN courses c ON c.id=ci.course_id
LEFT JOIN enrollments e ON e.course_id=c.id
 AND e.created_at>=sqlc.arg(from_time)
 AND e.created_at<sqlc.arg(to_time)
WHERE ci.instructor_id=sqlc.arg(instructor_id)
  AND c.organization_id=sqlc.arg(organization_id);

-- name: EnrollmentTrend :many
SELECT date_trunc('day',created_at) AS day,count(*) AS enrollments,count(*) FILTER(WHERE status='completed') AS completions
FROM enrollments WHERE organization_id=$1 AND created_at>=$2 AND created_at<$3
GROUP BY 1 ORDER BY 1 LIMIT 366;

-- name: RevenueByCourse :many
SELECT oi.course_id,oi.course_title,count(DISTINCT o.id) AS orders,COALESCE(sum(oi.amount_minor),0)::bigint AS revenue_minor,oi.currency
FROM order_items oi JOIN orders o ON o.id=oi.order_id
WHERE o.organization_id=$1 AND o.status IN ('paid','refunded') AND o.created_at>=$2 AND o.created_at<$3
GROUP BY oi.course_id,oi.course_title,oi.currency ORDER BY revenue_minor DESC,oi.course_id LIMIT $4;

-- name: RevenueTrend :many
SELECT day,currency,sum(gross_minor)::bigint AS gross_minor,sum(refund_minor)::bigint AS refund_minor
FROM (
 SELECT date_trunc('day',o.created_at)::timestamptz AS day,o.currency,o.amount_minor AS gross_minor,0::bigint AS refund_minor
 FROM orders o
 WHERE o.organization_id=sqlc.arg(organization_id) AND o.status IN ('paid','refunded') AND o.created_at>=sqlc.arg(from_time) AND o.created_at<sqlc.arg(to_time)
 UNION ALL
 SELECT date_trunc('day',r.created_at)::timestamptz AS day,r.currency,0::bigint AS gross_minor,r.amount_minor AS refund_minor
 FROM refunds r JOIN orders o ON o.id=r.order_id
 WHERE o.organization_id=sqlc.arg(organization_id) AND r.status='succeeded' AND r.created_at>=sqlc.arg(from_time) AND r.created_at<sqlc.arg(to_time)
) ledger
GROUP BY day,currency ORDER BY day,currency LIMIT 1000;

-- name: CompletionByCourse :many
SELECT c.id AS course_id,c.title,count(e.id) AS total_enrollments,count(e.id) FILTER(WHERE e.status='completed') AS completed_enrollments,
 CASE WHEN count(e.id)=0 THEN 0 ELSE round(count(e.id) FILTER(WHERE e.status='completed')::numeric/count(e.id)*100,2) END AS completion_rate
FROM courses c LEFT JOIN enrollments e ON e.course_id=c.id AND e.created_at>=sqlc.arg(from_time) AND e.created_at<sqlc.arg(to_time)
WHERE c.organization_id=sqlc.arg(organization_id) GROUP BY c.id,c.title ORDER BY total_enrollments DESC,c.id LIMIT sqlc.arg(page_size);

-- name: AssessmentReport :many
SELECT c.id AS course_id,c.title,
 COALESCE((SELECT round(count(*) FILTER(WHERE qa.passed)::numeric/NULLIF(count(*),0)*100,2) FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id WHERE q.course_id=c.id AND qa.status='graded' AND qa.submitted_at>=sqlc.arg(from_time) AND qa.submitted_at<sqlc.arg(to_time)),0) AS quiz_pass_rate,
 COALESCE((SELECT round(count(DISTINCT s.student_id)::numeric/NULLIF((SELECT count(*) FROM enrollments e WHERE e.course_id=c.id AND e.status IN ('active','completed')),0)*100,2) FROM assignment_submissions s JOIN assignments a ON a.id=s.assignment_id WHERE a.course_id=c.id AND s.submitted_at>=sqlc.arg(from_time) AND s.submitted_at<sqlc.arg(to_time)),0) AS assignment_submission_rate
FROM courses c WHERE c.organization_id=sqlc.arg(organization_id) ORDER BY c.title,c.id LIMIT sqlc.arg(page_size);

-- name: LiveAttendanceReport :many
SELECT l.id AS live_session_id,l.course_id,l.title,l.scheduled_start_at,count(DISTINCT a.user_id) AS attendees,COALESCE(sum(a.duration_seconds),0)::bigint AS attendance_seconds
FROM live_sessions l LEFT JOIN live_attendance_sessions a ON a.live_session_id=l.id
WHERE l.organization_id=$1 AND l.scheduled_start_at>=$2 AND l.scheduled_start_at<$3
GROUP BY l.id ORDER BY l.scheduled_start_at DESC,l.id DESC LIMIT $4;
