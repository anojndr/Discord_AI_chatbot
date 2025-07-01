package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"DiscordAIChatbot/internal/messaging"
)

// MessageNodeCache provides simple persistence for processed MsgNode objects.
// The data is stored as JSONB so the schema can evolve without migrations.
// Only the fields needed to skip expensive re-processing are kept.
// ParentMsg and mutex fields are deliberately omitted.

type MessageNodeCache struct {
	db *sql.DB
	mu sync.RWMutex
}

// msgNodeSerializable mirrors messaging.MsgNode but excludes unmarshalable fields.
// We dont embed RWMutex and ParentMsg to keep JSON lean.

type msgNodeSerializable struct {
	Text               string                            `json:"text"`
	Images             []messaging.ImageContent          `json:"images"`
	GeneratedImages    []messaging.GeneratedImageContent `json:"generated_images"`
	Role               string                            `json:"role"`
	UserID             string                            `json:"user_id"`
	HasBadAttachments  bool                              `json:"has_bad_attachments"`
	FetchParentFailed  bool                              `json:"fetch_parent_failed"`
	WebSearchPerformed bool                              `json:"web_search_performed"`
	SearchResultCount  int                               `json:"search_result_count"`
}

// NewMessageNodeCache initialises the cache with shared database connection.
func NewMessageNodeCache(dbURL string) *MessageNodeCache {
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required for message node cache")
	}

	// Use shared database connection
	db, err := GetDatabase(dbURL)
	if err != nil {
		log.Fatalf("Failed to get database connection: %v", err)
	}

	cache := &MessageNodeCache{db: db}
	return cache
}

// SaveNode upserts a processed node into the cache.
func (c *MessageNodeCache) SaveNode(messageID string, node *messaging.MsgNode) error {
	if node == nil || messageID == "" {
		return nil
	}

	c.mu.RLock()
	serial := msgNodeSerializable{
		Text:               node.GetText(),
		Images:             node.GetImages(),
		GeneratedImages:    node.GetGeneratedImages(),
		Role:               node.Role,
		UserID:             node.UserID,
		HasBadAttachments:  node.HasBadAttachments,
		FetchParentFailed:  node.FetchParentFailed,
		WebSearchPerformed: node.WebSearchPerformed,
		SearchResultCount:  node.SearchResultCount,
	}
	c.mu.RUnlock()

	data, err := json.Marshal(serial)
	if err != nil {
		return fmt.Errorf("failed to marshal node: %w", err)
	}

	_, err = c.db.Exec(`
        INSERT INTO message_nodes (message_id, data, updated_at)
        VALUES ($1, $2, $3)
        ON CONFLICT(message_id) DO UPDATE SET
            data = EXCLUDED.data,
            updated_at = EXCLUDED.updated_at
    `, messageID, data, time.Now().Unix())

	return err
}

// GetNode retrieves a cached node. Returns (nil, nil) if not found.
func (c *MessageNodeCache) GetNode(messageID string) (*messaging.MsgNode, error) {
	var rawData []byte
	err := c.db.QueryRow(`SELECT data FROM message_nodes WHERE message_id = $1`, messageID).Scan(&rawData)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // cache miss
		}
		return nil, fmt.Errorf("query error: %w", err)
	}

	var serial msgNodeSerializable
	if err := json.Unmarshal(rawData, &serial); err != nil {
		return nil, fmt.Errorf("unmarshal error: %w", err)
	}

	node := messaging.NewMsgNode()
	node.SetText(serial.Text)
	node.SetImages(serial.Images)
	node.SetGeneratedImages(serial.GeneratedImages)
	node.Role = serial.Role
	node.UserID = serial.UserID
	node.HasBadAttachments = serial.HasBadAttachments
	node.FetchParentFailed = serial.FetchParentFailed
	node.WebSearchPerformed = serial.WebSearchPerformed
	node.SearchResultCount = serial.SearchResultCount

	return node, nil
}


// Close closes the underlying DB connection.
func (c *MessageNodeCache) Close() error {
	// Database connection is shared, don't close it here
	return nil
}
