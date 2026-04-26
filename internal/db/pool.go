package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: %w", err)
	}

	// Clear session-level GUCs when a connection is returned to the pool.
	// The auth middleware uses set_config('app.current_user_id', ..., false) to
	// bind the current user's identity to a connection for RLS evaluation. Without
	// this hook, a released connection would carry that identity into the next
	// request that acquires it from the pool.
	config.AfterRelease = func(conn *pgx.Conn) bool {
		_, err := conn.Exec(context.Background(),
			"SELECT set_config('app.current_user_id', '', false), set_config('app.current_role', '', false)",
		)
		// Return false to discard the connection if the reset fails, rather than
		// returning a connection with a potentially stale identity to the pool.
		return err == nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("db: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: %w", err)
	}

	return pool, nil
}
