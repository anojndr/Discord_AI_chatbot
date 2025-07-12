package processors

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/utils"
)

// ChannelProcessor handles fetching and processing channel messages
type ChannelProcessor struct{}

// NewChannelProcessor creates a new channel processor
func NewChannelProcessor() *ChannelProcessor {
	return &ChannelProcessor{}
}

// ChannelResult contains the results of channel message fetching
type ChannelResult struct {
	Messages         []messaging.OpenAIMessage
	UserMessageCounts map[string]int
	TotalMessages    int
	TotalTokens      int
}

// FetchChannelMessages fetches messages from a Discord channel, respecting token limits
// It fetches from newest to oldest, excluding bot messages, and ensures the total
// (user query + channel messages) fits within the specified threshold of the provided token limit
func (cp *ChannelProcessor) FetchChannelMessages(ctx context.Context, session *discordgo.Session, channelID string, userQuery string, botUserID string, modelTokenLimit int, tokenThreshold float64) (*ChannelResult, error) {
	// Calculate threshold percentage of the model's token limit
	maxTokens := int(float64(modelTokenLimit) * tokenThreshold)
	
	// Estimate tokens for user query
	userQueryTokens := len(userQuery) / 4 // charsPerToken constant from utils
	if userQueryTokens >= maxTokens {
		return nil, fmt.Errorf("user query is too long (%d tokens), exceeds %.0f%% of token limit (%d tokens)", userQueryTokens, tokenThreshold*100, maxTokens)
	}
	
	// Available tokens for channel messages
	availableTokens := maxTokens - userQueryTokens
	
	var allMessages []messaging.OpenAIMessage
	var totalTokens int
	var beforeID string
	batchNum := 0
	userMessageCounts := make(map[string]int) // Track messages per user
	
	log.Printf("Starting channel message fetch with %d available tokens", availableTokens)
	
	// Fetch messages in batches
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		
		batchNum++
		
		// Fetch up to 100 messages per request (Discord's limit)
		messages, err := session.ChannelMessages(channelID, 100, beforeID, "", "")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch channel messages: %w", err)
		}
		
		// No more messages
		if len(messages) == 0 {
			log.Printf("No more messages available after batch %d", batchNum)
			break
		}
		
		// Process messages from newest to oldest (Discord's order)
		// Reverse the batch to process from oldest to newest for efficiency
		for i := len(messages) - 1; i >= 0; i-- {
			msg := messages[i]
			
			// Skip bot messages
			if msg.Author.Bot {
				continue
			}
			
			// Convert to OpenAI message format
			openAIMsg := cp.convertToOpenAIMessage(msg)
			
			// Estimate tokens for this message
			msgTokens := utils.EstimateTokenCount([]messaging.OpenAIMessage{openAIMsg})
			
			// Check if adding this message would exceed our token limit
			if totalTokens+msgTokens > availableTokens {
				log.Printf("Reached token limit during batch %d. Stopping at %d total tokens (limit: %d)", batchNum, totalTokens, availableTokens)
				return &ChannelResult{
					Messages:         allMessages,
					UserMessageCounts: userMessageCounts,
					TotalMessages:    len(allMessages),
					TotalTokens:      totalTokens,
				}, nil
			}
			
			// Add message to our collection (append for chronological order)
			allMessages = append(allMessages, openAIMsg)
			totalTokens += msgTokens
			
			// Track user message count
			username := msg.Author.Username
			if msg.Author.GlobalName != "" {
				username = msg.Author.GlobalName
			}
			userMessageCounts[username]++
		}
		
		// Set beforeID for next batch (last message in current batch)
		beforeID = messages[len(messages)-1].ID
		
		log.Printf("Processed batch %d: %d messages fetched, total tokens: %d", batchNum, len(messages), totalTokens)
	}
	
	log.Printf("Channel message fetch complete. Total messages: %d, total tokens: %d", len(allMessages), totalTokens)
	return &ChannelResult{
		Messages:         allMessages,
		UserMessageCounts: userMessageCounts,
		TotalMessages:    len(allMessages),
		TotalTokens:      totalTokens,
	}, nil
}

// convertToOpenAIMessage converts a Discord message to OpenAI message format
func (cp *ChannelProcessor) convertToOpenAIMessage(msg *discordgo.Message) messaging.OpenAIMessage {
	// Clean the message content (remove mentions, etc.)
	content := msg.Content
	
	// Add timestamp and author info for context
	timestamp := msg.Timestamp.Format("2006-01-02 15:04:05")
	username := msg.Author.Username
	if msg.Author.GlobalName != "" {
		username = msg.Author.GlobalName
	}
	
	// Format the message with context
	formattedContent := fmt.Sprintf("[%s] %s: %s", timestamp, username, content)
	
	// Handle attachments
	if len(msg.Attachments) > 0 {
		var attachmentDesc []string
		for _, attachment := range msg.Attachments {
			if strings.HasPrefix(attachment.ContentType, "image/") {
				attachmentDesc = append(attachmentDesc, fmt.Sprintf("[Image: %s]", attachment.Filename))
			} else {
				attachmentDesc = append(attachmentDesc, fmt.Sprintf("[File: %s]", attachment.Filename))
			}
		}
		formattedContent += " " + strings.Join(attachmentDesc, " ")
	}
	
	// Handle embeds
	if len(msg.Embeds) > 0 {
		for _, embed := range msg.Embeds {
			if embed.Title != "" {
				formattedContent += fmt.Sprintf(" [Embed: %s]", embed.Title)
			}
			if embed.Description != "" {
				formattedContent += fmt.Sprintf(" %s", embed.Description)
			}
		}
	}
	
	return messaging.OpenAIMessage{
		Role:    "user",
		Content: formattedContent,
	}
}

// IsAskChannelQuery checks if a query starts with "askchannel"
func IsAskChannelQuery(content string) (bool, string) {
	content = strings.TrimSpace(content)
	lowerContent := strings.ToLower(content)
	
	if strings.HasPrefix(lowerContent, "askchannel ") {
		// Extract the actual query after "askchannel "
		query := strings.TrimSpace(content[11:]) // 11 = len("askchannel ")
		return true, query
	}
	
	return false, ""
}