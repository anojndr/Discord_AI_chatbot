package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"

	"DiscordAIChatbot/internal/config"
	"DiscordAIChatbot/internal/logging"
	"DiscordAIChatbot/internal/llm/providers"
	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/storage"
)

// ImageCacheEntry represents a cached image
type ImageCacheEntry struct {
	Data     []byte
	MIMEType string
}

// LLMClient handles communication with LLM providers
type LLMClient struct {
	config         *config.Config
	apiKeyManager  *storage.APIKeyManager
	geminiProvider *providers.GeminiProvider
	imageCache     map[string]*ImageCacheEntry
	imageCacheMu   sync.RWMutex
}

// Client is an alias for LLMClient for convenience
type Client = LLMClient

// NewLLMClient creates a new LLM client
func NewLLMClient(cfg *config.Config, apiKeyManager *storage.APIKeyManager) *LLMClient {
	logging.LogToFile("Initializing LLM client")
	return &LLMClient{
		config:         cfg,
		apiKeyManager:  apiKeyManager,
		geminiProvider: providers.NewGeminiProvider(cfg, apiKeyManager),
		imageCache:     make(map[string]*ImageCacheEntry),
	}
}

// isGeminiModel checks if the given model uses the Gemini provider
func (c *LLMClient) isGeminiModel(model string) bool {
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 {
		return false
	}
	return parts[0] == "gemini"
}


// StreamResponse represents a streaming response chunk
type StreamResponse struct {
	Content       string
	FinishReason  string
	Error         error
	ImageData     []byte
	ImageMIMEType string
}

// createGeminiStream creates a streaming chat completion using Gemini
func (c *LLMClient) createGeminiStream(ctx context.Context, model string, messages []messaging.OpenAIMessage) (<-chan StreamResponse, error) {
	stream, err := c.geminiProvider.CreateGeminiStream(ctx, model, messages, c.downloadImageFromURL, c.isAPIKeyError, c.is503Error, c.retryWith503Backoff, c.isInternalError, c.retryWithInternalBackoff)
	if err != nil {
		return nil, err
	}

	// Convert provider.StreamResponse to llm.StreamResponse
	responseChan := make(chan StreamResponse, 10)
	go func() {
		defer close(responseChan)
		for resp := range stream {
			responseChan <- StreamResponse{
				Content:       resp.Content,
				FinishReason:  resp.FinishReason,
				Error:         resp.Error,
				ImageData:     resp.ImageData,
				ImageMIMEType: resp.ImageMIMEType,
			}
		}
	}()

	return responseChan, nil
}


// CreateChatCompletionStream creates a streaming chat completion
func (c *LLMClient) CreateChatCompletionStream(ctx context.Context, model string, messages []messaging.OpenAIMessage) (*openai.ChatCompletionStream, error) {
	// Check if this is a Gemini model
	if c.isGeminiModel(model) {
		return nil, fmt.Errorf("use StreamChatCompletion method for Gemini models")
	}

	// Parse provider and model
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid model format: %s (expected provider/model)", model)
	}

	providerName := parts[0]
	modelName := parts[1]

	// Get provider config
	provider, exists := c.config.Providers[providerName]
	if !exists {
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}

	// Get model parameters
	modelParams, exists := c.config.Models[model]
	if !exists {
		modelParams = config.ModelParams{}
	}

	// Get available API keys for this provider
	availableKeys := provider.GetAPIKeys()
	if len(availableKeys) == 0 {
		return nil, fmt.Errorf("no API keys configured for provider: %s", providerName)
	}

	// Convert messages to OpenAI format
	// System messages are correctly handled as part of the messages array for OpenAI ChatCompletions API
	openaiMessages := make([]openai.ChatCompletionMessage, len(messages))
	for i, msg := range messages {
		openaiMsg := openai.ChatCompletionMessage{
			Role: msg.Role,
			Name: msg.Name,
		}

		// Handle different content types
		switch content := msg.Content.(type) {
		case string:
			openaiMsg.Content = content
		case []messaging.MessageContent:
			// Convert to OpenAI multi-content format
			var parts []openai.ChatMessagePart
			for _, part := range content {
				if part.Type == "text" {
					parts = append(parts, openai.ChatMessagePart{
						Type: openai.ChatMessagePartTypeText,
						Text: part.Text,
					})
				} else if part.Type == "image_url" && part.ImageURL != nil {
					parts = append(parts, openai.ChatMessagePart{
						Type: openai.ChatMessagePartTypeImageURL,
						ImageURL: &openai.ChatMessageImageURL{
							URL: part.ImageURL.URL,
						},
					})
				}
			}
			openaiMsg.MultiContent = parts
		default:
			openaiMsg.Content = fmt.Sprintf("%v", content)
		}

		openaiMessages[i] = openaiMsg
	}

	// Create request
	req := openai.ChatCompletionRequest{
		Model:    modelName,
		Messages: openaiMessages,
		Stream:   true,
	}

	// Apply model-specific parameters
	if modelParams.Temperature != nil {
		req.Temperature = *modelParams.Temperature
	}

	// Handle provider-specific parameters
	if modelParams.ReasoningEffort != "" {
		// This would be handled differently per provider
		// For now, we'll add it as a generic parameter
		// TODO: Implement reasoning effort parameter handling
		log.Printf("ReasoningEffort parameter not yet implemented: %s", modelParams.ReasoningEffort)
	}

	if len(modelParams.SearchParameters) > 0 {
		// Handle search parameters for providers that support it
		// TODO: Implement search parameters handling
		log.Printf("SearchParameters not yet implemented: %v", modelParams.SearchParameters)
	}

	// Log request start
	logging.LogToFile("Starting OpenAI-compatible LLM request: Model=%s, Provider=%s", req.Model, providerName)

	// Debug: Print OpenAI payload
	c.logOpenAIPayload(req, providerName, provider.BaseURL)

	// Try API keys until one works or we run out
	maxRetries := len(availableKeys)
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Get next API key
		apiKey, err := c.apiKeyManager.GetNextAPIKey(providerName, availableKeys)
		if err != nil {
			return nil, fmt.Errorf("failed to get API key: %w", err)
		}

		// Create OpenAI client with current API key
		clientConfig := openai.DefaultConfig(apiKey)
		if provider.BaseURL != "" {
			clientConfig.BaseURL = provider.BaseURL
		}
		client := openai.NewClientWithConfig(clientConfig)

		// Try to create stream with 503 retry mechanism
		var stream *openai.ChatCompletionStream
		err = c.retryWith503Backoff(ctx, func() error {
			var streamErr error
			stream, streamErr = client.CreateChatCompletionStream(ctx, req)
			return streamErr
		})

		if err != nil {
			// Enhanced error reporting
			detailedErr := c.buildDetailedError(err, providerName, provider.BaseURL)

			// Check if this is an API key related error
			if c.isAPIKeyError(err) {
				// Mark this key as bad and try the next one
				markErr := c.apiKeyManager.MarkKeyAsBad(providerName, apiKey, err.Error())
				if markErr != nil {
					// Log the error but continue with the retry
					fmt.Printf("Failed to mark API key as bad: %v\n", markErr)
				}
				fmt.Printf("API key issue detected, trying next key. %v\n", detailedErr)
				continue
			}

			// For non-API key errors, return immediately with detailed error
			return nil, fmt.Errorf("failed to create chat completion stream: %w", detailedErr)
		}

		// Success! Return the stream
		return stream, nil
	}

	return nil, fmt.Errorf("all API keys failed for provider: %s", providerName)
}

// logOpenAIPayload logs the OpenAI API request payload for debugging
func (c *LLMClient) logOpenAIPayload(req openai.ChatCompletionRequest, providerName, baseURL string) {
	logging.LogExternalContentToFile("=== DEBUG: OpenAI-Compatible API Payload ===")
	logging.LogExternalContentToFile("Model: %s", req.Model)
	logging.LogExternalContentToFile("Provider: %s", providerName)
	logging.LogExternalContentToFile("BaseURL: %s", baseURL)
	logging.LogExternalContentToFile("Temperature: %f", req.Temperature)
	logging.LogExternalContentToFile("Stream: %t", req.Stream)
	logging.LogExternalContentToFile("Messages:")
	for i, msg := range req.Messages {
		logging.LogExternalContentToFile("  Message %d [Role: %s]:", i, msg.Role)
		if msg.Name != "" {
			logging.LogExternalContentToFile("    Name: %s", msg.Name)
		}
		if msg.Content != "" {
			// Highlight system messages in the debug output
			if msg.Role == "system" {
				logging.LogExternalContentToFile("    SystemPrompt: %s", msg.Content)
			} else {
				logging.LogExternalContentToFile("    Content: %s", msg.Content)
			}
		} else if len(msg.MultiContent) > 0 {
			logging.LogExternalContentToFile("    MultiContent:")
			for j, part := range msg.MultiContent {
				if part.Type == openai.ChatMessagePartTypeText {
					logging.LogExternalContentToFile("      Part %d [Text]: %s", j, part.Text)
				} else if part.Type == openai.ChatMessagePartTypeImageURL && part.ImageURL != nil {
					logging.LogExternalContentToFile("      Part %d [Image]: %s", j, part.ImageURL.URL)
				}
			}
		}
	}

	// Also print as JSON for complete payload visibility
	if payloadJSON, err := json.MarshalIndent(req, "", "  "); err == nil {
		logging.LogExternalContentToFile("Complete JSON Payload:\n%s", string(payloadJSON))
	}
	logging.LogExternalContentToFile("=== END DEBUG ===\n")
}

// buildDetailedError builds a detailed error message with helpful suggestions
func (c *LLMClient) buildDetailedError(err error, providerName, baseURL string) error {
	errStr := err.Error()

	// Detect common issues and provide helpful suggestions
	if strings.Contains(errStr, "invalid character") && strings.Contains(errStr, "looking for beginning of value") {
		return fmt.Errorf("server returned non-JSON response (likely HTML error page). "+
			"Provider: %s, BaseURL: %s, Error: %w. "+
			"This usually means: 1) The server at %s is down or misconfigured, "+
			"2) Wrong base URL in config, or 3) Server returning HTML error pages instead of JSON. "+
			"If using GitHub Copilot proxy, check proxy logs for upstream API errors",
			providerName, baseURL, err, baseURL)
	} else if strings.Contains(errStr, "Bad Request") || strings.Contains(errStr, "status code: 400") {
		return fmt.Errorf("bad request error from API. "+
			"Provider: %s, BaseURL: %s, Error: %w. "+
			"This usually means: 1) Invalid request parameters, 2) Authentication issues, "+
			"3) Model not available, or 4) Quota/rate limit exceeded",
			providerName, baseURL, err)
	} else if strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "no such host") {
		return fmt.Errorf("cannot connect to server. "+
			"Provider: %s, BaseURL: %s, Error: %w. "+
			"Please check: 1) Is the server running at %s? 2) Is the base URL correct? 3) Network connectivity",
			providerName, baseURL, err, baseURL)
	} else if strings.Contains(errStr, "timeout") {
		return fmt.Errorf("request timeout. "+
			"Provider: %s, BaseURL: %s, Error: %w. "+
			"The server may be overloaded or too slow to respond",
			providerName, baseURL, err)
	} else {
		return fmt.Errorf("request failed. "+
			"Provider: %s, BaseURL: %s, Error: %w",
			providerName, baseURL, err)
	}
}

// StreamChatCompletion streams chat completion responses
func (c *LLMClient) StreamChatCompletion(ctx context.Context, model string, messages []messaging.OpenAIMessage) (<-chan StreamResponse, error) {
	// Check if this is a Gemini model
	if c.isGeminiModel(model) {
		return c.createGeminiStream(ctx, model, messages)
	}

	// Handle OpenAI-compatible models
	stream, err := c.CreateChatCompletionStream(ctx, model, messages)
	if err != nil {
		return nil, err
	}

	responseChan := make(chan StreamResponse, 10)

	go func() {
		defer close(responseChan)
		defer func() {
			if err := stream.Close(); err != nil {
				log.Printf("Failed to close stream: %v", err)
			}
		}()

		for {
			response, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					// Stream finished normally
					responseChan <- StreamResponse{FinishReason: "stop"}
					return
				}

				// Stream error
				responseChan <- StreamResponse{Error: err}
				return
			}

			// Process response
			if len(response.Choices) > 0 {
				choice := response.Choices[0]

				// Log the response content
				if choice.Delta.Content != "" {
					logging.LogExternalContentToFile("OpenAI Response Text: %s", choice.Delta.Content)
				}
				if choice.FinishReason != "" {
					logging.LogExternalContentToFile("OpenAI Response Finish Reason: %s", string(choice.FinishReason))
				}

				streamResp := StreamResponse{
					Content:      choice.Delta.Content,
					FinishReason: string(choice.FinishReason),
				}

				select {
				case responseChan <- streamResp:
				case <-ctx.Done():
					return
				}

				// Check if stream is finished
				if choice.FinishReason != "" {
					return
				}
			}
		}
	}()

	return responseChan, nil
}

// GetChatCompletion gets a complete chat completion response (non-streaming)
// This is useful for summarization and other tasks that need the full response
func (c *LLMClient) GetChatCompletion(ctx context.Context, messages []messaging.OpenAIMessage, model string) (string, error) {
	// Use streaming and collect all chunks
	stream, err := c.StreamChatCompletion(ctx, model, messages)
	if err != nil {
		return "", fmt.Errorf("failed to create stream: %w", err)
	}

	var fullResponse strings.Builder
	for chunk := range stream {
		if chunk.Error != nil {
			return "", fmt.Errorf("stream error: %w", chunk.Error)
		}
		
		if chunk.Content != "" {
			fullResponse.WriteString(chunk.Content)
		}
		
		// Check for completion
		if chunk.FinishReason != "" {
			break
		}
	}

	result := strings.TrimSpace(fullResponse.String())
	if result == "" {
		return "", fmt.Errorf("received empty response from LLM")
	}

	return result, nil
}

// BuildMessages creates OpenAI messages from conversation chain
func (c *LLMClient) BuildMessages(nodes []*messaging.MsgNode, maxImages int, acceptImages, acceptUsernames bool) ([]messaging.OpenAIMessage, []string) {
	var messages []messaging.OpenAIMessage
	var warnings []string

	for _, node := range nodes {
		if node == nil {
			continue
		}

		text := node.GetText()
		images := node.GetImages()
		generatedImages := node.GetGeneratedImages()
		audioFiles := node.GetAudioFiles()

		// No character-based truncation
		truncatedText := text

		// Combine regular images and generated images for counting
		totalImages := len(images) + len(generatedImages)

		// Limit images if needed
		limitedImages := images
		limitedGeneratedImages := generatedImages
		if totalImages > maxImages {
			if maxImages > 0 {
				// Prioritize generated images (they're part of the conversation context)
				if len(generatedImages) <= maxImages {
					limitedGeneratedImages = generatedImages
					remainingSlots := maxImages - len(generatedImages)
					if len(images) <= remainingSlots {
						limitedImages = images
					} else {
						limitedImages = images[:remainingSlots]
					}
				} else {
					limitedGeneratedImages = generatedImages[:maxImages]
					limitedImages = nil
				}

				if maxImages == 1 {
					warnings = append(warnings, "⚠️ Max 1 image per message")
				} else {
					warnings = append(warnings, fmt.Sprintf("⚠️ Max %d images per message", maxImages))
				}
			} else {
				limitedImages = nil
				limitedGeneratedImages = nil
				warnings = append(warnings, "⚠️ Can't see images")
			}
		}

		// Add attachment warnings
		if node.HasBadAttachments {
			warnings = append(warnings, "⚠️ Unsupported attachments")
		}
		if node.FetchParentFailed {
			warnings = append(warnings, "⚠️ Failed to fetch parent message")
		}

		// Skip empty messages
		if truncatedText == "" && len(limitedImages) == 0 && len(limitedGeneratedImages) == 0 && len(audioFiles) == 0 {
			continue
		}

		// Create message
		message := messaging.OpenAIMessage{
			Role: node.Role,
		}

		// Add username if supported
		if acceptUsernames && node.UserID != "" {
			message.Name = node.UserID
		}

		// Set content
		if (acceptImages && (len(limitedImages) > 0 || len(limitedGeneratedImages) > 0)) || len(audioFiles) > 0 {
			// Multi-content format with text, images, and audio
			var content []messaging.MessageContent

			if truncatedText != "" {
				content = append(content, messaging.MessageContent{
					Type: "text",
					Text: truncatedText,
				})
			}

			// Add regular images
			for _, img := range limitedImages {
				content = append(content, messaging.MessageContent{
					Type:     "image_url",
					ImageURL: &img.ImageURL,
				})
			}

			// Add generated images
			for _, genImg := range limitedGeneratedImages {
				content = append(content, messaging.MessageContent{
					Type:           "generated_image",
					GeneratedImage: &genImg,
				})
			}

			// Add audio files
			for _, audio := range audioFiles {
				audio := audio // Capture loop variable
				content = append(content, messaging.MessageContent{
					Type:      "audio_file",
					AudioFile: &audio,
				})
			}

			message.Content = content
		} else {
			// Text-only format
			message.Content = truncatedText
		}

		messages = append(messages, message)
	}

	return messages, warnings
}

// AddSystemPrompt adds system prompt to messages with date/time replacement
func (c *LLMClient) AddSystemPrompt(messages []messaging.OpenAIMessage, systemPrompt string, acceptUsernames bool) []messaging.OpenAIMessage {
	if systemPrompt == "" {
		return messages
	}

	// Replace date and time placeholders
	now := time.Now()
	// Ensure the placeholder line is present even if the caller omitted it
	if !strings.Contains(systemPrompt, "{date}") && !strings.Contains(systemPrompt, "{time}") {
		if strings.TrimSpace(systemPrompt) != "" {
			systemPrompt = strings.TrimSpace(systemPrompt) + "\n\n"
		}
		systemPrompt += "Today's date is {date}. The current time is {time}."
	}

	systemPrompt = strings.ReplaceAll(systemPrompt, "{date}", now.Format("January 02 2006"))
	systemPrompt = strings.ReplaceAll(systemPrompt, "{time}", now.Format("15:04:05 MST-0700"))

	// Trim any leading/trailing whitespace that may have been introduced
	systemPrompt = strings.TrimSpace(systemPrompt)

	// Add username instruction if supported
	if acceptUsernames {
		systemPrompt += "\nUser's names are their Discord IDs and should be typed as '<@ID>'."
	}

	// Add system message
	systemMessage := messaging.OpenAIMessage{
		Role:    "system",
		Content: systemPrompt,
	}

	return append(messages, systemMessage)
}

// FallbackResult contains the result of a fallback operation
type FallbackResult struct {
	UsedFallback    bool
	FallbackModel   string
	OriginalError   error
}

// StreamChatCompletionWithFallback streams chat completion with GPT 4.1 fallback
func (c *LLMClient) StreamChatCompletionWithFallback(ctx context.Context, model string, messages []messaging.OpenAIMessage) (<-chan StreamResponse, *FallbackResult, error) {
	fallbackResult := &FallbackResult{
		UsedFallback:  false,
		FallbackModel: "",
		OriginalError: nil,
	}

	// Try original model first
	stream, err := c.StreamChatCompletion(ctx, model, messages)
	if err != nil {
		logging.LogToFile("Original model %s failed, attempting GPT 4.1 fallback: %v", model, err)
		
		// Store the original error
		fallbackResult.OriginalError = err
		
		// Try GPT 4.1 fallback
		fallbackModel := "pollinations/o3"
		fallbackResult.FallbackModel = fallbackModel
		
		fallbackStream, fallbackErr := c.StreamChatCompletion(ctx, fallbackModel, messages)
		if fallbackErr != nil {
			logging.LogToFile("GPT 4.1 fallback also failed: %v", fallbackErr)
			return nil, fallbackResult, fmt.Errorf("both original model (%s) and fallback model (%s) failed. Original error: %w, Fallback error: %v", model, fallbackModel, err, fallbackErr)
		}
		
		fallbackResult.UsedFallback = true
		logging.LogToFile("Successfully switched to GPT 4.1 fallback model")
		return fallbackStream, fallbackResult, nil
	}
	
	return stream, fallbackResult, nil
}

// GetChatCompletionWithFallback gets a complete chat completion with GPT 4.1 fallback
func (c *LLMClient) GetChatCompletionWithFallback(ctx context.Context, messages []messaging.OpenAIMessage, model string) (string, *FallbackResult, error) {
	fallbackResult := &FallbackResult{
		UsedFallback:  false,
		FallbackModel: "",
		OriginalError: nil,
	}

	// Try original model first
	response, err := c.GetChatCompletion(ctx, messages, model)
	if err != nil {
		logging.LogToFile("Original model %s failed, attempting GPT 4.1 fallback: %v", model, err)
		
		// Store the original error
		fallbackResult.OriginalError = err
		
		// Try GPT 4.1 fallback
		fallbackModel := "pollinations/o3"
		fallbackResult.FallbackModel = fallbackModel
		
		fallbackResponse, fallbackErr := c.GetChatCompletion(ctx, messages, fallbackModel)
		if fallbackErr != nil {
			logging.LogToFile("GPT 4.1 fallback also failed: %v", fallbackErr)
			return "", fallbackResult, fmt.Errorf("both original model (%s) and fallback model (%s) failed. Original error: %w, Fallback error: %v", model, fallbackModel, err, fallbackErr)
		}
		
		fallbackResult.UsedFallback = true
		logging.LogToFile("Successfully switched to GPT 4.1 fallback model")
		return fallbackResponse, fallbackResult, nil
	}
	
	return response, fallbackResult, nil
}

// TestProviderConnectivity tests if a provider's server is reachable and responds correctly
func (c *LLMClient) TestProviderConnectivity(providerName string) error {
	provider, exists := c.config.Providers[providerName]
	if !exists {
		return fmt.Errorf("unknown provider: %s", providerName)
	}

	if provider.BaseURL == "" {
		return fmt.Errorf("no base URL configured for provider: %s", providerName)
	}

	// Test basic connectivity with a simple HTTP request
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Try to reach the models endpoint (common for OpenAI-compatible APIs)
	testURL := strings.TrimSuffix(provider.BaseURL, "/") + "/models"

	resp, err := client.Get(testURL)
	if err != nil {
		return fmt.Errorf("cannot connect to %s: %w. "+
			"Please check: 1) Is the server running? 2) Is the URL correct? 3) Network connectivity",
			provider.BaseURL, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Failed to close response body: %v", err)
		}
	}()

	// Read a small portion of the response to check if it's JSON or HTML
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response from %s: %w", provider.BaseURL, err)
	}

	bodyStr := string(body)

	// Check response status and content
	if resp.StatusCode >= 500 {
		return fmt.Errorf("server error (status %d) from %s. Response: %s. "+
			"This indicates the server is reachable but has internal issues",
			resp.StatusCode, provider.BaseURL, bodyStr)
	}

	// Check if response looks like HTML (common sign of misconfiguration)
	if strings.Contains(strings.ToLower(bodyStr), "<html>") ||
		strings.Contains(strings.ToLower(bodyStr), "<!doctype") {
		return fmt.Errorf("server at %s returned HTML instead of JSON. "+
			"This usually means: 1) Wrong base URL (not an OpenAI-compatible API), "+
			"2) Server misconfiguration, or 3) Reverse proxy issues. Response: %.200s",
			provider.BaseURL, bodyStr)
	}

	// If we get here, basic connectivity works
	fmt.Printf("✓ Provider %s (%s) is reachable (status %d)\n",
		providerName, provider.BaseURL, resp.StatusCode)
	return nil
}