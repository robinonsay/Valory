-- migrations/017_must_change_password.sql
-- Sprint 20 — Admin-created account lifecycle.
-- REQ-USER-008..REQ-USER-019, REQ-AUTH-013..REQ-AUTH-014, REQ-SYS-063, REQ-SYS-064
--
-- Adds users.must_change_password to support forced first-login password changes
-- for accounts provisioned with a system-generated temporary password.
-- Existing rows backfill to false so no current session is affected.
--
-- Rollback: ALTER TABLE users DROP COLUMN IF EXISTS must_change_password;
BEGIN;

INSERT INTO schema_migrations (version) VALUES ('017_must_change_password')
    ON CONFLICT (version) DO NOTHING;

-- ADD COLUMN IF NOT EXISTS makes the migration idempotent: re-running it on a
-- DB that already has the column (e.g. after a failed rollback) is a no-op.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT false;

COMMIT;
