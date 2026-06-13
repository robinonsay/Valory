-- migrations/018_email_2fa.sql
-- Sprint 21 — Email-based two-factor authentication.
-- REQ-AUTH-015..REQ-AUTH-026, REQ-SYS-065, REQ-SYS-066
--
-- Creates the pending_2fa and otp_rate_limits tables, and seeds two new
-- system_config keys used by the 2FA toggle and prerequisites gate.
--
-- Rollback: see 018_email_2fa_rollback.sql

BEGIN;

INSERT INTO schema_migrations (version) VALUES ('018_email_2fa')
    ON CONFLICT (version) DO NOTHING;

-- pending_2fa holds short-lived pre-session state between password-OK and
-- OTP-verify. A row exists only for the duration of the pending window (max
-- 10 minutes). The table is NOT the sessions table; the auth middleware
-- explicitly rejects pending tokens because they are absent from sessions.
CREATE TABLE IF NOT EXISTS pending_2fa (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- pending_token_hash: SHA-256 of the raw pending token sent to the client.
    -- Same hashing approach as sessions.token_hash (see token.go:HashToken).
    pending_token_hash   TEXT        NOT NULL UNIQUE,
    -- otp_hash: SHA-256 of the 6-digit OTP string (e.g. "042817").
    -- SHA-256 is intentional — the attempt cap + 10-minute TTL make offline
    -- brute-force moot; SHA-256 avoids the ~100ms argon2id overhead per verify.
    otp_hash             TEXT        NOT NULL,
    -- attempt_count increments on each wrong OTP; row is deleted at count = 5.
    attempt_count        INT         NOT NULL DEFAULT 0,
    -- last_resend_at enables the 60-second resend throttle.
    last_resend_at       TIMESTAMPTZ,
    -- resend_count_24h is a rolling counter reset to 0 when resend_window_start
    -- is more than 24 hours ago. Maintained in application code, not a DB trigger.
    resend_count_24h     INT         NOT NULL DEFAULT 0,
    resend_window_start  TIMESTAMPTZ,
    expires_at           TIMESTAMPTZ NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for cleanup job and expiry checks.
CREATE INDEX IF NOT EXISTS idx_pending_2fa_expires_at ON pending_2fa (expires_at);
-- Index for user-level lookups (admin reset, break-glass).
CREATE INDEX IF NOT EXISTS idx_pending_2fa_user_id ON pending_2fa (user_id);

-- otp_rate_limits records each OTP send event for the daily cap.
-- Kept separate from pending_2fa so the daily cap survives pending row deletion
-- (e.g. after the attempt cap is hit, the pending row is deleted but subsequent
-- resend attempts for a new pending token must still count toward the daily cap).
-- Rows older than 24 hours are pruned at send time (no cron required).
CREATE TABLE IF NOT EXISTS otp_rate_limits (
    id        BIGSERIAL   PRIMARY KEY,
    user_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_otp_rate_limits_user_sent ON otp_rate_limits (user_id, sent_at);

-- system_config seed: add two_factor_enabled (off by default) and
-- email_test_send_verified_at (empty until a test-send has succeeded).
-- INSERT ... ON CONFLICT DO NOTHING makes re-runs safe (idempotent).
INSERT INTO system_config (key, value) VALUES ('two_factor_enabled', 'false')
    ON CONFLICT (key) DO NOTHING;

-- email_test_send_verified_at stores the RFC 3339 timestamp of the most recent
-- successful test-send. The 2FA toggle enable path reads this key and rejects
-- the write when it is empty. It is updated by the test-send handler on success.
INSERT INTO system_config (key, value) VALUES ('email_test_send_verified_at', '')
    ON CONFLICT (key) DO NOTHING;

COMMIT;
