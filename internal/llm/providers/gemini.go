package providers

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"google.golang.org/genai"

	"DiscordAIChatbot/internal/config"
	"DiscordAIChatbot/internal/logging"
	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/storage"
)

// GeminiProvider handles Gemini-specific operations
type GeminiProvider struct {
	config        *config.Config
	apiKeyManager *storage.APIKeyManager
}

// NewGeminiProvider creates a new Gemini provider
func NewGeminiProvider(cfg *config.Config, apiKeyManager *storage.APIKeyManager) *GeminiProvider {
	return &GeminiProvider{
		config:        cfg,
		apiKeyManager: apiKeyManager,
	}
}

// StreamResponse represents a streaming response chunk
type StreamResponse struct {
	Content       string
	FinishReason  string
	Error         error
	ImageData     []byte
	ImageMIMEType string
}

// ExtractSystemMessages extracts system messages from the messages array and returns them as a single string
// along with the remaining non-system messages. This is used for providers like Gemini that handle
// system instructions separately from the conversation messages.
func ExtractSystemMessages(messages []messaging.OpenAIMessage) (string, []messaging.OpenAIMessage) {
	var systemMessages []string
	var nonSystemMessages []messaging.OpenAIMessage

	for _, msg := range messages {
		if msg.Role == "system" {
			switch content := msg.Content.(type) {
			case string:
				if content != "" {
					systemMessages = append(systemMessages, content)
				}
			default:
				if str := fmt.Sprintf("%v", content); str != "" {
					systemMessages = append(systemMessages, str)
				}
			}
		} else {
			nonSystemMessages = append(nonSystemMessages, msg)
		}
	}

	return strings.Join(systemMessages, "\n\n"), nonSystemMessages
}

// ConvertToGeminiMessages converts OpenAI messages to Gemini format (excluding system messages)
// System messages are handled separately via the SystemInstruction parameter in GenerateContentConfig
func (g *GeminiProvider) ConvertToGeminiMessages(ctx context.Context, messages []messaging.OpenAIMessage, downloadImageFunc func(context.Context, string) ([]byte, string, error)) ([]*genai.Content, error) {
	var contents []*genai.Content

	for _, msg := range messages {
		// Skip system messages - they should be handled separately as system_instruction
		if msg.Role == "system" {
			continue
		}

		var role genai.Role
		switch msg.Role {
		case "user":
			role = genai.RoleUser
		case "assistant":
			role = genai.RoleModel
		default:
			role = genai.RoleUser
		}

		var parts []*genai.Part

		// Handle different content types
		switch content := msg.Content.(type) {
		case string:
			if content != "" {
				parts = append(parts, genai.NewPartFromText(content))
			}
		case []messaging.MessageContent:
			for _, part := range content {
				if part.Type == "text" && part.Text != "" {
					parts = append(parts, genai.NewPartFromText(part.Text))
				} else if part.Type == "image_url" && part.ImageURL != nil {
					// Download image from Discord CDN and convert to inline data
					imageData, mimeType, err := downloadImageFunc(ctx, part.ImageURL.URL)
					if err != nil {
						log.Printf("Failed to download image from %s: %v", part.ImageURL.URL, err)
						// Skip this image but continue processing other parts
						continue
					}

					blob := &genai.Blob{
						MIMEType: mimeType,
						Data:     imageData,
					}
					parts = append(parts, &genai.Part{InlineData: blob})
					if strings.HasPrefix(part.ImageURL.URL, "data:") {
						log.Printf("Successfully processed data URL image: %d bytes, %s", len(imageData), mimeType)
					} else {
						log.Printf("Successfully downloaded and converted Discord image to inline data: %d bytes, %s", len(imageData), mimeType)
					}
				} else if part.Type == "generated_image" && part.GeneratedImage != nil {
					// Handle generated images with inline data
					blob := &genai.Blob{
						MIMEType: part.GeneratedImage.MIMEType,
						Data:     part.GeneratedImage.Data,
					}
					parts = append(parts, &genai.Part{InlineData: blob})
				} else if part.Type == "audio_file" && part.AudioFile != nil {
					// Handle audio files with inline data
					blob := &genai.Blob{
						MIMEType: part.AudioFile.MIMEType,
						Data:     part.AudioFile.Data,
					}
					parts = append(parts, &genai.Part{InlineData: blob})
					log.Printf("Successfully processed audio file: %d bytes, %s", len(part.AudioFile.Data), part.AudioFile.MIMEType)
				}
			}
		default:
			if str := fmt.Sprintf("%v", content); str != "" {
				parts = append(parts, genai.NewPartFromText(str))
			}
		}

		if len(parts) > 0 {
			contents = append(contents, genai.NewContentFromParts(parts, role))
		}
	}

	return contents, nil
}

// CreateGeminiStream creates a streaming chat completion using Gemini
func (g *GeminiProvider) CreateGeminiStream(ctx context.Context, model string, messages []messaging.OpenAIMessage, downloadImageFunc func(context.Context, string) ([]byte, string, error), isAPIKeyError func(error) bool, is503Error func(error) bool, retryWith503Backoff func(context.Context, func() error) error, isInternalError func(error) bool, retryWithInternalBackoff func(context.Context, func() error) error) (<-chan StreamResponse, error) {
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid model format: %s (expected gemini/model)", model)
	}

	providerName := parts[0]
	modelName := parts[1]

	// Get provider config
	provider, exists := g.config.Providers[providerName]
	if !exists {
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}

	// Get available API keys for this provider
	availableKeys := provider.GetAPIKeys()
	if len(availableKeys) == 0 {
		return nil, fmt.Errorf("no API keys configured for provider: %s", providerName)
	}

	// Get model parameters
	modelParams, exists := g.config.Models[model]
	if !exists {
		modelParams = config.ModelParams{}
	}

	// Extract system messages and convert remaining messages to Gemini format
	systemInstruction, nonSystemMessages := ExtractSystemMessages(messages)
	contents, err := g.ConvertToGeminiMessages(ctx, nonSystemMessages, downloadImageFunc)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	// Log request start
	logging.LogToFile("Starting Gemini LLM request: Model=%s, Provider=%s", modelName, providerName)

	// Debug: Print Gemini payload
	logging.LogExternalContentToFile("=== DEBUG: Gemini Payload ===")
	logging.LogExternalContentToFile("Model: %s", modelName)
	logging.LogExternalContentToFile("Provider: %s", providerName)
	if systemInstruction != "" {
		logging.LogExternalContentToFile("SystemInstruction: %s", systemInstruction)
	}
	for i, content := range contents {
		logging.LogExternalContentToFile("Message %d [Role: %s]:", i, content.Role)
		for j, part := range content.Parts {
			if part.Text != "" {
				logging.LogExternalContentToFile("  Part %d [Text]: %s", j, part.Text)
			}
			if part.InlineData != nil {
				if strings.HasPrefix(part.InlineData.MIMEType, "image/") {
					logging.LogExternalContentToFile("  Part %d [Image]: %s, %d bytes", j, part.InlineData.MIMEType, len(part.InlineData.Data))
				} else if strings.HasPrefix(part.InlineData.MIMEType, "audio/") {
					logging.LogExternalContentToFile("  Part %d [Audio]: %s, %d bytes", j, part.InlineData.MIMEType, len(part.InlineData.Data))
				} else {
					logging.LogExternalContentToFile("  Part %d [Data]: %s, %d bytes", j, part.InlineData.MIMEType, len(part.InlineData.Data))
				}
			}
		}
	}
	logging.LogExternalContentToFile("=== END DEBUG ===\n")

	responseChan := make(chan StreamResponse, 10)

	go func() {
		defer close(responseChan)

		// Try API keys until one works or we run out
		maxRetries := len(availableKeys)
		for attempt := 0; attempt < maxRetries; attempt++ {
			// Get next API key
			apiKey, err := g.apiKeyManager.GetNextAPIKey(providerName, availableKeys)
			if err != nil {
				responseChan <- StreamResponse{Error: fmt.Errorf("failed to get API key: %w", err)}
				return
			}

			// Create Gemini client
			// Set API key in environment variable temporarily for this client
			oldKey := os.Getenv("GEMINI_API_KEY")
			if err := os.Setenv("GEMINI_API_KEY", apiKey); err != nil {
				log.Printf("Failed to set GEMINI_API_KEY: %v", err)
			}
			defer func() {
				if oldKey != "" {
					if err := os.Setenv("GEMINI_API_KEY", oldKey); err != nil {
						log.Printf("Failed to restore GEMINI_API_KEY: %v", err)
					}
				} else {
					if err := os.Unsetenv("GEMINI_API_KEY"); err != nil {
						log.Printf("Failed to unset GEMINI_API_KEY: %v", err)
					}
				}
			}()

			var client *genai.Client
			err = retryWithInternalBackoff(ctx, func() error {
				return retryWith503Backoff(ctx, func() error {
					var clientErr error
					client, clientErr = genai.NewClient(ctx, nil)
					return clientErr
				})
			})

			if err != nil {
				if isAPIKeyError(err) {
					// Mark this key as bad and try the next one
					markErr := g.apiKeyManager.MarkKeyAsBad(providerName, apiKey, err.Error())
					if markErr != nil {
						log.Printf("Failed to mark API key as bad: %v", markErr)
					}
					log.Printf("API key issue detected, trying next key: %v", err)
					continue
				}
				responseChan <- StreamResponse{Error: fmt.Errorf("failed to create Gemini client: %w", err)}
				return
			}

			// Prepare generation config
			config := &genai.GenerateContentConfig{
				SafetySettings: []*genai.SafetySetting{
					{
						Category:  "HARM_CATEGORY_HARASSMENT",
						Threshold: "BLOCK_NONE",
					},
					{
						Category:  "HARM_CATEGORY_HATE_SPEECH",
						Threshold: "BLOCK_NONE",
					},
					{
						Category:  "HARM_CATEGORY_SEXUALLY_EXPLICIT",
						Threshold: "BLOCK_NONE",
					},
					{
						Category:  "HARM_CATEGORY_DANGEROUS_CONTENT",
						Threshold: "BLOCK_NONE",
					},
					{
						Category:  "HARM_CATEGORY_CIVIC_INTEGRITY",
						Threshold: "BLOCK_NONE",
					},
				},
			}

			// Set system instruction if available - this is the correct way to handle system prompts in Gemini
			// according to the Google Gemini API documentation
			if systemInstruction != "" {
				config.SystemInstruction = &genai.Content{
					Role:  genai.RoleUser,
					Parts: []*genai.Part{genai.NewPartFromText(systemInstruction)},
				}
			}

			// Apply model-specific parameters
			if modelParams.Temperature != nil {
				config.Temperature = modelParams.Temperature
			}

			// Apply thinking budget configuration
			if modelParams.ThinkingBudget != nil {
				config.ThinkingConfig = &genai.ThinkingConfig{
					ThinkingBudget: modelParams.ThinkingBudget,
				}
			}

			// Check if this is an image generation model
			isImageGenModel := modelName == "gemini-2.0-flash-preview-image-generation"
			if isImageGenModel {
				// For image generation models, set response modalities to include both text and images
				config.ResponseModalities = []string{"TEXT", "IMAGE"}
				// Clear system instruction for image generation models as they don't support it
				config.SystemInstruction = nil
			}

			// Debug: Print Gemini generation config
			logging.LogExternalContentToFile("=== DEBUG: Gemini Generation Config ===")
			if config.SystemInstruction != nil {
				logging.LogExternalContentToFile("SystemInstruction: %s", systemInstruction)
			}
			if config.Temperature != nil {
				logging.LogExternalContentToFile("Temperature: %f", *config.Temperature)
			}
			if config.ThinkingConfig != nil && config.ThinkingConfig.ThinkingBudget != nil {
				logging.LogExternalContentToFile("ThinkingBudget: %d", *config.ThinkingConfig.ThinkingBudget)
			}
			if len(config.ResponseModalities) > 0 {
				logging.LogExternalContentToFile("ResponseModalities: %v", config.ResponseModalities)
			}
			logging.LogExternalContentToFile("=== END DEBUG ===\n")

			// Create the stream
			stream := client.Models.GenerateContentStream(ctx, modelName, contents, config)

			// Process stream responses
			streamErr := false
			for chunk, err := range stream {
				if err != nil {
					// Check if this is an INTERNAL error and retry the entire stream
					if isInternalError(err) {
						log.Printf("INTERNAL error during stream processing, retrying: %v", err)
						// Break out of the stream loop to retry with next API key
						streamErr = true
						break
					}

					// Check if this is a 503 error and retry the entire stream
					if is503Error(err) {
						log.Printf("503 error during stream processing, retrying: %v", err)
						// Break out of the stream loop to retry with next API key
						streamErr = true
						break
					}

					if isAPIKeyError(err) {
						// Mark this key as bad and try the next one
						markErr := g.apiKeyManager.MarkKeyAsBad(providerName, apiKey, err.Error())
						if markErr != nil {
							log.Printf("Failed to mark API key as bad: %v", markErr)
						}
						log.Printf("API key issue detected, trying next key: %v", err)
						streamErr = true
						break
					}
					responseChan <- StreamResponse{Error: err}
					return
				}

				// Extract text content from response
				if len(chunk.Candidates) > 0 {
					candidate := chunk.Candidates[0]
					if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
						for _, part := range candidate.Content.Parts {
							if part.Text != "" {
								logging.LogExternalContentToFile("Gemini Response Text: %s", part.Text)
								responseChan <- StreamResponse{
									Content: part.Text,
								}
							} else if part.InlineData != nil {
								// Handle image generation
								logging.LogExternalContentToFile("Gemini Response Image: %s, %d bytes", part.InlineData.MIMEType, len(part.InlineData.Data))
								responseChan <- StreamResponse{
									ImageData:     part.InlineData.Data,
									ImageMIMEType: part.InlineData.MIMEType,
								}
							}
						}
					}

					// Check if generation is finished
					if candidate.FinishReason != "" {
						logging.LogExternalContentToFile("Gemini Response Finish Reason: %s", string(candidate.FinishReason))
						responseChan <- StreamResponse{
							FinishReason: string(candidate.FinishReason),
						}
						return
					}
				}
			}

			// If we got here and didn't have a stream error, we succeeded
			if !streamErr {
				return
			}
		}

		// All API keys failed
		responseChan <- StreamResponse{Error: fmt.Errorf("all API keys failed for provider: %s", providerName)}
	}()

	return responseChan, nil
}

// GenerateVideo generates a video using a Gemini video generation model (e.g., Veo).
// It handles the long-running operation by polling for completion.
func (g *GeminiProvider) GenerateVideo(ctx context.Context, model string, prompt string, isAPIKeyError func(error) bool, is503Error func(error) bool, retryWith503Backoff func(context.Context, func() error) error, isInternalError func(error) bool, retryWithInternalBackoff func(context.Context, func() error) error) ([]byte, error) {
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid model format: %s (expected gemini/model)", model)
	}

	providerName := parts[0]
	modelName := parts[1]

	provider, exists := g.config.Providers[providerName]
	if !exists {
		return nil, fmt.Errorf("unknown provider: %s", providerName)
	}

	availableKeys := provider.GetAPIKeys()
	if len(availableKeys) == 0 {
		return nil, fmt.Errorf("no API keys configured for provider: %s", providerName)
	}

	modelParams, exists := g.config.Models[model]
	if !exists {
		return nil, fmt.Errorf("model parameters not found for: %s", model)
	}

	videoConfig := &genai.GenerateVideosConfig{}
	if modelParams.VideoGeneration != nil {
		if modelParams.VideoGeneration.AspectRatio != "" {
			videoConfig.AspectRatio = modelParams.VideoGeneration.AspectRatio
		}
		if modelParams.VideoGeneration.PersonGeneration != "" {
			videoConfig.PersonGeneration = modelParams.VideoGeneration.PersonGeneration
		}
	}

	logging.LogToFile("Starting Gemini Video Generation: Model=%s, Provider=%s", modelName, providerName)

	maxRetries := len(availableKeys)
	for attempt := 0; attempt < maxRetries; attempt++ {
		apiKey, err := g.apiKeyManager.GetNextAPIKey(providerName, availableKeys)
		if err != nil {
			return nil, fmt.Errorf("failed to get API key: %w", err)
		}

		oldKey := os.Getenv("GEMINI_API_KEY")
		if err := os.Setenv("GEMINI_API_KEY", apiKey); err != nil {
			log.Printf("Failed to set GEMINI_API_KEY: %v", err)
		}
		defer func() {
			if oldKey != "" {
				if err := os.Setenv("GEMINI_API_KEY", oldKey); err != nil {
					log.Printf("Failed to restore GEMINI_API_KEY: %v", err)
				}
			} else {
				if err := os.Unsetenv("GEMINI_API_KEY"); err != nil {
					log.Printf("Failed to unset GEMINI_API_KEY: %v", err)
				}
			}
		}()

		var client *genai.Client
		err = retryWithInternalBackoff(ctx, func() error {
			return retryWith503Backoff(ctx, func() error {
				var clientErr error
				client, clientErr = genai.NewClient(ctx, nil)
				return clientErr
			})
		})

		if err != nil {
			if isAPIKeyError(err) {
				g.apiKeyManager.MarkKeyAsBad(providerName, apiKey, err.Error())
				log.Printf("API key issue detected, trying next key: %v", err)
				continue
			}
			return nil, fmt.Errorf("failed to create Gemini client: %w", err)
		}

		operation, err := client.Models.GenerateVideos(ctx, modelName, prompt, nil, videoConfig)
		if err != nil {
			if isAPIKeyError(err) || is503Error(err) || isInternalError(err) {
				log.Printf("Retriable error during video generation initiation: %v", err)
				continue
			}
			return nil, fmt.Errorf("failed to start video generation: %w", err)
		}

		for !operation.Done {
			log.Printf("Video generation in progress, checking again in 20 seconds...")
			time.Sleep(20 * time.Second)
			operation, err = client.Operations.GetVideosOperation(ctx, operation, nil)
			if err != nil {
				if isAPIKeyError(err) || is503Error(err) || isInternalError(err) {
					log.Printf("Retriable error while polling video generation status: %v", err)
					break // Break inner loop to try next key
				}
				return nil, fmt.Errorf("failed to get video generation status: %w", err)
			}
		}

		if err != nil { // This checks for the polling error that broke the loop
			continue // Try next API key
		}

		if operation.Response != nil && len(operation.Response.GeneratedVideos) > 0 {
			video := operation.Response.GeneratedVideos[0]
			// The Download call populates the VideoBytes field.
			_, err := client.Files.Download(ctx, video.Video, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to download video content: %w", err)
			}

			if video.Video != nil && len(video.Video.VideoBytes) > 0 {
				log.Printf("Successfully generated and downloaded video: %d bytes", len(video.Video.VideoBytes))
				return video.Video.VideoBytes, nil
			}
		}

		log.Printf("Video generation finished but no video data was returned.")
		return nil, fmt.Errorf("video generation completed but no video was returned")
	}

	return nil, fmt.Errorf("all API keys failed for provider: %s", providerName)
}