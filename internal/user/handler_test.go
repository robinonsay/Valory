package user

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	authpkg "github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/audit"
)

func createTestUserWithPassword(ctx context.Context, t *testing.T, username string, email *string, plainPassword, role string) (uuid.UUID, string) {
	t.Helper()
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	passwordHash, err := authpkg.HashPassword(plainPassword)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}

	return createTestUser(ctx, t, username, email, passwordHash, role), plainPassword
}

func loginAsUser(ctx context.Context, t *testing.T, username string, password string) string {
	t.Helper()
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	authRepo := authpkg.NewRepository(pool)
	authSvc := authpkg.NewService(authRepo, 15*time.Minute, 24*time.Hour)

	rawToken, _, err := authSvc.Login(ctx, username, password)
	if err != nil {
		t.Fatalf("failed to login: %v", err)
	}

	return rawToken
}

type noOpTerminator struct{}

func (n *noOpTerminator) TerminateStudentOperations(ctx context.Context, studentID uuid.UUID) error {
	return nil
}

func makeAdminRequest(t *testing.T, method, path string, body interface{}, bearerToken string) *httptest.ResponseRecorder {
	t.Helper()

	handler := NewHandler(NewService(
		pool,
		NewRepository(pool),
		audit.NewRepository(pool),
		nil,
		15*time.Minute,
		&noOpTerminator{},
	))

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal request body: %v", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	} else {
		bodyReader = bytes.NewReader([]byte{})
	}

	req := httptest.NewRequest(method, path, bodyReader)
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	rr := httptest.NewRecorder()

	authMiddleware := authpkg.NewAuthMiddleware(
		authpkg.NewRepository(pool),
		pool,
		24*time.Hour,
		nil,
	)

	router := chi.NewRouter()
	router.Use(authMiddleware)
	handler.AdminRoutes(router)

	router.ServeHTTP(rr, req)
	return rr
}

func makePublicRequest(t *testing.T, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()

	handler := NewHandler(NewService(
		pool,
		NewRepository(pool),
		audit.NewRepository(pool),
		nil,
		15*time.Minute,
		&noOpTerminator{},
	))

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal request body: %v", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	} else {
		bodyReader = bytes.NewReader([]byte{})
	}

	req := httptest.NewRequest(method, path, bodyReader)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	handler.PublicRoutes(router)

	router.ServeHTTP(rr, req)
	return rr
}

func makeStudentRequest(t *testing.T, method, path string, body interface{}, bearerToken string) *httptest.ResponseRecorder {
	t.Helper()

	handler := NewHandler(NewService(
		pool,
		NewRepository(pool),
		audit.NewRepository(pool),
		nil,
		15*time.Minute,
		&noOpTerminator{},
	))

	var bodyReader io.Reader
	if body != nil {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal request body: %v", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	} else {
		bodyReader = bytes.NewReader([]byte{})
	}

	req := httptest.NewRequest(method, path, bodyReader)
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	rr := httptest.NewRecorder()

	authMiddleware := authpkg.NewAuthMiddleware(
		authpkg.NewRepository(pool),
		pool,
		24*time.Hour,
		nil,
	)

	router := chi.NewRouter()
	router.Use(authMiddleware)
	handler.StudentRoutes(router)

	router.ServeHTTP(rr, req)
	return rr
}

// @{"verifies": ["REQ-USER-001"]}
func TestHandlerCreateUser_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	adminUsername := "admin_" + uuid.New().String()
	adminPassword := "adminpass123"
	_, _ = createTestUserWithPassword(ctx, t, adminUsername, nil, adminPassword, "admin")

	token := loginAsUser(ctx, t, adminUsername, adminPassword)

	reqBody := map[string]interface{}{
		"username": "newuser_" + uuid.New().String(),
		"role":     "student",
		"password": "testpass123",
	}

	rr := makeAdminRequest(t, "POST", "/", reqBody, token)

	if rr.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rr.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["id"] == nil {
		t.Error("expected id in response")
	}
	if resp["username"] != reqBody["username"] {
		t.Errorf("expected username %v, got %v", reqBody["username"], resp["username"])
	}
	if resp["role"] != "student" {
		t.Errorf("expected role 'student', got %v", resp["role"])
	}
	if resp["is_active"] != true {
		t.Errorf("expected is_active true, got %v", resp["is_active"])
	}
}

// @{"verifies": ["REQ-USER-001"]}
func TestHandlerCreateUser_DuplicateUsername(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	adminUsername := "admin_dup_" + uuid.New().String()
	adminPassword := "adminpass123"
	_, _ = createTestUserWithPassword(ctx, t, adminUsername, nil, adminPassword, "admin")

	token := loginAsUser(ctx, t, adminUsername, adminPassword)

	duplicateUsername := "dupuser_" + uuid.New().String()

	reqBody1 := map[string]interface{}{
		"username": duplicateUsername,
		"role":     "student",
		"password": "testpass123",
	}

	rr1 := makeAdminRequest(t, "POST", "/", reqBody1, token)

	if rr1.Code != http.StatusCreated {
		t.Errorf("expected status 201 for first request, got %d", rr1.Code)
	}

	reqBody2 := map[string]interface{}{
		"username": duplicateUsername,
		"role":     "admin",
		"password": "testpass456",
	}

	rr2 := makeAdminRequest(t, "POST", "/", reqBody2, token)

	if rr2.Code != http.StatusConflict {
		t.Errorf("expected status 409, got %d", rr2.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(rr2.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errResp["error"] != "username already taken" {
		t.Errorf("expected error 'username already taken', got %q", errResp["error"])
	}
}

// @{"verifies": ["REQ-USER-002"]}
func TestHandlerModifyUser_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	adminUsername := "admin_modify_" + uuid.New().String()
	adminPassword := "adminpass123"
	_, _ = createTestUserWithPassword(ctx, t, adminUsername, nil, adminPassword, "admin")

	token := loginAsUser(ctx, t, adminUsername, adminPassword)

	targetUsername := "user_modify_" + uuid.New().String()
	targetID, _ := createTestUserWithPassword(ctx, t, targetUsername, nil, "pass123", "student")

	newUsername := "updated_" + uuid.New().String()
	newEmail := "newemail@example.com"

	reqBody := map[string]interface{}{
		"username": newUsername,
		"email":    newEmail,
	}

	rr := makeAdminRequest(t, "PATCH", "/"+targetID.String(), reqBody, token)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["username"] != newUsername {
		t.Errorf("expected username %q, got %v", newUsername, resp["username"])
	}
	if resp["email"] != newEmail {
		t.Errorf("expected email %q, got %v", newEmail, resp["email"])
	}
}

// @{"verifies": ["REQ-USER-003"]}
func TestHandlerDeactivateUser_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	adminUsername := "admin_deact_" + uuid.New().String()
	adminPassword := "adminpass123"
	_, _ = createTestUserWithPassword(ctx, t, adminUsername, nil, adminPassword, "admin")

	token := loginAsUser(ctx, t, adminUsername, adminPassword)

	targetUsername := "user_deact_" + uuid.New().String()
	targetID, _ := createTestUserWithPassword(ctx, t, targetUsername, nil, "pass123", "student")

	rr := makeAdminRequest(t, "POST", "/"+targetID.String()+"/deactivate", nil, token)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rr.Code)
	}

	repo := NewRepository(pool)
	user, err := repo.GetUserByID(ctx, targetID)
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}

	if user.IsActive {
		t.Error("expected user to be deactivated")
	}
}

// @{"verifies": ["REQ-USER-003"]}
func TestHandlerActivateUser_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	adminUsername := "admin_act_" + uuid.New().String()
	adminPassword := "adminpass123"
	_, _ = createTestUserWithPassword(ctx, t, adminUsername, nil, adminPassword, "admin")

	token := loginAsUser(ctx, t, adminUsername, adminPassword)

	targetUsername := "user_act_" + uuid.New().String()
	targetID, _ := createTestUserWithPassword(ctx, t, targetUsername, nil, "pass123", "student")

	// Deactivate first so activation has something to reverse.
	rr1 := makeAdminRequest(t, "POST", "/"+targetID.String()+"/deactivate", nil, token)
	if rr1.Code != http.StatusNoContent {
		t.Fatalf("deactivate setup: expected 204, got %d", rr1.Code)
	}

	rr := makeAdminRequest(t, "POST", "/"+targetID.String()+"/activate", nil, token)
	if rr.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rr.Code)
	}

	repo := NewRepository(pool)
	user, err := repo.GetUserByID(ctx, targetID)
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}
	if !user.IsActive {
		t.Error("expected user to be active after activation")
	}
}

// @{"verifies": ["REQ-USER-002"]}
func TestHandlerModifyUser_PartialUpdate(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	adminUsername := "admin_partial_" + uuid.New().String()
	adminPassword := "adminpass123"
	_, _ = createTestUserWithPassword(ctx, t, adminUsername, nil, adminPassword, "admin")

	token := loginAsUser(ctx, t, adminUsername, adminPassword)

	// Create user with both username and email set.
	email := "original@example.com"
	targetUsername := "user_partial_" + uuid.New().String()
	targetID := createTestUser(ctx, t, targetUsername, &email, "hash", "student")

	// PATCH only the username — email must be preserved.
	newUsername := "updated_partial_" + uuid.New().String()
	rr := makeAdminRequest(t, "PATCH", "/"+targetID.String(), map[string]interface{}{
		"username": newUsername,
	}, token)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: body=%s", rr.Code, rr.Body.String())
		return
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["username"] != newUsername {
		t.Errorf("expected username %q, got %v", newUsername, resp["username"])
	}
	if resp["email"] != email {
		t.Errorf("expected email %q to be preserved, got %v", email, resp["email"])
	}
}

// @{"verifies": ["REQ-SECURITY-003"]}
func TestHandlerRequestPasswordReset_RateLimit(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()

	// Create a user without email so the emailTransport nil is never reached.
	targetUsername := "user_rl_" + uuid.New().String()
	targetID := createTestUser(ctx, t, targetUsername, nil, "hash", "student")

	// Seed 3 password-reset attempts directly so the next request hits the limit.
	for i := 0; i < 3; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO password_reset_attempts (user_id) VALUES ($1)`,
			targetID); err != nil {
			t.Fatalf("failed to seed attempt %d: %v", i+1, err)
		}
	}

	rr := makePublicRequest(t, "POST", "/request", map[string]interface{}{
		"username": targetUsername,
	})

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected status 429, got %d: body=%s", rr.Code, rr.Body.String())
	}
}

// @{"verifies": ["REQ-USER-007"]}
func TestHandlerDeleteStudent_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	adminUsername := "admin_del_" + uuid.New().String()
	adminPassword := "adminpass123"
	_, _ = createTestUserWithPassword(ctx, t, adminUsername, nil, adminPassword, "admin")

	token := loginAsUser(ctx, t, adminUsername, adminPassword)

	targetUsername := "user_del_" + uuid.New().String()
	targetID, _ := createTestUserWithPassword(ctx, t, targetUsername, nil, "pass123", "student")

	rr := makeAdminRequest(t, "DELETE", "/"+targetID.String(), nil, token)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rr.Code)
	}

	repo := NewRepository(pool)
	_, err := repo.GetUserByID(ctx, targetID)
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

// @{"verifies": ["REQ-USER-005"]}
func TestHandlerRequestPasswordReset_NoEnumeration(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	rr := makePublicRequest(t, "POST", "/request", map[string]interface{}{
		"username": "nonexistent_" + uuid.New().String(),
	})

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rr.Code)
	}
}

// @{"verifies": ["REQ-USER-005", "REQ-USER-006"]}
func TestHandlerConfirmPasswordReset_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	repo := NewRepository(pool)

	targetUsername := "user_reset_" + uuid.New().String()
	targetID, _ := createTestUserWithPassword(ctx, t, targetUsername, nil, "oldpass123", "student")

	rawToken, tokenHash, expiresAt, err := authpkg.IssueToken(15 * time.Minute)
	if err != nil {
		t.Fatalf("failed to issue token: %v", err)
	}

	if err := repo.CreatePasswordResetToken(ctx, targetID, tokenHash, expiresAt); err != nil {
		t.Fatalf("failed to create reset token: %v", err)
	}

	newPassword := "newpass456"
	reqBody := map[string]interface{}{
		"token":        rawToken,
		"new_password": newPassword,
	}

	rr := makePublicRequest(t, "POST", "/confirm", reqBody)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rr.Code)
	}

	user, err := repo.GetUserByID(ctx, targetID)
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}

	ok, err := authpkg.CheckPassword(newPassword, user.PasswordHash)
	if err != nil {
		t.Fatalf("failed to check password: %v", err)
	}

	if !ok {
		t.Error("expected new password to be valid")
	}
}

// @{"verifies": ["REQ-SECURITY-005"]}
func TestHandlerRecordConsent_Success(t *testing.T) {
	if pool == nil {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	studentUsername := "student_consent_" + uuid.New().String()
	studentPassword := "studentpass123"
	studentID, _ := createTestUserWithPassword(ctx, t, studentUsername, nil, studentPassword, "student")

	token := loginAsUser(ctx, t, studentUsername, studentPassword)

	consentVersion := "2.0"
	reqBody := map[string]interface{}{
		"version": consentVersion,
	}

	rr := makeStudentRequest(t, "POST", "/", reqBody, token)

	if rr.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", rr.Code)
	}

	repo := NewRepository(pool)
	version, err := repo.GetConsentVersion(ctx, studentID)
	if err != nil {
		t.Fatalf("failed to get consent version: %v", err)
	}

	if version != consentVersion {
		t.Errorf("expected consent version %q, got %q", consentVersion, version)
	}
}
