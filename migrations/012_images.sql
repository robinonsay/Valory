-- migrations/012_images.sql
-- Sprint 15 — Image upload storage and submission image attachment.
-- REQ-SUBMISSION-004..006: submissions carry images, images pass to grading,
--   images deleted with personal data (ON DELETE CASCADE from users).
-- REQ-AGENT-023..025: chat attachments; content-inspection validation;
--   owner-or-server retrieval.
-- REQ-USER-007: personal data deletion cascade.
BEGIN;

INSERT INTO schema_migrations (version) VALUES ('012_images')
    ON CONFLICT (version) DO NOTHING;

CREATE TABLE IF NOT EXISTS images (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    -- owner: the student who uploaded the image
    student_id    UUID         NOT NULL REFERENCES users(id)       ON DELETE CASCADE,
    -- course scope: images are isolated per course
    course_id     UUID         NOT NULL REFERENCES courses(id)     ON DELETE CASCADE,
    -- nullable: set when the image is attached to a specific homework submission
    submission_id UUID         REFERENCES submissions(id)          ON DELETE SET NULL,
    mime_type     VARCHAR(32)  NOT NULL
                  CHECK (mime_type IN ('image/png','image/jpeg','image/gif','image/webp')),
    byte_size     BIGINT       NOT NULL CHECK (byte_size > 0 AND byte_size <= 5242880),
    -- sha256 hex digest: content-addressable identity and tamper detection
    sha256        CHAR(64)     NOT NULL,
    data          BYTEA        NOT NULL,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Fast lookups by owner + course (the most common read path is the grading runner
-- loading all images for a submission).
CREATE INDEX IF NOT EXISTS images_student_course_idx
    ON images (student_id, course_id);

-- Index for submission join.
CREATE INDEX IF NOT EXISTS images_submission_idx
    ON images (submission_id) WHERE submission_id IS NOT NULL;

ALTER TABLE images ENABLE ROW LEVEL SECURITY;
ALTER TABLE images FORCE ROW LEVEL SECURITY;

-- Students read and insert their own images.
DO $$ BEGIN
    CREATE POLICY images_student_policy ON images
        USING (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid)
        WITH CHECK (student_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Admins have full read access.
DO $$ BEGIN
    CREATE POLICY images_admin_policy ON images
        USING (current_setting('app.current_role', true) = 'admin');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server role can read (needed by grading runner and vision call paths).
DO $$ BEGIN
    CREATE POLICY images_server_select_policy ON images
        FOR SELECT
        USING (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Server role can insert (not needed by current paths, but included for symmetry
-- with other tables so future server-side image generation does not require a
-- migration).
DO $$ BEGIN
    CREATE POLICY images_server_insert_policy ON images
        FOR INSERT
        WITH CHECK (current_setting('app.current_role', true) = 'server');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON images TO valory_app;

-- Add image_ids to submissions: stores the ordered list of image UUIDs attached
-- at submission time.  NULL means no images were attached (not an empty array)
-- to avoid a default migration burden on existing rows.
ALTER TABLE submissions
    ADD COLUMN IF NOT EXISTS image_ids UUID[] DEFAULT NULL;

COMMIT;
