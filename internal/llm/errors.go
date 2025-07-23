package llm

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"DiscordAIChatbot/internal/llm/providers"
)

// isAPIKeyError checks if the error is related to API key authentication/authorization
func (c *LLMClient) isAPIKeyError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// 500 errors are often server-side issues, not API key issues
	// Only treat as API key error if specifically mentioned
	if strings.Contains(errStr, "500") {
		// Only consider 500 as API key error if it explicitly mentions key/auth issues
		return strings.Contains(errStr, "api key") ||
			strings.Contains(errStr, "authentication") ||
			strings.Contains(errStr, "unauthorized")
	}

	// Common API key error patterns
	apiKeyErrorPatterns := []string{
		"invalid api key",
		"unauthorized",
		"authentication",
		"invalid authentication",
		"incorrect api key",
		"invalid token",
		"authentication failed",
		"api key",
		"401",
		"403",
		"quota exceeded",
		"rate limit",
		"insufficient funds",
		"billing",
		"billed users",
	}

	for _, pattern := range apiKeyErrorPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// is503Error checks if the error is a 503 Service Unavailable error
func (c *LLMClient) is503Error(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Check for 503 status code patterns
	return strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "service unavailable") ||
		strings.Contains(errStr, "model is overloaded") ||
		strings.Contains(errStr, "server is overloaded") ||
		strings.Contains(errStr, "try again later")
}

// isInternalError checks if the error is an INTERNAL error (500) that should be retried
func (c *LLMClient) isInternalError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Check for INTERNAL error patterns from Gemini API
	return strings.Contains(errStr, "error 500") ||
		strings.Contains(errStr, "status: internal") ||
		strings.Contains(errStr, "an internal error has occurred") ||
		strings.Contains(errStr, "internal error") ||
		(strings.Contains(errStr, "500") && strings.Contains(errStr, "internal"))
}

// ShouldFallback checks if the given error warrants a fallback to another model
func (c *LLMClient) ShouldFallback(err error) bool {
	if err == nil {
		return false
	}

	// Check for our custom premature stream finish error from the providers package
	if _, ok := err.(*providers.PrematureStreamFinishError); ok {
		return true
	}

	errStr := strings.ToLower(err.Error())

	// List of error substrings that should trigger a fallback
	// Based on Gemini and OpenAI documentation for server-side/transient issues
	fallbackErrorPatterns := []string{
		// Gemini Errors
		"resource_exhausted", // 429
		"internal",           // 500
		"unavailable",        // 503

		// OpenAI Errors
		"rate limit",             // 429
		"server had an error",    // 500
		"engine is currently overloaded", // 503
		"apiconnectionerror",     // Python library error
		"apitimeouterror",        // Python library error
		"internalservererror",    // Python library error
	}

	for _, pattern := range fallbackErrorPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// retryWith503Backoff performs exponential backoff retry for 503 errors
func (c *LLMClient) retryWith503Backoff(ctx context.Context, retryFunc func() error) error {
	const maxRetries = 3
	const baseDelay = 1 * time.Second

	for attempt := 0; attempt < maxRetries; attempt++ {
		err := retryFunc()

		// If no error or not a 503 error, return immediately
		if err == nil || !c.is503Error(err) {
			return err
		}

		// If this is the last attempt, return the error
		if attempt == maxRetries-1 {
			return err
		}

		// Calculate exponential backoff delay: 1s, 2s, 4s
		delay := baseDelay * time.Duration(1<<attempt)

		log.Printf("503 error detected (attempt %d/%d), retrying in %v: %v",
			attempt+1, maxRetries, delay, err)

		// Wait with context cancellation support
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	// This should never be reached, but just in case
	return fmt.Errorf("maximum retries exceeded for 503 errors")
}

// retryWithInternalBackoff performs exponential backoff retry for INTERNAL errors (500)
func (c *LLMClient) retryWithInternalBackoff(ctx context.Context, retryFunc func() error) error {
	const maxRetries = 3
	const baseDelay = 2 * time.Second // Slightly longer delay for internal errors

	for attempt := 0; attempt < maxRetries; attempt++ {
		err := retryFunc()

		// If no error or not an internal error, return immediately
		if err == nil || !c.isInternalError(err) {
			return err
		}

		// If this is the last attempt, return the error
		if attempt == maxRetries-1 {
			return err
		}

		// Calculate exponential backoff delay: 2s, 4s, 8s
		delay := baseDelay * time.Duration(1<<attempt)

		log.Printf("INTERNAL error detected (attempt %d/%d), retrying in %v: %v",
			attempt+1, maxRetries, delay, err)

		// Wait with context cancellation support
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	// This should never be reached, but just in case
	return fmt.Errorf("maximum retries exceeded for INTERNAL errors")
}