BEGIN;

INSERT INTO schema_migrations (version) VALUES ('002_user_security_audit')
    ON CONFLICT (version) DO NOTHING;

ALTER TABLE users ADD COLUMN IF NOT EXISTS email TEXT UNIQUE;

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT        NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_prt_user_id    ON password_reset_tokens (user_id);
CREATE INDEX IF NOT EXISTS idx_prt_token_hash ON password_reset_tokens (token_hash);
CREATE INDEX IF NOT EXISTS idx_prt_expires_at ON password_reset_tokens (expires_at)
    WHERE used_at IS NULL;

CREATE TABLE IF NOT EXISTS password_reset_attempts (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_pra_user_requested
    ON password_reset_attempts (user_id, requested_at DESC);

CREATE TABLE IF NOT EXISTS student_consent (
    student_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    consented_at     TIMESTAMPTZ NOT NULL,
    consent_version  VARCHAR(16) NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_log (
    id           BIGSERIAL    PRIMARY KEY,
    admin_id     UUID         NOT NULL REFERENCES users(id),
    action       VARCHAR(64)  NOT NULL,
    target_type  VARCHAR(64)  NOT NULL,
    target_id    UUID,
    payload      JSONB        NOT NULL DEFAULT '{}',
    prev_hash    VARCHAR(64)  NOT NULL,
    entry_hash   VARCHAR(64)  NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_log_admin_id   ON audit_log (admin_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_target     ON audit_log (target_type, target_id);

CREATE TABLE IF NOT EXISTS system_config (
    key         VARCHAR(120) PRIMARY KEY,
    value       TEXT         NOT NULL,
    updated_by  UUID         REFERENCES users(id) ON DELETE SET NULL,
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

INSERT INTO system_config (key, value) VALUES
    ('agent_retry_limit',                  '3'),
    ('correction_loop_max_iterations',     '5'),
    ('per_student_token_limit',            '500000'),
    ('late_penalty_rate',                  '0.05'),
    ('homework_weight',                    '0.7'),
    ('project_weight',                     '0.3'),
    ('session_inactivity_seconds',         '1800'),
    ('account_lockout_seconds',            '900'),
    ('max_upload_bytes',                   '10485760'),
    ('content_generation_timeout_seconds', '300'),
    ('audit_retention_days',               '365'),
    ('notification_retention_days',        '90'),
    ('consent_version',                    '1.0')
ON CONFLICT (key) DO NOTHING;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'valory_app') THEN
        CREATE ROLE valory_app
            NOLOGIN
            NOINHERIT
            NOSUPERUSER
            NOCREATEDB
            NOCREATEROLE
            NOBYPASSRLS;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO valory_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO valory_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO valory_app;

-- audit_log is append-only: revoke UPDATE and DELETE so the tamper-evident chain cannot be altered.
REVOKE UPDATE, DELETE ON audit_log FROM valory_app;

-- schema_migrations is read-only at runtime: revoke write access.
REVOKE INSERT, UPDATE, DELETE ON schema_migrations FROM valory_app;

-- Ensure future tables and sequences created by any role are accessible to valory_app.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO valory_app;

ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO valory_app;

COMMIT;
