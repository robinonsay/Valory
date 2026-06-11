# Runbook: RLS Role Hardening — Migrating Existing Deployments

**Audience:** operators responsible for a running Valory deployment (production or long-lived staging).

**Scope:** converting a deployment whose `db_data` volume was initialized with `POSTGRES_USER: valory_app` (the old default) to the hardened role layout where `postgres` is the cluster superuser and `valory_app` is a `LOGIN NOSUPERUSER NOBYPASSRLS` application role. This conversion makes row-level security operative for the first time on such a deployment.

**When this runbook is NOT needed:** fresh deployments on a new empty `db_data` volume. The postgres image executes `scripts/initdb/01-app-role.sh` automatically on first boot, which creates `valory_app` as a non-superuser login role and leaves `postgres` as the cluster superuser. This runbook is only for existing volumes where that init script has already been skipped.

---

## Why this matters (the three Sprint 8 findings)

1. **Superuser RLS bypass.** `POSTGRES_USER: valory_app` made the application's login role the cluster bootstrap superuser. PostgreSQL superusers bypass row-level security unconditionally — even `FORCE ROW LEVEL SECURITY` has no effect. Every RLS policy in the schema (REQ-SECURITY-002: per-student data isolation for courses, submissions, grades, badges) was silently inert. Data isolation rested entirely on handler-level ownership checks.

2. **Missing NULLIF guards in student RLS policies.** With a genuine non-superuser role active, any connection whose `app.current_user_id` GUC is the empty string (e.g. pool connections after an `AfterRelease` reset) would raise `invalid input syntax for type uuid: ""` and abort the query. Migration 009 (`009_security_hardening.sql`) fixes these policy definitions automatically at server startup. It must be applied in the same deployment window as the role conversion — the two fixes are coupled.

3. **Missing GRANTs on badge tables.** Migration 006 created `badges` and `student_badges` but never granted `valory_app` any privileges on them. Under a proper non-superuser role, all badge operations fail with `permission denied for table`. Migration 009 adds the missing grants, again automatically at startup.

Findings 2 and 3 are fixed by migration 009, which the server applies on every startup via the embedded migration runner. The operator's manual work is confined to finding 1 (the role attribute conversion) and the `.env` update.

---

## Pre-flight

### 1. Take a full backup

```bash
# Dump all data to a local file — adjust filename to today's date.
docker compose exec db pg_dump -U valory_app -d valory -Fc \
  -f /tmp/valory_pre_hardening_$(date +%Y%m%d).dump

# Ensure the local backups directory exists before copying out of the container.
mkdir -p ./backups

# Copy the dump out of the container.
docker compose cp db:/tmp/valory_pre_hardening_$(date +%Y%m%d).dump ./backups/
```

Also snapshot the named volume if your infrastructure supports it (e.g. `docker run --rm -v db_data:/data -v $(pwd)/backups:/backup alpine tar czf /backup/db_data_$(date +%Y%m%d).tar.gz /data`). The dump alone is sufficient for data recovery; the volume snapshot enables a byte-for-byte rollback without a restore.

### 2. Schedule a maintenance window

The API container must be stopped for the duration of the role-attribute changes. With `valory_app` as a superuser, active connections are not blocked by privilege changes — but stopping the API ensures no new connections race with the `ALTER ROLE` steps and avoids ambiguous partial-execution states.

```bash
docker compose stop api frontend
```

Verify the API is down before proceeding:

```bash
docker compose ps api   # should show "exited" or "stopped"
```

### 3. Confirm a working psql shell

```bash
# Verify you can connect as valory_app (the current superuser).
docker compose exec db psql -U valory_app -d valory -c "SELECT current_user, pg_has_role('valory_app', 'member');"
```

Expected output: `current_user = valory_app`. If this fails, fix container connectivity before proceeding.

---

## Conversion procedure

The conversion has a chicken-and-egg constraint: `valory_app` is currently the **only** superuser in the cluster, and you cannot strip superuser from a role while connected as that role without first creating another superuser to take over. The steps below address this in the correct order.

### Step 1 — Create the `postgres` superuser (while still connected as `valory_app`)

Passwords must never be pasted as raw SQL string literals: a password containing a single quote, backslash, or other special character will break the literal or silently truncate at the escape sequence. Instead, pass the password via psql's `-v` flag and reference it as `:'pw'` (psql quoted-literal syntax), exactly as `scripts/initdb/01-app-role.sh` does (lines 47–50).

Set `DB_SUPERUSER_PASSWORD` in your shell to the value you have chosen for `.env` (Step 8), then run:

```bash
# Replace the export value with your chosen DB_SUPERUSER_PASSWORD.
export DB_SUPERUSER_PASSWORD="your-chosen-superuser-password"

# -T disables pseudo-TTY allocation so that stdin heredoc redirection is not
# blocked. The host-exported DB_SUPERUSER_PASSWORD is bound directly via
# -v pw=... — docker compose exec passes argv to execvp without a container
# shell, so there is no intermediate variable expansion to go wrong.
docker compose exec -T db \
  psql -U valory_app -d valory \
  -v ON_ERROR_STOP=1 \
  -v pw="$DB_SUPERUSER_PASSWORD" \
  <<'EOSQL'
-- Create the postgres superuser only if it does not already exist.
-- Attributes match those of the postgres image bootstrap superuser:
--   SUPERUSER BYPASSRLS CREATEROLE CREATEDB REPLICATION LOGIN NOINHERIT
-- NOINHERIT is set explicitly so the role matches the cluster default and
-- does not silently accumulate inherited privileges from future memberships.
-- BYPASSRLS is added for initdb parity: the bootstrap postgres user created
-- by the official postgres:16 image always has rolbypassrls = t, so a
-- manual postgres role created here must carry the same attribute to ensure
-- consistent behaviour and matching verification output (Steps 7 and 10).
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'postgres') THEN
        CREATE ROLE postgres
            SUPERUSER
            BYPASSRLS
            CREATEROLE
            CREATEDB
            REPLICATION
            LOGIN
            NOINHERIT
            PASSWORD :'pw';
    END IF;
END
$$;
EOSQL
```

Verify the role was created:

```bash
docker compose exec db psql -U valory_app -d valory \
  -c "SELECT rolname, rolsuper, rolbypassrls, rolcanlogin FROM pg_roles WHERE rolname = 'postgres';"
# Expected: rolsuper = t, rolbypassrls = t, rolcanlogin = t
```

### Step 2 — Reconnect as `postgres`

Exit the current psql session and open a new one as the new superuser. This is the critical handoff: from this point forward all remaining commands run under `postgres`.

```bash
docker compose exec db psql -U postgres -d valory
```

Confirm the identity:

```sql
SELECT current_user;
-- Expected: postgres
```

### Step 3 — Strip `valory_app` down to a non-superuser login role

As in Step 1, the password must not be pasted as a raw SQL string literal. Set `APP_DB_PASSWORD` in your shell to the value you have chosen for `.env` (Step 8), then run:

```bash
# Replace the export value with your chosen APP_DB_PASSWORD.
export APP_DB_PASSWORD="your-chosen-app-password"

# -T disables pseudo-TTY allocation so that stdin heredoc redirection is not
# blocked. The host-exported APP_DB_PASSWORD is bound directly via
# -v pw=... — no container shell, no intermediate variable expansion.
docker compose exec -T db \
  psql -U postgres -d valory \
  -v ON_ERROR_STOP=1 \
  -v pw="$APP_DB_PASSWORD" \
  <<'EOSQL'
-- Remove all elevated attributes. KEEP LOGIN — the application authenticates
-- with this role via DATABASE_URL. Migration 002 created valory_app as NOLOGIN
-- because that migration's design assumed SET ROLE usage from a separate login
-- role. In the hardened layout valory_app IS the login role, so LOGIN is
-- required and correct.
--
-- NOREPLICATION is set explicitly: the Docker bootstrap superuser inherits
-- REPLICATION, but the init-script-created valory_app role does not. Setting
-- NOREPLICATION here brings the converted role into exact attribute parity with
-- a fresh-install init-script role.
ALTER ROLE valory_app
    NOSUPERUSER
    NOBYPASSRLS
    NOCREATEDB
    NOCREATEROLE
    NOINHERIT
    NOREPLICATION
    LOGIN
    PASSWORD :'pw';
EOSQL
```

### Step 4 — Verify database ownership

The application database must be owned by `valory_app` so that the role can create objects if any migration requires it and so that future object defaults resolve correctly.

```sql
-- Check current owner of the valory database.
\l valory
-- Look for "valory_app" in the Owner column. If it already shows valory_app,
-- the next ALTER DATABASE is a no-op but harmless.

ALTER DATABASE valory OWNER TO valory_app;
```

### Step 5 — Verify table ownership

All tables were created by `valory_app` (the former superuser), so they should already be owned by `valory_app`. Confirm:

```sql
SELECT tablename, tableowner
FROM pg_tables
WHERE schemaname = 'public'
  AND tableowner != 'valory_app'
ORDER BY tablename;
-- Expected: zero rows. If any rows appear, fix them in the next sub-step.
```

If any table is owned by a different role (e.g. `postgres`):

```sql
-- REASSIGN OWNED transfers ownership of all objects currently owned by the
-- named role within the current database to the target role.
-- Only run if the query above returned rows.
REASSIGN OWNED BY postgres TO valory_app;
```

Re-run the ownership check to confirm zero rows remain.

### Step 6 — Grant `postgres` superuser access to the database (housekeeping)

The `postgres` role is a cluster-level superuser so it can access any database implicitly. No explicit `GRANT CONNECT` is required. This step is informational only — verify that `postgres` can connect:

```bash
docker compose exec db psql -U postgres -d valory -c "SELECT 1;"
```

### Step 7 — Confirm role attributes before deploying new compose

```sql
SELECT rolname, rolsuper, rolbypassrls, rolcanlogin, rolnoinherit, rolcreatedb, rolcreaterole
FROM pg_roles
WHERE rolname IN ('postgres', 'valory_app');
```

Expected output:

| rolname     | rolsuper | rolbypassrls | rolcanlogin | rolnoinherit | rolcreatedb | rolcreaterole |
|-------------|----------|--------------|-------------|--------------|-------------|---------------|
| postgres    | t        | t            | t           | t            | t           | t             |
| valory_app  | f        | f            | t           | t            | f           | f             |

`rolbypassrls = t` for `postgres` because the role was created with the explicit `BYPASSRLS` attribute (Step 1), matching the attribute set of the official postgres:16 image bootstrap superuser. Note: PostgreSQL superusers bypass RLS unconditionally at the engine level regardless of `rolbypassrls`; the column reflects whether the explicit attribute is set, not whether RLS bypass is in effect. `valory_app` must show `f` for both `rolsuper` and `rolbypassrls`.

---

## .env update and compose redeploy

The new `docker-compose.yml` references two distinct variables where the old file had one.

### Step 8 — Update `.env`

```bash
# In your .env file:
# Remove the old variable (if present):
#   DB_PASSWORD=...
# Add or update:
DB_SUPERUSER_PASSWORD=CHANGE_TO_DB_SUPERUSER_PASSWORD   # matches what you set in Step 1
APP_DB_PASSWORD=CHANGE_TO_APP_DB_PASSWORD               # matches what you set in Step 3

# DATABASE_URL password segment MUST equal APP_DB_PASSWORD.
DATABASE_URL=postgres://valory_app:CHANGE_TO_APP_DB_PASSWORD@db:5432/valory?sslmode=disable
```

The `.env.example` file documents these three variables and the password-equality constraint.

### Step 9 — Pull and bring up the new compose file

The compose file's healthcheck now probes `-U postgres`. Because you created the `postgres` role in Step 1 (before this deployment step), the healthcheck will pass immediately on startup.

```bash
# Pull any updated images (no-op if building locally).
docker compose pull

# Restart the db service to pick up the new environment variable names,
# then bring up api and frontend. The db data volume is unchanged — only
# the container configuration changes.
docker compose up -d
```

Wait for the db healthcheck to pass, then confirm the api comes up:

```bash
docker compose ps
# db and api should both show "healthy" within ~30 seconds.
```

---

## Post-conversion verification

### Step 10 — Role attributes

```bash
docker compose exec db psql -U postgres -d valory -c \
  "SELECT rolname, rolsuper, rolbypassrls FROM pg_roles WHERE rolname IN ('postgres','valory_app');"
```

Expected:

```
  rolname   | rolsuper | rolbypassrls
------------+----------+-------------
 postgres   | t        | t
 valory_app | f        | f
```

`rolbypassrls = t` for `postgres` reflects the explicit `BYPASSRLS` attribute set in Step 1 (initdb parity). Superusers bypass RLS unconditionally at the engine level even when `rolbypassrls = f`; here both the superuser flag and the explicit attribute are set. `valory_app` must show `f` on both columns: it is a non-superuser role without the `BYPASSRLS` attribute, so every RLS policy in the schema is evaluated for its connections.

### Step 11 — Migration 009 applied

```bash
docker compose exec db psql -U postgres -d valory -c \
  "SELECT version FROM schema_migrations ORDER BY version;"
```

Confirm `009_security_hardening` appears in the output. If it is missing, the server did not start cleanly — check `docker compose logs api` for migration errors.

### Step 12 — RLS smoke test

Connect as `valory_app` (the application role) and simulate what the application does for a student session. Pick a real `student_id` UUID from your `users` table.

```bash
docker compose exec db psql -U valory_app -d valory
```

```sql
-- Simulate the auth middleware's GUC setup for a student session.
-- Replace <student_uuid> with a real UUID from your users table.
SELECT set_config('app.current_user_id', '<student_uuid>', false);
SELECT set_config('app.current_role', 'student', false);

-- A student should see only their own courses.
SELECT id, title FROM courses WHERE student_id IS NOT NULL LIMIT 5;
-- Every row returned must have a student_id column matching <student_uuid>
-- (or no rows if that student has no courses — both are correct).

-- Cross-check: set a different (non-existent) user ID and confirm zero rows.
SELECT set_config('app.current_user_id', '00000000-0000-0000-0000-000000000001', false);
SELECT id, title FROM courses WHERE student_id IS NOT NULL LIMIT 5;
-- Expected: 0 rows (RLS is now operative).

-- Confirm empty-string GUC no longer crashes (migration 009 fix).
SELECT set_config('app.current_user_id', '', false);
SELECT id FROM courses LIMIT 1;
-- Expected: 0 rows (NULLIF guard resolves '' to NULL, policy evaluates false).
-- A crash here means migration 009 did not apply — check schema_migrations.
```

### Step 13 — API health endpoint

```bash
curl -kf https://localhost:8443/health
# Expected: 200 OK
```

---

## Rollback

Role attribute changes are fully reversible — no data is touched by this runbook.

### Restore from volume snapshot (primary rollback path)

The volume snapshot taken in Pre-flight Step 1 (the `tar.gz`) is the fastest and most reliable rollback path. It restores the exact on-disk state before any changes were made and requires no server coordination:

```bash
# Stop all services and remove the data volume.
docker compose down
docker volume rm valory_db_data   # adjust project prefix if different

# Recreate the volume and extract the snapshot directly into it.
# The volume is mounted at /data to mirror the snapshot path used during
# the tar.gz creation (entries are data/*), so files land at the correct
# location inside the volume.
docker volume create valory_db_data
docker run --rm \
  -v valory_db_data:/data \
  -v "$(pwd)/backups":/backup \
  alpine \
  tar xzf /backup/db_data_<date>.tar.gz -C /
```

Revert `.env` to the old `DB_PASSWORD` variable, revert `docker-compose.yml` to `POSTGRES_USER: valory_app`, and bring services back up.

### Restore from pg_dump backup (fallback rollback path)

Use this path only if no volume snapshot is available. The dump taken in Pre-flight Step 1 was created with `-Fc` (custom format) and without `--create`, so it contains data only — not `CREATE DATABASE`. Restoring it requires a live PostgreSQL server with a target database already in place:

```bash
# Stop all services and remove the data volume.
docker compose down
docker volume rm valory_db_data   # adjust project prefix if different

# Bring up only the db container. The postgres image entrypoint will run
# initdb on the fresh empty volume and start the server. The init script
# (scripts/initdb/01-app-role.sh) will also run and recreate the valory_app
# role with the hardened attributes — this is expected and harmless; you will
# overwrite the data with the pre-hardening dump immediately after.
docker compose up -d db
# Wait for the db healthcheck to pass (the server must be accepting connections
# before pg_restore can connect).
docker compose ps db   # wait until status shows "healthy"

# The POSTGRES_DB=valory environment variable causes the entrypoint to create
# the valory database automatically, so createdb is not needed.
# If for any reason the database is absent, create it manually:
#   docker compose exec db createdb -U postgres valory

# Copy the dump from the host into the container, then restore it.
# pg_restore runs inside the container and requires the file to be accessible
# there — the host ./backups/ directory is not mounted in the db service.
docker compose cp ./backups/valory_pre_hardening_<date>.dump db:/tmp/valory_pre_hardening_<date>.dump

docker compose exec db \
  pg_restore -U postgres -d valory --clean --if-exists -Fc \
  /tmp/valory_pre_hardening_<date>.dump
```

Revert `.env` to the old `DB_PASSWORD` variable, revert `docker-compose.yml` to `POSTGRES_USER: valory_app`, then bring up the remaining services:

```bash
docker compose up -d
```

### Reverse only the role attributes (partial rollback, keeping data)

If data is intact and you only need to undo the role changes temporarily:

```bash
docker compose exec db psql -U postgres -d valory
```

```sql
-- Re-elevate valory_app to its former superuser state.
ALTER ROLE valory_app SUPERUSER BYPASSRLS;
-- Revert .env to DB_PASSWORD and update docker-compose.yml accordingly,
-- then docker compose up -d.
```

### One-way doors

There are none. Role attributes are reversible. The NULLIF policy changes in migration 009 are also safe to revert if necessary (they only add a NULLIF wrapper — no data semantics change). The badge GRANTs added by 009 can be revoked with `REVOKE SELECT ON badges FROM valory_app` etc. if for some reason a rollback requires it.

---

## Troubleshooting

**`pg_isready` healthcheck fails after `docker compose up -d`**

The healthcheck now probes `-U postgres`. If `postgres` was not created in Step 1 before deploying, the check will fail. Fix: connect to the container directly (`docker compose exec db psql -U valory_app -d postgres`) and run Step 1's `CREATE ROLE postgres ...` manually, then restart the db service.

**`FATAL: role "valory_app" does not have login privilege`**

The `ALTER ROLE` in Step 3 accidentally omitted `LOGIN`. Fix: connect as `postgres` and re-run `ALTER ROLE valory_app LOGIN;`.

**`ERROR: permission denied for table badges`**

Migration 009 has not applied yet. Check `docker compose logs api` for the migration runner output. The server applies 009 automatically at startup via the embedded migration runner; if the api failed to start cleanly, 009 may have been skipped. Connect as `postgres` and manually run the grant:

```sql
GRANT SELECT ON badges TO valory_app;
GRANT SELECT, INSERT, UPDATE ON student_badges TO valory_app;
```

Then restart the api container.

**`ERROR: invalid input syntax for type uuid: ""`**

Migration 009 has not applied (the NULLIF guards are missing). Apply it manually:

```bash
# -T disables pseudo-TTY allocation so that stdin file redirection is not blocked.
docker compose exec -T db psql -U postgres -d valory -f /dev/stdin < migrations/009_security_hardening.sql
```
