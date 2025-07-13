package utils

import (
	"DiscordAIChatbot/internal/messaging"
	"log"

	"github.com/pkoukk/tiktoken-go"
)

// DefaultTokenLimit provides a conservative context window size for most 16k models.
const DefaultTokenLimit = 128000

// EstimateTokenCount provides accurate token count of a slice of messages using tiktoken.
// Always uses o200k_base encoding regardless of the model used.
func EstimateTokenCount(msgs []messaging.OpenAIMessage) int {
	// Get o200k_base encoding
	tke, err := tiktoken.GetEncoding("o200k_base")
	if err != nil {
		log.Printf("Warning: failed to get o200k_base encoding: %v, falling back to rough estimate", err)
		// Fallback to rough estimation if tiktoken fails
		totalChars := 0
		for _, msg := range msgs {
			switch c := msg.Content.(type) {
			case string:
				totalChars += len(c)
			case []messaging.MessageContent:
				for _, part := range c {
					if part.Type == "text" {
						totalChars += len(part.Text)
					}
				}
			}
		}
		return totalChars / 4 // rough fallback
	}

	totalTokens := 0
	
	// Count tokens for each message
	for _, msg := range msgs {
		// Add tokens per message overhead (approximate)
		totalTokens += 3 // message formatting overhead
		
		// Add role tokens
		if msg.Role != "" {
			roleTokens := tke.Encode(msg.Role, nil, nil)
			totalTokens += len(roleTokens)
		}
		
		// Add content tokens
		switch c := msg.Content.(type) {
		case string:
			if c != "" {
				contentTokens := tke.Encode(c, nil, nil)
				totalTokens += len(contentTokens)
			}
		case []messaging.MessageContent:
			for _, part := range c {
				if part.Type == "text" && part.Text != "" {
					textTokens := tke.Encode(part.Text, nil, nil)
					totalTokens += len(textTokens)
				}
			}
		}
	}
	
	// Add reply priming overhead
	totalTokens += 3
	
	return totalTokens
}

// EstimateTokenCountFromText provides accurate token count for plain text using tiktoken.
// Always uses o200k_base encoding regardless of the model used.
func EstimateTokenCountFromText(text string) int {
	// Get o200k_base encoding
	tke, err := tiktoken.GetEncoding("o200k_base")
	if err != nil {
		log.Printf("Warning: failed to get o200k_base encoding: %v, falling back to rough estimate", err)
		// Fallback to rough estimation if tiktoken fails
		return len(text) / 4
	}

	tokens := tke.Encode(text, nil, nil)
	return len(tokens)
} 