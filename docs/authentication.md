# Authentication and session lifecycle

LMS Service owns first-party email/password authentication. Passwords are Argon2id PHC strings. Registration creates a `pending` account, student profile/roles, an active default-organization membership, and a hashed one-use email-verification token. Verification activates the account.

Login applies Redis/IP rate limits and a per-account lock threshold, verifies Argon2id in constant time, rechecks account state, and issues a short-lived signed access JWT plus an opaque refresh token. JWT validation requires HS256, the configured key ID, issuer, audience, `access` token type, UUID subject, and valid time claims. Database user state and roles are reloaded on every request rather than trusted from stale claims.

Each refresh token is represented by a SHA-256 hash in a session family. Rotation locks the presented row, marks it rotated, creates one child, and returns a new pair. Presenting a rotated or revoked token commits family-wide revocation and an audit event before returning unauthorized. Logout revokes one hash; logout-all, password change/reset, account suspension, and role replacement revoke every active session.

Forgot-password always returns the same accepted response. Reset and verification tokens expire and are consumed atomically. Development/test responses expose newly created workflow tokens so local completion does not require email credentials; production never returns or logs them.

Argon2id defaults are 64 MiB memory, three iterations, parallelism two, a 16-byte random salt, and a 32-byte key. Encoded parameters are strictly bounded before verification to prevent attacker-controlled memory/CPU amplification.
