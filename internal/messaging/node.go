package messaging

import (
	"sync"

	"github.com/bwmarrin/discordgo"
	lru "github.com/hashicorp/golang-lru/v2"
)

// MsgNode represents a node in the conversation chain
type MsgNode struct {
	Text            string                  `json:"text"`
	Images          []ImageContent          `json:"images"`
	GeneratedImages []GeneratedImageContent `json:"generated_images"`
	AudioFiles      []AudioContent          `json:"audio_files"`
	PDFFiles        []PDFContent            `json:"pdf_files"`

	Role   string `json:"role"` // "user" or "assistant"
	UserID string `json:"user_id"`

	HasBadAttachments bool `json:"has_bad_attachments"`
	FetchParentFailed bool `json:"fetch_parent_failed"`

	// Web search information
	WebSearchPerformed bool `json:"web_search_performed"`
	SearchResultCount  int  `json:"search_result_count"`
	GroundingMetadata  *GroundingMetadata `json:"grounding_metadata,omitempty"`
	DetectedURLs       []string           `json:"detected_urls,omitempty"`

	ParentMsg *discordgo.Message `json:"-"`

	mu sync.RWMutex
}

// ImageContent represents an image attachment
type ImageContent struct {
	Type     string   `json:"type"`
	ImageURL ImageURL `json:"image_url"`
}

// GeneratedImageContent represents a generated image with inline data
type GeneratedImageContent struct {
	Data     []byte `json:"data"`
	MIMEType string `json:"mime_type"`
}

// ImageURL represents the image URL structure for OpenAI API
type ImageURL struct {
	URL string `json:"url"`
}

// AudioContent represents an audio file attachment
type AudioContent struct {
	Type     string `json:"type"`
	MIMEType string `json:"mime_type"`
	URL      string `json:"url"`
	Data     []byte `json:"data,omitempty"`
}

// PDFContent represents a PDF file attachment
type PDFContent struct {
	Type     string `json:"type"`
	MIMEType string `json:"mime_type"`
	URL      string `json:"url"`
	Data     []byte `json:"data,omitempty"`
}

// ProcessedNode is a container for a message ID and its corresponding MsgNode, used for batch saving.
type ProcessedNode struct {
	MessageID string
	Node      *MsgNode
}

// MessageContent represents content for OpenAI API
type MessageContent struct {
	Type           string                 `json:"type"`
	Text           string                 `json:"text,omitempty"`
	ImageURL       *ImageURL              `json:"image_url,omitempty"`
	GeneratedImage *GeneratedImageContent `json:"generated_image,omitempty"`
	AudioFile      *AudioContent          `json:"audio_file,omitempty"`
	PDFFile        *PDFContent            `json:"pdf_file,omitempty"`
}

// OpenAIMessage represents a message in OpenAI format
type OpenAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
	Name    string `json:"name,omitempty"`
}

// GroundingMetadata stores the metadata for grounding with Google Search
type GroundingMetadata struct {
	WebSearchQueries []string        `json:"web_search_queries"`
	GroundingChunks  []GroundingChunk `json:"grounding_chunks"`
}

// GroundingChunk represents a single source for grounding
type GroundingChunk struct {
	Web struct {
		URI   string `json:"uri"`
		Title string `json:"title"`
	} `json:"web"`
}
	
// NewMsgNode creates a new message node
func NewMsgNode() *MsgNode {
	return &MsgNode{
		Role:            "assistant",
		Images:          make([]ImageContent, 0),
		GeneratedImages: make([]GeneratedImageContent, 0),
		AudioFiles:      make([]AudioContent, 0),
		PDFFiles:        make([]PDFContent, 0),
	}
}

// GetText safely gets the text content
func (m *MsgNode) GetText() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.Text
}

// SetText safely sets the text content
func (m *MsgNode) SetText(text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Text = text
}

// GetImages safely gets the images
func (m *MsgNode) GetImages() []ImageContent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]ImageContent(nil), m.Images...)
}

// SetImages safely sets the images
func (m *MsgNode) SetImages(images []ImageContent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Images = images
}

// AddImage safely adds an image
func (m *MsgNode) AddImage(image ImageContent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Images = append(m.Images, image)
}

// GetGeneratedImages safely gets the generated images
func (m *MsgNode) GetGeneratedImages() []GeneratedImageContent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]GeneratedImageContent(nil), m.GeneratedImages...)
}

// SetGeneratedImages safely sets the generated images
func (m *MsgNode) SetGeneratedImages(images []GeneratedImageContent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GeneratedImages = images
}

// AddGeneratedImage safely adds a generated image
func (m *MsgNode) AddGeneratedImage(image GeneratedImageContent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GeneratedImages = append(m.GeneratedImages, image)
}

// GetAudioFiles safely gets the audio files
func (m *MsgNode) GetAudioFiles() []AudioContent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]AudioContent(nil), m.AudioFiles...)
}

// SetAudioFiles safely sets the audio files
func (m *MsgNode) SetAudioFiles(audioFiles []AudioContent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.AudioFiles = audioFiles
}

// AddAudioFile safely adds an audio file
func (m *MsgNode) AddAudioFile(audioFile AudioContent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.AudioFiles = append(m.AudioFiles, audioFile)
}

// GetPDFFiles safely gets the PDF files
func (m *MsgNode) GetPDFFiles() []PDFContent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]PDFContent(nil), m.PDFFiles...)
}

// SetPDFFiles safely sets the PDF files
func (m *MsgNode) SetPDFFiles(pdfFiles []PDFContent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PDFFiles = pdfFiles
}

// AddPDFFile safely adds a PDF file
func (m *MsgNode) AddPDFFile(pdfFile PDFContent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PDFFiles = append(m.PDFFiles, pdfFile)
}

// GetWebSearchInfo safely gets web search information
func (m *MsgNode) GetWebSearchInfo() (bool, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.WebSearchPerformed, m.SearchResultCount
}

// SetWebSearchInfo safely sets web search information
func (m *MsgNode) SetWebSearchInfo(performed bool, resultCount int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.WebSearchPerformed = performed
	m.SearchResultCount = resultCount
}

// GetGroundingMetadata safely gets grounding metadata
func (m *MsgNode) GetGroundingMetadata() *GroundingMetadata {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.GroundingMetadata
}

// SetGroundingMetadata safely sets grounding metadata
func (m *MsgNode) SetGroundingMetadata(metadata *GroundingMetadata) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GroundingMetadata = metadata
}

// GetDetectedURLs safely gets the detected URLs
func (m *MsgNode) GetDetectedURLs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.DetectedURLs
}

// SetDetectedURLs safely sets the detected URLs
func (m *MsgNode) SetDetectedURLs(urls []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DetectedURLs = urls
}

// MsgNodeManager manages message nodes with caching
type MsgNodeManager struct {
	cache *lru.Cache[string, *MsgNode] // Use the LRU cache
	mu    sync.RWMutex
}

// NewMsgNodeManager creates a new message node manager
func NewMsgNodeManager(maxNodes int) *MsgNodeManager {
	cache, _ := lru.New[string, *MsgNode](maxNodes)
	return &MsgNodeManager{
		cache: cache,
	}
}

// GetOrCreate gets an existing node or creates a new one
func (m *MsgNodeManager) GetOrCreate(messageID string) *MsgNode {
	m.mu.Lock()
	defer m.mu.Unlock()

	if node, exists := m.cache.Get(messageID); exists {
		return node
	}

	node := NewMsgNode()
	m.cache.Add(messageID, node)
	return node
}

// Get gets an existing node
func (m *MsgNodeManager) Get(messageID string) (*MsgNode, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cache.Get(messageID)
}

// Set sets a node
func (m *MsgNodeManager) Set(messageID string, node *MsgNode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cache.Add(messageID, node)
}

// Delete removes a node
func (m *MsgNodeManager) Delete(messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cache.Remove(messageID)
}

// Size returns the number of cached nodes
func (m *MsgNodeManager) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cache.Len()
}
