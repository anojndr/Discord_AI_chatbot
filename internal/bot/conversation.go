package bot

import (
	"context"
	"fmt"
	"log"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/utils"
)



// buildConversationChainWithWebSearch builds the conversation chain from message history with optional web search analysis
func (b *Bot) buildConversationChainWithWebSearch(s *discordgo.Session, m *discordgo.MessageCreate, acceptImages, acceptUsernames, enableWebSearch bool, progressMgr *utils.ProgressManager) ([]messaging.OpenAIMessage, []string) {
	// Add cycle detection to prevent infinite loops
	processedMessages := make(map[string]bool)
	var messages []messaging.OpenAIMessage
	var warnings []string

	// Thread-safe access to config
	b.mu.RLock()
	config := b.config
	b.mu.RUnlock()

	currentMsg := m.Message
	maxMessages := config.MaxMessages
	maxImages := config.MaxImages
	if !acceptImages {
		maxImages = 0
	}


	// Build chain by following parent messages
	isCurrentMessage := true // The first message is the current one being responded to
	maxMessagesReached := false
	for currentMsg != nil && len(messages) < maxMessages {
		// Check for cycles to prevent infinite loops
		if processedMessages[currentMsg.ID] {
			log.Printf("Cycle detected in message chain at message ID %s, breaking", currentMsg.ID)
			warnings = append(warnings, "⚠️ Conversation chain cycle detected")
			break
		}
		processedMessages[currentMsg.ID] = true
		// Attempt to retrieve node from in-memory cache first
		node, exists := b.nodeManager.Get(currentMsg.ID)
		if !exists {
			// Cache miss – try persistent cache
			if b.messageCache != nil {
				dbNode, err := b.messageCache.GetNode(context.Background(), currentMsg.ID)
				if err != nil {
					log.Printf("Failed to load node from DB: %v", err)
				}
				if dbNode != nil {
					node = dbNode
					b.nodeManager.Set(currentMsg.ID, node)
				}
			}

			if node == nil {
				node = b.nodeManager.GetOrCreate(currentMsg.ID)
			}
		}

		// Note: Backup nodes are not used in regular conversation chain building
		// They are only used for specific scenarios where the original enhanced content
		// needs to be reconstructed. Regular conversation flow should use the actual
		// processed nodes to respect the user's context choice.

		// Process message if not already processed (text empty)
		if node.GetText() == "" {
			// Check if this message originally had web search enabled
			webSearchPerformed, _ := node.GetWebSearchInfo()

			// Enable web search for:
			// 1. Current message if enableWebSearch is true
			// 2. Historical messages that originally had web search enabled
			processForWebSearch := (isCurrentMessage && enableWebSearch) || webSearchPerformed

			b.processMessage(s, currentMsg, node, processForWebSearch, progressMgr)
		}

		// If ParentMsg not cached, attempt lightweight parent lookup (no URL extraction)
		if node.ParentMsg == nil {
			parentMsg, _, err := utils.FindParentMessage(s, &discordgo.MessageCreate{Message: currentMsg}, s.State.User)
			if err == nil {
				node.ParentMsg = parentMsg
			}
		}

		// Build OpenAI message
		if node.GetText() != "" || len(node.GetImages()) > 0 || len(node.GetAudioFiles()) > 0 {
			openaiMsg := messaging.OpenAIMessage{
				Role: node.Role,
			}

			if acceptUsernames && node.UserID != "" {
				openaiMsg.Name = node.UserID
			}

			text := node.GetText()
			images := node.GetImages()
			audioFiles := node.GetAudioFiles()

			// Apply image limits and collect warnings
			if len(images) > maxImages {
				if maxImages > 0 {
					images = images[:maxImages]
					if maxImages == 1 {
						warnings = append(warnings, "⚠️ Max 1 image per message")
					} else {
						warnings = append(warnings, fmt.Sprintf("⚠️ Max %d images per message", maxImages))
					}
				} else {
					images = nil
					warnings = append(warnings, "⚠️ Can't see images")
				}
			}

			if node.HasBadAttachments {
				warnings = append(warnings, "⚠️ Unsupported attachments")
			}
			if node.FetchParentFailed {
				warnings = append(warnings, fmt.Sprintf("⚠️ Only using last %d messages", len(messages)+1))
			}

			// Set content
			if (acceptImages && len(images) > 0) || len(audioFiles) > 0 {
				var content []messaging.MessageContent
				if text != "" {
					content = append(content, messaging.MessageContent{
						Type: "text",
						Text: text,
					})
				}
				for _, img := range images {
					content = append(content, messaging.MessageContent{
						Type:     "image_url",
						ImageURL: &img.ImageURL,
					})
				}
				for _, audio := range audioFiles {
					audio := audio // Capture loop variable
					content = append(content, messaging.MessageContent{
						Type:      "audio_file",
						AudioFile: &audio,
					})
				}
				openaiMsg.Content = content
			} else {
				openaiMsg.Content = text
			}

			messages = append(messages, openaiMsg)
		}

		isCurrentMessage = false // All subsequent messages are historical

		// Get parent message
		if node.ParentMsg != nil {
			currentMsg = node.ParentMsg
		} else {
			break
		}
	}

	// Check if we exited because max_messages was reached
	if currentMsg != nil && len(messages) >= maxMessages {
		maxMessagesReached = true
	}

	// Reverse messages to have oldest first
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}


	// Reverse back to have newest first (as expected by rest of the code)
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	// Add warning if max_messages was reached
	if maxMessagesReached {
		warnings = append(warnings, fmt.Sprintf("⚠️ Only using last %d messages", maxMessages))
	}

	// Token usage will be shown in embed footer, no need to add as warning
 
	return messages, utils.UniqueStrings(warnings)
}

