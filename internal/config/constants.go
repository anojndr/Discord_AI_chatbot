package config

// Default values for configuration
const (
	// Discord limits and defaults
	DefaultMaxImages     = 5
	DefaultMaxMessages   = 25
	DefaultStatusMessage = "i love you"
	DefaultAllowDMs      = true

	// LLM defaults
	DefaultModel = "gemini/gemini-2.5-pro"

	// Discord status message length limit
	MaxStatusMessageLength = 128

	// Web search defaults (aligned with RAG-Forge API documentation)
	DefaultWebSearchBaseURL    = "http://localhost:8080"
	DefaultWebSearchMaxResults = 10
	DefaultWebSearchMaxChars   = 50000 // Increased to prevent truncation of comments/replies
	DefaultWebSearchModel      = "gemini/gemini-2.5-flash"

	// Bot message handling
	MaxMessageNodes = 500

	// HTTP timeouts and limits
	DefaultHTTPTimeout    = 30 // seconds
	MaxIdleConns          = 100
	MaxIdleConnsPerHost   = 100
	IdleConnTimeout       = 90 // seconds
	TLSHandshakeTimeout   = 10 // seconds
	ExpectContinueTimeout = 1  // second

	// Stream response channel buffer size
	StreamResponseBufferSize = 10

	// Discord embed colors
	EmbedColorComplete   = 0x00ff00 // Green
	EmbedColorProcessing = 0xffa500 // Orange
	EmbedColorError      = 0xff0000 // Red
	EmbedColorInfo       = 0x0099ff // Blue

	// File processing
	MaxFileSize  = 10 * 1024 * 1024 // 10MB
	PDFPageLimit = 50

	// Logging defaults
	DefaultLogLevel = "INFO"

	// Table rendering defaults
	DefaultTableRenderingMethod   = "gg"  // Use gg graphics by default
	DefaultRodTimeout             = 10    // seconds
	DefaultRodQuality             = 90    // PNG quality (0-100)

	// Context summarization defaults
	DefaultContextSummarizationEnabled           = true
	DefaultContextSummarizationTriggerThreshold  = 0.8                     // 80% of token limit
	DefaultContextSummarizationModel             = "gemini/gemini-2.5-flash"
	DefaultContextSummarizationMaxPairsPerBatch  = 1
	DefaultContextSummarizationMinUnsummarizedPairs = 0
)
