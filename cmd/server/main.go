package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/admin"
	"github.com/valory/valory/internal/agent"
	"github.com/valory/valory/internal/audit"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/badge"
	"github.com/valory/valory/internal/content"
	"github.com/valory/valory/internal/course"
	"github.com/valory/valory/internal/db"
	"github.com/valory/valory/internal/email"
	"github.com/valory/valory/internal/grade"
	"github.com/valory/valory/internal/image"
	"github.com/valory/valory/internal/infra"
	"github.com/valory/valory/internal/notify"
	profilepkg "github.com/valory/valory/internal/profile"
	"github.com/valory/valory/internal/security"
	"github.com/valory/valory/internal/setup"
	"github.com/valory/valory/internal/submission"
	"github.com/valory/valory/internal/user"
	"github.com/valory/valory/migrations"
)

func main() {
	// Signal-aware context: cancels on SIGTERM or SIGINT so agentRunner and
	// other goroutines stop cleanly before the process exits.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	// --- Required environment variables ---
	databaseURL := mustEnv("DATABASE_URL")
	// ANTHROPIC_API_KEY is now optional at startup: the managed-secrets subsystem
	// can supply it via the admin UI without a container restart (REQ-ADMIN-005).
	// A WARN is logged at the end of startup if neither source is configured.
	anthropicAPIKey := envOrDefault("ANTHROPIC_API_KEY", "")
	uploadsDir := envOrDefault("UPLOADS_DIR", "/app/uploads")
	acmeDomain := os.Getenv("ACME_DOMAIN")
	acmeCacheDir := envOrDefault("ACME_CACHE_DIR", "/app/acme-cache")
	// REQ-INFRA-003..008: operator-supplied cert pair and reverse-proxy mode.
	tlsCertFile := os.Getenv("VALORY_TLS_CERT_FILE")
	tlsKeyFile := os.Getenv("VALORY_TLS_KEY_FILE")
	behindProxy := os.Getenv("VALORY_BEHIND_PROXY")

	lockoutDuration := parseDuration("AUTH_LOCKOUT_DURATION", 15*time.Minute)
	sessionMaxDuration := parseDuration("AUTH_SESSION_MAX_DURATION", 24*time.Hour)
	inactivityPeriod := parseDuration("AUTH_INACTIVITY_PERIOD", 30*time.Minute)

	smtpHost := envOrDefault("SMTP_HOST", "")
	smtpPort, err := strconv.Atoi(envOrDefault("SMTP_PORT", "587"))
	if err != nil {
		log.Fatalf("server: SMTP_PORT is not a valid integer: %v", err)
	}
	smtpFrom := envOrDefault("SMTP_FROM", "")
	// SMTP_USERNAME: new in Sprint 19. Operators who previously relied on
	// SMTP_FROM as the username (old SMTPTransport) should set this explicitly.
	smtpUsername := envOrDefault("SMTP_USERNAME", "")
	// SMTP_PASSWORD is now resolved per-send via SecretProvider (managed_secrets >
	// SMTP_PASSWORD env). The local variable is removed; resolution moves into the
	// email package via SecretGetter.Get("smtp_password").
	passwordResetTTL := parseDuration("PASSWORD_RESET_TTL", 1*time.Hour)

	// --- Database ---
	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("server: connect to database: %v", err)
	}
	defer pool.Close()

	// serverPool is a separate pool whose every acquired connection is
	// pre-configured with app.current_role='server' and the all-zeros sentinel
	// UUID via BeforeAcquire. It is passed exclusively to server-side actors
	// (agent pipeline, grading runner, notify writes) that write to FORCE-RLS
	// tables whose server-side policies require app.current_role='server'.
	// HTTP handler repositories keep the plain pool and use the request-scoped
	// connection (carrying the student's GUCs) injected by the auth middleware.
	// @{"req": ["REQ-AGENT-003", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-CONTENT-001", "REQ-NOTIFY-001", "REQ-SECURITY-002"]}
	serverPool, err := db.NewServerPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("server: connect to database (server pool): %v", err)
	}
	defer serverPool.Close()

	if err := runMigrations(ctx, pool); err != nil {
		log.Fatalf("server: run migrations: %v", err)
	}

	// Startup recovery: any agent_run left in 'running' state survived a crash.
	// Mark them failed so the polling loop can schedule fresh runs.
	// agent_runs has no RLS so either pool works here.
	// @{"req": ["REQ-AGENT-016"]}
	if _, err := pool.Exec(ctx,
		`UPDATE agent_runs SET status = 'failed', error = 'server restart' WHERE status = 'running'`,
	); err != nil {
		log.Printf("server: startup recovery: mark stale runs failed: %v", err)
	}

	// @{"req": ["REQ-SECURITY-005"]}
	// --- Config service (provides consent version) ---
	configSvc := admin.NewConfigService(pool)
	if err := configSvc.Load(ctx); err != nil {
		log.Fatalf("server: load config service: %v", err)
	}

	// --- Managed secrets subsystem (REQ-ADMIN-005..008, REQ-SECURITY-006..008) ---
	// loadKEK reads VALORY_SECRET_KEY; returns nil+false with a single WARN when
	// absent or invalid.  The server continues in env-fallback mode either way.
	kek, _ := admin.LoadKEK()
	secretProvider := admin.NewSecretProvider(kek, pool)
	if anthropicAPIKey == "" && !secretProvider.HasManaged("anthropic_api_key") {
		log.Printf("server: WARN: ANTHROPIC_API_KEY is not set and no managed secret exists; AI features will fail until a key is configured")
	}

	// --- Auth wiring ---
	authRepo := auth.NewRepository(pool)
	authSvc := auth.NewService(authRepo, lockoutDuration, sessionMaxDuration)

	// @{"req": ["REQ-AUTH-015", "REQ-AUTH-020"]}
	// VALORY_2FA_BREAK_GLASS startup warning: operators who set this variable to
	// recover from a sole-admin lockout should be aware the bypass is active.
	if os.Getenv("VALORY_2FA_BREAK_GLASS") != "" {
		log.Printf("server: WARN: VALORY_2FA_BREAK_GLASS is set; admin 2FA bypass is active")
	}

	// NewHandlerFull wires the additional dependencies (repo, pool, sessionMaxDuration,
	// consentProvider) needed by the login cookie (REQ-AUTH-009) and the
	// GET /auth/session restore endpoint (REQ-AUTH-012).
	authHandler := auth.NewHandlerFull(authSvc, authRepo, pool, sessionMaxDuration, configSvc)
	// authMW enforces session validity AND the consent gate (REQ-SECURITY-005).
	authMW := auth.NewAuthMiddleware(authRepo, pool, inactivityPeriod, configSvc)
	// authOnlyMW enforces session validity only — no consent gate. Used for the
	// /consent endpoint itself so students can accept consent without being blocked
	// by the gate they are trying to satisfy.
	authOnlyMW := auth.NewAuthMiddleware(authRepo, pool, inactivityPeriod, nil)

	// --- Agent module wiring ---
	// Agent is wired before User so AgentRunner can be passed to UserService as
	// the terminator that cancels in-flight runs on account deletion (REQ-AGENT-013).
	// Brave key and Anthropic key are both resolved per-call via secretProvider
	// so that admin UI updates take effect within the cache TTL (REQ-ADMIN-005..008).
	// Log a startup hint when brave is absent in both env and managed store.
	if envOrDefault("BRAVE_API_KEY", "") == "" && !secretProvider.HasManaged("brave_api_key") {
		log.Printf("server: BRAVE_API_KEY is not set and no managed secret exists; web search grounding will be unavailable")
	}

	// @{"req": ["REQ-AGENT-012", "REQ-ADMIN-005", "REQ-ADMIN-008"]}
	throttledClient := agent.NewThrottledClient(anthropicAPIKey, pool, configSvc, secretProvider)

	// Agent-side repositories and actors use serverPool so that every pool
	// connection already carries app.current_role='server' via BeforeAcquire.
	// This satisfies the server-side RLS policies on courses (005),
	// lesson_content, section_feedback, chat_messages, notifications,
	// submissions, grades, and student_badges without requiring every call site
	// to manually acquire a server-role connection via db.AcquireServerConn.
	// db.AcquireServerConn calls that already exist in chair/professor/reviewer
	// are harmless with serverPool: BeforeAcquire has already set the GUCs and
	// AcquireServerConn sets them again — a no-op double-set is safe.
	// @{"req": ["REQ-AGENT-003", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-CONTENT-001", "REQ-NOTIFY-001", "REQ-SECURITY-002"]}
	agentRepo := agent.NewAgentRepository(serverPool)
	chatRepo := agent.NewChatRepository(serverPool)
	chair := agent.NewChair(throttledClient, serverPool, agentRepo, chatRepo)
	// Inject the server pool so Chair can load the student's learning profile
	// for prompt injection (REQ-PROFILE-008, REQ-PROFILE-009). The server pool's
	// BeforeAcquire sets app.current_role='server', satisfying the server-select
	// RLS policy on learning_profiles.
	chair.SetServerPool(serverPool)
	// Professor resolves brave_api_key per-call via secretProvider (REQ-ADMIN-006)
	// and professor_max_tokens per-call via configSvc (REQ-ADMIN-010).
	professor := agent.NewProfessor(throttledClient, serverPool, agentRepo, secretProvider, configSvc)
	// Inject the server pool so Professor can load the student's learning profile
	// for prompt injection (REQ-PROFILE-006, REQ-PROFILE-007).
	professor.SetServerPool(serverPool)
	reviewer := agent.NewReviewer(throttledClient, serverPool, agentRepo)

	// --- Profile module wiring (REQ-PROFILE-001..016) ---
	// profileRepo uses the plain pool; the handler always passes in the
	// request-scoped connection (from auth middleware context) so FORCE RLS
	// on learning_profiles is satisfied via the conn(ctx) helper.
	// @{"req": ["REQ-PROFILE-001", "REQ-PROFILE-004", "REQ-PROFILE-005", "REQ-PROFILE-011", "REQ-PROFILE-012", "REQ-PROFILE-014", "REQ-PROFILE-015"]}
	profileRepo := profilepkg.NewProfileRepository(pool)
	// secretProvider satisfies profile.SecretResolver (both expose Get(ctx, name) string).
	onboardingSvc := profilepkg.NewOnboardingService(profileRepo, serverPool, secretProvider)
	profileHandler := profilepkg.NewHandler(profileRepo, onboardingSvc, serverPool)

	// --- Badge module wiring (required by grade module) ---
	badgeRepo := badge.NewRepository(pool)
	badgeSvc := badge.NewService(badgeRepo)
	badgeHandler := badge.NewHandler(badgeSvc)

	// --- Submission module wiring ---
	submissionRepo := submission.NewRepository(pool)

	// --- Grade module wiring ---
	gradeRepo := grade.NewRepository(pool)
	gradeSvc := grade.NewService(gradeRepo, pool, badgeSvc, configSvc)
	gradeHandler := grade.NewHandler(gradeSvc, gradeRepo)

	// agentRunner uses serverPool so that notify.Write calls and direct pool
	// queries inside the runner (generateAllSections, ensureDueDates,
	// lookupAdminID, pollFeedback, pollGradingQueue) all execute under the
	// server role without needing per-call AcquireServerConn wrappers.
	// @{"req": ["REQ-AGENT-003", "REQ-AGENT-007", "REQ-AGENT-008", "REQ-SECURITY-002"]}
	agentRunner := agent.NewAgentRunner(serverPool, agentRepo, chair, professor, reviewer, submissionRepo, gradeSvc, throttledClient, configSvc)
	agentHandler := agent.NewAgentHandler(agentRunner, chair, chatRepo, agentRepo)

	// Start background polling goroutines (30s gen poll, 60s feedback poll, 30s grade poll).
	go agentRunner.Start(ctx)

	// --- Email mailer wiring (REQ-EMAIL-001..011) ---
	// SMTPEnvVars captures env reads once at startup; per-send resolution in
	// SMTPMailer reads admin config first and falls back to these values.
	// Password resolution is handled inside SecretProvider.Get("smtp_password")
	// which applies the managed_secrets > SMTP_PASSWORD env fallback automatically.
	smtpEnv := email.SMTPEnvVars{
		Host:       smtpHost,
		Port:       smtpPort,
		From:       smtpFrom,
		Username:   smtpUsername,
		Encryption: "starttls", // safe default; operator overrides via admin UI
	}
	mailer := email.NewSMTPMailer(configSvc, secretProvider, smtpEnv)
	if !mailer.IsConfigured() {
		log.Printf("server: WARN: SMTP is not configured; email features will be unavailable")
	}

	// --- Setup module wiring (REQ-SETUP-001..011, REQ-SYS-071) ---
	// Constructed before the router so setupHandler is available when routes are
	// registered. auditRepo is constructed below alongside the user module; we
	// construct it here early so setup can share the same instance.
	setupAuditRepo := audit.NewRepository(pool)
	setupRepo := setup.NewRepository(pool)
	setupSvc := setup.NewService(pool, setupRepo, setupAuditRepo)
	setupHandler := setup.NewHandler(setupSvc)

	// --- User module wiring ---
	userRepo := user.NewRepository(pool)
	auditRepo := setupAuditRepo // reuse the instance already created above
	// mailerAdapter wraps the email.Mailer so it satisfies the legacy
	// EmailTransport interface consumed by user.Service (REQ-USER-005).
	emailTransport := user.NewMailerAdapter(mailer)
	// AgentRunner implements user.AgentTerminator — cancels in-flight runs on account deletion.
	userSvc := user.NewService(pool, userRepo, auditRepo, emailTransport, passwordResetTTL, agentRunner)
	// Inject the Mailer directly so user.Service can send welcome emails with
	// full subject/body control (REQ-USER-010). The mailerAdapter covers only the
	// password-reset path; welcome emails need the Mailer.Send surface directly.
	// @{"req": ["REQ-USER-010", "REQ-USER-012"]}
	userSvc.SetMailer(mailer)
	userHandler := user.NewHandler(userSvc)
	// mustChangePwMW rejects requests from flagged accounts on all paths except
	// change-password, logout, and GET /users/me (REQ-AUTH-014, REQ-SYS-064).
	mustChangePwMW := auth.MustChangePasswordMiddleware(pool)

	// --- 2FA wiring (REQ-AUTH-015..REQ-AUTH-026) ---
	// TwoFactorService depends on auditRepo (constructed above via user module
	// wiring). Wire it here and inject into authSvc so the Login path can branch.
	// @{"req": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-017", "REQ-AUTH-018", "REQ-AUTH-019", "REQ-AUTH-020", "REQ-AUTH-026"]}
	twoFactorSvc := auth.NewTwoFactorService(authRepo, auditRepo, pool, configSvc, mailer)
	authSvc.SetTwoFactor(twoFactorSvc, configSvc)

	// --- Audit module wiring ---
	auditHandler := audit.NewHandler(auditRepo)

	// --- Course module wiring ---
	courseRepo := course.NewRepository(pool)
	courseSvc := course.NewService(courseRepo)
	courseHandler := course.NewHandler(courseSvc)
	// Inject the intake kickoff trigger. chair satisfies course.IntakeStarter via
	// Chair.StartIntake. SetIntakeStarter is called after both modules are wired
	// so that neither package imports the other (no circular dependency).
	// @{"req": ["REQ-AGENT-018"]}
	courseHandler.SetIntakeStarter(chair)

	// --- Image module wiring ---
	// imageRepo uses the plain pool; upload handler acquires a server-role conn
	// internally via db.AcquireServerConn (identical to the chat-message pattern).
	// @{"req": ["REQ-AGENT-023", "REQ-AGENT-024", "REQ-AGENT-025", "REQ-SUBMISSION-004", "REQ-SUBMISSION-005", "REQ-SUBMISSION-006"]}
	imageRepo := image.NewRepository(pool)

	// courseOwnedBy is the ownership predicate injected into the image handler.
	// It uses the request-scoped connection (carrying the student's GUCs) so that
	// the courses_student_policy is satisfied without a server-role bypass.
	// The closure mirrors the ownership check pattern used in submission/handler.go.
	courseOwnedBy := func(r *http.Request, courseID, studentID uuid.UUID) bool {
		c, err := courseRepo.GetCourseByID(r.Context(), courseID)
		if err != nil {
			return false
		}
		return c.StudentID == studentID
	}
	imageHandler := image.NewHandler(imageRepo, courseOwnedBy)

	// Inject the image repository into the agent handler so chat attachments
	// resolve to vision blocks (REQ-AGENT-023).
	agentHandler.SetImageRepository(imageRepo)

	// Inject the image repository into the agent runner so grading vision
	// blocks can be built from submission image_ids (REQ-AGENT-025).
	agentRunner.SetImageRepository(imageRepo)

	// --- Submission handler wiring (depends on courseRepo for ownership checks) ---
	submissionHandler := submission.NewHandler(submissionRepo, courseRepo, uploadsDir, configSvc)

	// Inject the image repository into the submission handler so image_ids
	// ownership is validated before inserting the submission row (REQ-SUBMISSION-005).
	submissionHandler.SetImageRepository(imageRepo)

	// --- Admin config handler ---
	// mailer is passed so the test-send endpoint (REQ-EMAIL-008) can send via
	// the configured Mailer and redact the password from any error response.
	adminConfigHandler := admin.NewConfigHandler(configSvc, auditRepo, pool, mailer)

	// --- Admin assignment handler (REQ-ASSIGN-001..011, REQ-SYS-067, REQ-SYS-068) ---
	// chair satisfies admin.syllabusGenerator via Chair.GenerateSyllabusFromParams.
	// Uses the plain pool (admin session connection carries app.current_role='admin',
	// which satisfies courses_admin_policy for the GetAssignmentStudents query).
	// @{"req": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-006", "REQ-ASSIGN-007", "REQ-ASSIGN-009", "REQ-ASSIGN-010", "REQ-ASSIGN-011"]}
	assignmentRepo := admin.NewAssignmentRepository(pool)
	assignmentSvc := admin.NewAssignmentService(assignmentRepo, chair)
	assignmentHandler := admin.NewAssignmentHandler(assignmentSvc, auditRepo, pool)

	// --- Admin secrets handler (REQ-ADMIN-005..008, REQ-SECURITY-006..008) ---
	// Mounted under the same admin role + CSRF group as adminConfigHandler.
	// @{"req": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-ADMIN-007", "REQ-ADMIN-008", "REQ-SECURITY-008"]}
	secretsHandler := admin.NewSecretsHandler(secretProvider, auditRepo, pool)

	// --- Content module wiring ---
	contentRepo := content.NewContentRepository(pool)
	contentHandler := content.NewContentHandler(contentRepo, courseSvc)

	// --- Notify module wiring ---
	notifyRepo := notify.NewRepository(pool)
	notifyHandler := notify.NewNotifyHandler(notifyRepo)
	// @{"req": ["REQ-SYS-035", "REQ-SYS-043"]}
	// Start the background retention worker that purges aged notifications.
	// configSvc satisfies the GetInt64 interface the worker requires.
	notifyRepo.StartRetentionWorker(ctx, configSvc)

	// --- Router ---
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", infra.NewHealthHandler(pool, uploadsDir))

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			authHandler.Routes(r)
			// @{"req": ["REQ-AUTH-015", "REQ-AUTH-016", "REQ-AUTH-017", "REQ-AUTH-026"]}
			// 2FA verify and resend are pre-session endpoints: the pending token (from
			// the phase-1 login response body) is the credential. They must be mounted
			// outside the auth middleware group so they are reachable before a full
			// session exists. The handler itself returns 404 when 2FA is globally off.
			authHandler.TwoFARoutes(r)
		})

		// @{"req": ["REQ-SETUP-001", "REQ-SETUP-003", "REQ-SETUP-011", "REQ-SYS-071"]}
		// Setup routes are public: no auth, no CSRF. They must remain reachable on
		// a fresh install before any user account exists. Mounted alongside /auth
		// and /password-reset, outside every middleware group that carries
		// authentication or CSRF middleware.
		r.Route("/setup", func(r chi.Router) {
			setupHandler.Routes(r)
		})

		// @{"req": ["REQ-USER-005", "REQ-USER-006"]}
		// --- Public password-reset routes (no authentication required) ---
		r.Route("/password-reset", func(r chi.Router) {
			userHandler.PublicRoutes(r)
		})

		// @{"req": ["REQ-AUTH-012", "REQ-FEAUTH-169"]}
		// GET /auth/session is registered under authOnlyMW (no consent gate) so the
		// frontend can call it before consent has been accepted. The consent field
		// in the response tells the router whether to redirect to /consent.
		r.Group(func(r chi.Router) {
			r.Use(authOnlyMW)
			r.Get("/auth/session", authHandler.GetSession)
		})

		// @{"req": ["REQ-SECURITY-005"]}
		// The consent endpoint uses authOnlyMW (no consent gate) so that a student
		// who has not yet accepted consent can POST to /consent without being blocked
		// by the gate they are trying to satisfy.
		r.Group(func(r chi.Router) {
			r.Use(authOnlyMW)
			r.Use(security.CSRFMiddleware)
			r.Route("/consent", func(r chi.Router) {
				userHandler.StudentRoutes(r)
			})
		})

		// @{"req": ["REQ-SECURITY-004"]}
		// All other /api/v1/* routes require a valid session, CSRF protection, and
		// accepted consent (enforced by authMW's consent gate).
		r.Group(func(r chi.Router) {
			r.Use(authMW)
			r.Use(security.CSRFMiddleware)
			// @{"req": ["REQ-AUTH-014", "REQ-SYS-064"]}
			// mustChangePwMW enforces that flagged (must_change_password=true) sessions
			// can only reach change-password, logout, and GET /users/me. All other routes
			// return 403 MUST_CHANGE_PASSWORD. The exempt-path check is exact-path
			// based and lives inside the middleware (see auth/middleware.go).
			r.Use(mustChangePwMW)

			// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-007", "REQ-USER-014", "REQ-USER-015", "REQ-USER-016"]}
			// --- User routes ---
			r.Route("/users", func(r chi.Router) {
				// GET /users/me and POST /users/me/password — any authenticated user.
				userHandler.MeRoutes(r)
				r.Group(func(r chi.Router) {
					r.Use(auth.RequireRole("admin"))
					userHandler.AdminRoutes(r)
				})
			})

			// @{"req": ["REQ-AUDIT-001", "REQ-AUDIT-002"]}
			// --- Audit routes (require admin role) ---
			r.Route("/audit", func(r chi.Router) {
				r.Group(func(r chi.Router) {
					r.Use(auth.RequireRole("admin"))
					auditHandler.Routes(r)
				})
			})

			// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-007", "REQ-COURSE-008", "REQ-AGENT-001", "REQ-AGENT-006", "REQ-AGENT-015", "REQ-CONTENT-001", "REQ-CONTENT-002", "REQ-CONTENT-003", "REQ-CONTENT-004", "REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-003", "REQ-GRADE-004", "REQ-GRADE-005", "REQ-AGENT-023", "REQ-AGENT-024", "REQ-AGENT-025"]}
			// --- Course, agent, content, submission, and grade routes share the /courses prefix.
			r.Route("/courses", func(r chi.Router) {
				// Top-level course CRUD (list, create, etc.)
				courseHandler.Routes(r)
				// Per-course sub-routes.
				r.Route("/{id}", func(r chi.Router) {
					// @{"req": ["REQ-AGENT-001", "REQ-AGENT-006", "REQ-AGENT-015"]}
					agentHandler.Routes(r)
					// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-002", "REQ-CONTENT-003", "REQ-CONTENT-004"]}
					r.Route("/content", func(r chi.Router) {
						contentHandler.Routes(r)
					})
					// @{"req": ["REQ-CONTENT-001"]}
					// GET /courses/{id}/sections — section list for nav and hub views.
					r.Route("/sections", func(r chi.Router) {
						contentHandler.SectionListRoutes(r)
					})
					// @{"req": ["REQ-GRADE-003", "REQ-GRADE-004"]}
					// Course-level grade summary for the requesting student.
					r.Route("/grade", func(r chi.Router) {
						gradeHandler.CourseRoutes(r)
					})
					// @{"req": ["REQ-SUBMISSION-001", "REQ-SUBMISSION-002", "REQ-SUBMISSION-003", "REQ-GRADE-001", "REQ-GRADE-002", "REQ-GRADE-005"]}
					r.Route("/homework/{homeworkId}", func(r chi.Router) {
						// Submission upload and retrieval.
						submissionHandler.Routes(r)
						// Per-homework grade read.
						r.Route("/grade", func(r chi.Router) {
							gradeHandler.HomeworkRoutes(r)
						})
					})
					// @{"req": ["REQ-AGENT-023", "REQ-AGENT-024"]}
					// POST /courses/{id}/images — image upload (auth + CSRF already applied
					// by the enclosing group; ownership is checked inside the handler).
					imageHandler.UploadRoutes(r)
				})
			})

			// @{"req": ["REQ-AGENT-025"]}
			// GET /images/{imageId} — image retrieval. Mounted outside /courses because
			// the URL carries only the image UUID, not the course. Auth is enforced by
			// the enclosing group; CSRF is not required for GET (safe method).
			imageHandler.ServeRoutes(r)

			// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
			// --- Notification routes ---
			r.Route("/notifications", func(r chi.Router) {
				notifyHandler.Routes(r)
			})

			// @{"req": ["REQ-BADGE-001", "REQ-BADGE-002", "REQ-BADGE-003"]}
			badgeHandler.Routes(r)

			// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003"]}
			// --- Admin config routes ---
			r.Route("/admin/config", func(r chi.Router) {
				r.Group(func(r chi.Router) {
					r.Use(auth.RequireRole("admin"))
					adminConfigHandler.Routes(r)
				})
			})

			// @{"req": ["REQ-AUTH-019"]}
			// Admin per-user 2FA reset endpoint. Requires admin role (enforced here).
			// DELETE is used because the action is idempotent removal of pending state.
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole("admin"))
				r.Delete("/admin/users/{id}/2fa/reset", authHandler.ResetUserTwoFactor)
			})

			// @{"req": ["REQ-ADMIN-005", "REQ-ADMIN-006", "REQ-ADMIN-007", "REQ-ADMIN-008", "REQ-SECURITY-008"]}
			// --- Admin secrets routes ---
			// CSRF is already enforced by the enclosing group; RequireRole("admin")
			// is applied here so only admins can read/write managed secrets.
			r.Route("/admin/secrets", func(r chi.Router) {
				r.Group(func(r chi.Router) {
					r.Use(auth.RequireRole("admin"))
					secretsHandler.Routes(r)
				})
			})

			// @{"req": ["REQ-ASSIGN-001", "REQ-ASSIGN-002", "REQ-ASSIGN-003", "REQ-ASSIGN-004", "REQ-ASSIGN-005", "REQ-ASSIGN-006", "REQ-ASSIGN-007", "REQ-ASSIGN-009", "REQ-ASSIGN-010", "REQ-ASSIGN-011", "REQ-SYS-067", "REQ-SYS-068"]}
			// --- Admin assignment routes ---
			// CSRF is enforced by the enclosing group; RequireRole("admin") gates
			// all assignment endpoints so students cannot reach this surface.
			r.Route("/admin/assignments", func(r chi.Router) {
				r.Group(func(r chi.Router) {
					r.Use(auth.RequireRole("admin"))
					assignmentHandler.Routes(r)
				})
			})

			// @{"req": ["REQ-PROFILE-004", "REQ-PROFILE-011", "REQ-PROFILE-012", "REQ-PROFILE-014", "REQ-PROFILE-015", "REQ-PROFILE-016"]}
			// --- Profile and onboarding routes ---
			// Mounted under /profile; RequireRole("student") gives admins a clean
			// 403 instead of an RLS-violation 500 (learning_profiles student-write
			// policy rejects non-student roles). Matches the test stack in
			// profile_http_integration_test.go which also applies RequireRole.
			r.Route("/profile", func(r chi.Router) {
				r.Use(auth.RequireRole("student"))
				profileHandler.Routes(r)
			})
		})
	})

	// --- TLS / transport mode ---
	// @{"req": ["REQ-INFRA-003", "REQ-INFRA-004", "REQ-INFRA-005", "REQ-INFRA-006", "REQ-INFRA-007", "REQ-INFRA-008"]}
	tlsResult, err := infra.BuildTLSConfig(tlsCertFile, tlsKeyFile, acmeDomain, acmeCacheDir, behindProxy)
	if err != nil {
		log.Fatalf("server: build TLS config: %v", err)
	}

	// Block until SIGTERM or SIGINT arrives, then drain all active servers.
	// @{"req": ["REQ-INFRA-002"]}
	if tlsResult.Mode == infra.ModeBehindProxy {
		// Proxy mode: the reverse proxy owns TLS; we serve the application
		// handler directly over plain HTTP.  We keep the same :8443 the HTTPS
		// listener would use so docker-compose port mappings and operator
		// muscle memory are mode-independent — only the scheme changes.  No
		// HTTPS or redirect listener is needed — the proxy handles that
		// boundary.
		httpServer := &http.Server{
			Addr:    ":8443",
			Handler: r,
		}
		go func() {
			log.Printf("server: listening on :8443 (plain HTTP; behind reverse proxy)")
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server: HTTP listener error: %v", err)
			}
		}()

		<-ctx.Done()
		stop()
		log.Printf("server: shutdown signal received, draining connections")

		shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutCancel()
		if err := httpServer.Shutdown(shutCtx); err != nil {
			log.Printf("server: HTTP shutdown error: %v", err)
		}
	} else {
		// TLS modes (operator-cert, acme, self-signed): HTTPS on :8443 plus the
		// HTTP handler on :80 for ACME challenges / redirects.
		httpsServer := &http.Server{
			Addr:      ":8443",
			Handler:   r,
			TLSConfig: tlsResult.TLSConfig,
		}
		httpServer := &http.Server{
			Addr:    ":80",
			Handler: tlsResult.HTTPHandler,
		}

		go func() {
			log.Printf("server: listening on :80 (HTTP redirect / ACME)")
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server: HTTP listener error: %v", err)
			}
		}()
		go func() {
			log.Printf("server: listening on :8443 (HTTPS)")
			if err := httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server: HTTPS listener error: %v", err)
			}
		}()

		<-ctx.Done()
		stop()
		log.Printf("server: shutdown signal received, draining connections")

		// Each server gets its own 30-second deadline so a slow HTTPS drain (e.g.
		// long-lived SSE streams) cannot starve the HTTP redirect listener.
		httpsCtx, httpsCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer httpsCancel()
		httpCtx, httpCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer httpCancel()

		if err := httpsServer.Shutdown(httpsCtx); err != nil {
			log.Printf("server: HTTPS shutdown error: %v", err)
		}
		if err := httpServer.Shutdown(httpCtx); err != nil {
			log.Printf("server: HTTP shutdown error: %v", err)
		}
	}
	log.Printf("server: shutdown complete")
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("server: required environment variable %s is not set", key)
	}
	return v
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parseDuration reads the named env var as either a plain integer (seconds) or
// a Go duration string (e.g. "15m", "24h"). Returns def when unset or invalid.
func parseDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		return time.Duration(secs) * time.Second
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("server: %s=%q is not a valid duration, using default %v", key, v, def)
		return def
	}
	return d
}

// runMigrations applies embedded SQL migration files in lexicographic order.
// Each file is executed via pgconn's simple query protocol so that multi-statement
// files (including BEGIN/COMMIT blocks) are executed atomically as written.
// Already-applied migrations are idempotent via the schema_migrations table.
func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		sql, err := migrations.Files.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		// Use pgconn (simple query protocol) to support multi-statement SQL.
		// pool.Exec uses the extended protocol which would only execute the first statement.
		if err := conn.Conn().PgConn().Exec(ctx, string(sql)).Close(); err != nil {
			return err
		}
		log.Printf("server: applied migration %s", entry.Name())
	}
	return nil
}
