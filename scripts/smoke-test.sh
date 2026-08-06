#!/usr/bin/env bash
set -euo pipefail

api_url="${PUBLIC_API_URL:-http://localhost:8080}"
admin_email="${SEED_ADMIN_EMAIL:-admin@lms.local}"
admin_password="${SEED_ADMIN_PASSWORD:-local-admin-password-change-before-use}"
demo_password="${SEED_DEMO_PASSWORD:-local-demo-password-change-before-use}"
dummy_payment_secret="${DUMMY_PAYMENT_WEBHOOK_SECRET:-local-dummy-payment-webhook-secret-change-me}"
run_id="$(date +%s)-$$"
teacher_email="smoke-teacher-${run_id}@example.test"
student_email="smoke-student-${run_id}@example.test"
user_password="Smoke-Test-${run_id}-Password!"

command -v curl >/dev/null
command -v jq >/dev/null
command -v openssl >/dev/null

if [[ "${SMOKE_RUN_SEED:-true}" == "true" ]]; then
  if docker compose ps --status running postgres 2>/dev/null | grep -q postgres; then
    docker compose --profile tools run --rm seed >/dev/null
  else
    : "${DATABASE_URL:?DATABASE_URL is required when PostgreSQL is not running through this Compose project}"
    APP_ENV="${APP_ENV:-development}" \
      SEED_ADMIN_EMAIL="${admin_email}" \
      SEED_ADMIN_DISPLAY_NAME="${SEED_ADMIN_DISPLAY_NAME:-Development Administrator}" \
      SEED_ADMIN_PASSWORD="${admin_password}" \
      SEED_DEMO_PASSWORD="${demo_password}" \
      go run ./cmd/seed >/dev/null
  fi
fi

curl -fsS "${api_url}/health/ready" | jq -e '.status == "ready"' >/dev/null

login() {
  local email="$1" password="$2"
  curl -fsS -X POST -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg email "${email}" --arg password "${password}" '{email:$email,password:$password}')" \
    "${api_url}/api/v1/auth/login" | jq -er '.access_token'
}

register_and_verify() {
  local email="$1" display_name="$2" response token
  response="$(curl -fsS -X POST -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg email "${email}" --arg password "${user_password}" --arg name "${display_name}" '{email:$email,password:$password,display_name:$name}')" \
    "${api_url}/api/v1/auth/register")"
  token="$(jq -er '.verification_token' <<<"${response}")"
  curl -fsS -o /dev/null -X POST -H 'Content-Type: application/json' \
    --data "$(jq -cn --arg token "${token}" '{token:$token}')" \
    "${api_url}/api/v1/auth/verify-email"
  jq -er '.user.id' <<<"${response}"
}

admin_token="$(login "${admin_email}" "${admin_password}")"
admin_auth=(-H "Authorization: Bearer ${admin_token}")

teacher_id="$(register_and_verify "${teacher_email}" 'Smoke Teacher')"
student_id="$(register_and_verify "${student_email}" 'Smoke Student')"

curl -fsS -o /dev/null -X PUT "${admin_auth[@]}" -H 'Content-Type: application/json' \
  --data '{"roles":["teacher"]}' "${api_url}/api/v1/admin/users/${teacher_id}/roles"
curl -fsS -o /dev/null -X PUT "${admin_auth[@]}" -H 'Content-Type: application/json' \
  --data '{"roles":["student"]}' "${api_url}/api/v1/admin/users/${student_id}/roles"

teacher_token="$(login "${teacher_email}" "${user_password}")"
student_token="$(login "${student_email}" "${user_password}")"
teacher_auth=(-H "Authorization: Bearer ${teacher_token}")
student_auth=(-H "Authorization: Bearer ${student_token}")

course_slug="smoke-phase-1-${run_id}"
course_json="$(curl -fsS -X POST "${teacher_auth[@]}" -H 'Content-Type: application/json' \
  --data "$(jq -cn --arg slug "${course_slug}" '{title:"Smoke Phase-1 Course",slug:$slug,description:"End-to-end smoke course with valid published content.",language:"en",level:"beginner",is_free:true,price_minor:0,currency:"BDT",version:0}')" \
  "${api_url}/api/v1/teacher/courses")"
course_id="$(jq -er '.id' <<<"${course_json}")"

module_json="$(curl -fsS -X POST "${teacher_auth[@]}" -H 'Content-Type: application/json' \
  --data '{"title":"Smoke Module","description":"Required content","position":1}' \
  "${api_url}/api/v1/teacher/courses/${course_id}/modules")"
module_id="$(jq -er '.id' <<<"${module_json}")"

lesson_json="$(curl -fsS -X POST "${teacher_auth[@]}" -H 'Content-Type: application/json' \
  --data '{"title":"Smoke Lesson","description":"Complete this lesson","lesson_type":"text","body":"Smoke content","position":1,"is_preview":false,"is_required":true,"is_published":true}' \
  "${api_url}/api/v1/teacher/modules/${module_id}/lessons")"
lesson_id="$(jq -er '.id' <<<"${lesson_json}")"

curl -fsS -o /dev/null -X POST "${teacher_auth[@]}" "${api_url}/api/v1/teacher/courses/${course_id}/submit-review"
curl -fsS -o /dev/null -X POST "${admin_auth[@]}" "${api_url}/api/v1/admin/courses/${course_id}/publish"
curl -fsS "${api_url}/api/v1/courses/${course_slug}" | jq -e --arg id "${course_id}" '.course.id == $id or .id == $id' >/dev/null

enrollment_json="$(curl -fsS -X POST "${student_auth[@]}" -H "Idempotency-Key: smoke-enroll-${run_id}" \
  "${api_url}/api/v1/courses/${course_id}/enroll")"
enrollment_id="$(jq -er '.id' <<<"${enrollment_json}")"
curl -fsS -o /dev/null -X PUT "${student_auth[@]}" -H 'Content-Type: application/json' \
  --data '{"position_seconds":0,"watched_seconds_delta":0,"manual_complete":true}' \
  "${api_url}/api/v1/student/enrollments/${enrollment_id}/lessons/${lesson_id}/progress"

seed_course_json="$(curl -fsS "${api_url}/api/v1/courses/production-go-foundations")"
seed_course_id="$(jq -er '.course.id // .id' <<<"${seed_course_json}")"
curl -fsS -o /dev/null -X POST "${student_auth[@]}" -H "Idempotency-Key: smoke-seed-enroll-${run_id}" \
  "${api_url}/api/v1/courses/${seed_course_id}/enroll"

quiz_id="$(curl -fsS "${admin_auth[@]}" "${api_url}/api/v1/teacher/quizzes?course_id=${seed_course_id}" | jq -er '.items[0].id')"
quiz_detail="$(curl -fsS "${admin_auth[@]}" "${api_url}/api/v1/teacher/quizzes/${quiz_id}")"
correct_option_id="$(jq -er '.questions[0].options[] | select(.is_correct == true) | .id' <<<"${quiz_detail}")"
attempt_json="$(curl -fsS -X POST "${student_auth[@]}" "${api_url}/api/v1/student/quizzes/${quiz_id}/attempts")"
attempt_id="$(jq -er '.id' <<<"${attempt_json}")"
question_id="$(jq -er '.questions[0].id' <<<"${attempt_json}")"
if jq -e '.. | objects | has("correct") or has("is_correct")' <<<"${attempt_json}" >/dev/null; then
  echo 'student quiz payload leaked a correct-answer flag' >&2
  exit 1
fi
curl -fsS -o /dev/null -X PUT "${student_auth[@]}" -H 'Content-Type: application/json' \
  --data "$(jq -cn --arg question "${question_id}" --arg option "${correct_option_id}" '{question_id:$question,selected_option_ids:[$option]}')" \
  "${api_url}/api/v1/student/quiz-attempts/${attempt_id}/answers"
curl -fsS -X POST "${student_auth[@]}" "${api_url}/api/v1/student/quiz-attempts/${attempt_id}/submit" | jq -e '.passed == true' >/dev/null

assignment_id="$(curl -fsS "${admin_auth[@]}" "${api_url}/api/v1/teacher/assignments?course_id=${seed_course_id}" | jq -er '.items[0].id')"
submission_json="$(curl -fsS -X POST "${student_auth[@]}" -H 'Content-Type: application/json' \
  --data '{"text_content":"Phase-1 smoke assignment submission."}' \
  "${api_url}/api/v1/student/assignments/${assignment_id}/submissions")"
submission_id="$(jq -er '.id' <<<"${submission_json}")"
curl -fsS -o /dev/null -X POST "${student_auth[@]}" "${api_url}/api/v1/student/submissions/${submission_id}/submit"
curl -fsS -o /dev/null -X PATCH "${admin_auth[@]}" -H 'Content-Type: application/json' \
  --data '{"points":95,"feedback":"Smoke journey passed."}' \
  "${api_url}/api/v1/teacher/submissions/${submission_id}/grade"

live_id="$(curl -fsS "${admin_auth[@]}" "${api_url}/api/v1/teacher/live-classes?status=scheduled,live" | jq -er --arg course "${seed_course_id}" '.items[] | select(.course_id == $course) | .id' | head -1)"
curl -fsS -X POST "${student_auth[@]}" "${api_url}/api/v1/live-classes/${live_id}/token" | jq -e '.token | length > 40' >/dev/null

paid_course_json="$(curl -fsS "${api_url}/api/v1/courses/advanced-go-systems")"
paid_course_id="$(jq -er '.course.id // .id' <<<"${paid_course_json}")"
order_json="$(curl -fsS -X POST "${student_auth[@]}" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: smoke-order-${run_id}" --data "$(jq -cn --arg id "${paid_course_id}" '{course_id:$id}')" \
  "${api_url}/api/v1/payments/orders")"
order_id="$(jq -er '.id' <<<"${order_json}")"
replayed_order_id="$(curl -fsS -X POST "${student_auth[@]}" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: smoke-order-${run_id}" --data "$(jq -cn --arg id "${paid_course_id}" '{course_id:$id}')" \
  "${api_url}/api/v1/payments/orders" | jq -er '.id')"
[[ "${order_id}" == "${replayed_order_id}" ]]
curl -fsS -X POST "${student_auth[@]}" "${api_url}/api/v1/payments/orders/${order_id}/payment-intent" | jq -e '.client_secret | length > 0' >/dev/null

dummy_timestamp="$(date +%s)"
dummy_event="$(jq -cn --arg event "dummy_evt_smoke_${run_id}" --arg order "${order_id}" --arg payment "dummy_pi_${order_id}" '{id:$event,type:"payment.succeeded",payment_id:$payment,order_id:$order,status:"succeeded",amount_minor:100000,currency:"BDT"}')"
dummy_signature="$(printf '%s.%s' "${dummy_timestamp}" "${dummy_event}" | openssl dgst -sha256 -hmac "${dummy_payment_secret}" | awk '{print $NF}')"
for _ in 1 2; do
  curl -fsS -o /dev/null -X POST -H 'Content-Type: application/json' \
    -H "X-Dummy-Payment-Signature: t=${dummy_timestamp},v1=${dummy_signature}" --data "${dummy_event}" \
    "${api_url}/api/v1/payments/webhooks/dummy"
done
curl -fsS "${student_auth[@]}" "${api_url}/api/v1/student/enrollments" | \
  jq -e --arg course "${paid_course_id}" '.items | any(.course_id == $course and .status == "active")' >/dev/null

for _ in $(seq 1 45); do
  certificate_json="$(curl -fsS "${student_auth[@]}" "${api_url}/api/v1/student/certificates" | jq -ec --arg course "${course_id}" '.items[]? | select(.course_id == $course and .status == "ready")' | head -1 || true)"
  if [[ -n "${certificate_json}" ]]; then
    certificate_number="$(jq -er '.certificate_number' <<<"${certificate_json}")"
    curl -fsS "${api_url}/api/v1/certificates/verify/${certificate_number}" | jq -e '.valid == true' >/dev/null
    echo "smoke test passed: users, course, enrollment, progress, quiz, assignment, live token, dummy payment, and certificate"
    exit 0
  fi
  sleep 1
done

echo 'certificate was not ready within 45 seconds' >&2
exit 1
