package bot

import (
    "context"
    "fmt"
    "log"
    "strings"
    "time"

    "DiscordAIChatbot/internal/messaging"
)

// summarizeMessages sends a small set of conversation messages to the LLM and returns a concise summary.
// It streams the completion internally but returns the final aggregated text.
func (b *Bot) summarizeMessages(ctx context.Context, msgs []messaging.OpenAIMessage) (string, error) {
    // Thread-safe access to config to pick a model. We reuse the default model configured.
    b.mu.RLock()
    model := b.config.Context.SummarizerModel
    if model == "" {
        model = b.config.GetDefaultModel()
    }
    b.mu.RUnlock()

    // Build prompt: system primer + original messages + summarization request.
    prompt := []messaging.OpenAIMessage{{
        Role: "system",
        Content: `You are an expert conversation summarizer for a Discord AI chatbot. Your task is to create concise, informative summaries that preserve the most important context and decisions from conversations.

Guidelines:
- Focus on key topics, decisions, and actionable items
- Preserve important technical details, code snippets, or specific instructions
- Maintain the chronological flow of the conversation
- Use clear, structured language with bullet points when appropriate
- Keep summaries under 200 words while capturing essential information
- Note any unresolved questions or pending tasks
- Include usernames when context is important for understanding

Format your summary to be easily scannable and useful for continuing the conversation.`,
    }}
    prompt = append(prompt, msgs...)
    prompt = append(prompt, messaging.OpenAIMessage{
        Role:    "user",
        Content: "Please provide a structured summary of this conversation segment, highlighting the main topics discussed, any decisions made, technical details shared, and any unresolved items that may need follow-up. /no_think",
    })

    // Use a reasonably short timeout because context windows are small.
    ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
    defer cancel()

    return b.summarizeWithRetry(ctx, model, prompt)
}

// summarizeWithRetry implements retry logic for the summarizer model with exponential backoff
func (b *Bot) summarizeWithRetry(ctx context.Context, model string, prompt []messaging.OpenAIMessage) (string, error) {
    const maxRetries = 3
    const baseDelay = 1 * time.Second

    var lastError error

    for attempt := 0; attempt < maxRetries; attempt++ {
        stream, err := b.llmClient.StreamChatCompletion(ctx, model, prompt)
        if err != nil {
            lastError = err
            
            if attempt == maxRetries-1 {
                log.Printf("Summarizer failed after %d attempts: %v", maxRetries, err)
                return "", fmt.Errorf("summarizer failed after %d retries: %w", maxRetries, err)
            }

            if b.shouldRetryError(err) {
                delay := baseDelay * time.Duration(1<<attempt)
                log.Printf("Summarizer error (attempt %d/%d), retrying in %v: %v", 
                    attempt+1, maxRetries, delay, err)
                
                select {
                case <-ctx.Done():
                    return "", ctx.Err()
                case <-time.After(delay):
                    continue
                }
            } else {
                return "", fmt.Errorf("summarizer failed with non-retryable error: %w", err)
            }
        }

        var summary string
        streamErr := false
        
        for chunk := range stream {
            if chunk.Error != nil {
                lastError = chunk.Error
                streamErr = true
                break
            }
            if chunk.Content != "" {
                summary += chunk.Content
            }
        }

        if !streamErr {
            return summary, nil
        }

        if attempt == maxRetries-1 {
            log.Printf("Summarizer stream failed after %d attempts: %v", maxRetries, lastError)
            return "", fmt.Errorf("summarizer stream failed after %d retries: %w", maxRetries, lastError)
        }

        if b.shouldRetryError(lastError) {
            delay := baseDelay * time.Duration(1<<attempt)
            log.Printf("Summarizer stream error (attempt %d/%d), retrying in %v: %v", 
                attempt+1, maxRetries, delay, lastError)
            
            select {
            case <-ctx.Done():
                return "", ctx.Err()
            case <-time.After(delay):
                continue
            }
        } else {
            return "", fmt.Errorf("summarizer stream failed with non-retryable error: %w", lastError)
        }
    }

    return "", fmt.Errorf("summarizer failed after %d retries: %w", maxRetries, lastError)
}

// shouldRetryError determines if an error is retryable for the summarizer
func (b *Bot) shouldRetryError(err error) bool {
    if err == nil {
        return false
    }

    errStr := strings.ToLower(err.Error())

    retryablePatterns := []string{
        "503",
        "service unavailable", 
        "model is overloaded",
        "server is overloaded",
        "try again later",
        "timeout",
        "connection reset",
        "connection refused",
        "temporary failure",
        "rate limit",
        "too many requests",
        "502",
        "bad gateway",
        "504",
        "gateway timeout",
    }

    for _, pattern := range retryablePatterns {
        if strings.Contains(errStr, pattern) {
            return true
        }
    }

    return false
} 