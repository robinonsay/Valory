-- migrations/015_professor_max_tokens.sql
-- Sprint 24 — Admin-configurable section-generation output token cap.
-- REQ-ADMIN-010
--
-- Adds system_config key 'professor_max_tokens': the max_tokens value the
-- Professor agent passes to the Anthropic API when generating or regenerating
-- a lesson section.  The previous hardcoded 4096 truncated 200-500 line
-- sections mid-word, so every review failed and runs burned the full
-- generation timeout.  16384 gives 500 lines of AsciiDoc comfortable headroom
-- while staying under the non-streaming SDK HTTP-timeout ceiling (~16K).
--
-- Rollback: DELETE FROM system_config WHERE key = 'professor_max_tokens';
BEGIN;

INSERT INTO schema_migrations (version) VALUES ('015_professor_max_tokens')
    ON CONFLICT (version) DO NOTHING;

INSERT INTO system_config (key, value) VALUES ('professor_max_tokens', '16384')
    ON CONFLICT (key) DO NOTHING;

COMMIT;
