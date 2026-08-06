-- +goose Up
ALTER TABLE payment_transactions DROP CONSTRAINT payment_transactions_provider_check;
ALTER TABLE payment_webhook_events DROP CONSTRAINT payment_webhook_events_provider_check;

UPDATE payment_transactions SET provider = 'dummy' WHERE provider = 'stripe';
UPDATE payment_webhook_events SET provider = 'dummy' WHERE provider = 'stripe';

ALTER TABLE payment_transactions ADD CONSTRAINT payment_transactions_provider_check
    CHECK (provider = 'dummy');
ALTER TABLE payment_webhook_events ADD CONSTRAINT payment_webhook_events_provider_check
    CHECK (provider = 'dummy');

-- +goose Down
ALTER TABLE payment_transactions DROP CONSTRAINT payment_transactions_provider_check;
ALTER TABLE payment_webhook_events DROP CONSTRAINT payment_webhook_events_provider_check;

UPDATE payment_transactions SET provider = 'stripe' WHERE provider = 'dummy';
UPDATE payment_webhook_events SET provider = 'stripe' WHERE provider = 'dummy';

ALTER TABLE payment_transactions ADD CONSTRAINT payment_transactions_provider_check
    CHECK (provider = 'stripe');
ALTER TABLE payment_webhook_events ADD CONSTRAINT payment_webhook_events_provider_check
    CHECK (provider = 'stripe');
