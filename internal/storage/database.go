package storage

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// DatabasePool manages a shared database connection pool for all storage components
type DatabasePool struct {
	db    *sql.DB
	mutex sync.RWMutex
	once  sync.Once
}

var globalPool *DatabasePool

// GetDatabase returns a shared database connection, creating it if necessary
func GetDatabase(dbURL string) (*sql.DB, error) {
	if globalPool == nil {
		globalPool = &DatabasePool{}
	}

	globalPool.mutex.RLock()
	if globalPool.db != nil {
		defer globalPool.mutex.RUnlock()
		return globalPool.db, nil
	}
	globalPool.mutex.RUnlock()

	// Initialize database connection once
	var initErr error
	globalPool.once.Do(func() {
		globalPool.mutex.Lock()
		defer globalPool.mutex.Unlock()

		db, err := sql.Open("pgx", dbURL)
		if err != nil {
			initErr = err
			return
		}

		// Configure connection pool for better performance
		// Tune connection pool for better concurrency
		// Set MaxOpenConns to be a multiple of your core/thread count
		db.SetMaxOpenConns(16) // e.g., 2 * 8 threads
		db.SetMaxIdleConns(8)  // e.g., 1 * 8 threads
		db.SetConnMaxLifetime(5 * time.Minute)
		db.SetConnMaxIdleTime(2 * time.Minute)

		// Test connectivity once with a timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			initErr = fmt.Errorf("database ping failed: %w", err)
			return
		}

		globalPool.db = db
	})

	return globalPool.db, initErr
}

// CloseDatabase closes the shared database connection
func CloseDatabase() error {
	if globalPool != nil && globalPool.db != nil {
		globalPool.mutex.Lock()
		defer globalPool.mutex.Unlock()
		return globalPool.db.Close()
	}
	return nil
}

// InitializeAllTables creates all required tables in a single transaction for faster startup
func InitializeAllTables(ctx context.Context, dbURL string) error {
	db, err := GetDatabase(dbURL)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Create all tables in a single transaction - matching exact schemas from individual components
	tables := []string{
		// Bad API keys table (from api_key_manager.go)
		`CREATE TABLE IF NOT EXISTS bad_api_keys (
			provider TEXT NOT NULL,
			api_key TEXT NOT NULL,
			reason TEXT NOT NULL,
			marked_at BIGINT NOT NULL,
			PRIMARY KEY (provider, api_key)
		)`,

		// User preferences table (from user_preferences.go)
		`CREATE TABLE IF NOT EXISTS user_preferences (
			user_id TEXT PRIMARY KEY,
			preferred_model TEXT NOT NULL,
			system_prompt TEXT,
			last_updated BIGINT NOT NULL
		)`,

		// Chart libraries table (from chart_library_manager.go)
		`CREATE TABLE IF NOT EXISTS chart_libraries (
			name TEXT PRIMARY KEY,
			version TEXT NOT NULL,
			install_date BIGINT NOT NULL,
			last_used BIGINT NOT NULL,
			is_installed BOOLEAN NOT NULL DEFAULT false,
			dependencies TEXT NOT NULL DEFAULT '{}'
		)`,

		// Message nodes cache table (from message_cache.go)
		`CREATE TABLE IF NOT EXISTS message_nodes (
			message_id TEXT PRIMARY KEY,
			data JSONB NOT NULL,
			updated_at BIGINT NOT NULL
		)`,
	}

	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, table); err != nil {
			return err
		}
	}

	// Create indexes (from chart_library_manager.go)
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_chart_libraries_installed ON chart_libraries(is_installed)`,
		`CREATE INDEX IF NOT EXISTS idx_chart_libraries_last_used ON chart_libraries(last_used)`,
		`CREATE INDEX IF NOT EXISTS idx_user_preferences_user_id ON user_preferences(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_bad_api_keys_provider ON bad_api_keys(provider)`,
	}

	for _, index := range indexes {
		if _, err := tx.ExecContext(ctx, index); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// DropAllTables drops all tables from the database
func DropAllTables(ctx context.Context, dbURL string) error {
	db, err := GetDatabase(dbURL)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	tables := []string{
		"message_nodes",
		"chart_libraries",
		"user_preferences",
		"bad_api_keys",
	}

	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE"); err != nil {
			return err
		}
	}

	return tx.Commit()
}