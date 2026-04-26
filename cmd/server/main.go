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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/valory/valory/internal/auth"
	"github.com/valory/valory/internal/db"
	"github.com/valory/valory/internal/infra"
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

	// --- Database ---
	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("server: connect to database: %v", err)
	}
	defer pool.Close()

	if err := runMigrations(ctx, pool); err != nil {
		log.Fatalf("server: run migrations: %v", err)
	}

	// --- Auth wiring ---
	repo := auth.NewRepository(pool)
	svc := auth.NewService(repo, lockoutDuration, sessionMaxDuration)
	authHandler := auth.NewHandler(svc)
	authMW := auth.NewAuthMiddleware(repo, pool, inactivityPeriod)

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

		// All other /api/v1/* routes require a valid session.
		r.Group(func(r chi.Router) {
			r.Use(authMW)
			// Additional module routes are registered here as modules are added.
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
