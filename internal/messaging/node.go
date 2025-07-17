package messaging

import (
	"sync"

	"github.com/bwmarrin/discordgo"
)

// MsgNode represents a node in the conversation chain
type MsgNode struct {
	Text            string                  `json:"text"`
	Images          []ImageContent          `json:"images"`
	GeneratedImages []GeneratedImageContent `json:"generated_images"`
	AudioFiles      []AudioContent          `json:"audio_files"`

	Role   string `json:"role"` // "user" or "assistant"
	UserID string `json:"user_id"`

	HasBadAttachments bool `json:"has_bad_attachments"`
	FetchParentFailed bool `json:"fetch_parent_failed"`

	// Web search information
	WebSearchPerformed bool `json:"web_search_performed"`
	SearchResultCount  int  `json:"search_result_count"`

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

// MessageContent represents content for OpenAI API
type MessageContent struct {
	Type           string                 `json:"type"`
	Text           string                 `json:"text,omitempty"`
	ImageURL       *ImageURL              `json:"image_url,omitempty"`
	GeneratedImage *GeneratedImageContent `json:"generated_image,omitempty"`
	AudioFile      *AudioContent          `json:"audio_file,omitempty"`
}

// OpenAIMessage represents a message in OpenAI format
type OpenAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
	Name    string `json:"name,omitempty"`
}

// NewMsgNode creates a new message node
func NewMsgNode() *MsgNode {
	return &MsgNode{
		Role:            "assistant",
		Images:          make([]ImageContent, 0),
		GeneratedImages: make([]GeneratedImageContent, 0),
		AudioFiles:      make([]AudioContent, 0),
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


// MsgNodeManager manages message nodes with caching
type MsgNodeManager struct {
	nodes    map[string]*MsgNode
	mu       sync.RWMutex
	maxNodes int
}

// NewMsgNodeManager creates a new message node manager
func NewMsgNodeManager(maxNodes int) *MsgNodeManager {
	return &MsgNodeManager{
		nodes:    make(map[string]*MsgNode),
		maxNodes: maxNodes,
	}
}

// GetOrCreate gets an existing node or creates a new one
func (m *MsgNodeManager) GetOrCreate(messageID string) *MsgNode {
	m.mu.Lock()
	defer m.mu.Unlock()

	if node, exists := m.nodes[messageID]; exists {
		return node
	}

	node := NewMsgNode()
	m.nodes[messageID] = node
	return node
}

// Get gets an existing node
func (m *MsgNodeManager) Get(messageID string) (*MsgNode, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	node, exists := m.nodes[messageID]
	return node, exists
}

// Set sets a node
func (m *MsgNodeManager) Set(messageID string, node *MsgNode) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nodes[messageID] = node
	m.cleanup()
}

// Delete removes a node
func (m *MsgNodeManager) Delete(messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.nodes, messageID)
}

// cleanup removes oldest nodes if we exceed maxNodes
func (m *MsgNodeManager) cleanup() {
	if len(m.nodes) <= m.maxNodes {
		return
	}

	// Find oldest nodes by message ID (string comparison works for Discord snowflakes)
	var messageIDs []string
	for id := range m.nodes {
		messageIDs = append(messageIDs, id)
	}

	// Sort by ID (older IDs are smaller)
	for i := 0; i < len(messageIDs)-1; i++ {
		for j := i + 1; j < len(messageIDs); j++ {
			if messageIDs[i] > messageIDs[j] {
				messageIDs[i], messageIDs[j] = messageIDs[j], messageIDs[i]
			}
		}
	}

	// Remove oldest nodes
	toRemove := len(m.nodes) - m.maxNodes
	for i := 0; i < toRemove; i++ {
		delete(m.nodes, messageIDs[i])
	}
}

// Size returns the number of cached nodes
func (m *MsgNodeManager) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.nodes)
}
