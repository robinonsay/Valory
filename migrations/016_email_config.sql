-- migrations/016_email_config.sql
-- Sprint 19 — Admin-configurable email subsystem.
-- REQ-EMAIL-001..REQ-EMAIL-011
-- Adds SMTP config keys to system_config seed rows and a rate-limit table for
-- the admin test-send endpoint.
--
-- Rollback: DELETE FROM system_config WHERE key IN
--   ('smtp_host','smtp_port','smtp_from','smtp_username','smtp_encryption');
--   DROP TABLE IF EXISTS email_test_send_attempts;
BEGIN;

INSERT INTO schema_migrations (version) VALUES ('016_email_config')
    ON CONFLICT (version) DO NOTHING;

-- Seed rows use INSERT ... ON CONFLICT DO NOTHING so re-running the migration
-- on an existing DB with admin-set values is a no-op (i.e. operator changes
-- are preserved across container upgrades).
INSERT INTO system_config (key, value) VALUES
    ('smtp_host',       ''),
    ('smtp_port',       '587'),
    ('smtp_from',       ''),
    ('smtp_username',   ''),
    ('smtp_encryption', 'starttls')
ON CONFLICT (key) DO NOTHING;

-- Rate-limit table for POST /api/v1/admin/config/email/test.
-- Mirrors the password_reset_attempts pattern (migration 002).
-- No RLS required: only the admin handler under the valory_app role reads/writes
-- this table; there is no student-facing access path.
CREATE TABLE IF NOT EXISTS email_test_send_attempts (
    admin_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    attempted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS email_test_attempts_idx
    ON email_test_send_attempts (admin_id, attempted_at);

COMMIT;
