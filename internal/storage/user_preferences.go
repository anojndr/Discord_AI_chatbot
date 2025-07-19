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
	db                      *sql.DB
	mu                      sync.RWMutex
	getUserModelStmt        *sql.Stmt
	setUserModelStmt        *sql.Stmt
	getUserSystemPromptStmt *sql.Stmt
	setUserSystemPromptStmt *sql.Stmt
	clearUserSystemPrompt   *sql.Stmt
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
	manager.prepareStatements()

	return manager
}

func (upm *UserPreferencesManager) prepareStatements() {
	var err error
	upm.getUserModelStmt, err = upm.db.PrepareContext(context.Background(), "SELECT preferred_model FROM user_preferences WHERE user_id = $1")
	if err != nil {
		log.Fatalf("Failed to prepare getUserModelStmt: %v", err)
	}
	upm.setUserModelStmt, err = upm.db.PrepareContext(context.Background(), `
		INSERT INTO user_preferences (user_id, preferred_model, system_prompt, last_updated)
		VALUES ($1, $2, NULL, $3)
		ON CONFLICT(user_id) DO UPDATE SET
			preferred_model = EXCLUDED.preferred_model,
			last_updated = EXCLUDED.last_updated
	`)
	if err != nil {
		log.Fatalf("Failed to prepare setUserModelStmt: %v", err)
	}
	upm.getUserSystemPromptStmt, err = upm.db.PrepareContext(context.Background(), "SELECT system_prompt FROM user_preferences WHERE user_id = $1")
	if err != nil {
		log.Fatalf("Failed to prepare getUserSystemPromptStmt: %v", err)
	}
	upm.setUserSystemPromptStmt, err = upm.db.PrepareContext(context.Background(), `
		INSERT INTO user_preferences (user_id, preferred_model, system_prompt, last_updated)
		VALUES ($1, '', $2, $3)
		ON CONFLICT(user_id) DO UPDATE SET
			system_prompt = EXCLUDED.system_prompt,
			last_updated = EXCLUDED.last_updated
	`)
	if err != nil {
		log.Fatalf("Failed to prepare setUserSystemPromptStmt: %v", err)
	}
	upm.clearUserSystemPrompt, err = upm.db.PrepareContext(context.Background(), `
		UPDATE user_preferences
		SET system_prompt = NULL, last_updated = $1
		WHERE user_id = $2
	`)
	if err != nil {
		log.Fatalf("Failed to prepare clearUserSystemPrompt: %v", err)
	}
}

// GetUserModel gets the preferred model for a user, returns default if not set
func (upm *UserPreferencesManager) GetUserModel(ctx context.Context, userID, defaultModel string) string {
	upm.mu.RLock()
	defer upm.mu.RUnlock()

	var preferredModel string
	err := upm.getUserModelStmt.QueryRowContext(ctx, userID).Scan(&preferredModel)
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
	_, err := upm.setUserModelStmt.ExecContext(ctx, userID, model, time.Now().Unix())

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
	err := upm.getUserSystemPromptStmt.QueryRowContext(ctx, userID).Scan(&systemPrompt)
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
	_, err := upm.setUserSystemPromptStmt.ExecContext(ctx, userID, prompt, time.Now().Unix())

	if err != nil {
		return fmt.Errorf("failed to save user system prompt: %w", err)
	}

	return nil
}

// ClearUserSystemPrompt clears the custom system prompt for a user
func (upm *UserPreferencesManager) ClearUserSystemPrompt(ctx context.Context, userID string) error {
	upm.mu.Lock()
	defer upm.mu.Unlock()

	_, err := upm.clearUserSystemPrompt.ExecContext(ctx, time.Now().Unix(), userID)

	if err != nil {
		return fmt.Errorf("failed to clear user system prompt: %w", err)
	}

	return nil
}


// Close closes the database connection
func (upm *UserPreferencesManager) Close() error {
	var err error
	if err = upm.getUserModelStmt.Close(); err != nil {
		log.Printf("Failed to close getUserModelStmt: %v", err)
	}
	if err = upm.setUserModelStmt.Close(); err != nil {
		log.Printf("Failed to close setUserModelStmt: %v", err)
	}
	if err = upm.getUserSystemPromptStmt.Close(); err != nil {
		log.Printf("Failed to close getUserSystemPromptStmt: %v", err)
	}
	if err = upm.setUserSystemPromptStmt.Close(); err != nil {
		log.Printf("Failed to close setUserSystemPromptStmt: %v", err)
	}
	if err = upm.clearUserSystemPrompt.Close(); err != nil {
		log.Printf("Failed to close clearUserSystemPrompt: %v", err)
	}
	return err
}
