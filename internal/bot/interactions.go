package bot

import (
	"context"
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

	switch {
	case strings.HasPrefix(data.CustomID, "download_response_"):
		b.handleDownloadResponse(s, i)
	case strings.HasPrefix(data.CustomID, "view_output_better_"):
		b.handleViewOutputBetter(s, i)
	case strings.HasPrefix(data.CustomID, "retry_with_web_search_"):
		b.handleRetry(s, i, true)
	case strings.HasPrefix(data.CustomID, "retry_without_web_search_"):
		b.handleRetry(s, i, false)
	case strings.HasPrefix(data.CustomID, "show_sources_"):
		b.handleShowSources(s, i)
	case strings.HasPrefix(data.CustomID, "paginate_sources_"):
		b.handlePaginateSources(s, i)
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
		// Fallback: try to get the full content by traversing the reply chain
		content, err := b.getFullResponseContent(s, i.ChannelID, messageID)
		if err != nil {
			log.Printf("Failed to get full response content: %v", err)
			if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "❌ Could not retrieve full response content.",
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			}); err != nil {
				log.Printf("Failed to respond to interaction: %v", err)
			}
			return
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
		// Fallback: try to get the full content by traversing the reply chain
		var err error
		content, err = b.getFullResponseContent(s, i.ChannelID, messageID)
		if err != nil {
			log.Printf("Failed to get full response content: %v", err)
			if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "❌ Could not retrieve full response content.",
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			}); err != nil {
				log.Printf("Failed to respond to interaction: %v", err)
			}
			return
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

// handleShowSources handles the "Show Sources" button click
func (b *Bot) handleShowSources(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID
	messageID := strings.TrimPrefix(customID, "show_sources_")

	node, exists := b.nodeManager.Get(messageID)
	if !exists || node.GetGroundingMetadata() == nil {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Could not retrieve grounding sources for this message.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	metadata := node.GetGroundingMetadata()
	var sourcesEmbed *discordgo.MessageEmbed
	if len(metadata.WebSearchQueries) > 0 || len(metadata.GroundingChunks) > 0 {
		sourcesEmbed = &discordgo.MessageEmbed{
			Title: "📚 Grounding Sources",
			Color: 0x4A90E2, // A nice blue color
		}

		if len(metadata.WebSearchQueries) > 0 {
			sourcesEmbed.Fields = append(sourcesEmbed.Fields, &discordgo.MessageEmbedField{
				Name:  "🔍 Search Queries",
				Value: "```\n" + strings.Join(metadata.WebSearchQueries, "\n") + "\n```",
			})
		}

		if len(metadata.GroundingChunks) > 0 {
			urls := make([]string, len(metadata.GroundingChunks))
			for i, chunk := range metadata.GroundingChunks {
				urls[i] = fmt.Sprintf("%d. [%s](%s)", i+1, chunk.Web.Title, chunk.Web.URI)
			}
			b.sendPaginatedSources(s, i, messageID, urls, metadata.WebSearchQueries, 0)
			return // The work is done by the paginated sender
		}
	}

	// This part is reached only if there are no sources to show
	sourcesEmbed = &discordgo.MessageEmbed{
		Title:       "📚 Grounding Sources",
		Description: "No grounding sources were used for this response.",
		Color:       0x9B9B9B, // Grey color
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{sourcesEmbed},
			Flags:  discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		log.Printf("Failed to respond to interaction: %v", err)
	}
}

func (b *Bot) handlePaginateSources(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.Split(i.MessageComponentData().CustomID, "_")
	if len(parts) != 4 {
		return
	}
	messageID := parts[2]
	page := 0
	fmt.Sscanf(parts[3], "%d", &page)

	pages, ok := b.paginationCache.Get(messageID)
	if !ok {
		// Respond with an error message if the pagination data has expired
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Content:    "This interaction has expired. Please click 'Show Sources' again.",
				Embeds:     []*discordgo.MessageEmbed{},
				Components: []discordgo.MessageComponent{},
			},
		}); err != nil {
			log.Printf("Failed to send pagination expiration message: %v", err)
		}
		return
	}

	// Generate the embed for the new page
	node, exists := b.nodeManager.Get(messageID)
	var webSearchQueries []string
	if exists && node.GetGroundingMetadata() != nil {
		webSearchQueries = node.GetGroundingMetadata().WebSearchQueries
	}

	embed := b.createSourcesEmbed(pages[page], webSearchQueries, page, len(pages))

	// Generate the buttons for the new page
	components := b.createPaginationButtons(messageID, page, len(pages))

	// Update the original message with the new page content
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		},
	}); err != nil {
		log.Printf("Failed to update paginated sources: %v", err)
	}
}

func (b *Bot) sendPaginatedSources(s *discordgo.Session, i *discordgo.InteractionCreate, messageID string, urls []string, webSearchQueries []string, page int) {
	const maxEmbedSize = 4000 // A bit less than the 6000 limit to be safe
	const maxFieldLength = 1024

	var pages [][]string
	var currentPage []string
	var currentPageSize int

	for _, url := range urls {
		if currentPageSize+len(url) > maxEmbedSize {
			pages = append(pages, currentPage)
			currentPage = nil
			currentPageSize = 0
		}
		currentPage = append(currentPage, url)
		currentPageSize += len(url)
	}
	if len(currentPage) > 0 {
		pages = append(pages, currentPage)
	}

	b.paginationCache.Set(messageID, pages)

	embed := b.createSourcesEmbed(pages[page], webSearchQueries, page, len(pages))
	components := b.createPaginationButtons(messageID, page, len(pages))

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Flags:      discordgo.MessageFlagsEphemeral,
			Components: components,
		},
	}); err != nil {
		log.Printf("Failed to send paginated sources: %v", err)
	}
}

func (b *Bot) createSourcesEmbed(pageContent []string, webSearchQueries []string, page, pageCount int) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Title: "📚 Grounding Sources",
		Color: 0x4A90E2,
	}

	if len(webSearchQueries) > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "🔍 Search Queries",
			Value: "```\n" + strings.Join(webSearchQueries, "\n") + "\n```",
		})
	}

	var currentField strings.Builder
	fieldCount := 1
	const maxFieldLength = 1024

	for _, url := range pageContent {
		if currentField.Len()+len(url)+1 > maxFieldLength {
			fieldName := "🌐 URLs"
			if fieldCount > 1 {
				fieldName = fmt.Sprintf("🌐 URLs (Cont.)")
			}
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
				Name:  fieldName,
				Value: currentField.String(),
			})
			currentField.Reset()
			fieldCount++
		}
		if currentField.Len() > 0 {
			currentField.WriteString("\n")
		}
		currentField.WriteString(url)
	}

	if currentField.Len() > 0 {
		fieldName := "🌐 URLs"
		if fieldCount > 1 {
			fieldName = fmt.Sprintf("🌐 URLs (Cont.)")
		}
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  fieldName,
			Value: currentField.String(),
		})
	}

	if pageCount > 1 {
		embed.Footer = &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Page %d of %d", page+1, pageCount),
		}
	}

	return embed
}

func (b *Bot) createPaginationButtons(messageID string, page, pageCount int) []discordgo.MessageComponent {
	if pageCount <= 1 {
		return nil
	}

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Previous",
					Style:    discordgo.PrimaryButton,
					CustomID: fmt.Sprintf("paginate_sources_%s_%d", messageID, page-1),
					Disabled: page == 0,
				},
				discordgo.Button{
					Label:    "Next",
					Style:    discordgo.PrimaryButton,
					CustomID: fmt.Sprintf("paginate_sources_%s_%d", messageID, page+1),
					Disabled: page == pageCount-1,
				},
			},
		},
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

// getFullResponseContent traverses the reply chain to reconstruct the full
// content of a potentially multi-part bot response.
func (b *Bot) getFullResponseContent(s *discordgo.Session, channelID, messageID string) (string, error) {
	var contentParts []string
	currentMessageID := messageID

	for currentMessageID != "" {
		msg, err := s.ChannelMessage(channelID, currentMessageID)
		if err != nil {
			// If we can't fetch a message, we stop traversing.
			// This can happen if a message is deleted or we lose permissions.
			log.Printf("Error fetching message %s: %v. Returning assembled content so far.", currentMessageID, err)
			break
		}

		// Extract content from the current message's embed
		var currentContent string
		if len(msg.Embeds) > 0 && msg.Embeds[0].Description != "" {
			currentContent = msg.Embeds[0].Description
		} else {
			currentContent = msg.Content
		}

		// Prepend the content to our parts slice
		if currentContent != "" {
			contentParts = append([]string{currentContent}, contentParts...)
		}

		// Check if this message is a reply and if the author of the replied-to message is the bot
		if msg.MessageReference != nil && msg.MessageReference.MessageID != "" {
			refMsg, err := s.ChannelMessage(channelID, msg.MessageReference.MessageID)
			if err != nil {
				log.Printf("Error fetching referenced message %s: %v. Stopping traversal.", msg.MessageReference.MessageID, err)
				break // Stop if we can't get the parent
			}

			// We only continue up the chain if the parent message is also from our bot.
			// This prevents us from accidentally including user messages in the download.
			if refMsg.Author.ID == s.State.User.ID {
				currentMessageID = refMsg.ID
			} else {
				currentMessageID = "" // Stop traversal
			}
		} else {
			currentMessageID = "" // Stop traversal
		}
	}

	if len(contentParts) == 0 {
		return "", fmt.Errorf("no content could be retrieved for message %s", messageID)
	}

	return strings.Join(contentParts, "\n\n"), nil
}

// handleRetry handles the retry button clicks
func (b *Bot) handleRetry(s *discordgo.Session, i *discordgo.InteractionCreate, withWebSearch bool) {
	customID := i.MessageComponentData().CustomID
	parts := strings.Split(customID, "_")
	if len(parts) < 4 {
		log.Printf("Invalid retry button CustomID: %s", customID)
		return
	}
	originalBotResponseID := parts[len(parts)-1]

	// 1. Acknowledge the interaction immediately
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		log.Printf("Failed to send deferred response for retry: %v", err)
		return
	}

	// 2. Find the original user message that triggered the bot's response
	node, exists := b.nodeManager.Get(originalBotResponseID)
	if !exists || node.ParentMsg == nil {
		// Node not in memory or is incomplete, try fetching from persistent cache
		if b.messageCache != nil {
			dbNode, err := b.messageCache.GetNode(context.Background(), originalBotResponseID)
			if err != nil {
				log.Printf("Error fetching node from DB for retry: %v", err)
				// Do not expose DB errors to user
			}
			if dbNode != nil {
				// Node found in DB, but ParentMsg is not stored. We need to fetch it.
				if i.Message.MessageReference != nil && i.Message.MessageReference.MessageID != "" {
					parentMsg, err := s.ChannelMessage(i.ChannelID, i.Message.MessageReference.MessageID)
					if err != nil {
						log.Printf("Could not fetch original user message (%s) for retry: %v", i.Message.MessageReference.MessageID, err)
					} else {
						dbNode.ParentMsg = parentMsg
						node = dbNode
						exists = true
						// Put the now-complete node back into the in-memory manager
						b.nodeManager.Set(originalBotResponseID, node)
					}
				}
			}
		}
	}

	// Final check after attempting to recover from cache
	if !exists || node.ParentMsg == nil {
		log.Printf("Could not find or reconstruct original message for bot response ID: %s", originalBotResponseID)
		errorContent := "❌ Could not find the original message to retry. It might be too old."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &errorContent,
		})
		return
	}
	originalUserMessage := node.ParentMsg

	// 3. Create a new, synthetic message object to re-process
	syntheticMsg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:              fmt.Sprintf("%d", time.Now().UnixNano()), // Unique ID for this retry
			ChannelID:       originalUserMessage.ChannelID,
			GuildID:         originalUserMessage.GuildID,
			Content:         originalUserMessage.Content,
			Author:          originalUserMessage.Author,
			Attachments:     originalUserMessage.Attachments,
			Embeds:          originalUserMessage.Embeds,
			Mentions:        originalUserMessage.Mentions,
			MentionRoles:    originalUserMessage.MentionRoles,
			MentionEveryone: originalUserMessage.MentionEveryone,
			Timestamp:       time.Now(),
			// Set the reference to the original user message to maintain conversation context
			MessageReference: originalUserMessage.Reference(),
		},
	}

	// 4. Modify content based on web search choice
	if !withWebSearch {
		// Prepend a directive to skip the web search decider LLM call
		syntheticMsg.Content = "SKIP_WEB_SEARCH_DECIDER\n\n" + originalUserMessage.Content
	} else {
		// Append a directive to force web search
		syntheticMsg.Content = originalUserMessage.Content + "\n\nSEARCH THE NET"
	}

	// 5. Submit the synthetic message to the processing queue
	select {
	case b.messageJobs <- syntheticMsg:
		// Job successfully submitted
		successContent := "✅ Your request has been resubmitted for processing."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &successContent,
		})
	default:
		// Pool is busy
		errorContent := "❌ The message processing pool is currently full. Please try again in a moment."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &errorContent,
		})
		log.Printf("Message processing pool is full. Dropping retry request from user %s", i.Member.User.ID)
	}
}
