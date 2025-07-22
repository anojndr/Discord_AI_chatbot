// Package context provides context management and summarization capabilities
// for managing conversation history within token limits.
package context

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"DiscordAIChatbot/internal/config"
	"DiscordAIChatbot/internal/llm"
	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/utils"
)

var builderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
}

// ConversationPair represents a user message and its corresponding assistant response
type ConversationPair struct {
	UserMessage      messaging.OpenAIMessage
	AssistantMessage messaging.OpenAIMessage
	OriginalTokens   int // Token count of the original pair
}

// SummaryResult contains the result of summarizing conversation pairs
type SummaryResult struct {
	SummaryMessage messaging.OpenAIMessage
	OriginalTokens int // Total tokens of the original pairs
	SummaryTokens  int // Tokens in the summary
	TokensSaved    int // How many tokens were saved
}

// ContextSummarizer handles summarization of conversation pairs to manage context length
type ContextSummarizer struct {
	llmClient *llm.Client
	config    *config.Config
}

// NewContextSummarizer creates a new context summarizer
func NewContextSummarizer(llmClient *llm.Client, cfg *config.Config) *ContextSummarizer {
	return &ContextSummarizer{
		llmClient: llmClient,
		config:    cfg,
	}
}

// SummarizePairs summarizes a batch of conversation pairs into a single summary message
func (cs *ContextSummarizer) SummarizePairs(ctx context.Context, pairs []ConversationPair) (*SummaryResult, error) {
	if len(pairs) == 0 {
		return nil, fmt.Errorf("no conversation pairs provided for summarization")
	}

	// Calculate original token count
	originalTokens := 0
	for _, pair := range pairs {
		originalTokens += pair.OriginalTokens
	}

	// Build the summarization prompt
	conversationText := cs.buildConversationText(pairs)
	summarizationPrompt := cs.buildSummarizationPrompt(conversationText)

	// Prepare messages for the summarization LLM
	summarizationMessages := []messaging.OpenAIMessage{
		{
			Role:    "system",
			Content: "You are a helpful assistant that creates concise summaries of conversations while preserving key information and context.",
		},
		{
			Role:    "user",
			Content: summarizationPrompt,
		},
	}

	// Use the configured summarization model
	summarizationModel := cs.config.GetContextSummarizationModel()
	
	log.Printf("Summarizing %d conversation pairs (%d tokens) using model %s", len(pairs), originalTokens, summarizationModel)

	// Get summary from LLM with fallback
	response, fallbackResult, err := cs.llmClient.GetChatCompletionWithFallback(ctx, summarizationMessages, summarizationModel)
	if err != nil {
		return nil, fmt.Errorf("failed to get summarization from LLM (original and fallback models failed): %w", err)
	}
	
	// Log if fallback was used
	if fallbackResult.UsedFallback {
		log.Printf("Context summarization: Using fallback model %s (original model %s failed)", fallbackResult.FallbackModel, summarizationModel)
	}

	// Extract summary content
	summaryContent := strings.TrimSpace(response)
	if summaryContent == "" {
		return nil, fmt.Errorf("received empty summary from LLM")
	}

	// Create summary message
	summaryMessage := messaging.OpenAIMessage{
		Role:    "system",
		Content: fmt.Sprintf("[Summary of earlier conversation: %s]", summaryContent),
	}

	// Calculate summary tokens
	summaryTokens := utils.EstimateTokenCount([]messaging.OpenAIMessage{summaryMessage})
	tokensSaved := originalTokens - summaryTokens

	log.Printf("Summarization complete: %d original tokens → %d summary tokens (saved %d tokens)", originalTokens, summaryTokens, tokensSaved)

	return &SummaryResult{
		SummaryMessage: summaryMessage,
		OriginalTokens: originalTokens,
		SummaryTokens:  summaryTokens,
		TokensSaved:    tokensSaved,
	}, nil
}

// buildConversationText converts conversation pairs into a readable text format for summarization
func (cs *ContextSummarizer) buildConversationText(pairs []ConversationPair) string {
	builder := builderPool.Get().(*strings.Builder)
	defer func() {
		builder.Reset()
		builderPool.Put(builder)
	}()
	
	for i, pair := range pairs {
		if i > 0 {
			builder.WriteString("\n\n")
		}
		
		// Add user message
		builder.WriteString("User: ")
		builder.WriteString(cs.extractTextContent(pair.UserMessage))
		builder.WriteString("\n\n")
		
		// Add assistant message
		builder.WriteString("Assistant: ")
		builder.WriteString(cs.extractTextContent(pair.AssistantMessage))
	}
	
	return builder.String()
}

// extractTextContent extracts text content from a message, handling both string and structured content
func (cs *ContextSummarizer) extractTextContent(message messaging.OpenAIMessage) string {
	switch content := message.Content.(type) {
	case string:
		return content
	case []messaging.MessageContent:
		var textParts []string
		for _, part := range content {
			switch part.Type {
			case "text":
				textParts = append(textParts, part.Text)
			case "image_url":
				textParts = append(textParts, "[Image attachment]")
			}
		}
		return strings.Join(textParts, " ")
	default:
		return fmt.Sprintf("%v", content)
	}
}

// buildSummarizationPrompt creates the prompt for summarizing conversation pairs
func (cs *ContextSummarizer) buildSummarizationPrompt(conversationText string) string {
	return fmt.Sprintf(`Please create a concise summary of the following conversation excerpt. The summary should:

1. Preserve key information, decisions, and context that would be important for continuing the conversation
2. Maintain the chronological flow of the discussion
3. Include specific details that the user or assistant referenced
4. Be significantly shorter than the original while retaining essential meaning
5. Focus on the main topics, questions asked, and answers provided

Conversation to summarize:
%s

Please provide a clear, concise summary that captures the essential points of this conversation:`, conversationText)
}

// IdentifyConversationPairs analyzes a conversation history and identifies user-assistant pairs
// Returns pairs in chronological order (oldest first)
func (cs *ContextSummarizer) IdentifyConversationPairs(messages []messaging.OpenAIMessage) []ConversationPair {
	var pairs []ConversationPair
	var currentUserMsg *messaging.OpenAIMessage
	
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			// If we had a previous user message without an assistant response, skip it
			// Store the current user message
			currentUserMsg = &msg
			
		case "assistant":
			// If we have a user message, create a pair
			if currentUserMsg != nil {
				pair := ConversationPair{
					UserMessage:      *currentUserMsg,
					AssistantMessage: msg,
				}
				// Calculate token count for this pair
				pairMessages := []messaging.OpenAIMessage{*currentUserMsg, msg}
				pair.OriginalTokens = utils.EstimateTokenCount(pairMessages)
				
				pairs = append(pairs, pair)
				currentUserMsg = nil // Reset
			}
			
		case "system":
			// System messages don't participate in user-assistant pairs
			// but we don't want to break the pairing logic
			continue
		}
	}
	
	return pairs
}

// TruncateQuery truncates a user query to fit within the remaining token budget
func (cs *ContextSummarizer) TruncateQuery(query string, maxTokens int) string {
	queryTokens := utils.EstimateTokenCountFromText(query)
	
	if queryTokens <= maxTokens {
		return query // No truncation needed
	}
	
	log.Printf("Truncating query from %d tokens to fit %d token limit", queryTokens, maxTokens)
	
	// Calculate the ratio of tokens to characters (rough estimate)
	chars := len(query)
	if chars == 0 {
		return query
	}
	
	tokensPerChar := float64(queryTokens) / float64(chars)
	targetChars := int(float64(maxTokens) / tokensPerChar)
	
	// Leave some buffer for the truncation notice
	truncationNotice := "... (truncated)"
	bufferChars := len(truncationNotice) + 10
	
	if targetChars <= bufferChars {
		// Query is too short to truncate meaningfully
		return truncationNotice
	}
	
	targetChars -= bufferChars
	
	// Truncate at word boundary if possible
	if targetChars < chars {
		truncated := query[:targetChars]
		
		// Try to truncate at the last space to avoid cutting words
		if lastSpace := strings.LastIndex(truncated, " "); lastSpace > targetChars/2 {
			truncated = truncated[:lastSpace]
		}
		
		return truncated + truncationNotice
	}
	
	return query
}