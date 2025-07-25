package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// APIKeyManager manages API key rotation and tracks bad keys
type APIKeyManager struct {
	db               *sql.DB
	mu               sync.RWMutex
	keyRotationIndex map[string]int            // provider -> current key index
	badKeyCache      *lru.Cache[string, []string] // provider -> []badKeys
}

// NewAPIKeyManager creates a new API key manager with shared database connection
func NewAPIKeyManager(dbURL string) *APIKeyManager {
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	// Use shared database connection
	db, err := GetDatabase(dbURL)
	if err != nil {
		log.Fatalf("Failed to get database connection: %v", err)
	}

	cache, _ := lru.New[string, []string](128) // Cache up to 128 providers
	manager := &APIKeyManager{
		db:               db,
		keyRotationIndex: make(map[string]int),
		badKeyCache:      cache,
	}

	return manager
}

// GetNextAPIKey returns the next available API key for a provider
func (akm *APIKeyManager) GetNextAPIKey(ctx context.Context, provider string, availableKeys []string) (string, error) {
	if len(availableKeys) == 0 {
		return "", fmt.Errorf("no API keys available for provider %s", provider)
	}

	akm.mu.Lock()
	defer akm.mu.Unlock()

	// Check cache first
	badKeys, ok := akm.badKeyCache.Get(provider)
	if !ok {
		// Cache miss: query DB
		dbBadKeys, err := akm.getBadKeys(ctx, provider)
		if err != nil {
			return "", fmt.Errorf("failed to get bad keys: %w", err)
		}
		akm.badKeyCache.Add(provider, dbBadKeys)
		badKeys = dbBadKeys
	}

	// Filter out bad keys
	var goodKeys []string
	for _, key := range availableKeys {
		if !contains(badKeys, key) {
			goodKeys = append(goodKeys, key)
		}
	}

	// If all keys are bad, reset the bad keys and try again
	if len(goodKeys) == 0 {
		log.Printf("All API keys for provider %s are marked as bad, resetting...", provider)
		if err := akm.resetBadKeys(ctx, provider); err != nil {
			return "", fmt.Errorf("failed to reset bad keys: %w", err)
		}
		goodKeys = availableKeys
		akm.keyRotationIndex[provider] = 0
	}

	// Get current rotation index for this provider
	currentIndex, exists := akm.keyRotationIndex[provider]
	if !exists || currentIndex >= len(goodKeys) {
		currentIndex = 0
	}

	// Get the key at current index
	selectedKey := goodKeys[currentIndex]

	// Increment index for next time (with wrap-around)
	akm.keyRotationIndex[provider] = (currentIndex + 1) % len(goodKeys)

	return selectedKey, nil
}

// MarkKeyAsBad marks an API key as bad so it won't be used again
func (akm *APIKeyManager) MarkKeyAsBad(ctx context.Context, provider, apiKey string, reason string) error {
	akm.mu.Lock()
	defer akm.mu.Unlock()

	_, err := akm.db.ExecContext(ctx, `
		INSERT INTO bad_api_keys
		(provider, api_key, reason, marked_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, api_key) DO UPDATE SET
			reason = EXCLUDED.reason,
			marked_at = EXCLUDED.marked_at
	`, provider, apiKey, reason, time.Now().Unix())

	if err != nil {
		return fmt.Errorf("failed to mark API key as bad: %w", err)
	}

	akm.badKeyCache.Remove(provider) // Simple invalidation
	log.Printf("Marked API key as bad for provider %s: %s", provider, reason)
	return nil
}

// getBadKeys returns all bad keys for a provider
func (akm *APIKeyManager) getBadKeys(ctx context.Context, provider string) ([]string, error) {
	rows, err := akm.db.QueryContext(ctx, `
		SELECT api_key FROM bad_api_keys
		WHERE provider = $1
	`, provider)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("Failed to close rows: %v", err)
		}
	}()

	var badKeys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		badKeys = append(badKeys, key)
	}

	return badKeys, nil
}

// resetBadKeys removes all bad key entries for a provider
func (akm *APIKeyManager) resetBadKeys(ctx context.Context, provider string) error {
	_, err := akm.db.ExecContext(ctx, `
		DELETE FROM bad_api_keys WHERE provider = $1
	`, provider)
	if err != nil {
		return err
	}
	akm.badKeyCache.Remove(provider)
	return nil
}

// ResetBadKeys is a public method to reset bad keys for a provider
func (akm *APIKeyManager) ResetBadKeys(ctx context.Context, provider string) error {
	akm.mu.Lock()
	defer akm.mu.Unlock()

	err := akm.resetBadKeys(ctx, provider)
	if err != nil {
		return fmt.Errorf("failed to reset bad keys for provider %s: %w", provider, err)
	}

	// Reset rotation index for this provider
	akm.keyRotationIndex[provider] = 0

	log.Printf("Reset bad API keys for provider: %s", provider)
	return nil
}


// GetBadKeyStats returns statistics about bad keys for monitoring
func (akm *APIKeyManager) GetBadKeyStats(ctx context.Context) (map[string]int, error) {
	akm.mu.RLock()
	defer akm.mu.RUnlock()

	rows, err := akm.db.QueryContext(ctx, `
		SELECT provider, COUNT(*) as count
		FROM bad_api_keys
		GROUP BY provider
	`)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("Failed to close rows: %v", err)
		}
	}()

	stats := make(map[string]int)
	for rows.Next() {
		var provider string
		var count int
		if err := rows.Scan(&provider, &count); err != nil {
			return nil, err
		}
		stats[provider] = count
	}

	return stats, nil
}

// Close does nothing since we use a shared database connection
func (akm *APIKeyManager) Close() error {
	// Database connection is shared, don't close it here
	return nil
}

// Helper function to check if slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
