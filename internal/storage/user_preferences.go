package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// UserPreferences represents user-specific settings
type UserPreferences struct {
	PreferredModel string `json:"preferred_model"`
	LastUpdated    int64  `json:"last_updated"`
}

// UserPreferencesManager manages user preferences with PostgreSQL persistence
type UserPreferencesManager struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewUserPreferencesManager creates a new user preferences manager with shared database connection
func NewUserPreferencesManager(dbURL string) *UserPreferencesManager {
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	// Use shared database connection
	db, err := GetDatabase(dbURL)
	if err != nil {
		log.Fatalf("Failed to get database connection: %v", err)
	}

	manager := &UserPreferencesManager{
		db: db,
	}

	return manager
}

// GetUserModel gets the preferred model for a user, returns default if not set
func (upm *UserPreferencesManager) GetUserModel(ctx context.Context, userID, defaultModel string) string {
	upm.mu.RLock()
	defer upm.mu.RUnlock()

	var preferredModel string
	err := upm.db.QueryRowContext(ctx, "SELECT preferred_model FROM user_preferences WHERE user_id = $1", userID).Scan(&preferredModel)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("Failed to query user preferences: %v", err)
		}
		return defaultModel
	}

	if preferredModel != "" {
		return preferredModel
	}
	return defaultModel
}

// SetUserModel sets the preferred model for a user
func (upm *UserPreferencesManager) SetUserModel(ctx context.Context, userID, model string) error {
	upm.mu.Lock()
	defer upm.mu.Unlock()

	// Use UPSERT to handle both new and existing users, preserving existing system_prompt
	_, err := upm.db.ExecContext(ctx, `
		INSERT INTO user_preferences (user_id, preferred_model, system_prompt, last_updated)
		VALUES ($1, $2, NULL, $3)
		ON CONFLICT(user_id) DO UPDATE SET
			preferred_model = EXCLUDED.preferred_model,
			last_updated = EXCLUDED.last_updated
	`, userID, model, time.Now().Unix())

	if err != nil {
		return fmt.Errorf("failed to save user preference: %w", err)
	}

	return nil
}

// GetUserSystemPrompt gets the custom system prompt for a user, returns empty string if not set
func (upm *UserPreferencesManager) GetUserSystemPrompt(ctx context.Context, userID string) string {
	upm.mu.RLock()
	defer upm.mu.RUnlock()

	var systemPrompt sql.NullString
	err := upm.db.QueryRowContext(ctx, "SELECT system_prompt FROM user_preferences WHERE user_id = $1", userID).Scan(&systemPrompt)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("Failed to query user system prompt: %v", err)
		}
		return ""
	}

	if systemPrompt.Valid {
		return systemPrompt.String
	}
	return ""
}

// SetUserSystemPrompt sets the custom system prompt for a user
func (upm *UserPreferencesManager) SetUserSystemPrompt(ctx context.Context, userID, prompt string) error {
	upm.mu.Lock()
	defer upm.mu.Unlock()

	// Use UPSERT to handle both new and existing users, preserving existing preferred_model
	_, err := upm.db.ExecContext(ctx, `
		INSERT INTO user_preferences (user_id, preferred_model, system_prompt, last_updated)
		VALUES ($1, '', $2, $3)
		ON CONFLICT(user_id) DO UPDATE SET
			system_prompt = EXCLUDED.system_prompt,
			last_updated = EXCLUDED.last_updated
	`, userID, prompt, time.Now().Unix())

	if err != nil {
		return fmt.Errorf("failed to save user system prompt: %w", err)
	}

	return nil
}

// ClearUserSystemPrompt clears the custom system prompt for a user
func (upm *UserPreferencesManager) ClearUserSystemPrompt(ctx context.Context, userID string) error {
	upm.mu.Lock()
	defer upm.mu.Unlock()

	_, err := upm.db.ExecContext(ctx, `
		UPDATE user_preferences
		SET system_prompt = NULL, last_updated = $1
		WHERE user_id = $2
	`, time.Now().Unix(), userID)

	if err != nil {
		return fmt.Errorf("failed to clear user system prompt: %w", err)
	}

	return nil
}


// Close closes the database connection
func (upm *UserPreferencesManager) Close() error {
	// Database connection is shared, don't close it here
	return nil
}
