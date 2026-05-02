//go:build testing

package course

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/valory/valory/internal/auth"
)

func createTestUserWithRole(ctx context.Context, t *testing.T, username string, role string) uuid.UUID {
	var userID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		username, "testhash", role).
		Scan(&userID)
	if err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}
	return userID
}

// @{"verifies": ["REQ-COURSE-001"]}
func TestHandler_CreateCourse_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.createCourse(w, r)
	})

	body, _ := json.Marshal(map[string]string{
		"topic": "Machine Learning Basics",
	})

	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["topic"] != "Machine Learning Basics" {
		t.Errorf("expected topic 'Machine Learning Basics', got %v", resp["topic"])
	}
	if resp["status"] != "intake" {
		t.Errorf("expected status 'intake', got %v", resp["status"])
	}
}

// @{"verifies": ["REQ-COURSE-001"]}
func TestHandler_CreateCourse_DuplicateActive_Returns409(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())

	repo := NewRepository(pool)
	svc := NewService(repo)

	course1, err := svc.CreateCourse(ctx, studentID, "Course 1")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE courses SET status = 'active' WHERE id = $1`, course1.ID); err != nil {
		t.Fatalf("failed to set course status: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.createCourse(w, r)
	})

	body, _ := json.Marshal(map[string]string{
		"topic": "Course 2",
	})

	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["error"] != "COURSE_ALREADY_ACTIVE" {
		t.Errorf("expected error 'COURSE_ALREADY_ACTIVE', got %s", resp["error"])
	}
}

// @{"verifies": ["REQ-COURSE-001"]}
func TestHandler_CreateCourse_MissingTopic_Returns400(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())

	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.createCourse(w, r)
	})

	body, _ := json.Marshal(map[string]string{
		"topic": "",
	})

	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-002"]}
func TestHandler_ListCourses_StudentSeesOnlyOwn(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentA := createTestUserWithRole(ctx, t, "student_a_"+uuid.New().String(), "student")
	studentB := createTestUserWithRole(ctx, t, "student_b_"+uuid.New().String(), "student")

	repo := NewRepository(pool)
	svc := NewService(repo)

	courseA, err := svc.CreateCourse(ctx, studentA, "Course A")
	if err != nil {
		t.Fatalf("CreateCourse for studentA failed: %v", err)
	}
	_, err = svc.CreateCourse(ctx, studentB, "Course B")
	if err != nil {
		t.Fatalf("CreateCourse for studentB failed: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentA), "student"))
		handler.listCourses(w, r)
	})

	req := httptest.NewRequest("GET", "/?limit=20", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	courses, ok := resp["courses"].([]interface{})
	if !ok || courses == nil {
		t.Fatalf("expected courses array in response")
	}

	if len(courses) != 1 {
		t.Errorf("expected 1 course, got %d", len(courses))
	}

	if len(courses) > 0 {
		course := courses[0].(map[string]interface{})
		if course["id"] != courseA.ID.String() {
			t.Errorf("expected course %s, got %s", courseA.ID.String(), course["id"])
		}
	}
}

// @{"verifies": ["REQ-COURSE-002"]}
func TestHandler_ListCourses_AdminSeesAll(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentA := createTestUserWithRole(ctx, t, "student_a_"+uuid.New().String(), "student")
	studentB := createTestUserWithRole(ctx, t, "student_b_"+uuid.New().String(), "student")
	adminID := createTestUserWithRole(ctx, t, "admin_"+uuid.New().String(), "admin")

	repo := NewRepository(pool)
	svc := NewService(repo)

	courseA, err := svc.CreateCourse(ctx, studentA, "Course A")
	if err != nil {
		t.Fatalf("CreateCourse for studentA failed: %v", err)
	}
	courseB, err := svc.CreateCourse(ctx, studentB, "Course B")
	if err != nil {
		t.Fatalf("CreateCourse for studentB failed: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(adminID), "admin"))
		handler.listCourses(w, r)
	})

	req := httptest.NewRequest("GET", "/?limit=200", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	courses, ok := resp["courses"].([]interface{})
	if !ok || courses == nil {
		t.Fatalf("expected courses array in response")
	}

	// Admin sees all courses; at minimum the two just created must be present.
	if len(courses) < 2 {
		t.Errorf("expected at least 2 courses (admin sees all), got %d", len(courses))
	}

	ids := make(map[string]bool, len(courses))
	for _, c := range courses {
		ids[c.(map[string]interface{})["id"].(string)] = true
	}
	if !ids[courseA.ID.String()] {
		t.Errorf("expected course A (%s) in admin listing", courseA.ID)
	}
	if !ids[courseB.ID.String()] {
		t.Errorf("expected course B (%s) in admin listing", courseB.ID)
	}
}

// @{"verifies": ["REQ-COURSE-003"]}
func TestHandler_Withdraw_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Test Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE courses SET status = 'active' WHERE id = $1`, course.ID); err != nil {
		t.Fatalf("failed to set course status: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/{id}/withdraw", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.withdraw(w, r)
	})

	req := httptest.NewRequest("POST", "/"+course.ID.String()+"/withdraw", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "archived" {
		t.Errorf("expected status 'archived', got %v", resp["status"])
	}
}

// @{"verifies": ["REQ-COURSE-003"]}
func TestHandler_Withdraw_WrongOwner_Returns403(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentA := createTestUserWithRole(ctx, t, "student_a_"+uuid.New().String(), "student")
	studentB := createTestUserWithRole(ctx, t, "student_b_"+uuid.New().String(), "student")

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentA, "Test Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/{id}/withdraw", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentB), "student"))
		handler.withdraw(w, r)
	})

	req := httptest.NewRequest("POST", "/"+course.ID.String()+"/withdraw", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-COURSE-003"]}
func TestHandler_Withdraw_AlreadyArchived_Returns409(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Test Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE courses SET status = 'archived' WHERE id = $1`, course.ID); err != nil {
		t.Fatalf("failed to archive course: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/{id}/withdraw", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.withdraw(w, r)
	})

	req := httptest.NewRequest("POST", "/"+course.ID.String()+"/withdraw", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["error"] != "INVALID_TRANSITION" {
		t.Errorf("expected error 'INVALID_TRANSITION', got %s", resp["error"])
	}
}

// @{"verifies": ["REQ-COURSE-004"]}
func TestHandler_Resume_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Test Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE courses SET status = 'archived', pre_withdrawal_status = 'active' WHERE id = $1`, course.ID); err != nil {
		t.Fatalf("failed to archive course: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.resume(w, r)
	})

	req := httptest.NewRequest("POST", "/"+course.ID.String()+"/resume", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "active" {
		t.Errorf("expected status 'active', got %v", resp["status"])
	}
}

// @{"verifies": ["REQ-COURSE-004"]}
// Setup: archive course1 first so course2 can be created, then set course2 active.
// Resuming course1 should fail because course2 is already active.
func TestHandler_Resume_ActiveCourseExists_Returns409(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())

	repo := NewRepository(pool)
	svc := NewService(repo)

	// Create course1, archive it so the unique constraint allows course2 to be created.
	course1, err := svc.CreateCourse(ctx, studentID, "Course 1")
	if err != nil {
		t.Fatalf("CreateCourse (course1) failed: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE courses SET status = 'archived', pre_withdrawal_status = 'intake' WHERE id = $1`, course1.ID); err != nil {
		t.Fatalf("failed to archive course1: %v", err)
	}

	// Create course2 (now allowed since course1 is archived).
	course2, err := svc.CreateCourse(ctx, studentID, "Course 2")
	if err != nil {
		t.Fatalf("CreateCourse (course2) failed: %v", err)
	}
	// Set course2 to active so resuming course1 conflicts.
	if _, err := pool.Exec(ctx, `UPDATE courses SET status = 'active' WHERE id = $1`, course2.ID); err != nil {
		t.Fatalf("failed to activate course2: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.resume(w, r)
	})

	req := httptest.NewRequest("POST", "/"+course1.ID.String()+"/resume", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["error"] != "COURSE_ALREADY_ACTIVE" {
		t.Errorf("expected error 'COURSE_ALREADY_ACTIVE', got %s", resp["error"])
	}
}

// @{"verifies": ["REQ-COURSE-006"]}
func TestHandler_ApproveSyllabus_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Test Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE courses SET status = 'syllabus_draft' WHERE id = $1`, course.ID); err != nil {
		t.Fatalf("failed to set course to syllabus_draft: %v", err)
	}
	if _, err := repo.InsertSyllabus(ctx, course.ID, "# Test Syllabus", 1); err != nil {
		t.Fatalf("failed to insert syllabus: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/{id}/syllabus/approve", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.approveSyllabus(w, r)
	})

	req := httptest.NewRequest("POST", "/"+course.ID.String()+"/syllabus/approve", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "syllabus_approved" {
		t.Errorf("expected status 'syllabus_approved', got %v", resp["status"])
	}
	if resp["syllabus_version"] != float64(1) {
		t.Errorf("expected version 1, got %v", resp["syllabus_version"])
	}
}

// @{"verifies": ["REQ-COURSE-006"]}
func TestHandler_ApproveSyllabus_WrongState_Returns409(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Test Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE courses SET status = 'intake' WHERE id = $1`, course.ID); err != nil {
		t.Fatalf("failed to set course to intake: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/{id}/syllabus/approve", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.approveSyllabus(w, r)
	})

	req := httptest.NewRequest("POST", "/"+course.ID.String()+"/syllabus/approve", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["error"] != "NOT_IN_SYLLABUS_DRAFT" {
		t.Errorf("expected error 'NOT_IN_SYLLABUS_DRAFT', got %s", resp["error"])
	}
}

// @{"verifies": ["REQ-COURSE-005"]}
func TestHandler_RequestModification_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Test Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE courses SET status = 'syllabus_draft' WHERE id = $1`, course.ID); err != nil {
		t.Fatalf("failed to set course to syllabus_draft: %v", err)
	}
	if _, err := repo.InsertSyllabus(ctx, course.ID, "# Original Syllabus", 1); err != nil {
		t.Fatalf("failed to insert syllabus: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/{id}/syllabus/modification", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.requestModification(w, r)
	})

	body, _ := json.Marshal(map[string]string{
		"request": "Please add more content",
	})

	req := httptest.NewRequest("POST", "/"+course.ID.String()+"/syllabus/modification", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["syllabus_version"] != float64(2) {
		t.Errorf("expected version 2, got %v", resp["syllabus_version"])
	}
}

// @{"verifies": ["REQ-COURSE-005"]}
func TestHandler_RequestModification_EmptyRequest_Returns400(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Test Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/{id}/syllabus/modification", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.requestModification(w, r)
	})

	body, _ := json.Marshal(map[string]string{
		"request": "",
	})

	req := httptest.NewRequest("POST", "/"+course.ID.String()+"/syllabus/modification", bytes.NewReader(body))
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-COURSE-008"]}
func TestHandler_AgreeToSchedule_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Test Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	homework, err := repo.InsertHomework(ctx, course.ID, 1, "HW 1", "Rubric", 0.5)
	if err != nil {
		t.Fatalf("InsertHomework failed: %v", err)
	}
	if err := repo.InsertDueDateSchedule(ctx, course.ID, homework.ID, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatalf("InsertDueDateSchedule failed: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/{id}/schedule/agree", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.agreeToSchedule(w, r)
	})

	req := httptest.NewRequest("POST", "/"+course.ID.String()+"/schedule/agree", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	agreedCount, ok := resp["agreed_count"].(float64)
	if !ok || agreedCount == 0 {
		t.Errorf("expected agreed_count > 0, got %v", resp["agreed_count"])
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-007"]}
func TestHandler_GetCourse_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUserWithRole(ctx, t, "student_"+uuid.New().String(), "student")

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Intro to Go")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Get("/{id}", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.getCourse(w, r)
	})

	req := httptest.NewRequest("GET", "/"+course.ID.String(), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["id"] != course.ID.String() {
		t.Errorf("expected id %s, got %v", course.ID.String(), resp["id"])
	}
	if resp["topic"] != "Intro to Go" {
		t.Errorf("expected topic 'Intro to Go', got %v", resp["topic"])
	}
	if resp["status"] != "intake" {
		t.Errorf("expected status 'intake', got %v", resp["status"])
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-007"]}
func TestHandler_GetCourse_NotFound_Returns404(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUserWithRole(ctx, t, "student_"+uuid.New().String(), "student")

	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Get("/{id}", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.getCourse(w, r)
	})

	nonExistentID := uuid.New()
	req := httptest.NewRequest("GET", "/"+nonExistentID.String(), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-007"]}
func TestHandler_GetCourse_WrongOwner_Returns403(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentA := createTestUserWithRole(ctx, t, "student_a_"+uuid.New().String(), "student")
	studentB := createTestUserWithRole(ctx, t, "student_b_"+uuid.New().String(), "student")

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentA, "Private Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Get("/{id}", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentB), "student"))
		handler.getCourse(w, r)
	})

	req := httptest.NewRequest("GET", "/"+course.ID.String(), nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-007"]}
func TestHandler_GetSyllabus_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUserWithRole(ctx, t, "student_"+uuid.New().String(), "student")

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Advanced Go")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}
	if _, err := repo.InsertSyllabus(ctx, course.ID, "= Advanced Go\n\nWeek 1: Concurrency", 1); err != nil {
		t.Fatalf("InsertSyllabus failed: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Get("/{id}/syllabus", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.getSyllabus(w, r)
	})

	req := httptest.NewRequest("GET", "/"+course.ID.String()+"/syllabus", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["course_id"] != course.ID.String() {
		t.Errorf("expected course_id %s, got %v", course.ID.String(), resp["course_id"])
	}
	if resp["version"] != float64(1) {
		t.Errorf("expected version 1, got %v", resp["version"])
	}
	if resp["content_adoc"] != "= Advanced Go\n\nWeek 1: Concurrency" {
		t.Errorf("unexpected content_adoc: %v", resp["content_adoc"])
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-007"]}
func TestHandler_GetSyllabus_NotFound_Returns404(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUserWithRole(ctx, t, "student_"+uuid.New().String(), "student")

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "No Syllabus Yet")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Get("/{id}/syllabus", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.getSyllabus(w, r)
	})

	req := httptest.NewRequest("GET", "/"+course.ID.String()+"/syllabus", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-007"]}
func TestHandler_ListHomework_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUserWithRole(ctx, t, "student_"+uuid.New().String(), "student")

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Data Structures")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	hw1, err := repo.InsertHomework(ctx, course.ID, 1, "Arrays", "Rubric 1", 0.3)
	if err != nil {
		t.Fatalf("InsertHomework 1 failed: %v", err)
	}
	hw2, err := repo.InsertHomework(ctx, course.ID, 2, "Linked Lists", "Rubric 2", 0.4)
	if err != nil {
		t.Fatalf("InsertHomework 2 failed: %v", err)
	}
	dueDate := time.Now().Add(7 * 24 * time.Hour).UTC().Truncate(time.Second)
	if err := repo.InsertDueDateSchedule(ctx, course.ID, hw1.ID, dueDate); err != nil {
		t.Fatalf("InsertDueDateSchedule failed: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Get("/{id}/homework", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.listHomework(w, r)
	})

	req := httptest.NewRequest("GET", "/"+course.ID.String()+"/homework", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	items, ok := resp["homework"].([]interface{})
	if !ok {
		t.Fatalf("expected homework array in response")
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 homework items, got %d", len(items))
	}

	first := items[0].(map[string]interface{})
	if first["id"] != hw1.ID.String() {
		t.Errorf("expected first homework id %s, got %v", hw1.ID.String(), first["id"])
	}
	if first["title"] != "Arrays" {
		t.Errorf("expected title 'Arrays', got %v", first["title"])
	}
	if _, hasDueDate := first["due_date"]; !hasDueDate {
		t.Errorf("expected due_date to be present for hw1")
	}

	second := items[1].(map[string]interface{})
	if second["id"] != hw2.ID.String() {
		t.Errorf("expected second homework id %s, got %v", hw2.ID.String(), second["id"])
	}
	if _, hasDueDate := second["due_date"]; hasDueDate {
		t.Errorf("expected due_date to be absent for hw2 (no schedule inserted)")
	}
}

// @{"verifies": ["REQ-COURSE-001", "REQ-COURSE-007"]}
func TestHandler_ListHomework_EmptyArray(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUserWithRole(ctx, t, "student_"+uuid.New().String(), "student")

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Empty Homework Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Get("/{id}/homework", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.listHomework(w, r)
	})

	req := httptest.NewRequest("GET", "/"+course.ID.String()+"/homework", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	items, ok := resp["homework"].([]interface{})
	if !ok {
		t.Fatalf("expected homework array in response, got: %v", resp["homework"])
	}
	if len(items) != 0 {
		t.Errorf("expected empty homework array, got %d items", len(items))
	}
}

// @{"verifies": ["REQ-COURSE-008"]}
func TestHandler_AgreeToSchedule_NoPending_Returns409(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentID := createTestUser(ctx, t, "student_"+uuid.New().String())

	repo := NewRepository(pool)
	svc := NewService(repo)

	course, err := svc.CreateCourse(ctx, studentID, "Test Course")
	if err != nil {
		t.Fatalf("CreateCourse failed: %v", err)
	}

	handler := NewHandler(svc)

	router := chi.NewRouter()
	router.Post("/{id}/schedule/agree", func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.SetTestContext(r.Context(), [16]byte(studentID), "student"))
		handler.agreeToSchedule(w, r)
	})

	req := httptest.NewRequest("POST", "/"+course.ID.String()+"/schedule/agree", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["error"] != "NO_PENDING_SCHEDULE" {
		t.Errorf("expected error 'NO_PENDING_SCHEDULE', got %s", resp["error"])
	}
}
