-- migrations/014_anthropic_base_url.sql
-- Sprint 17 fast-follow — Configurable Anthropic API endpoint.
-- REQ-ADMIN-009
--
-- Adds system_config key 'anthropic_base_url'.  Empty string (the default)
-- means "use the Anthropic-SDK default endpoint"; a non-empty value must be
-- an absolute http(s) URL and is passed to option.WithBaseURL per call.
--
-- Rollback: DELETE FROM system_config WHERE key = 'anthropic_base_url';
BEGIN;

INSERT INTO schema_migrations (version) VALUES ('014_anthropic_base_url')
    ON CONFLICT (version) DO NOTHING;

INSERT INTO system_config (key, value) VALUES ('anthropic_base_url', '')
    ON CONFLICT (key) DO NOTHING;

COMMIT;
