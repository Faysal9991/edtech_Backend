-- name: CreateOrder :one
INSERT INTO orders (id,organization_id,user_id,status,amount_minor,currency,idempotency_key)
VALUES ($1,$2,$3,'pending',$4,$5,$6) ON CONFLICT(user_id,idempotency_key) DO NOTHING RETURNING *;

-- name: CreateOrderItem :one
INSERT INTO order_items (id,order_id,course_id,course_title,amount_minor,currency) VALUES ($1,$2,$3,$4,$5,$6) RETURNING *;

-- name: GetOrder :one
SELECT * FROM orders WHERE id=$1;

-- name: GetOrderForUpdate :one
SELECT * FROM orders WHERE id=$1 FOR UPDATE;

-- name: GetOrderByUserIdempotency :one
SELECT o.*,oi.course_id FROM orders o JOIN order_items oi ON oi.order_id=o.id WHERE o.user_id=$1 AND o.idempotency_key=$2;

-- name: GetOrderByPaymentIntentForUpdate :one
SELECT * FROM orders WHERE provider_payment_intent_id=$1 FOR UPDATE;

-- name: SetOrderPaymentIntent :one
UPDATE orders SET provider_payment_intent_id=$2,status='processing',updated_at=now() WHERE id=$1 AND status IN ('pending','processing') RETURNING *;

-- name: SetOrderStatus :one
UPDATE orders SET status=$2,paid_at=CASE WHEN $2='paid' THEN COALESCE(paid_at,now()) ELSE paid_at END,updated_at=now() WHERE id=$1 RETURNING *;

-- name: ListUserOrders :many
SELECT * FROM orders WHERE user_id=sqlc.arg(user_id)
 AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(cursor_created_at),sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_size);

-- name: CreatePaymentWebhookEvent :execrows
INSERT INTO payment_webhook_events (id,provider,provider_event_id,event_type,payload,processed_at) VALUES ($1,'stripe',$2,$3,$4,now()) ON CONFLICT(provider,provider_event_id) DO NOTHING;

-- name: CreatePaymentTransaction :one
INSERT INTO payment_transactions (id,order_id,provider,provider_transaction_id,kind,status,amount_minor,currency,failure_code)
VALUES (sqlc.arg(id),sqlc.arg(order_id),'stripe',sqlc.arg(provider_transaction_id),sqlc.arg(kind),sqlc.arg(status),sqlc.arg(amount_minor),sqlc.arg(currency),sqlc.narg(failure_code))
ON CONFLICT(provider_transaction_id) DO UPDATE SET updated_at=now() RETURNING *;

-- name: ListUserPayments :many
SELECT p.* FROM payment_transactions p JOIN orders o ON o.id=p.order_id WHERE o.user_id=sqlc.arg(user_id)
 AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (p.created_at,p.id)<(sqlc.narg(cursor_created_at),sqlc.narg(cursor_id)::uuid))
ORDER BY p.created_at DESC,p.id DESC LIMIT sqlc.arg(page_size);

-- name: GetSuccessfulPaymentForOrder :one
SELECT * FROM payment_transactions WHERE order_id=$1 AND kind='payment' AND status='succeeded' ORDER BY created_at DESC LIMIT 1;

-- name: SumSucceededRefunds :one
SELECT COALESCE(sum(amount_minor),0)::bigint FROM refunds WHERE order_id=$1 AND status='succeeded';

-- name: CreateRefund :one
INSERT INTO refunds (id,order_id,payment_transaction_id,provider_refund_id,amount_minor,currency,status,reason)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(provider_refund_id) DO UPDATE SET updated_at=now() RETURNING *;
