package utils

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/messaging"
)

const (
	StreamingIndicator    = " ⚪"
	EditDelaySeconds      = 1
	MaxMessageLength      = 4096
	PlainMaxMessageLength = 2000

	// Simplified progress indicator text (no animations)
	ProgressProcessing = "<a:anthropicgif:1389926560391368765> Processing..."
)

var (
	EmbedColorComplete   = 0x2D7D32 // Dark green
	EmbedColorIncomplete = 0xFF9800 // Orange
	EmbedColorProcessing = 0x2196F3 // Blue for processing
)

// ProgressManager handles progress indication for bot responses
type ProgressManager struct {
	session   *discordgo.Session
	channelID string
	messageID string
}

// NewProgressManager creates a new progress manager
func NewProgressManager(session *discordgo.Session, channelID string) *ProgressManager {
	return &ProgressManager{
		session:   session,
		channelID: channelID,
	}
}

// UpdateProgress shows a simple progress message only once at the start
func (p *ProgressManager) UpdateProgress(state string, warnings []string, replyRef *discordgo.MessageReference) error {
	// Only show progress message once at the beginning
	if p.messageID != "" {
		return nil // Already shown progress, don't update
	}

	// Create simple embed without animations
	embed := &discordgo.MessageEmbed{
		Description: ProgressProcessing,
		Color:       EmbedColorProcessing,
	}

	// Add warnings as fields if any
	for _, warning := range warnings {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   warning,
			Value:  "⚠️",
			Inline: false,
		})
	}

	// Send initial progress message only – as a reply when possible
	msg, err := p.session.ChannelMessageSendComplex(p.channelID, &discordgo.MessageSend{
		Embed:     embed,
		Reference: replyRef,
		AllowedMentions: &discordgo.MessageAllowedMentions{
			Parse:       []discordgo.AllowedMentionType{},
			RepliedUser: false,
		},
	})
	if err == nil {
		p.messageID = msg.ID
	}
	return err
}

// GetMessageID returns the message ID of the progress message
func (p *ProgressManager) GetMessageID() string {
	return p.messageID
}

// GetChannelID returns the channel ID
func (p *ProgressManager) GetChannelID() string {
	return p.channelID
}

// CreateProgressBar creates a visual progress bar using Unicode characters
func CreateProgressBar(current, total int, width int) string {
	if total == 0 {
		return strings.Repeat("⚪", width)
	}

	filled := int(float64(current) / float64(total) * float64(width))
	if filled > width {
		filled = width
	}

	bar := strings.Repeat("🔵", filled) + strings.Repeat("⚪", width-filled)
	percentage := int(float64(current) / float64(total) * 100)

	return fmt.Sprintf("%s %d%%", bar, percentage)
}

// FooterInfo contains information to be displayed in the embed footer
type FooterInfo struct {
	Model              string
	WebSearchPerformed bool
	SearchResultCount  int
	CurrentTokens      int
	TokenLimit         int
}

// CreateEmbed creates a Discord embed with warnings, content, and footer information
func CreateEmbed(content string, warnings []string, isComplete bool, footerInfo *FooterInfo) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{}

	// Only set description if content is not empty
	if strings.TrimSpace(content) != "" {
		embed.Description = content
	} else if !isComplete {
		// For incomplete responses with no content, show a placeholder
		embed.Description = "Generating response..." + StreamingIndicator
	} else {
		// For complete responses with no content, provide a minimal message
		embed.Description = "Response completed."
	}

	if isComplete {
		embed.Color = EmbedColorComplete
	} else {
		embed.Color = EmbedColorIncomplete
	}

	// Add warnings as fields
	for _, warning := range warnings {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   warning,
			Value:  "⚠️", // Provide a non-empty value for warning fields
			Inline: false,
		})
	}

	// Add footer information if provided
	if footerInfo != nil {
		var footerParts []string

		// Add model information
		if footerInfo.Model != "" {
			footerParts = append(footerParts, fmt.Sprintf("🤖 Model: %s", footerInfo.Model))
		}

		// Add token usage if provided
		if footerInfo.CurrentTokens > 0 && footerInfo.TokenLimit > 0 {
			footerParts = append(footerParts, fmt.Sprintf("🧮 %d/%d tokens", footerInfo.CurrentTokens, footerInfo.TokenLimit))
		}

		// Add web search information
		if footerInfo.WebSearchPerformed && footerInfo.SearchResultCount > 0 {
			footerParts = append(footerParts, fmt.Sprintf("🌐 Web search: %d results", footerInfo.SearchResultCount))
		} else {
			footerParts = append(footerParts, "WEB SEARCH WAS NOT PERFORMED 💔🥀")
		}

		if len(footerParts) > 0 {
			embed.Footer = &discordgo.MessageEmbedFooter{
				Text: strings.Join(footerParts, " • "),
			}
		}
	}

	return embed
}

// CreateActionButtons returns buttons for download, view output better, and retry options.
func CreateActionButtons(messageID string, webSearchPerformed bool) []discordgo.MessageComponent {
	// Include download and view output better buttons
	buttons := []discordgo.MessageComponent{
		discordgo.Button{
			Label:    "📄 Download as Text File",
			Style:    discordgo.SecondaryButton,
			CustomID: "download_response_" + messageID,
		},
		discordgo.Button{
			Label:    "🔗 View Output Better",
			Style:    discordgo.SecondaryButton,
			CustomID: "view_output_better_" + messageID,
		},
	}

	// Add retry button based on whether web search was performed
	var retryButtons []discordgo.MessageComponent
	if webSearchPerformed {
		// If web search was used, offer retry without web search
		retryButtons = []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "🔄 Retry without Web Search",
				Style:    discordgo.PrimaryButton,
				CustomID: "retry_without_search_" + messageID,
			},
		}
	} else {
		// If web search was not used, offer retry with web search
		retryButtons = []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "🔄 Retry with Web Search",
				Style:    discordgo.PrimaryButton,
				CustomID: "retry_with_search_" + messageID,
			},
		}
	}

	// Return buttons in two rows
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: buttons,
		},
		discordgo.ActionsRow{
			Components: retryButtons,
		},
	}
}

// ProcessAttachments downloads and processes message attachments

// ExtractEmbedText extracts text from message embeds
func ExtractEmbedText(embeds []*discordgo.MessageEmbed) string {
	var parts []string

	for _, embed := range embeds {
		var embedParts []string

		if embed.Title != "" {
			embedParts = append(embedParts, embed.Title)
		}
		if embed.Description != "" {
			embedParts = append(embedParts, embed.Description)
		}
		// Footer text is excluded to prevent metadata (model info, etc.) from being sent to LLM

		if len(embedParts) > 0 {
			parts = append(parts, strings.Join(embedParts, "\n"))
		}
	}

	return strings.Join(parts, "\n")
}

// isStandaloneMessage checks if a message is standalone (contains bot mention or "at ai" prefix)
func isStandaloneMessage(content, botMention string) bool {
	// Check for bot mention
	if strings.Contains(content, botMention) {
		return true
	}

	// Check for "at ai" prefix (case insensitive)
	trimmed := strings.TrimSpace(content)
	trimmedLower := strings.ToLower(trimmed)

	// Check if it starts with "at ai" followed by whitespace or end of string
	return trimmedLower == "at ai" || strings.HasPrefix(trimmedLower, "at ai ")
}

// FindParentMessage finds the parent message in a conversation chain
func FindParentMessage(s *discordgo.Session, m *discordgo.MessageCreate, botUser *discordgo.User) (*discordgo.Message, bool, error) {
	var parentMsg *discordgo.Message
	var fetchFailed bool

	// Handle direct reply
	if m.MessageReference != nil && m.MessageReference.MessageID != "" {
		msg, err := s.ChannelMessage(m.ChannelID, m.MessageReference.MessageID)
		if err != nil {
			fetchFailed = true
		} else {
			parentMsg = msg
		}
		return parentMsg, fetchFailed, nil
	}

	// Handle thread start
	channel, err := s.Channel(m.ChannelID)
	if err == nil && channel.Type == discordgo.ChannelTypeGuildPublicThread && m.MessageReference == nil {
		// This is a thread, check if we should use the starter message
		if channel.ParentID != "" {
			// Get parent channel
			parentChannel, err := s.Channel(channel.ParentID)
			if err == nil && parentChannel.Type == discordgo.ChannelTypeGuildText {
				// Try to get starter message
				if channel.ID != "" {
					msg, err := s.ChannelMessage(channel.ParentID, channel.ID)
					if err != nil {
						fetchFailed = true
					} else {
						parentMsg = msg
					}
				}
			}
		}
		return parentMsg, fetchFailed, nil
	}

	// Handle implicit conversation continuation (check previous message)
	// Don't look for parent messages if this is a standalone message (bot mention or "at ai" prefix)
	if !isStandaloneMessage(m.Content, botUser.Mention()) {
		messages, err := s.ChannelMessages(m.ChannelID, 1, m.ID, "", "")
		if err == nil && len(messages) > 0 {
			prevMsg := messages[0]
			if prevMsg.Type == discordgo.MessageTypeDefault || prevMsg.Type == discordgo.MessageTypeReply {
				// Check if it's a conversation continuation
				isDM := m.GuildID == ""
				if isDM && prevMsg.Author.ID == m.Author.ID {
					parentMsg = prevMsg
				} else if !isDM && prevMsg.Author.ID == botUser.ID {
					// For bot messages, only continue if the bot message is a direct reply
					// to a message from the same user AND the timing is reasonable for continuation
					if prevMsg.MessageReference != nil && prevMsg.MessageReference.MessageID != "" {
						// Check if the bot's reply was to a message from the current user
						referencedMsg, err := s.ChannelMessage(m.ChannelID, prevMsg.MessageReference.MessageID)
						if err == nil && referencedMsg.Author.ID == m.Author.ID {
							// Additional check: ensure messages are close in time (within 1 hour)
							// This prevents picking up old conversations
							timeDiff := m.Timestamp.Sub(prevMsg.Timestamp)
							if timeDiff.Hours() <= 1 && timeDiff >= 0 {
								parentMsg = prevMsg
							}
						}
					}
				}
			}
		}
	}

	return parentMsg, fetchFailed, nil
}

// RemoveMentionAndAtAIPrefix removes both bot mention and "at ai" prefix from start of message
func RemoveMentionAndAtAIPrefix(content, botMention string, isReply bool) string {
	var cleaned string

	// First try to remove bot mention
	if strings.HasPrefix(content, botMention) {
		cleaned = strings.TrimPrefix(content, botMention)
		cleaned = strings.TrimSpace(cleaned)
	} else {
		// If no bot mention, try to remove "at ai" prefix (case insensitive)
		trimmed := strings.TrimSpace(content)
		trimmedLower := strings.ToLower(trimmed)

		// Check if it starts with "at ai" followed by whitespace or end of string
		if trimmedLower == "at ai" {
			cleaned = ""
		} else if strings.HasPrefix(trimmedLower, "at ai ") {
			// Remove "at ai " (5 characters) and return the rest
			cleaned = strings.TrimSpace(trimmed[5:])
		} else {
			// No prefixes found, return original content trimmed
			cleaned = trimmed
		}
	}

	// If the cleaned content is empty (just a mention or "at ai" with no query),
	// replace it with appropriate default based on whether it's a reply
	if cleaned == "" {
		if isReply {
			return "Process this message; never ask for confirmation, just do it."
		} else {
			return "hi"
		}
	}

	return cleaned
}

// ReverseMessages reverses a slice of messages
func ReverseMessages(messages []messaging.OpenAIMessage) []messaging.OpenAIMessage {
	reversed := make([]messaging.OpenAIMessage, len(messages))
	for i, msg := range messages {
		reversed[len(messages)-1-i] = msg
	}
	return reversed
}

// UniqueStrings removes duplicates from a string slice while preserving order
func UniqueStrings(slice []string) []string {
	seen := make(map[string]bool)
	var result []string

	for _, item := range slice {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}

	return result
}

// IsGoodFinishReason checks if a finish reason indicates successful completion
func IsGoodFinishReason(reason string) bool {
	reasonLower := strings.ToLower(reason)
	return reasonLower == "stop" || reasonLower == "end_turn"
}

// PostToTextIs posts content to text.is and returns the shareable URL
func PostToTextIs(content string) (string, error) {
	// text.is API endpoint
	apiURL := "https://text.is/"

	// Prepare form data - text.is expects the content in the "text" field
	formData := url.Values{}
	formData.Set("text", content)

	// Create HTTP client with timeout and allow redirects
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Allow redirects, but capture the final URL
			return nil
		},
	}

	// Make POST request
	resp, err := client.PostForm(apiURL, formData)
	if err != nil {
		return "", fmt.Errorf("failed to post to text.is: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Failed to close response body: %v", err)
		}
	}()

	// Check for successful response
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("text.is returned error status %d", resp.StatusCode)
	}

	// The final URL after any redirects should be the paste URL
	finalURL := resp.Request.URL.String()

	// Verify we got a valid text.is URL (should not be the original API endpoint)
	if finalURL == apiURL || finalURL == apiURL+"/" {
		return "", fmt.Errorf("failed to create paste - no redirect occurred")
	}

	return finalURL, nil
}
