package storage

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// APIKeyManager manages API key rotation and tracks bad keys
type APIKeyManager struct {
	db               *sql.DB
	mu               sync.RWMutex
	keyRotationIndex map[string]int // provider -> current key index
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

	manager := &APIKeyManager{
		db:               db,
		keyRotationIndex: make(map[string]int),
	}

	return manager
}

// GetNextAPIKey returns the next available API key for a provider
func (akm *APIKeyManager) GetNextAPIKey(provider string, availableKeys []string) (string, error) {
	if len(availableKeys) == 0 {
		return "", fmt.Errorf("no API keys available for provider %s", provider)
	}

	akm.mu.Lock()
	defer akm.mu.Unlock()

	// Get bad keys for this provider
	badKeys, err := akm.getBadKeys(provider)
	if err != nil {
		return "", fmt.Errorf("failed to get bad keys: %w", err)
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
		if err := akm.resetBadKeys(provider); err != nil {
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
func (akm *APIKeyManager) MarkKeyAsBad(provider, apiKey string, reason string) error {
	akm.mu.Lock()
	defer akm.mu.Unlock()

	_, err := akm.db.Exec(`
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

	log.Printf("Marked API key as bad for provider %s: %s", provider, reason)
	return nil
}

// getBadKeys returns all bad keys for a provider
func (akm *APIKeyManager) getBadKeys(provider string) ([]string, error) {
	rows, err := akm.db.Query(`
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
func (akm *APIKeyManager) resetBadKeys(provider string) error {
	_, err := akm.db.Exec(`
		DELETE FROM bad_api_keys WHERE provider = $1
	`, provider)
	return err
}

// ResetBadKeys is a public method to reset bad keys for a provider
func (akm *APIKeyManager) ResetBadKeys(provider string) error {
	akm.mu.Lock()
	defer akm.mu.Unlock()

	err := akm.resetBadKeys(provider)
	if err != nil {
		return fmt.Errorf("failed to reset bad keys for provider %s: %w", provider, err)
	}

	// Reset rotation index for this provider
	akm.keyRotationIndex[provider] = 0

	log.Printf("Reset bad API keys for provider: %s", provider)
	return nil
}


// GetBadKeyStats returns statistics about bad keys for monitoring
func (akm *APIKeyManager) GetBadKeyStats() (map[string]int, error) {
	akm.mu.RLock()
	defer akm.mu.RUnlock()

	rows, err := akm.db.Query(`
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
