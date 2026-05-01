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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/admin"
	"github.com/valory/valory/internal/agent"
	"github.com/valory/valory/internal/audit"
	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/content"
	"github.com/valory/valory/internal/course"
	"github.com/valory/valory/internal/db"
	"github.com/valory/valory/internal/infra"
	"github.com/valory/valory/internal/notify"
	"github.com/valory/valory/internal/security"
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
	anthropicAPIKey := mustEnv("ANTHROPIC_API_KEY") // validated at startup; consumed by ThrottledClient
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

	// Startup recovery: any agent_run left in 'running' state survived a crash.
	// Mark them failed so the polling loop can schedule fresh runs.
	// (No requirement currently covers crash recovery of stale runs; PM follow-up needed.)
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

	// --- Agent module wiring ---
	// Agent is wired before User so AgentRunner can be passed to UserService as
	// the terminator that cancels in-flight runs on account deletion (REQ-AGENT-013).
	braveAPIKey := envOrDefault("BRAVE_API_KEY", "")
	if braveAPIKey == "" {
		log.Printf("server: BRAVE_API_KEY is not set; web search grounding will be unavailable")
	}

	throttledClient := agent.NewThrottledClient(anthropicAPIKey, pool, configSvc)

	agentRepo := agent.NewAgentRepository(pool)
	chatRepo := agent.NewChatRepository(pool)
	chair := agent.NewChair(throttledClient, pool, agentRepo, chatRepo)
	professor := agent.NewProfessor(throttledClient, pool, agentRepo, braveAPIKey)
	reviewer := agent.NewReviewer(throttledClient, pool, agentRepo)
	agentRunner := agent.NewAgentRunner(pool, agentRepo, chair, professor, reviewer, configSvc)
	agentHandler := agent.NewAgentHandler(agentRunner, chair, chatRepo)

	// Start background polling goroutines (30s gen poll, 60s feedback poll).
	go agentRunner.Start(ctx)

	// --- User module wiring ---
	userRepo := user.NewRepository(pool)
	auditRepo := audit.NewRepository(pool)
	emailTransport := user.NewEmailTransport(smtpHost, smtpPort, smtpFrom, smtpPassword, log.Writer())
	// AgentRunner implements user.AgentTerminator — cancels in-flight runs on account deletion.
	userSvc := user.NewService(pool, userRepo, auditRepo, emailTransport, passwordResetTTL, agentRunner)
	userHandler := user.NewHandler(userSvc)

	// --- Audit module wiring ---
	auditHandler := audit.NewHandler(auditRepo)

	// --- Course module wiring ---
	courseRepo := course.NewRepository(pool)
	courseSvc := course.NewService(courseRepo)
	courseHandler := course.NewHandler(courseSvc)

	// --- Admin config handler ---
	adminConfigHandler := admin.NewConfigHandler(configSvc, auditRepo, pool)

	// --- Content module wiring ---
	contentRepo := content.NewContentRepository(pool)
	contentHandler := content.NewContentHandler(contentRepo)

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

			// @{"req": ["REQ-COURSE-001", "REQ-COURSE-002", "REQ-COURSE-003", "REQ-COURSE-004", "REQ-COURSE-005", "REQ-COURSE-006", "REQ-COURSE-007", "REQ-COURSE-008", "REQ-AGENT-001", "REQ-AGENT-006", "REQ-AGENT-015", "REQ-CONTENT-001", "REQ-CONTENT-002", "REQ-CONTENT-003", "REQ-CONTENT-004"]}
			// --- Course, agent, and content routes share the /courses prefix so that
			// chi builds a single tree branch and {id} parameters do not conflict.
			r.Route("/courses", func(r chi.Router) {
				// Top-level course CRUD (list, create, etc.)
				courseHandler.Routes(r)
				// Per-course sub-routes: agent SSE/chat and content delivery.
				r.Route("/{id}", func(r chi.Router) {
					// @{"req": ["REQ-AGENT-001", "REQ-AGENT-006", "REQ-AGENT-015"]}
					agentHandler.Routes(r)
					// @{"req": ["REQ-CONTENT-001", "REQ-CONTENT-002", "REQ-CONTENT-003", "REQ-CONTENT-004"]}
					r.Route("/content", func(r chi.Router) {
						contentHandler.Routes(r)
					})
				})
			})

			// @{"req": ["REQ-NOTIFY-001", "REQ-NOTIFY-002"]}
			// --- Notification routes ---
			r.Route("/notifications", func(r chi.Router) {
				notifyHandler.Routes(r)
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

	go func() {
		log.Printf("server: listening on :8443 (HTTPS)")
		if err := httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: HTTPS listener error: %v", err)
		}
	}()

	// Block until SIGTERM or SIGINT arrives, then drain both servers.
	// (No requirement currently covers graceful shutdown; PM follow-up needed.)
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
