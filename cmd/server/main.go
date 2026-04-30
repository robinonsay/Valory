package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/admin"
	"github.com/valory/valory/internal/audit"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/course"
	"github.com/valory/valory/internal/db"
	"github.com/valory/valory/internal/infra"
	"github.com/valory/valory/internal/security"
	"github.com/valory/valory/internal/user"
	"github.com/valory/valory/migrations"
)

func main() {
	ctx := context.Background()

	// --- Required environment variables ---
	databaseURL := mustEnv("DATABASE_URL")
	_ = mustEnv("ANTHROPIC_API_KEY") // validated at startup; consumed by Anthropic SDK calls
	uploadsDir := envOrDefault("UPLOADS_DIR", "/app/uploads")
	acmeDomain := os.Getenv("ACME_DOMAIN")
	acmeCacheDir := envOrDefault("ACME_CACHE_DIR", "/app/acme-cache")

	lockoutDuration := parseDuration("AUTH_LOCKOUT_DURATION", 15*time.Minute)
	sessionMaxDuration := parseDuration("AUTH_SESSION_MAX_DURATION", 24*time.Hour)
	inactivityPeriod := parseDuration("AUTH_INACTIVITY_PERIOD", 30*time.Minute)

	smtpHost := envOrDefault("SMTP_HOST", "")
	smtpPort, err := strconv.Atoi(envOrDefault("SMTP_PORT", "587"))
	if err != nil {
		log.Fatalf("server: SMTP_PORT is not a valid integer: %v", err)
	}
	smtpFrom := envOrDefault("SMTP_FROM", "")
	smtpPassword := envOrDefault("SMTP_PASSWORD", "")
	passwordResetTTL := parseDuration("PASSWORD_RESET_TTL", 1*time.Hour)

	// --- Database ---
	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("server: connect to database: %v", err)
	}
	defer pool.Close()

	if err := runMigrations(ctx, pool); err != nil {
		log.Fatalf("server: run migrations: %v", err)
	}

	// @{"req": ["REQ-SECURITY-005"]}
	// --- Config service (provides consent version) ---
	configSvc := admin.NewConfigService(pool)
	if err := configSvc.Load(ctx); err != nil {
		log.Fatalf("server: load config service: %v", err)
	}

	// --- Auth wiring ---
	authRepo := auth.NewRepository(pool)
	authSvc := auth.NewService(authRepo, lockoutDuration, sessionMaxDuration)
	authHandler := auth.NewHandler(authSvc)
	// authMW enforces session validity AND the consent gate (REQ-SECURITY-005).
	authMW := auth.NewAuthMiddleware(authRepo, pool, inactivityPeriod, configSvc)
	// authOnlyMW enforces session validity only — no consent gate. Used for the
	// /consent endpoint itself so students can accept consent without being blocked
	// by the gate they are trying to satisfy.
	authOnlyMW := auth.NewAuthMiddleware(authRepo, pool, inactivityPeriod, nil)

	// --- User module wiring ---
	userRepo := user.NewRepository(pool)
	auditRepo := audit.NewRepository(pool)
	emailTransport := user.NewEmailTransport(smtpHost, smtpPort, smtpFrom, smtpPassword, log.Writer())
	userSvc := user.NewService(pool, userRepo, auditRepo, emailTransport, passwordResetTTL, noopTerminator{})
	userHandler := user.NewHandler(userSvc)

	// --- Audit module wiring ---
	auditHandler := audit.NewHandler(auditRepo)

	// --- Course module wiring ---
	courseRepo := course.NewRepository(pool)
	courseSvc := course.NewService(courseRepo)
	courseHandler := course.NewHandler(courseSvc)

	// --- Admin config handler ---
	adminConfigHandler := admin.NewConfigHandler(configSvc, auditRepo, pool)

	// Warn if BRAVE_API_KEY is absent — required in Sprint 4 for web search.
	if os.Getenv("BRAVE_API_KEY") == "" {
		log.Printf("server: BRAVE_API_KEY is not set; web search will be unavailable in Sprint 4")
	}

	// --- Router ---
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", infra.NewHealthHandler(pool, uploadsDir))

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			authHandler.Routes(r)
		})

		// @{"req": ["REQ-USER-005", "REQ-USER-006"]}
		// --- Public password-reset routes (no authentication required) ---
		r.Route("/password-reset", func(r chi.Router) {
			userHandler.PublicRoutes(r)
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

			// @{"req": ["REQ-USER-001", "REQ-USER-002", "REQ-USER-003", "REQ-USER-007"]}
			// --- User admin routes (require admin role) ---
			r.Route("/users", func(r chi.Router) {
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

			// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-007", "REQ-COURSE-008"]}
			// --- Course routes (authenticated students and admins) ---
			r.Route("/courses", func(r chi.Router) {
				courseHandler.Routes(r)
			})

			// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003"]}
			// --- Admin config routes ---
			r.Route("/admin/config", func(r chi.Router) {
				r.Group(func(r chi.Router) {
					r.Use(auth.RequireRole("admin"))
					adminConfigHandler.Routes(r)
				})
			})
		})
	})

	// --- TLS ---
	tlsCfg, httpHandler, err := infra.BuildTLSConfig(acmeDomain, acmeCacheDir)
	if err != nil {
		log.Fatalf("server: build TLS config: %v", err)
	}

	httpsServer := &http.Server{
		Addr:      ":8443",
		Handler:   r,
		TLSConfig: tlsCfg,
	}

	httpServer := &http.Server{
		Addr:    ":80",
		Handler: httpHandler,
	}

	go func() {
		log.Printf("server: listening on :80 (HTTP redirect / ACME)")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: HTTP listener error: %v", err)
		}
	}()

	log.Printf("server: listening on :8443 (HTTPS)")
	log.Fatal(httpsServer.ListenAndServeTLS("", ""))
}

// @{"req": ["REQ-USER-007"]}
// noopTerminator is a no-op implementation of user.AgentTerminator for Sprint 2.
type noopTerminator struct{}

func (noopTerminator) TerminateStudentOperations(_ context.Context, _ uuid.UUID) error {
	return nil
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
