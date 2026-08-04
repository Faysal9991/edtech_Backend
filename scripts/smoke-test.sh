#!/usr/bin/env bash
set -euo pipefail

api_url="${PUBLIC_API_URL:-http://localhost:8080}"
database_url="${DATABASE_URL:-postgres://lms:lms@localhost:5432/lms?sslmode=disable}"
student_token="dev:student1@acme.test"
instructor_token="dev:instructor1@acme.test"
auth=(-H "Authorization: Bearer ${student_token}")

curl -fsS "${api_url}/health/ready" >/dev/null
me_json=$(curl -fsS -X POST "${auth[@]}" "${api_url}/api/v1/auth/bootstrap")
organization_id=$(jq -er '.memberships[0].organization_id' <<<"${me_json}")
course_json=$(curl -fsS "${auth[@]}" "${api_url}/api/v1/courses" | jq -ec '.items[] | select(.is_free == true)' | head -1)
course_id=$(jq -er '.id' <<<"${course_json}")
enrollment_json=$(curl -fsS -X POST "${auth[@]}" -H 'Idempotency-Key: smoke-free-enrollment' "${api_url}/api/v1/courses/${course_id}/enroll")
enrollment_id=$(jq -er '.id' <<<"${enrollment_json}")
detail_json=$(curl -fsS "${auth[@]}" "${api_url}/api/v1/courses/${course_id}")
while IFS= read -r lesson_id; do
  curl -fsS -X PUT "${auth[@]}" -H 'Content-Type: application/json' --data '{"position_seconds":0,"watched_seconds_delta":0,"manual_complete":true}' "${api_url}/api/v1/enrollments/${enrollment_id}/lessons/${lesson_id}/progress" >/dev/null
done < <(jq -er '.modules[].lessons[] | select(.type != "video") | .id' <<<"${detail_json}")

quiz_id=$(curl -fsS "${auth[@]}" "${api_url}/api/v1/quizzes?course_id=${course_id}" | jq -er '.items[0].id')
attempt_json=$(curl -fsS -X POST "${auth[@]}" "${api_url}/api/v1/quizzes/${quiz_id}/attempts")
attempt_id=$(jq -er '.id' <<<"${attempt_json}")
question_id=$(jq -er '.questions[0].id' <<<"${attempt_json}")
option_id=$(jq -er '.questions[0].options[0].id' <<<"${attempt_json}")
curl -fsS -X PUT "${auth[@]}" -H 'Content-Type: application/json' --data "{\"question_id\":\"${question_id}\",\"selected_option_ids\":[\"${option_id}\"]}" "${api_url}/api/v1/quiz-attempts/${attempt_id}/answers" >/dev/null
curl -fsS -X POST "${auth[@]}" "${api_url}/api/v1/quiz-attempts/${attempt_id}/submit" | jq -e '.passed == true' >/dev/null

assignment_id=$(curl -fsS "${auth[@]}" "${api_url}/api/v1/assignments?course_id=${course_id}" | jq -er '.items[0].id')
submission_json=$(curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' --data '{"text_content":"Smoke-test assignment submission."}' "${api_url}/api/v1/assignments/${assignment_id}/submissions")
submission_id=$(jq -er '.id' <<<"${submission_json}")
curl -fsS -X POST "${auth[@]}" "${api_url}/api/v1/assignment-submissions/${submission_id}/submit" >/dev/null
curl -fsS -X POST -H "Authorization: Bearer ${instructor_token}" -H "X-Organization-ID: ${organization_id}" -H 'Content-Type: application/json' --data '{"points":95,"feedback":"Smoke test passed."}' "${api_url}/api/v1/assignment-submissions/${submission_id}/grade" >/dev/null

paid_course_id=$(curl -fsS "${auth[@]}" "${api_url}/api/v1/courses" | jq -er '.items[] | select(.is_free == false) | .id' | head -1)
order_json=$(curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' -H 'Idempotency-Key: smoke-paid-order-v1' --data "{\"course_id\":\"${paid_course_id}\"}" "${api_url}/api/v1/orders")
order_id=$(jq -er '.id' <<<"${order_json}")
replayed_order_id=$(curl -fsS -X POST "${auth[@]}" -H 'Content-Type: application/json' -H 'Idempotency-Key: smoke-paid-order-v1' --data "{\"course_id\":\"${paid_course_id}\"}" "${api_url}/api/v1/orders" | jq -er '.id')
[[ "${order_id}" == "${replayed_order_id}" ]]
curl -fsS -X POST "${auth[@]}" "${api_url}/api/v1/orders/${order_id}/payment-intent" | jq -e '.client_secret | length > 0' >/dev/null
stripe_timestamp=$(date +%s)
stripe_event=$(jq -cn --arg order_id "${order_id}" --arg intent_id "pi_test_${order_id}" '{id:"evt_smoke_payment",type:"payment_intent.succeeded",data:{object:{id:$intent_id,status:"succeeded",amount_received:100000,currency:"bdt",metadata:{order_id:$order_id}}}}')
stripe_secret="${STRIPE_WEBHOOK_SECRET:-whsec_replace_me}"
stripe_signature=$(printf '%s.%s' "${stripe_timestamp}" "${stripe_event}" | openssl dgst -sha256 -hmac "${stripe_secret}" | awk '{print $NF}')
for _ in 1 2; do
  curl -fsS -X POST -H 'Content-Type: application/json' -H "Stripe-Signature: t=${stripe_timestamp},v1=${stripe_signature}" --data "${stripe_event}" "${api_url}/api/v1/webhooks/stripe" >/dev/null
done
curl -fsS "${auth[@]}" "${api_url}/api/v1/enrollments" | jq -e --arg course_id "${paid_course_id}" '.items | any(.course_id == $course_id and .status == "active")' >/dev/null

for _ in $(seq 1 30); do
  certificate_json=$(curl -fsS "${auth[@]}" "${api_url}/api/v1/certificates" | jq -ec '.items[]? | select(.status == "ready")' | head -1 || true)
  if [[ -n "${certificate_json}" ]]; then
    certificate_id=$(jq -er '.id' <<<"${certificate_json}")
    curl -fsS -o /dev/null -L "${auth[@]}" "${api_url}/api/v1/certificates/${certificate_id}/download"
    verification_code=$(psql "${database_url}" -Atc "select verification_code from certificates where id='${certificate_id}'")
    curl -fsS "${api_url}/api/v1/public/certificates/verify/${verification_code}" | jq -e '.valid == true' >/dev/null
    echo "smoke test passed"
    exit 0
  fi
  sleep 1
done

echo "certificate was not ready within 30 seconds" >&2
exit 1
