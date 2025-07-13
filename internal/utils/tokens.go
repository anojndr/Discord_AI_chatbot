package utils

import (
	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/config"
)

// DefaultTokenLimit provides a conservative context window size for most 16k models.
const DefaultTokenLimit = 128000

// EstimateTokenCount provides a rough token count of a slice of messages.
// It counts characters in textual content and divides by the configured charsPerToken ratio.
// This is a heuristic but sufficient for window management.
func EstimateTokenCount(msgs []messaging.OpenAIMessage, cfg *config.Config) int {
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
    // add some overhead per message
    totalChars += len(msgs) * 20

    return totalChars / cfg.GetCharsPerToken()
} 