package bot

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/utils"
)

// handleButtonInteraction handles button click interactions
func (b *Bot) handleButtonInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()

	// Handle download response button
	if strings.HasPrefix(data.CustomID, "download_response_") {
		b.handleDownloadResponse(s, i)
		return
	}

	// Handle view output better button
	if strings.HasPrefix(data.CustomID, "view_output_better_") {
		b.handleViewOutputBetter(s, i)
		return
	}

	// Handle retry with web search button
	if strings.HasPrefix(data.CustomID, "retry_with_search_") {
		b.handleRetryWithSearch(s, i)
		return
	}

	// Handle retry without web search button
	if strings.HasPrefix(data.CustomID, "retry_without_search_") {
		b.handleRetryWithoutSearch(s, i)
		return
	}

}

// handleDownloadResponse handles the download response button click
func (b *Bot) handleDownloadResponse(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract message ID from custom ID
	customID := i.MessageComponentData().CustomID
	messageID := strings.TrimPrefix(customID, "download_response_")

	// Get the message content from the node manager
	node, exists := b.nodeManager.Get(messageID)
	if !exists {
		// Fallback: try to get content from the actual Discord message
		msg, err := s.ChannelMessage(i.ChannelID, messageID)
		if err != nil {
			if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "❌ Could not retrieve response content.",
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			}); err != nil {
				log.Printf("Failed to respond to interaction: %v", err)
			}
			return
		}

		// Extract content from embed or message content
		var content string
		if len(msg.Embeds) > 0 && msg.Embeds[0].Description != "" {
			content = msg.Embeds[0].Description
		} else {
			content = msg.Content
		}

		b.sendResponseAsFile(s, i, content, messageID)
		return
	}

	// Get content from node
	content := node.GetText()
	if content == "" {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ No content available for download.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	b.sendResponseAsFile(s, i, content, messageID)
}

// handleViewOutputBetter handles the view output better button click
func (b *Bot) handleViewOutputBetter(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract message ID from custom ID
	customID := i.MessageComponentData().CustomID
	messageID := strings.TrimPrefix(customID, "view_output_better_")

	// Get the message content from the node manager
	node, exists := b.nodeManager.Get(messageID)
	var content string

	if !exists {
		// Fallback: try to get content from the actual Discord message
		msg, err := s.ChannelMessage(i.ChannelID, messageID)
		if err != nil {
			if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "❌ Could not retrieve response content.",
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			}); err != nil {
				log.Printf("Failed to respond to interaction: %v", err)
			}
			return
		}

		// Extract content from embed or message content
		if len(msg.Embeds) > 0 && msg.Embeds[0].Description != "" {
			content = msg.Embeds[0].Description
		} else {
			content = msg.Content
		}
	} else {
		// Get content from node
		content = node.GetText()
	}

	if content == "" {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ No content available to view.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	// Post to text.is
	textIsURL, err := utils.PostToTextIs(content)
	if err != nil {
		log.Printf("Failed to post to text.is: %v", err)
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Failed to create text.is link. Please try again later.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	// Send success response with the link
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("🔗 **View output better:** %s\n\nThis link displays the response with improved formatting and readability.", textIsURL),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		log.Printf("Failed to respond to interaction: %v", err)
	}
}

// sendResponseAsFile creates and sends a text file with the response content
func (b *Bot) sendResponseAsFile(s *discordgo.Session, i *discordgo.InteractionCreate, content, messageID string) {
	// Create filename with timestamp
	filename := fmt.Sprintf("DiscordAIChatbot_response_%s_%d.txt", messageID[:8], time.Now().Unix())

	// Send the file as an attachment
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "📄 Response downloaded as text file:",
			Files: []*discordgo.File{
				{
					Name:   filename,
					Reader: strings.NewReader(content),
				},
			},
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		log.Printf("Failed to send file: %v", err)
		// Try to send error response
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Failed to create download file.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
	}
}

// handleRetryWithSearch handles the retry with web search button click
func (b *Bot) handleRetryWithSearch(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract message ID from custom ID
	customID := i.MessageComponentData().CustomID
	messageID := strings.TrimPrefix(customID, "retry_with_search_")

	// Find the original user message that triggered this bot response
	originalMessage, err := b.findOriginalUserMessage(s, i.ChannelID, messageID)
	if err != nil {
		log.Printf("Failed to find original message: %v", err)
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not find original message to retry.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	// Create synthetic retry message with web search forced
	b.createRetryMessage(s, i, originalMessage, true)
}

// handleRetryWithoutSearch handles the retry without web search button click
func (b *Bot) handleRetryWithoutSearch(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Extract message ID from custom ID
	customID := i.MessageComponentData().CustomID
	messageID := strings.TrimPrefix(customID, "retry_without_search_")

	// Find the original user message that triggered this bot response
	originalMessage, err := b.findOriginalUserMessage(s, i.ChannelID, messageID)
	if err != nil {
		log.Printf("Failed to find original message: %v", err)
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not find original message to retry.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	// Create synthetic retry message without web search
	b.createRetryMessage(s, i, originalMessage, false)
}

// findOriginalUserMessage finds the original user message that triggered the bot response
func (b *Bot) findOriginalUserMessage(s *discordgo.Session, channelID, botMessageID string) (*discordgo.Message, error) {
	// Get the bot message
	botMessage, err := s.ChannelMessage(channelID, botMessageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get bot message: %w", err)
	}

	// Check if the bot message has a message reference (reply)
	if botMessage.MessageReference != nil && botMessage.MessageReference.MessageID != "" {
		// Get the referenced message
		referencedMessage, err := s.ChannelMessage(channelID, botMessage.MessageReference.MessageID)
		if err != nil {
			return nil, fmt.Errorf("failed to get referenced message: %w", err)
		}
		return referencedMessage, nil
	}

	// If no reference, search for the most recent user message before the bot message
	messages, err := s.ChannelMessages(channelID, 50, botMessageID, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to get channel messages: %w", err)
	}

	for _, msg := range messages {
		if !msg.Author.Bot {
			return msg, nil
		}
	}

	return nil, fmt.Errorf("no user message found")
}

// createRetryMessage creates a synthetic retry message and processes it
func (b *Bot) createRetryMessage(s *discordgo.Session, i *discordgo.InteractionCreate, originalMessage *discordgo.Message, forceWebSearch bool) {
	// Acknowledge the interaction first
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "🔄 Retrying...",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		log.Printf("Failed to acknowledge interaction: %v", err)
		return
	}

	// Get the bot message ID that triggered the retry (from the button)
	customID := i.MessageComponentData().CustomID
	var botMessageID string
	if strings.HasPrefix(customID, "retry_with_search_") {
		botMessageID = strings.TrimPrefix(customID, "retry_with_search_")
	} else if strings.HasPrefix(customID, "retry_without_search_") {
		botMessageID = strings.TrimPrefix(customID, "retry_without_search_")
	}

	// Prepare the retry message content
	retryContent := originalMessage.Content
	
	if forceWebSearch {
		// Append search directive if not already present
		if !strings.Contains(strings.ToUpper(retryContent), "SEARCH THE NET") {
			retryContent += "\n\nSEARCH THE NET"
		}
	}

	// Create a synthetic message ID that won't conflict with existing messages
	// Use a simple timestamp-based approach to avoid Discord snowflake validation issues
	syntheticID := fmt.Sprintf("%d", time.Now().UnixNano())

	// Create a message reference that points to the bot message being retried
	// This ensures the conversation chain is maintained properly
	var messageRef *discordgo.MessageReference
	if botMessageID != "" {
		messageRef = &discordgo.MessageReference{
			MessageID: botMessageID,
			ChannelID: originalMessage.ChannelID,
			GuildID:   originalMessage.GuildID,
		}
	} else {
		// Fallback to original message reference if we can't get bot message ID
		messageRef = originalMessage.MessageReference
	}

	// Create a synthetic message create event
	syntheticMessage := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        syntheticID, // Use unique synthetic ID to avoid conflicts
			Content:   retryContent,
			Author:    originalMessage.Author,
			ChannelID: originalMessage.ChannelID,
			GuildID:   originalMessage.GuildID,
			Timestamp: time.Now(),
			MessageReference: messageRef, // Point to the bot message being retried
			Attachments: originalMessage.Attachments,
		},
	}

	// Set retry flag to skip web search decider if needed
	if !forceWebSearch {
		// We need to modify the bot to support a flag to skip web search
		// For now, we'll handle this in the web search logic
		syntheticMessage.Content = "SKIP_WEB_SEARCH_DECIDER\n\n" + retryContent
	}

	// Process the synthetic message
	go b.handleMessage(s, syntheticMessage)
}
