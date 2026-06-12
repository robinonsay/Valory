// handler.go — HTTP handlers for image upload and retrieval.
// POST /api/v1/courses/{id}/images  — upload an image (REQ-AGENT-023/024)
// GET  /api/v1/images/{imageId}     — serve image bytes   (REQ-AGENT-025)
package image

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"log"
	"net/http"

	// Register stdlib image decoders so image.DecodeConfig can parse PNG, JPEG,
	// and GIF headers for the dimension cap check.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	// Register WebP decoder so image.DecodeConfig handles WebP as well.
	// Without this, WebP headers cannot be parsed and would be rejected by
	// DecodeConfig even though http.DetectContentType accepts them.
	_ "golang.org/x/image/webp"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/valory/valory/internal/auth"
)

const (
	// maxImageBodySize is the MaxBytesReader cap: 5 MB + 1 sentinel byte. The
	// extra byte lets us distinguish "exactly at limit" from "over limit" when
	// comparing header.Size against softCapBytes.
	maxImageBodySize = int64(5<<20 + 1)
	// softCapBytes is the actual 5 MB limit checked against header.Size.
	softCapBytes = 5 << 20
	// perCourseCap is the maximum number of images a student may upload per course.
	perCourseCap = 200
	// maxImageDimension is the maximum allowed width or height in pixels.
	// An 8000×8000 JPEG encodes to roughly 43k Claude vision tokens; 8 such
	// images would exceed the 200k context window for a grading call. Capping
	// at 4096px bounds the worst-case token cost to a safe level for both chat
	// (4 images) and grading (8 images) paths.
	maxImageDimension = 4096
)

// allowedMIME is the set of MIME types accepted after http.DetectContentType.
var allowedMIME = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// Handler serves the image upload and retrieval endpoints.
// courseOwnedBy is a predicate closure that verifies course ownership via the
// request-scoped connection — same pattern as courseOwnedBy in the agent
// handler, but expressed as an injected func to avoid an image ↔ course
// import cycle.
type Handler struct {
	repo          *Repository
	courseOwnedBy func(r *http.Request, courseID, studentID uuid.UUID) bool
}

// NewHandler constructs an image handler. courseOwnedBy must return true when
// studentID owns courseID; it is typically a closure over the course repository
// using the request-scoped connection from the auth middleware.
//
// @{"req": ["REQ-AGENT-023", "REQ-AGENT-024", "REQ-AGENT-025"]}
func NewHandler(repo *Repository, courseOwnedBy func(r *http.Request, courseID, studentID uuid.UUID) bool) *Handler {
	return &Handler{repo: repo, courseOwnedBy: courseOwnedBy}
}

// UploadRoutes mounts POST /images under /courses/{id}.
// Must be called inside the authenticated + CSRF group.
//
// @{"req": ["REQ-AGENT-023", "REQ-AGENT-024"]}
func (h *Handler) UploadRoutes(r chi.Router) {
	r.Post("/images", h.upload)
}

// ServeRoutes mounts GET /images/{imageId} at the top level.
// Must be called inside the authenticated group (no CSRF — GET is safe).
//
// @{"req": ["REQ-AGENT-025"]}
func (h *Handler) ServeRoutes(r chi.Router) {
	r.Get("/images/{imageId}", h.serve)
}

// upload handles POST /api/v1/courses/{id}/images.
// Processing order per SDD section 4.1:
//  1. MaxBytesReader cap (5 MB + 1 sentinel)
//  2. ParseMultipartForm
//  3. Header size check → 413
//  4. Read bytes into memory
//  5. DetectContentType → 422 if not in allowlist
//  5b. image.DecodeConfig dimension check → 422 if width or height > 4096
//  6. SHA-256 of bytes
//  7. Per-course rate cap check → 429
//  8. INSERT via server-role conn → 201
//
// @{"req": ["REQ-AGENT-023", "REQ-AGENT-024"]}
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	rawUserID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeImageError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawUserID)

	courseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeImageError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	// Course ownership gate via request-scoped connection (courses_student_policy).
	if !h.courseOwnedBy(r, courseID, studentID) {
		writeImageError(w, http.StatusForbidden, "FORBIDDEN", "course not found")
		return
	}

	// Step 1: cap the body so oversized uploads are rejected before multipart parsing.
	r.Body = http.MaxBytesReader(w, r.Body, maxImageBodySize)

	// Step 2: parse the multipart form.
	if err := r.ParseMultipartForm(softCapBytes); err != nil {
		writeImageError(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "image exceeds 5 MB limit")
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		writeImageError(w, http.StatusBadRequest, "BAD_REQUEST", "image field is required")
		return
	}
	defer file.Close()

	// Step 3: header size check — the +1 sentinel distinguishes exactly-at-limit
	// from over-limit without a second read.
	if header.Size > softCapBytes {
		writeImageError(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "image exceeds 5 MB limit")
		return
	}

	// Step 4: read bytes. Safe to buffer because we know we're within softCapBytes.
	data, err := io.ReadAll(file)
	if err != nil {
		writeImageError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Step 5: content-type sniffing on first 512 bytes.
	// Never trust the browser-supplied Content-Type or file extension.
	sniffBuf := data
	if len(sniffBuf) > 512 {
		sniffBuf = sniffBuf[:512]
	}
	detected := http.DetectContentType(sniffBuf)
	if !allowedMIME[detected] {
		writeImageError(w, http.StatusUnprocessableEntity, "UNSUPPORTED_MIME", "unsupported image type; accepted: png, jpeg, gif, webp")
		return
	}

	// Step 5b: decode the image header to check pixel dimensions.
	// image.DecodeConfig reads only the file header — it does not allocate a
	// full pixel buffer — so this is cheap even for large files. The stdlib
	// decoders (png/jpeg/gif) and the x/image/webp decoder are registered via
	// blank imports above. A decode failure here means the bytes are not a
	// well-formed image even though the MIME sniff passed (e.g. a polyglot);
	// we reject it with 422. Dimension over-cap also yields 422: a 4097-wide
	// image would cost ~14k Claude vision tokens per image vs ~3.5k at 4096.
	cfg, _, decErr := image.DecodeConfig(bytes.NewReader(data))
	if decErr != nil {
		writeImageError(w, http.StatusUnprocessableEntity, "INVALID_IMAGE", "image could not be decoded; file may be corrupt or unsupported")
		return
	}
	if cfg.Width > maxImageDimension || cfg.Height > maxImageDimension {
		writeImageError(w, http.StatusUnprocessableEntity, "IMAGE_TOO_LARGE", "image dimensions must not exceed 4096x4096")
		return
	}

	// Step 6: SHA-256 for tamper evidence stored alongside the bytes.
	sum := sha256.Sum256(data)
	sha256hex := fmt.Sprintf("%x", sum)

	// Step 7: per-course rate cap — soft guard against unbounded storage abuse.
	// Race tolerance: at most one extra image may slip through between count and
	// insert on concurrent requests; the cap is a usage guard, not a billing quota.
	count, err := h.repo.CountByStudentCourse(r.Context(), studentID, courseID)
	if err != nil {
		log.Printf("image: upload: count images: %v", err)
		writeImageError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if count >= perCourseCap {
		writeImageError(w, http.StatusTooManyRequests, "CAP_EXCEEDED", "per-course image upload limit reached")
		return
	}

	// Step 8: INSERT via server-role connection (images_server_insert_policy).
	row, err := h.repo.Insert(r.Context(), studentID, courseID, detected, data, sha256hex)
	if err != nil {
		log.Printf("image: upload: insert: %v", err)
		writeImageError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	log.Printf("image: uploaded id=%s student=%s course=%s mime=%s bytes=%d",
		row.ID, studentID, courseID, detected, len(data))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
		"image_id": row.ID.String(),
		"url":      "/api/v1/images/" + row.ID.String(),
	})
}

// serve handles GET /api/v1/images/{imageId}.
// Fetches via server-role conn (images_server_select_policy), then verifies
// ownership in Go: image.student_id must equal the authenticated user.
//
// @{"req": ["REQ-AGENT-025"]}
func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	rawUserID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeImageError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawUserID)

	imageID, err := uuid.Parse(chi.URLParam(r, "imageId"))
	if err != nil {
		writeImageError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid image id")
		return
	}

	// Fetch using a server-role connection so images_server_select_policy passes.
	// Ownership is enforced in Go after the fetch, not by the RLS USING clause,
	// because the student SELECT policy requires the student's GUC which is not
	// available in a server-role connection. The server conn is needed here because
	// the GET handler has no request-scoped student conn to inject into context.
	row, err := h.repo.GetByIDServerConn(r.Context(), imageID)
	if err != nil {
		if err == ErrNotFound {
			writeImageError(w, http.StatusNotFound, "NOT_FOUND", "image not found")
			return
		}
		log.Printf("image: serve: get: %v", err)
		writeImageError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Ownership check: only the student who uploaded the image may retrieve it.
	if row.StudentID != studentID {
		writeImageError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
		return
	}

	// Security headers per SDD section 4.2:
	// - nosniff: defence against MIME-confusion even if stored bytes are ambiguous
	// - attachment disposition: prevents direct navigation (SVG-script defence in depth)
	// - private cache: this is an authenticated, per-user endpoint; "public" would
	//   permit shared proxies and CDN edge nodes to cache one user's image and serve
	//   it to another (cross-user data leak). "private" restricts caching to the
	//   individual browser only.
	// - immutable: UUID paths never reuse an ID, so the browser cache entry is
	//   permanently valid once populated (safe with "private").
	w.Header().Set("Content-Type", row.MimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Disposition", "attachment")
	w.WriteHeader(http.StatusOK)
	w.Write(row.Data) //nolint:errcheck — client disconnect is non-fatal
}

// @{"req": ["REQ-AGENT-023", "REQ-AGENT-024", "REQ-AGENT-025"]}
func writeImageError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message}) //nolint:errcheck
}
