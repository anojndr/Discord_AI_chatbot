package interfaces

import (
	"context"

	"github.com/bwmarrin/discordgo"
	openai "github.com/sashabaranov/go-openai"

	"DiscordAIChatbot/internal/messaging"
)

// LLMProvider defines the interface for LLM communication
type LLMProvider interface {
	// CreateChatCompletionStream creates a streaming chat completion
	CreateChatCompletionStream(ctx context.Context, model string, messages []messaging.OpenAIMessage) (*openai.ChatCompletionStream, error)

	// StreamChatCompletion streams chat completion responses
	StreamChatCompletion(ctx context.Context, model string, messages []messaging.OpenAIMessage) (<-chan StreamResponse, error)

	// GenerateVideo generates a video
	GenerateVideo(ctx context.Context, model string, prompt string) ([]byte, error)

	// BuildMessages creates OpenAI messages from conversation chain
	BuildMessages(nodes []*messaging.MsgNode, maxImages int, acceptImages, acceptUsernames bool) ([]messaging.OpenAIMessage, []string)

	// AddSystemPrompt adds system prompt to messages
	AddSystemPrompt(messages []messaging.OpenAIMessage, systemPrompt string, acceptUsernames bool) []messaging.OpenAIMessage

	// TestProviderConnectivity tests if a provider's server is reachable
	TestProviderConnectivity(providerName string) error
}

// StreamResponse represents a streaming response chunk
type StreamResponse struct {
	Content       string
	FinishReason  string
	Error         error
	ImageData     []byte
	ImageMIMEType string
}

// FileProcessor defines the interface for file processing
type FileProcessor interface {
	// ProcessFile processes a file and returns its text content
	ProcessFile(data []byte, contentType, filename string) (string, error)

	// GetSupportedFileTypes returns supported file types
	GetSupportedFileTypes() []string

	// GetProcessingInfo returns processing information
	GetProcessingInfo() string
}

// WebSearchProvider defines the interface for web search functionality
type WebSearchProvider interface {
	// Search performs a web search and returns formatted results
	Search(ctx context.Context, query string) (string, error)

	// SearchMultiple performs multiple web searches and combines results
	SearchMultiple(ctx context.Context, queries []string) (string, error)

	// ExtractURLs extracts content from specific URLs
	ExtractURLs(ctx context.Context, urls []string) (string, error)

	// CheckHealth performs a health check on the web search API
	CheckHealth(ctx context.Context) error
}

// PermissionChecker defines the interface for permission checking
type PermissionChecker interface {
	// CheckPermissions checks if a user has permission to use the bot
	CheckPermissions(m *discordgo.MessageCreate) bool
}

// UserPreferences defines the interface for user preference management
type UserPreferences interface {
	// GetUserModel returns the user's preferred model
	GetUserModel(ctx context.Context, userID, defaultModel string) string

	// SetUserModel sets the user's preferred model
	SetUserModel(ctx context.Context, userID, model string) error

	// GetUserSystemPrompt gets the custom system prompt for a user
	GetUserSystemPrompt(ctx context.Context, userID string) string

	// SetUserSystemPrompt sets the custom system prompt for a user
	SetUserSystemPrompt(ctx context.Context, userID, prompt string) error

	// ClearUserSystemPrompt clears the custom system prompt for a user
	ClearUserSystemPrompt(ctx context.Context, userID string) error

	// Close closes the database connection
	Close() error
}

// APIKeyManager defines the interface for API key management
type APIKeyManager interface {
	// GetNextAPIKey returns the next available API key for a provider
	GetNextAPIKey(provider string, availableKeys []string) (string, error)

	// MarkKeyAsBad marks an API key as bad so it won't be used again
	MarkKeyAsBad(provider, apiKey string, reason string) error

	// ResetBadKeys resets bad keys for a provider
	ResetBadKeys(provider string) error

	// GetBadKeyStats returns statistics about bad keys
	GetBadKeyStats() (map[string]int, error)

	// Close closes the database connection
	Close() error
}

// MessageNodeManager defines the interface for managing conversation nodes
type MessageNodeManager interface {
	// GetOrCreate gets or creates a message node
	GetOrCreate(messageID string) *messaging.MsgNode

	// Get retrieves a message node
	Get(messageID string) (*messaging.MsgNode, bool)

	// Set stores a message node
	Set(messageID string, node *messaging.MsgNode)

	// Delete removes a message node
	Delete(messageID string)

	// Size returns the number of nodes
	Size() int
}
