package storage

import (
	"context"
	"database/sql"
	json "github.com/json-iterator/go"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"DiscordAIChatbot/internal/messaging"
)

const (
	batchSize    = 50
	batchTimeout = 5 * time.Second
)

// MessageNodeCache provides simple persistence for processed MsgNode objects.
// The data is stored as JSONB so the schema can evolve without migrations.
// Only the fields needed to skip expensive re-processing are kept.
// ParentMsg and mutex fields are deliberately omitted.
type MessageNodeCache struct {
	db        *sql.DB
	mu        sync.RWMutex
	nodeQueue chan *messaging.ProcessedNode
	wg        sync.WaitGroup
}

// msgNodeSerializable mirrors messaging.MsgNode but excludes unmarshalable fields.
// We don t embed RWMutex and ParentMsg to keep JSON lean.

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

	queue := make(chan *messaging.ProcessedNode, batchSize*2)
	cache := &MessageNodeCache{
		db:        db,
		nodeQueue: queue,
	}

	cache.wg.Add(1)
	go cache.batchWorker()

	return cache
}

func (c *MessageNodeCache) batchWorker() {
	defer c.wg.Done()
	batch := make([]*messaging.ProcessedNode, 0, batchSize)
	ticker := time.NewTicker(batchTimeout)
	defer ticker.Stop()

	for {
		select {
		case node, ok := <-c.nodeQueue:
			if !ok {
				// Channel closed, save remaining batch and exit
				if len(batch) > 0 {
					if err := c.saveNodes(context.Background(), batch); err != nil {
						log.Printf("Error saving final batch: %v", err)
					}
				}
				return
			}
			batch = append(batch, node)
			if len(batch) >= batchSize {
				if err := c.saveNodes(context.Background(), batch); err != nil {
					log.Printf("Error saving batch: %v", err)
				}
				batch = make([]*messaging.ProcessedNode, 0, batchSize) // Reset batch
			}
		case <-ticker.C:
			// Timeout, save whatever is in the batch
			if len(batch) > 0 {
				if err := c.saveNodes(context.Background(), batch); err != nil {
					log.Printf("Error saving batch on timeout: %v", err)
				}
				batch = make([]*messaging.ProcessedNode, 0, batchSize) // Reset batch
			}
		}
	}
}

// SaveNode sends a processed node to the batching queue.
func (c *MessageNodeCache) SaveNode(ctx context.Context, messageID string, node *messaging.MsgNode) error {
	if node == nil || messageID == "" {
		return nil
	}

	processedNode := &messaging.ProcessedNode{
		MessageID: messageID,
		Node:      node,
	}

	select {
	case c.nodeQueue <- processedNode:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		log.Printf("Warning: Message node queue is full. Discarding node %s.", messageID)
		return fmt.Errorf("node queue is full")
	}
}

// saveNodes uses `COPY FROM` for efficient bulk insertion/updates.
func (c *MessageNodeCache) saveNodes(ctx context.Context, nodes []*messaging.ProcessedNode) error {
	if len(nodes) == 0 {
		return nil
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Create a temporary table to hold the batch data
	_, err = tx.ExecContext(ctx, `
		CREATE TEMP TABLE temp_message_nodes (
			message_id TEXT PRIMARY KEY,
			data JSONB NOT NULL,
			updated_at BIGINT NOT NULL
		) ON COMMIT DROP
	`)
	if err != nil {
		return fmt.Errorf("failed to create temp table: %w", err)
	}

	// Use pgx-specific COPY FROM
	stmt, err := tx.PrepareContext(ctx, `COPY temp_message_nodes (message_id, data, updated_at) FROM STDIN`)
	if err != nil {
		return fmt.Errorf("failed to prepare copy statement: %w", err)
	}

	for _, pNode := range nodes {
		serial := msgNodeSerializable{
			Text:               pNode.Node.GetText(),
			Images:             pNode.Node.GetImages(),
			GeneratedImages:    pNode.Node.GetGeneratedImages(),
			Role:               pNode.Node.Role,
			UserID:             pNode.Node.UserID,
			HasBadAttachments:  pNode.Node.HasBadAttachments,
			FetchParentFailed:  pNode.Node.FetchParentFailed,
			WebSearchPerformed: pNode.Node.WebSearchPerformed,
			SearchResultCount:  pNode.Node.SearchResultCount,
		}
		data, err := json.Marshal(serial)
		if err != nil {
			log.Printf("Failed to marshal node %s: %v", pNode.MessageID, err)
			continue // Skip this node
		}
		_, err = stmt.ExecContext(ctx, pNode.MessageID, data, time.Now().Unix())
		if err != nil {
			return fmt.Errorf("failed to execute copy: %w", err)
		}
	}

	err = stmt.Close()
	if err != nil {
		return fmt.Errorf("failed to close copy statement: %w", err)
	}

	// Upsert from the temporary table to the main table
	_, err = tx.ExecContext(ctx, `
		INSERT INTO message_nodes (message_id, data, updated_at)
		SELECT message_id, data, updated_at FROM temp_message_nodes
		ON CONFLICT (message_id) DO UPDATE SET
			data = EXCLUDED.data,
			updated_at = EXCLUDED.updated_at
	`)
	if err != nil {
		return fmt.Errorf("failed to upsert from temp table: %w", err)
	}

	return tx.Commit()
}

// GetNode retrieves a cached node. Returns (nil, nil) if not found.
func (c *MessageNodeCache) GetNode(ctx context.Context, messageID string) (*messaging.MsgNode, error) {
	var rawData []byte
	// Since writes are now async, we can't rely on a prepared statement from the old struct
	err := c.db.QueryRowContext(ctx, `SELECT data FROM message_nodes WHERE message_id = $1`, messageID).Scan(&rawData)
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


// Close closes the underlying DB connection and waits for the batch worker to finish.
func (c *MessageNodeCache) Close() error {
	close(c.nodeQueue)
	c.wg.Wait()
	return nil
}
