package context

import (
	"context"
	"fmt"
	"log"

	"DiscordAIChatbot/internal/config"
	"DiscordAIChatbot/internal/llm"
	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/utils"
)

// ContextManager manages conversation context within token limits using summarization
type ContextManager struct {
	summarizer *ContextSummarizer
	config     *config.Config
}

// NewContextManager creates a new context manager
func NewContextManager(llmClient *llm.Client, cfg *config.Config) *ContextManager {
	return &ContextManager{
		summarizer: NewContextSummarizer(llmClient, cfg),
		config:     cfg,
	}
}

// ManageContextResult contains the result of context management
type ManageContextResult struct {
	Messages       []messaging.OpenAIMessage
	TokensUsed     int
	WasSummarized  bool
	WasTruncated   bool
	SummariesCount int
}

// ManageContext processes a conversation to fit within token limits
// It applies summarization and truncation as needed according to the configured thresholds
func (cm *ContextManager) ManageContext(ctx context.Context, messages []messaging.OpenAIMessage, modelName string) (*ManageContextResult, error) {
	// Check if context summarization is enabled
	if !cm.config.GetContextSummarizationEnabled() {
		// Context summarization is disabled, return messages as-is
		tokens := utils.EstimateTokenCount(messages)
		return &ManageContextResult{
			Messages:       messages,
			TokensUsed:     tokens,
			WasSummarized:  false,
			WasTruncated:   false,
			SummariesCount: 0,
		}, nil
	}

	// Get model token limit and trigger threshold
	tokenLimit := cm.config.GetModelTokenLimit(modelName)
	triggerThreshold := cm.config.GetContextSummarizationTriggerThreshold()
	triggerTokenLimit := int(float64(tokenLimit) * triggerThreshold)

	// Calculate current token usage
	currentTokens := utils.EstimateTokenCount(messages)
	
	log.Printf("Context management: %d tokens, limit: %d, trigger at: %d (%.1f%%)", 
		currentTokens, tokenLimit, triggerTokenLimit, triggerThreshold*100)

	// If we're under the trigger threshold, no action needed
	if currentTokens <= triggerTokenLimit {
		return &ManageContextResult{
			Messages:       messages,
			TokensUsed:     currentTokens,
			WasSummarized:  false,
			WasTruncated:   false,
			SummariesCount: 0,
		}, nil
	}

	log.Printf("Token limit threshold exceeded, starting context management")

	// Separate system messages from conversation messages
	systemMessages, conversationMessages := cm.separateSystemMessages(messages)
	
	// Calculate tokens used by system messages
	systemTokens := utils.EstimateTokenCount(systemMessages)
	availableTokens := triggerTokenLimit - systemTokens
	
	if availableTokens <= 0 {
		return nil, fmt.Errorf("system messages alone exceed token budget")
	}

	// Apply context management to conversation messages
	managedMessages, wasSummarized, wasTruncated, summariesCount, err := cm.manageConversationMessages(ctx, conversationMessages, availableTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to manage conversation context: %w", err)
	}

	// Combine system messages with managed conversation messages
	finalMessages := append(systemMessages, managedMessages...)
	finalTokens := utils.EstimateTokenCount(finalMessages)

	log.Printf("Context management complete: %d → %d tokens (summarized: %v, truncated: %v, summaries: %d)", 
		currentTokens, finalTokens, wasSummarized, wasTruncated, summariesCount)

	return &ManageContextResult{
		Messages:       finalMessages,
		TokensUsed:     finalTokens,
		WasSummarized:  wasSummarized,
		WasTruncated:   wasTruncated,
		SummariesCount: summariesCount,
	}, nil
}

// separateSystemMessages separates system messages from conversation messages
func (cm *ContextManager) separateSystemMessages(messages []messaging.OpenAIMessage) ([]messaging.OpenAIMessage, []messaging.OpenAIMessage) {
	var systemMessages []messaging.OpenAIMessage
	var conversationMessages []messaging.OpenAIMessage

	for _, msg := range messages {
		if msg.Role == "system" {
			systemMessages = append(systemMessages, msg)
		} else {
			conversationMessages = append(conversationMessages, msg)
		}
	}

	return systemMessages, conversationMessages
}

// manageConversationMessages applies summarization and truncation to conversation messages
func (cm *ContextManager) manageConversationMessages(ctx context.Context, messages []messaging.OpenAIMessage, availableTokens int) ([]messaging.OpenAIMessage, bool, bool, int, error) {
	if len(messages) == 0 {
		return messages, false, false, 0, nil
	}

	// Identify conversation pairs
	pairs := cm.summarizer.IdentifyConversationPairs(messages)
	
	if len(pairs) == 0 {
		// No pairs to summarize, check if we need to truncate
		currentTokens := utils.EstimateTokenCount(messages)
		if currentTokens <= availableTokens {
			return messages, false, false, 0, nil
		}
		
		// Try to truncate if the last message is a user message
		truncatedMessages, wasTruncated, _, err := cm.handleTruncation(messages, availableTokens)
		return truncatedMessages, false, wasTruncated, 0, err
	}

	// Apply progressive summarization
	managedMessages, summariesCount, err := cm.applySummarization(ctx, messages, pairs, availableTokens)
	if err != nil {
		return nil, false, false, 0, err
	}

	wasSummarized := summariesCount > 0

	// Check if we still exceed the limit after summarization
	finalTokens := utils.EstimateTokenCount(managedMessages)
	if finalTokens <= availableTokens {
		return managedMessages, wasSummarized, false, summariesCount, nil
	}

	// Still over limit, try truncation as last resort
	truncatedMessages, wasTruncated, _, err := cm.handleTruncation(managedMessages, availableTokens)
	if err != nil {
		return nil, wasSummarized, false, summariesCount, err
	}

	return truncatedMessages, wasSummarized, wasTruncated, summariesCount, nil
}

// applySummarization progressively summarizes conversation pairs until we fit within the token budget
func (cm *ContextManager) applySummarization(ctx context.Context, messages []messaging.OpenAIMessage, pairs []ConversationPair, availableTokens int) ([]messaging.OpenAIMessage, int, error) {
	maxPairsPerBatch := cm.config.GetContextSummarizationMaxPairsPerBatch()
	minUnsummarizedPairs := cm.config.GetContextSummarizationMinUnsummarizedPairs()
	
	currentMessages := make([]messaging.OpenAIMessage, len(messages))
	copy(currentMessages, messages)
	
	summariesCount := 0
	currentPairs := pairs

	for {
		// Check current token usage
		currentTokens := utils.EstimateTokenCount(currentMessages)
		if currentTokens <= availableTokens {
			// We fit within the budget
			break
		}

		// Check if we have enough pairs to summarize
		if len(currentPairs) <= minUnsummarizedPairs {
			log.Printf("Cannot summarize further: only %d pairs left (minimum: %d)", len(currentPairs), minUnsummarizedPairs)
			break
		}

		// Determine how many pairs to summarize in this batch
		pairsToSummarize := maxPairsPerBatch
		maxPossiblePairs := len(currentPairs) - minUnsummarizedPairs
		if pairsToSummarize > maxPossiblePairs {
			pairsToSummarize = maxPossiblePairs
		}

		if pairsToSummarize <= 0 {
			break
		}

		// Summarize the oldest pairs
		pairsForSummary := currentPairs[:pairsToSummarize]
		
		log.Printf("Summarizing %d conversation pairs to reduce token usage", len(pairsForSummary))
		
		summaryResult, err := cm.summarizer.SummarizePairs(ctx, pairsForSummary)
		if err != nil {
			return nil, summariesCount, fmt.Errorf("failed to summarize conversation pairs: %w", err)
		}

		// Replace the summarized pairs with the summary
		newMessages := []messaging.OpenAIMessage{summaryResult.SummaryMessage}
		
		// Add remaining unsummarized messages
		remainingPairs := currentPairs[pairsToSummarize:]
		for _, pair := range remainingPairs {
			newMessages = append(newMessages, pair.UserMessage, pair.AssistantMessage)
		}

		// Add any non-paired messages at the end (e.g., a user message without an assistant response)
		nonPairedMessages := cm.findNonPairedMessages(messages, pairs)
		newMessages = append(newMessages, nonPairedMessages...)

		currentMessages = newMessages
		currentPairs = remainingPairs
		summariesCount++

		log.Printf("Summarization batch complete: %d pairs summarized, %d pairs remaining", pairsToSummarize, len(remainingPairs))
	}

	return currentMessages, summariesCount, nil
}

// findNonPairedMessages finds messages that aren't part of conversation pairs
func (cm *ContextManager) findNonPairedMessages(allMessages []messaging.OpenAIMessage, pairs []ConversationPair) []messaging.OpenAIMessage {
	// Create a map of paired messages for quick lookup
	pairedMessages := make(map[*messaging.OpenAIMessage]bool)
	
	for _, pair := range pairs {
		// Note: This is a simplified approach. In a real implementation,
		// you might want to use message IDs or other unique identifiers
		for i := range allMessages {
			if allMessages[i].Role == pair.UserMessage.Role && 
			   cm.messagesEqual(allMessages[i], pair.UserMessage) {
				pairedMessages[&allMessages[i]] = true
			}
			if allMessages[i].Role == pair.AssistantMessage.Role && 
			   cm.messagesEqual(allMessages[i], pair.AssistantMessage) {
				pairedMessages[&allMessages[i]] = true
			}
		}
	}

	// Find unpaired messages
	var nonPaired []messaging.OpenAIMessage
	for i := range allMessages {
		if !pairedMessages[&allMessages[i]] && allMessages[i].Role != "system" {
			nonPaired = append(nonPaired, allMessages[i])
		}
	}

	return nonPaired
}

// messagesEqual compares two messages for equality (simplified)
func (cm *ContextManager) messagesEqual(msg1, msg2 messaging.OpenAIMessage) bool {
	if msg1.Role != msg2.Role {
		return false
	}
	
	// Simple content comparison
	content1 := fmt.Sprintf("%v", msg1.Content)
	content2 := fmt.Sprintf("%v", msg2.Content)
	return content1 == content2
}

// handleTruncation handles truncation of the latest user query as a last resort
func (cm *ContextManager) handleTruncation(messages []messaging.OpenAIMessage, availableTokens int) ([]messaging.OpenAIMessage, bool, int, error) {
	if len(messages) == 0 {
		return messages, false, 0, nil
	}

	// Find the last user message
	lastUserMessageIndex := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUserMessageIndex = i
			break
		}
	}

	if lastUserMessageIndex == -1 {
		// No user message to truncate
		return messages, false, utils.EstimateTokenCount(messages), nil
	}

	// Calculate tokens used by all messages except the last user message
	messagesWithoutLastUser := make([]messaging.OpenAIMessage, 0, len(messages)-1)
	messagesWithoutLastUser = append(messagesWithoutLastUser, messages[:lastUserMessageIndex]...)
	if lastUserMessageIndex+1 < len(messages) {
		messagesWithoutLastUser = append(messagesWithoutLastUser, messages[lastUserMessageIndex+1:]...)
	}

	tokensWithoutLastUser := utils.EstimateTokenCount(messagesWithoutLastUser)
	availableForLastUser := availableTokens - tokensWithoutLastUser

	if availableForLastUser <= 0 {
		return nil, false, 0, fmt.Errorf("cannot fit conversation even after truncating last user message")
	}

	// Extract content from the last user message
	lastUserMessage := messages[lastUserMessageIndex]
	originalContent := cm.summarizer.extractTextContent(lastUserMessage)
	
	// Truncate the content
	truncatedContent := cm.summarizer.TruncateQuery(originalContent, availableForLastUser)
	
	wasTruncated := truncatedContent != originalContent

	if wasTruncated {
		log.Printf("Truncated last user message: %d → %d tokens", 
			utils.EstimateTokenCountFromText(originalContent),
			utils.EstimateTokenCountFromText(truncatedContent))
	}

	// Create new message with truncated content
	truncatedMessage := messaging.OpenAIMessage{
		Role:    lastUserMessage.Role,
		Content: truncatedContent,
	}

	// Build final message list
	finalMessages := make([]messaging.OpenAIMessage, len(messages))
	copy(finalMessages, messages)
	finalMessages[lastUserMessageIndex] = truncatedMessage

	finalTokens := utils.EstimateTokenCount(finalMessages)
	
	return finalMessages, wasTruncated, finalTokens, nil
}