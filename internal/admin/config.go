package admin

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// @{"req": ["REQ-SECURITY-005"]}
type ConsentVersionProvider interface {
	GetString(key string) string
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-GRADE-002", "REQ-GRADE-003"]}
type ConfigService struct {
	mu     sync.RWMutex
	values map[string]string
	pool   *pgxpool.Pool
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003"]}
func NewConfigService(pool *pgxpool.Pool) *ConfigService {
	return &ConfigService{
		values: make(map[string]string),
		pool:   pool,
	}
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003"]}
func (c *ConfigService) Load(ctx context.Context) error {
	rows, err := c.pool.Query(ctx, "SELECT key, value FROM system_config")
	if err != nil {
		return fmt.Errorf("admin: failed to query system_config: %w", err)
	}
	defer rows.Close()

	newValues := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("admin: failed to scan row: %w", err)
		}
		newValues[key] = value
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("admin: row iteration error: %w", err)
	}

	c.mu.Lock()
	c.values = newValues
	c.mu.Unlock()

	return nil
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003", "REQ-GRADE-002", "REQ-GRADE-003"]}
func (c *ConfigService) GetString(key string) string {
	c.mu.RLock()
	value := c.values[key]
	c.mu.RUnlock()
	return value
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003"]}
func (c *ConfigService) GetInt64(key string) int64 {
	c.mu.RLock()
	value := c.values[key]
	c.mu.RUnlock()

	if value == "" {
		return 0
	}

	num, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}

	return num
}

// @{"req": ["REQ-ADMIN-001", "REQ-ADMIN-002", "REQ-ADMIN-003"]}
func (c *ConfigService) GetFloat64(key string) float64 {
	c.mu.RLock()
	value := c.values[key]
	c.mu.RUnlock()

	if value == "" {
		return 0.0
	}

	num, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0.0
	}

	return num
}
