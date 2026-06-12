package submission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/course"
)

// maxSubmissionImages is the maximum number of image IDs a submission may carry
// (REQ-SUBMISSION-006).
const maxSubmissionImages = 8

// defaultMaxUploadBytes is used when the config key is absent or zero.
const defaultMaxUploadBytes = int64(10485760) // 10 MB

// courseOwnershipChecker is satisfied by *course.CourseRepository and by test stubs.
// Defined locally to avoid coupling handler tests to the concrete repository type.
type courseOwnershipChecker interface {
	GetCourseByID(ctx context.Context, id uuid.UUID) (course.CourseRow, error)
}

// imageOwnershipValidator is satisfied by *image.Repository and by test stubs.
// Defined locally (consumer side) to avoid an import cycle between submission
// and image packages. Only ValidateOwnership and the cap check are needed here.
type imageOwnershipValidator interface {
	ValidateOwnership(ctx context.Context, ids []uuid.UUID, studentID, courseID uuid.UUID) error
}

// homeworkDetailFetcher is satisfied by *Repository and by test stubs. Defined
// locally so handler tests can inject a stub without a live database.
type homeworkDetailFetcher interface {
	GetHomework(ctx context.Context, homeworkID uuid.UUID) (HomeworkRow, error)
}

// Handler serves the submission upload and retrieval endpoints.
type Handler struct {
	repo       *Repository
	hwFetcher  homeworkDetailFetcher   // used by getHomeworkDetail; defaults to repo
	courseRepo courseOwnershipChecker  // used to verify course ownership before accepting uploads
	imageRepo  imageOwnershipValidator // nil when image support is not wired (graceful degradation)
	uploadsDir string
	configSvc  interface{ GetInt64(string) int64 }
}

// @{"req": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-002", "REQ-SUBMISSION-003"]}
func NewHandler(repo *Repository, courseRepo courseOwnershipChecker, uploadsDir string, configSvc interface{ GetInt64(string) int64 }) *Handler {
	return &Handler{
		repo:       repo,
		hwFetcher:  repo, // *Repository satisfies homeworkDetailFetcher
		courseRepo: courseRepo,
		uploadsDir: uploadsDir,
		configSvc:  configSvc,
	}
}

// SetImageRepository injects the image repository so the submission upload
// handler can validate image_ids ownership (REQ-SUBMISSION-005). Called from
// main after both packages are wired to avoid an import cycle.
//
// @{"req": ["REQ-SUBMISSION-005"]}
func (h *Handler) SetImageRepository(repo imageOwnershipValidator) {
	h.imageRepo = repo
}

// Routes mounts submission endpoints under /courses/{id}/homework/{homeworkId}.
// Must be mounted inside the authenticated + CSRF group.
//
// @{"req": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-002", "REQ-SUBMISSION-003", "REQ-FECONTENT-020"]}
func (h *Handler) Routes(r chi.Router) {
	r.Get("/", h.getHomeworkDetail)
	r.Post("/submissions", h.upload)
	r.Get("/submissions/latest", h.getLatest)
}

// getHomeworkDetail returns the homework title, description (rubric), and due
// date for the given homework assignment. It enforces the same IDOR checks as
// the upload handler: the homework must belong to the course in the URL, and
// the course must belong to the authenticated student.
//
// Response shape mirrors the HomeworkDetail TypeScript interface in
// HomeworkSubmissionView.vue: {id, title, description, due_date}.
//
// @{"req": ["REQ-FECONTENT-020", "REQ-SUBMISSION-001"]}
func (h *Handler) getHomeworkDetail(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawID)

	courseIDStr := chi.URLParam(r, "id")
	courseID, err := uuid.Parse(courseIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	homeworkIDStr := chi.URLParam(r, "homeworkId")
	homeworkID, err := uuid.Parse(homeworkIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid homework id")
		return
	}

	// Verify the course belongs to the requesting student before revealing
	// any homework data (mirrors the ownership check in upload and getLatest).
	c, err := h.courseRepo.GetCourseByID(r.Context(), courseID)
	if err != nil {
		if errors.Is(err, course.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if c.StudentID != studentID {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
		return
	}

	hw, err := h.hwFetcher.GetHomework(r.Context(), homeworkID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "homework not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	// Verify the homework belongs to the course from the URL to prevent IDOR
	// (a student supplying their own valid courseId but a homeworkId from
	// another course would otherwise leak data across course boundaries).
	if hw.CourseID != courseID {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "homework does not belong to this course")
		return
	}

	resp := homeworkDetailToResponse(hw)
	writeJSON(w, http.StatusOK, resp)
}

// homeworkDetailToResponse converts a HomeworkRow to the JSON shape expected
// by the HomeworkDetail TypeScript interface in HomeworkSubmissionView.vue:
// {id, title, description, due_date}. The rubric column is surfaced as
// "description" because that is the field name the frontend destructures.
// due_date is omitted from the response when no schedule row exists.
//
// @{"req": ["REQ-FECONTENT-020", "REQ-SUBMISSION-001"]}
func homeworkDetailToResponse(hw HomeworkRow) map[string]interface{} {
	resp := map[string]interface{}{
		"id":          hw.ID.String(),
		"title":       hw.Title,
		"description": hw.Rubric,
	}
	if hw.DueDate != nil {
		resp["due_date"] = hw.DueDate.Format(time.RFC3339)
	}
	return resp
}

// upload accepts a multipart file upload for a homework assignment.
// It enforces format restrictions (REQ-SUBMISSION-001), size limits
// (REQ-SUBMISSION-002), and content validation (REQ-SUBMISSION-003).
//
// @{"req": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-002", "REQ-SUBMISSION-003"]}
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawID)

	courseIDStr := chi.URLParam(r, "id")
	courseID, err := uuid.Parse(courseIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	homeworkIDStr := chi.URLParam(r, "homeworkId")
	homeworkID, err := uuid.Parse(homeworkIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid homework id")
		return
	}

	// Enforce the size limit before any DB access so oversized requests are
	// rejected cheaply. We add 1 byte to the reader limit so that a body at
	// exactly maxBytes is distinguishable from one that exceeds it when we
	// later check header.Size.
	maxBytes := h.configSvc.GetInt64("max_upload_bytes")
	if maxBytes == 0 {
		maxBytes = defaultMaxUploadBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1)

	if err := r.ParseMultipartForm(maxBytes); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "file exceeds maximum allowed size")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "file field is required")
		return
	}
	defer file.Close()

	if header.Size > maxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "file exceeds maximum allowed size")
		return
	}

	format, err := DetectFormat(header.Filename)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "UNSUPPORTED_FORMAT", "unsupported file format; accepted: .tex, .md, .markdown, .adoc, .asciidoc")
		return
	}

	// Read file content into memory so we can both validate and persist it.
	// We already know the file is within maxBytes from the header.Size check above,
	// so buffering is safe.
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	if err := ValidateContent(format, bytes.NewReader(fileBytes)); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "INVALID_CONTENT", "file content does not match declared format")
		return
	}

	// Verify the course belongs to the requesting student before accepting data.
	c, err := h.courseRepo.GetCourseByID(r.Context(), courseID)
	if err != nil {
		if errors.Is(err, course.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if c.StudentID != studentID {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
		return
	}

	// Verify the homework belongs to this course to prevent IDOR. Without this
	// check a student could supply their own valid courseId (passing ownership)
	// but a homeworkId from a different course, allowing cross-course submission
	// injection.
	hwCourseID, err := h.repo.GetHomeworkCourseID(r.Context(), homeworkID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "homework not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if hwCourseID != courseID {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "homework does not belong to this course")
		return
	}

	// Parse optional image_ids JSON array from the multipart form field
	// (REQ-SUBMISSION-004). The field is a JSON-encoded array of UUID strings,
	// e.g. '["uuid-1","uuid-2"]'. Absent or empty field means no images attached.
	var imageIDs []uuid.UUID
	if rawImageIDs := r.FormValue("image_ids"); rawImageIDs != "" {
		var rawStrs []string
		if err := json.Unmarshal([]byte(rawImageIDs), &rawStrs); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "INVALID_IMAGE_IDS", "image_ids must be a JSON array of UUID strings")
			return
		}
		// REQ-SUBMISSION-006: cap at 8 images.
		if len(rawStrs) > maxSubmissionImages {
			writeError(w, http.StatusUnprocessableEntity, "TOO_MANY_IMAGES", "image_ids must not exceed 8 entries")
			return
		}
		for _, s := range rawStrs {
			id, err := uuid.Parse(s)
			if err != nil {
				writeError(w, http.StatusUnprocessableEntity, "INVALID_IMAGE_ID", "image_ids contains an invalid UUID")
				return
			}
			imageIDs = append(imageIDs, id)
		}
	}

	// REQ-SUBMISSION-005: validate that every image ID is owned by this student
	// in this course. imageRepo may be nil when the image package is not wired
	// (test environments); in that case we skip the check and store an empty slice.
	if h.imageRepo != nil && len(imageIDs) > 0 {
		if err := h.imageRepo.ValidateOwnership(r.Context(), imageIDs, studentID, courseID); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "INVALID_IMAGE_ID", "one or more image_ids are invalid or not owned by you in this course")
			return
		}
	}

	// Build the storage path scoped to student + homework to avoid collisions.
	submissionID := uuid.New()
	ext := filepath.Ext(header.Filename)
	storagePath := filepath.Join(
		h.uploadsDir,
		studentID.String(),
		homeworkID.String(),
		fmt.Sprintf("%s%s", submissionID.String(), ext),
	)

	if err := os.MkdirAll(filepath.Dir(storagePath), 0750); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	if err := os.WriteFile(storagePath, fileBytes, 0640); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	row, err := h.repo.Insert(r.Context(), homeworkID, studentID, format, storagePath, int64(len(fileBytes)), imageIDs)
	if err != nil {
		if errors.Is(err, ErrAlreadySubmitted) {
			// Clean up the file we just wrote since the DB insert was rejected.
			_ = os.Remove(storagePath)
			writeError(w, http.StatusConflict, "ALREADY_SUBMITTED", "student already submitted for this homework")
			return
		}
		_ = os.Remove(storagePath)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusCreated, submissionToResponse(row))
}

// getLatest returns the most recent submission for a student/homework pair.
//
// @{"req": ["REQ-SUBMISSION-001"]}
func (h *Handler) getLatest(w http.ResponseWriter, r *http.Request) {
	rawID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized")
		return
	}
	studentID := uuid.UUID(rawID)

	courseIDStr := chi.URLParam(r, "id")
	courseID, err := uuid.Parse(courseIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid course id")
		return
	}

	homeworkIDStr := chi.URLParam(r, "homeworkId")
	homeworkID, err := uuid.Parse(homeworkIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid homework id")
		return
	}

	// Verify course ownership before returning submission data.
	c, err := h.courseRepo.GetCourseByID(r.Context(), courseID)
	if err != nil {
		if errors.Is(err, course.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}
	if c.StudentID != studentID {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "access forbidden")
		return
	}

	row, err := h.repo.GetLatestByHomework(r.Context(), homeworkID, studentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "no submission found")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, submissionToResponse(row))
}

// submissionToResponse converts a SubmissionRow to the JSON response shape.
// image_count is derived from len(ImageIDs) per the SDD; it is not persisted
// separately but is included so the frontend can display the count without
// separately fetching the image_ids array (REQ-FECONTENT-224).
//
// @{"req": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-004"]}
func submissionToResponse(row SubmissionRow) map[string]interface{} {
	resp := map[string]interface{}{
		"id":              row.ID.String(),
		"homework_id":     row.HomeworkID.String(),
		"student_id":      row.StudentID.String(),
		"format":          row.Format,
		"file_size_bytes": row.FileSizeBytes,
		"submitted_at":    row.SubmittedAt.Format(time.RFC3339),
		"grading_status":  row.GradingStatus,
		"image_count":     len(row.ImageIDs),
	}
	if row.RawScore != nil {
		resp["raw_score"] = *row.RawScore
	}
	return resp
}

// writeJSON serialises v as JSON with the given status code.
//
// @{"req": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-002", "REQ-SUBMISSION-003"]}
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error envelope.
//
// @{"req": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-002", "REQ-SUBMISSION-003"]}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}
