# Sprint 15 — Image Upload: Chat + Homework, with Claude Vision

## Objective

PM request: chat and homework submissions support image upload; images reach
the model (vision) for chat replies and grading.

## Work performed

| Task | Deliverable | Contributor | Verifier | Outcome |
|---|---|---|---|---|
| 15.1 | docs/sdd/17-image-upload.adoc: Postgres bytea storage (pg_dump story, RLS, tamper sha256), endpoint shapes, caps (5MB, 4/chat, 8/submission, 200/course), vision call shapes | design-author | SQE + systems | PASS (its "free ID" claim was wrong — caught by lead, again by SQE) |
| Reqs | REQ-AGENT-023..025, REQ-SUBMISSION-004..006, REQ-FECOURSE-622..626, REQ-FECONTENT-220..224 | requirements-author | SQE + schema | PASS |
| 15.2 | migration 012 (images + RLS + CASCADE + submissions.image_ids); internal/image/ (upload/serve/repository/vision); chat attachments (≤4) with current-turn vision on BOTH intake and general paths; grading ≤8 images; route wiring | senior-engineer (×2 — first stalled; continuation finished) | SQE + systems | PASS after fix round |
| 15.3 | Attach UI both views (previews, remove, caps, count feedback), imageUpload util, uploadMultipart client, renderMarkdown same-origin /api/v1/images/{uuid} allowlist | junior-engineer | SQE + systems | PASS after fix round |
| In-sprint find | **GET /courses/{id}/homework/{hwId} did not exist — the homework view has been 404-broken in production since launch**; e2e found it; endpoint implemented (+4 tests) | test-author → senior-engineer | SQE + e2e | PASS |
| 15.4 | e2e image-upload.spec.ts: upload round-trip + headers, type rejection (client+server), owner-only access, full homework attach flow; seedHomework helper | test-author | senior SQE + live runs | PASS |

## Review findings (the headline)

- **CRITICAL (live-confirmed): nginx had no client_max_body_size — its 1MB
  default rejected every 1-5MB upload before the Go handler ran.** The
  feature didn't actually work through the proxy. Fixed (6m).
- **HIGH: Cache-Control public on the authenticated image endpoint**
  (shared-cache cross-user leak) → private.
- **MEDIUM: vision token explosion** — an 8000×8000 image ≈ 43k tokens; 8
  grading images would exceed the 200k context → DecodeConfig dimension cap
  4096×4096 (also rejects the GIF89a polyglot cheaply). x/image dep for webp.
- **HIGH: student bubbles showed literal `![image](...)` markdown** → bound
  :src attachment rendering (plain-text guarantee intact).
- **MEDIUM: intake path silently dropped vision blocks** (attach UI lives on
  the intake view!) → RunIntakeStepWithImages; REQ-AGENT-023 now honest.
- Phantom REQ-FECOURSE-271..273 annotations (design doc's stale ID claim)
  remapped to 622..626; FECONTENT-224 display implemented; wrong @verifies
  fixed; vacuous cap tests replaced with real handler tests.
- IDOR/limits probes all clean: cross-user GET 403, cross-course attach
  rejected, duplicates rejected, SVG rejected, zero-byte rejected.

## Senior gate: NO-SHIP → SHIP

The first gate REJECTED on deployment provenance: the running containers
predated the fix round — and the API image build was silently failing
(`go get x/image` bumped go.mod to go 1.25; the Dockerfile builder was
golang:1.23). The earlier "rebuild" output was truncated and the failure
missed. Fixed (builder → golang:1.25-bookworm), rebuilt, and the gate's three
live probes now pass: 2MB PNG → 201; 4097-wide PNG and GIF89a polyglot →
422; image GET → Cache-Control private + nosniff. Probe rows cleaned.

**Process lesson recorded**: never trust `tail -1` on a build; verify
deployed-image provenance before live verification (memory note exists).

## Verification

- Go matrix + make test-integration green (migration 012 exercised);
  vitest 348/348; build green; AI-free e2e 37/37 (multiple runs).
- Live probes (above) against the freshly built stack. 0 open courses.

## Backlog

- REQ-SUBMISSION-006 cascade integration test (user-deletion fixture
  restructuring needed).
- sha256 stored but unconsumed (tamper-evidence is operator-facing today);
  full-decode validation (DecodeConfig is header-only).
- Student-bubble attachments don't survive history reload (text-only storage;
  by design, documented).
- TOCTOU overshoot on the 200-image cap (single-digit; accepted).
- KaTeX/Vite chunk size; image bytes inflate pg_dump (operator note).
