-- migrations/013_managed_secrets.sql
-- Sprint 16, Task 16.2 — Managed Secrets subsystem.
-- REQ-ADMIN-005..008, REQ-SECURITY-006..008
--
-- Stores AES-256-GCM encrypted API keys that can be updated via the admin UI
-- without a container restart.  The ciphertext and nonce are opaque bytes;
-- decryption always happens in the Go application layer, never in the database.
--
-- Rollback: DROP TABLE IF EXISTS managed_secrets;
BEGIN;

INSERT INTO schema_migrations (version) VALUES ('013_managed_secrets')
    ON CONFLICT (version) DO NOTHING;

CREATE TABLE IF NOT EXISTS managed_secrets (
    -- One of the known secret names: 'anthropic_api_key', 'brave_api_key'.
    name        VARCHAR(120) PRIMARY KEY,

    -- AES-256-GCM ciphertext; variable length; bytea avoids encoding overhead.
    ciphertext  BYTEA        NOT NULL,

    -- Random 12-byte GCM nonce, generated fresh on every write.
    nonce       BYTEA        NOT NULL,

    -- Last 4 characters of the plaintext value stored in cleartext for UI badges.
    -- Never contains more than 4 chars so exposure is negligible.
    last4       VARCHAR(4)   NOT NULL DEFAULT '',

    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),

    -- NULL when the row was written by a system process with no admin context.
    updated_by  UUID         REFERENCES users(id) ON DELETE SET NULL
);

-- No default rows: a missing row means the secret is not managed.

-- Narrow permissions: PUBLIC has no access; only valory_app may read/write.
REVOKE ALL ON managed_secrets FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON managed_secrets TO valory_app;

COMMIT;

-- RLS consistency hygiene (systems-review observation A): the actual access
-- protection for this table is the GRANT model above (REVOKE ALL FROM PUBLIC;
-- narrow GRANT to valory_app) -- any future database role is blocked by the
-- absence of a grant, not by RLS. ENABLE+FORCE RLS is applied here only for
-- consistency with the project's RLS-everywhere posture; FORCE would deny
-- even valory_app, so the permissive policy below restores its access. Net
-- behavioral change: zero (senior-SQE assessed; kept for posture uniformity).
ALTER TABLE managed_secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE managed_secrets FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY managed_secrets_app_policy ON managed_secrets
        FOR ALL USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
