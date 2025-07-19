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

// ChartLibrary represents a Python chart library with installation metadata
type ChartLibrary struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	InstallDate  int64  `json:"install_date"`
	LastUsed     int64  `json:"last_used"`
	IsInstalled  bool   `json:"is_installed"`
	Dependencies string `json:"dependencies"` // JSON string of dependencies
}

// ChartLibraryManager manages chart libraries with PostgreSQL persistence
type ChartLibraryManager struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewChartLibraryManager creates a new chart library manager with shared database connection
func NewChartLibraryManager(dbURL string) *ChartLibraryManager {
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	// Use shared database connection
	db, err := GetDatabase(dbURL)
	if err != nil {
		log.Fatalf("Failed to get database connection: %v", err)
	}

	manager := &ChartLibraryManager{
		db: db,
	}

	return manager
}

// GetLibrary retrieves a chart library by name
func (clm *ChartLibraryManager) GetLibrary(ctx context.Context, name string) (*ChartLibrary, error) {
	clm.mu.RLock()
	defer clm.mu.RUnlock()

	var library ChartLibrary
	err := clm.db.QueryRowContext(ctx, `
		SELECT name, version, install_date, last_used, is_installed, dependencies
		FROM chart_libraries WHERE name = $1
	`, name).Scan(
		&library.Name,
		&library.Version,
		&library.InstallDate,
		&library.LastUsed,
		&library.IsInstalled,
		&library.Dependencies,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Library not found
		}
		return nil, fmt.Errorf("failed to query chart library: %w", err)
	}

	return &library, nil
}

// GetAllLibraries retrieves all chart libraries
func (clm *ChartLibraryManager) GetAllLibraries(ctx context.Context) ([]ChartLibrary, error) {
	clm.mu.RLock()
	defer clm.mu.RUnlock()

	rows, err := clm.db.QueryContext(ctx, `
		SELECT name, version, install_date, last_used, is_installed, dependencies
		FROM chart_libraries ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query chart libraries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var libraries []ChartLibrary
	for rows.Next() {
		var library ChartLibrary
		if err := rows.Scan(
			&library.Name,
			&library.Version,
			&library.InstallDate,
			&library.LastUsed,
			&library.IsInstalled,
			&library.Dependencies,
		); err != nil {
			return nil, fmt.Errorf("failed to scan library row: %w", err)
		}
		libraries = append(libraries, library)
	}

	return libraries, nil
}

// GetInstalledLibraries retrieves all installed chart libraries
func (clm *ChartLibraryManager) GetInstalledLibraries(ctx context.Context) ([]ChartLibrary, error) {
	clm.mu.RLock()
	defer clm.mu.RUnlock()

	rows, err := clm.db.QueryContext(ctx, `
		SELECT name, version, install_date, last_used, is_installed, dependencies
		FROM chart_libraries WHERE is_installed = true ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query installed chart libraries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var libraries []ChartLibrary
	for rows.Next() {
		var library ChartLibrary
		if err := rows.Scan(
			&library.Name,
			&library.Version,
			&library.InstallDate,
			&library.LastUsed,
			&library.IsInstalled,
			&library.Dependencies,
		); err != nil {
			return nil, fmt.Errorf("failed to scan library row: %w", err)
		}
		libraries = append(libraries, library)
	}

	return libraries, nil
}

// AddLibrary adds or updates a chart library
func (clm *ChartLibraryManager) AddLibrary(ctx context.Context, library *ChartLibrary) error {
	clm.mu.Lock()
	defer clm.mu.Unlock()

	// Use UPSERT to handle both new and existing libraries
	_, err := clm.db.ExecContext(ctx, `
		INSERT INTO chart_libraries (name, version, install_date, last_used, is_installed, dependencies)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT(name) DO UPDATE SET
			version = EXCLUDED.version,
			install_date = EXCLUDED.install_date,
			last_used = EXCLUDED.last_used,
			is_installed = EXCLUDED.is_installed,
			dependencies = EXCLUDED.dependencies
	`, library.Name, library.Version, library.InstallDate, library.LastUsed, library.IsInstalled, library.Dependencies)

	if err != nil {
		return fmt.Errorf("failed to add chart library: %w", err)
	}

	return nil
}

// MarkLibraryInstalled marks a library as installed
func (clm *ChartLibraryManager) MarkLibraryInstalled(ctx context.Context, name, version string) error {
	clm.mu.Lock()
	defer clm.mu.Unlock()

	now := time.Now().Unix()
	_, err := clm.db.ExecContext(ctx, `
		INSERT INTO chart_libraries (name, version, install_date, last_used, is_installed, dependencies)
		VALUES ($1, $2, $3, $4, true, '{}')
		ON CONFLICT(name) DO UPDATE SET
			version = EXCLUDED.version,
			install_date = EXCLUDED.install_date,
			last_used = EXCLUDED.last_used,
			is_installed = true
	`, name, version, now, now)

	if err != nil {
		return fmt.Errorf("failed to mark library as installed: %w", err)
	}

	return nil
}

// MarkLibraryUninstalled marks a library as uninstalled
func (clm *ChartLibraryManager) MarkLibraryUninstalled(ctx context.Context, name string) error {
	clm.mu.Lock()
	defer clm.mu.Unlock()

	_, err := clm.db.ExecContext(ctx, `
		UPDATE chart_libraries
		SET is_installed = false, last_used = $1
		WHERE name = $2
	`, time.Now().Unix(), name)

	if err != nil {
		return fmt.Errorf("failed to mark library as uninstalled: %w", err)
	}

	return nil
}

// UpdateLastUsed updates the last used timestamp for a library
func (clm *ChartLibraryManager) UpdateLastUsed(ctx context.Context, name string) error {
	clm.mu.Lock()
	defer clm.mu.Unlock()

	_, err := clm.db.ExecContext(ctx, `
		UPDATE chart_libraries
		SET last_used = $1
		WHERE name = $2
	`, time.Now().Unix(), name)

	if err != nil {
		return fmt.Errorf("failed to update last used timestamp: %w", err)
	}

	return nil
}

// IsLibraryInstalled checks if a library is marked as installed
func (clm *ChartLibraryManager) IsLibraryInstalled(ctx context.Context, name string) (bool, error) {
	clm.mu.RLock()
	defer clm.mu.RUnlock()

	var isInstalled bool
	err := clm.db.QueryRowContext(ctx, `
		SELECT is_installed FROM chart_libraries WHERE name = $1
	`, name).Scan(&isInstalled)

	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil // Library not found, assume not installed
		}
		return false, fmt.Errorf("failed to check library installation status: %w", err)
	}

	return isInstalled, nil
}

// RemoveLibrary removes a library from the database
func (clm *ChartLibraryManager) RemoveLibrary(ctx context.Context, name string) error {
	clm.mu.Lock()
	defer clm.mu.Unlock()

	_, err := clm.db.ExecContext(ctx, `DELETE FROM chart_libraries WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("failed to remove library: %w", err)
	}

	return nil
}

// GetLibraryStats returns statistics about chart libraries
func (clm *ChartLibraryManager) GetLibraryStats(ctx context.Context) (map[string]interface{}, error) {
	clm.mu.RLock()
	defer clm.mu.RUnlock()

	var totalLibraries, installedLibraries int
	var mostRecentInstall, oldestInstall int64

	// Get total and installed counts
	err := clm.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) as total,
			COUNT(CASE WHEN is_installed = true THEN 1 END) as installed
		FROM chart_libraries
	`).Scan(&totalLibraries, &installedLibraries)

	if err != nil {
		return nil, fmt.Errorf("failed to get library counts: %w", err)
	}

	// Get install date range
	err = clm.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(MAX(install_date), 0) as most_recent,
			COALESCE(MIN(install_date), 0) as oldest
		FROM chart_libraries WHERE is_installed = true
	`).Scan(&mostRecentInstall, &oldestInstall)

	if err != nil {
		return nil, fmt.Errorf("failed to get install date range: %w", err)
	}

	stats := map[string]interface{}{
		"total_libraries":     totalLibraries,
		"installed_libraries": installedLibraries,
		"most_recent_install": mostRecentInstall,
		"oldest_install":      oldestInstall,
	}

	return stats, nil
}


// Close closes the database connection
func (clm *ChartLibraryManager) Close() error {
	// Database connection is shared, don't close it here
	return nil
}
