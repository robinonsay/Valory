#!/usr/bin/env bash
# scripts/initdb/01-app-role.sh
#
# Executed by the official postgres:16 image entrypoint on FIRST initialization
# of an empty data volume ONLY (docker-entrypoint-initdb.d convention).
# At that point the entrypoint is already running as the bootstrap superuser
# ($POSTGRES_USER = "postgres"), so we have the privileges we need.
#
# Purpose: create the application login role "valory_app" with LOGIN and
# NOBYPASSRLS so that all RLS policies (REQ-SECURITY-002) are actually
# enforced in production.  Without this, the role created by migration 002
# is NOLOGIN (the pre-hardening compose set POSTGRES_USER=valory_app, making
# it a superuser that bypassed RLS entirely — Sprint 8 finding 1).
#
# EXISTING DEPLOYMENTS: this script will NOT run automatically on volumes that
# were already initialized before this change.  Follow the manual runbook at
# docs/runbooks/rls-role-hardening.md to create the role and adjust grants.
#
# Migration 002 contains:
#   IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'valory_app')
#     CREATE ROLE valory_app NOLOGIN …
# That block is a no-op on any deployment that ran this script first; the role
# already exists with LOGIN set, and migrations never alter an existing role's
# password or login attribute.
#
# @{"req": ["REQ-SECURITY-002"]}

set -euo pipefail

# Fail loudly and immediately if APP_DB_PASSWORD is unset or empty.
# An empty password would either produce a role with no password (allowing
# peer/ident bypass paths) or break the psql variable binding below.
if [[ -z "${APP_DB_PASSWORD:-}" ]]; then
    echo "ERROR: APP_DB_PASSWORD is unset or empty — aborting init." >&2
    exit 1
fi

# Use the psql invocation pattern documented by the postgres image:
#   --username "$POSTGRES_USER"  — the bootstrap superuser set by POSTGRES_USER
#   --dbname   "$POSTGRES_DB"    — the database set by POSTGRES_DB
#   -v ON_ERROR_STOP=1           — abort the whole script on any SQL error
#
# Password binding: we pass APP_DB_PASSWORD as a psql variable ("pw") and
# reference it in SQL as :'pw' (psql quoted-literal (SQL string) syntax).  This avoids
# interpolating the raw string into SQL where single quotes in the password
# would break the literal — the documented safe approach per psql(1).
psql -v ON_ERROR_STOP=1 \
     --username "$POSTGRES_USER" \
     --dbname   "$POSTGRES_DB" \
     -v pw="$APP_DB_PASSWORD" \
     <<'EOSQL'

-- Create the application role only if it does not already exist.
-- The entrypoint only runs this on empty volumes, but the guard costs nothing
-- and protects against future operator mistakes (e.g. manually re-running).
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'valory_app') THEN
        -- LOGIN  : required for the api to authenticate as this role.
        -- NOSUPERUSER / NOCREATEDB / NOCREATEROLE : principle of least privilege.
        -- NOBYPASSRLS : the critical attribute — ensures every RLS policy in the
        --   schema is evaluated for connections from the api.  A superuser (or a
        --   role with BYPASSRLS) would skip all policies, making REQ-SECURITY-002
        --   silently inert, which is exactly the Sprint 8 finding 1 root cause.
        -- NOINHERIT  : role does not automatically inherit privileges from any
        --   group memberships; grants are explicit and auditable.
        -- PASSWORD is set below via ALTER ROLE (outside this DO block) so we can
        --   safely use psql's :'pw' quoting to handle special characters.
        CREATE ROLE valory_app
            LOGIN
            NOSUPERUSER
            NOCREATEDB
            NOCREATEROLE
            NOBYPASSRLS
            NOINHERIT;
    END IF;
END
$$;

-- Set the password using psql variable binding.
-- :'pw' expands to a properly-escaped SQL string literal, so passwords
-- containing single quotes, backslashes, or other special characters are
-- handled correctly without any manual escaping.
ALTER ROLE valory_app PASSWORD :'pw';

-- Transfer database ownership to valory_app.
-- Why: the api runs embedded migrations at startup AS valory_app.  Database
-- ownership grants it CREATE on schema public (PG15+: schema public is owned
-- by pg_database_owner, which tracks the db owner) and the right to install
-- trusted extensions (pgcrypto is trusted in PG13+, used in migration 001).
-- Tables the api creates are owned by valory_app, which is exactly why every
-- per-student table uses FORCE ROW LEVEL SECURITY (see migrations 003–008) and
-- the role is NOBYPASSRLS: an owner with BYPASSRLS would skip its own policies.
ALTER DATABASE valory OWNER TO valory_app;

EOSQL
