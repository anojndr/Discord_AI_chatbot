package providers

import (
	"bytes"
	"context"
	"fmt"
	"image/gif"
	"image/png"
	"log"
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

const geminiInlinePDFMaxBytes = 20 * 1024 * 1024 // 20MB limit from Gemini inline upload guidance

type pdfUploadRef struct {
	contentIdx  int
	partIdx     int
	data        []byte
	mimeType    string
	displayName string
	sizeBytes   int
}

// Context key to disable Gemini grounding tools dynamically
type disableGroundingKey struct{}

// DisableGeminiGroundingInContext returns a context that instructs the Gemini provider
// to skip adding grounding (Google Search) tools regardless of global config.
func DisableGeminiGroundingInContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, disableGroundingKey{}, true)
}

// isGroundingDisabled checks the context for grounding disable flag
func isGroundingDisabled(ctx context.Context) bool {
	v := ctx.Value(disableGroundingKey{})
	disabled, _ := v.(bool)
	return disabled
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
	Content           string
	FinishReason      string
	Error             error
	ImageData         []byte
	ImageMIMEType     string
	GroundingMetadata *genai.GroundingMetadata
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
func (g *GeminiProvider) ConvertToGeminiMessages(ctx context.Context, messages []messaging.OpenAIMessage, downloadImageFunc func(context.Context, string) ([]byte, string, error)) ([]*genai.Content, []pdfUploadRef, error) {
	var contents []*genai.Content
	var pdfUploads []pdfUploadRef

	for _, msg := range messages {
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
		type pdfUploadCandidate struct {
			partIdx     int
			data        []byte
			mimeType    string
			displayName string
			sizeBytes   int
		}
		var pendingPDFs []pdfUploadCandidate

		switch content := msg.Content.(type) {
		case string:
			if content != "" {
				parts = append(parts, genai.NewPartFromText(content))
			}
		case []messaging.MessageContent:
			for _, part := range content {
				switch part.Type {
				case "text":
					if part.Text != "" {
						parts = append(parts, genai.NewPartFromText(part.Text))
					}
				case "image_url":
					if part.ImageURL == nil {
						continue
					}

					imageData, mimeType, err := downloadImageFunc(ctx, part.ImageURL.URL)
					if err != nil {
						log.Printf("Failed to download image from %s: %v", part.ImageURL.URL, err)
						continue
					}

					if mimeType == "image/gif" {
						log.Printf("Processing GIF: %d bytes", len(imageData))
						gifImage, err := gif.DecodeAll(bytes.NewReader(imageData))
						if err != nil {
							log.Printf("Failed to decode GIF: %v", err)
							continue
						}

						for i, frame := range gifImage.Image {
							var buf bytes.Buffer
							if err := png.Encode(&buf, frame); err != nil {
								log.Printf("Failed to encode GIF frame %d to PNG: %v", i, err)
								continue
							}
							frameData := buf.Bytes()
							parts = append(parts, genai.NewPartFromBytes(frameData, "image/png"))
							log.Printf("Successfully processed GIF frame %d as PNG: %d bytes", i, len(frameData))
						}
					} else {
						parts = append(parts, genai.NewPartFromBytes(imageData, mimeType))
						if strings.HasPrefix(part.ImageURL.URL, "data:") {
							log.Printf("Successfully processed data URL image: %d bytes, %s", len(imageData), mimeType)
						} else {
							log.Printf("Successfully downloaded and converted Discord image to inline data: %d bytes, %s", len(imageData), mimeType)
						}
					}
				case "generated_image":
					if part.GeneratedImage == nil {
						continue
					}
					parts = append(parts, genai.NewPartFromBytes(part.GeneratedImage.Data, part.GeneratedImage.MIMEType))
				case "audio_file":
					if part.AudioFile == nil {
						continue
					}
					parts = append(parts, genai.NewPartFromBytes(part.AudioFile.Data, part.AudioFile.MIMEType))
					log.Printf("Successfully processed audio file: %d bytes, %s", len(part.AudioFile.Data), part.AudioFile.MIMEType)
				case "pdf_file":
					if part.PDFFile == nil {
						continue
					}

					pdfData := part.PDFFile.Data
					if len(pdfData) == 0 {
						log.Printf("Skipping PDF attachment with empty payload (url=%s)", part.PDFFile.URL)
						continue
					}

					mimeType := part.PDFFile.MIMEType
					if strings.TrimSpace(mimeType) == "" {
						mimeType = "application/pdf"
					}

					displayName := part.PDFFile.Filename
					if strings.TrimSpace(displayName) == "" {
						displayName = fmt.Sprintf("attachment-%d.pdf", len(pdfUploads)+len(pendingPDFs)+1)
					}

					if len(pdfData) > geminiInlinePDFMaxBytes {
						parts = append(parts, &genai.Part{})
						pendingPDFs = append(pendingPDFs, pdfUploadCandidate{
							partIdx:     len(parts) - 1,
							data:        pdfData,
							mimeType:    mimeType,
							displayName: displayName,
							sizeBytes:   len(pdfData),
						})
						log.Printf("Queued PDF for File API upload: %s (%d bytes)", displayName, len(pdfData))
					} else {
						parts = append(parts, genai.NewPartFromBytes(pdfData, mimeType))
						log.Printf("Embedded PDF inline: %s (%d bytes)", displayName, len(pdfData))
					}
				}
			}
		default:
			if str := fmt.Sprintf("%v", content); str != "" {
				parts = append(parts, genai.NewPartFromText(str))
			}
		}

		if len(parts) > 0 {
			content := genai.NewContentFromParts(parts, role)
			contentIdx := len(contents)
			contents = append(contents, content)
			for _, pending := range pendingPDFs {
				pdfUploads = append(pdfUploads, pdfUploadRef{
					contentIdx:  contentIdx,
					partIdx:     pending.partIdx,
					data:        pending.data,
					mimeType:    pending.mimeType,
					displayName: pending.displayName,
					sizeBytes:   pending.sizeBytes,
				})
			}
		}
	}

	return contents, pdfUploads, nil
}

// CreateGeminiStream creates a streaming chat completion using Gemini
func (g *GeminiProvider) CreateGeminiStream(ctx context.Context, model string, messages []messaging.OpenAIMessage, detectedURLs []string, downloadImageFunc func(context.Context, string) ([]byte, string, error), isAPIKeyError func(error) bool, is503Error func(error) bool, retryWith503Backoff func(context.Context, func() error) error, isInternalError func(error) bool, retryWithInternalBackoff func(context.Context, func() error) error) (<-chan StreamResponse, error) {
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
	baseContents, basePDFUploads, err := g.ConvertToGeminiMessages(ctx, nonSystemMessages, downloadImageFunc)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	// Log request start
	logging.LogToFile("Starting Gemini LLM request: Model=%s, Provider=%s", modelName, providerName)

	responseChan := make(chan StreamResponse, 10)

	go func() {
		defer close(responseChan)

		// Try API keys until one works or we run out
		maxRetries := len(availableKeys)
		for attempt := 0; attempt < maxRetries; attempt++ {
			contents := cloneGeminiContents(baseContents)
			pdfUploads := clonePDFUploads(basePDFUploads)

			// Get next API key
			apiKey, err := g.apiKeyManager.GetNextAPIKey(ctx, providerName, availableKeys)
			if err != nil {
				responseChan <- StreamResponse{Error: fmt.Errorf("failed to get API key: %w", err)}
				return
			}

			// Create Gemini client
			var client *genai.Client
			err = retryWithInternalBackoff(ctx, func() error {
				return retryWith503Backoff(ctx, func() error {
					var clientErr error
					// Pass key directly via options
					client, clientErr = genai.NewClient(ctx, &genai.ClientConfig{
						APIKey: apiKey,
					})
					return clientErr
				})
			})

			if err != nil {
				if isAPIKeyError(err) {
					// Mark this key as bad and try the next one
					markErr := g.apiKeyManager.MarkKeyAsBad(ctx, providerName, apiKey, err.Error())
					if markErr != nil {
						log.Printf("Failed to mark API key as bad: %v", markErr)
					}
					log.Printf("API key issue detected, trying next key: %v", err)
					continue
				}
				responseChan <- StreamResponse{Error: fmt.Errorf("failed to create Gemini client: %w", err)}
				return
			}

			if len(pdfUploads) > 0 {
				uploadErr := retryWithInternalBackoff(ctx, func() error {
					return retryWith503Backoff(ctx, func() error {
						return g.attachPDFUploads(ctx, client, contents, pdfUploads)
					})
				})
				if uploadErr != nil {
					if isAPIKeyError(uploadErr) {
						markErr := g.apiKeyManager.MarkKeyAsBad(ctx, providerName, apiKey, uploadErr.Error())
						if markErr != nil {
							log.Printf("Failed to mark API key as bad after PDF upload failure: %v", markErr)
						}
						log.Printf("API key issue detected during PDF upload, trying next key: %v", uploadErr)
						continue
					}
					if is503Error(uploadErr) || isInternalError(uploadErr) {
						log.Printf("Transient error during PDF upload, retrying with next key: %v", uploadErr)
						continue
					}
					responseChan <- StreamResponse{Error: fmt.Errorf("failed to upload PDF attachments: %w", uploadErr)}
					return
				}
			}

			g.logGeminiPayload(modelName, providerName, systemInstruction, contents, attempt)

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

			// Apply Gemini Grounding with Google Search if enabled (unless disabled by context or excluded model)
			// Requirement: For gemini 2.5 pro we should use the external Web Search (RAG-Forge) pipeline instead of native Gemini grounding.
			// Therefore we explicitly skip adding GoogleSearch tool when modelName starts with gemini-2.5-pro.
			if g.config.WebSearch.GeminiGrounding && !isGroundingDisabled(ctx) {
				if strings.HasPrefix(modelName, "gemini-2.5-pro") {
					log.Printf("Skipping native Gemini grounding for %s in favor of external Web Search (RAG-Forge)", modelName)
				} else if modelName != "gemini-2.0-flash-preview-image-generation" { // existing exclusion
					config.Tools = []*genai.Tool{
						{GoogleSearch: &genai.GoogleSearch{}},
					}
				} else {
					log.Printf("Skipping Gemini grounding for model %s due to exclusion rules", modelName)
				}
			}

			// Enable URL context tool if the model supports it and URLs are detected
			if g.SupportsURLContext(modelName) && len(detectedURLs) > 0 {
				if config.Tools == nil {
					config.Tools = []*genai.Tool{}
				}
				config.Tools = append(config.Tools, &genai.Tool{URLContext: &genai.URLContext{}})
				logging.LogExternalContentToFile("Enabled URL Context Tool for %d URLs", len(detectedURLs))
			}

			// Check if this is an image generation model
			isImageGenModel := modelName == "gemini-2.0-flash-preview-image-generation" || strings.HasPrefix(modelName, "imagen")
			if isImageGenModel {
				// For image generation models, set response modalities to include both text and images
				config.ResponseModalities = []string{"TEXT", "IMAGE"}
				// Clear system instruction for image generation models as they don't support it
				config.SystemInstruction = nil
			}

			// Debug: Print Gemini generation config
			logging.LogExternalContentToFile("=== DEBUG: Gemini Generation Config (attempt %d) ===", attempt+1)
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
						markErr := g.apiKeyManager.MarkKeyAsBad(ctx, providerName, apiKey, err.Error())
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

					if candidate.GroundingMetadata != nil {
						logging.LogExternalContentToFile("Gemini Grounding Metadata: %+v", candidate.GroundingMetadata)
						responseChan <- StreamResponse{
							GroundingMetadata: candidate.GroundingMetadata,
						}
					}

					// Check if generation is finished
					if candidate.FinishReason != "" {
						finishReasonStr := string(candidate.FinishReason)
						logging.LogExternalContentToFile("Gemini Response Finish Reason: %s", finishReasonStr)

						// Check for abnormal finish reasons that should trigger a fallback
						switch candidate.FinishReason {
						case genai.FinishReasonStop, genai.FinishReasonMaxTokens:
							// These are normal finish reasons, pass them through
							responseChan <- StreamResponse{
								FinishReason: finishReasonStr,
							}
						default:
							// Any other reason is considered a premature finish that should trigger a fallback
							responseChan <- StreamResponse{
								Error: &PrematureStreamFinishError{FinishReason: finishReasonStr},
							}
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

func cloneGeminiContents(contents []*genai.Content) []*genai.Content {
	if len(contents) == 0 {
		return nil
	}

	clones := make([]*genai.Content, len(contents))
	for i, content := range contents {
		if content == nil {
			continue
		}
		copyContent := &genai.Content{
			Role:  content.Role,
			Parts: make([]*genai.Part, len(content.Parts)),
		}
		for j, part := range content.Parts {
			if part == nil {
				continue
			}
			partCopy := *part
			copyContent.Parts[j] = &partCopy
		}
		clones[i] = copyContent
	}
	return clones
}

func clonePDFUploads(uploads []pdfUploadRef) []pdfUploadRef {
	if len(uploads) == 0 {
		return nil
	}
	cloned := make([]pdfUploadRef, len(uploads))
	copy(cloned, uploads)
	return cloned
}

func (g *GeminiProvider) attachPDFUploads(ctx context.Context, client *genai.Client, contents []*genai.Content, uploads []pdfUploadRef) error {
	for _, upload := range uploads {
		if upload.contentIdx < 0 || upload.contentIdx >= len(contents) {
			return fmt.Errorf("invalid PDF content index %d", upload.contentIdx)
		}
		content := contents[upload.contentIdx]
		if content == nil {
			return fmt.Errorf("missing content at index %d for PDF upload", upload.contentIdx)
		}
		if upload.partIdx < 0 || upload.partIdx >= len(content.Parts) {
			return fmt.Errorf("invalid PDF part index %d", upload.partIdx)
		}
		part := content.Parts[upload.partIdx]
		if part == nil {
			part = &genai.Part{}
			content.Parts[upload.partIdx] = part
		} else {
			part.Text = ""
			part.InlineData = nil
			part.FileData = nil
		}

		reader := bytes.NewReader(upload.data)
		file, err := client.Files.Upload(ctx, reader, &genai.UploadFileConfig{
			MIMEType:    upload.mimeType,
			DisplayName: upload.displayName,
		})
		if err != nil {
			return err
		}

		uploadedPart := genai.NewPartFromFile(*file)
		if uploadedPart == nil {
			return fmt.Errorf("failed to build Gemini part for uploaded PDF %s", upload.displayName)
		}

		*part = *uploadedPart
		logging.LogExternalContentToFile("Uploaded PDF via File API: %s (%d bytes) -> %s", upload.displayName, upload.sizeBytes, file.Name)
	}
	return nil
}

func (g *GeminiProvider) logGeminiPayload(modelName, providerName, systemInstruction string, contents []*genai.Content, attempt int) {
	logging.LogExternalContentToFile("=== DEBUG: Gemini Payload (attempt %d) ===", attempt+1)
	logging.LogExternalContentToFile("Model: %s", modelName)
	logging.LogExternalContentToFile("Provider: %s", providerName)
	if systemInstruction != "" {
		logging.LogExternalContentToFile("SystemInstruction: %s", systemInstruction)
	}
	for i, content := range contents {
		if content == nil {
			continue
		}
		logging.LogExternalContentToFile("Message %d [Role: %s]:", i, content.Role)
		for j, part := range content.Parts {
			if part == nil {
				continue
			}
			if part.Text != "" {
				logging.LogExternalContentToFile("  Part %d [Text]: %s", j, part.Text)
			}
			if part.InlineData != nil {
				mimeType := part.InlineData.MIMEType
				byteLen := len(part.InlineData.Data)
				switch {
				case strings.HasPrefix(mimeType, "image/"):
					logging.LogExternalContentToFile("  Part %d [Image]: %s, %d bytes", j, mimeType, byteLen)
				case strings.HasPrefix(mimeType, "audio/"):
					logging.LogExternalContentToFile("  Part %d [Audio]: %s, %d bytes", j, mimeType, byteLen)
				case mimeType == "application/pdf":
					logging.LogExternalContentToFile("  Part %d [PDF Inline]: %s, %d bytes", j, mimeType, byteLen)
				default:
					logging.LogExternalContentToFile("  Part %d [Data]: %s, %d bytes", j, mimeType, byteLen)
				}
			}
			if part.FileData != nil {
				logging.LogExternalContentToFile("  Part %d [PDF File]: %s (%s)", j, part.FileData.FileURI, part.FileData.MIMEType)
			}
		}
	}
	logging.LogExternalContentToFile("=== END DEBUG ===\n")
}

// GenerateImage generates an image using a Gemini image generation model (e.g., Imagen).
func (g *GeminiProvider) GenerateImage(ctx context.Context, model string, prompt string, isAPIKeyError func(error) bool, is503Error func(error) bool, retryWith503Backoff func(context.Context, func() error) error, isInternalError func(error) bool, retryWithInternalBackoff func(context.Context, func() error) error) ([]*genai.GeneratedImage, error) {
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

	if _, exists := g.config.Models[model]; !exists {
		return nil, fmt.Errorf("model parameters not found for: %s", model)
	}

	imageConfig := &genai.GenerateImagesConfig{
		PersonGeneration: genai.PersonGenerationAllowAll,
	}

	logging.LogToFile("Starting Gemini Image Generation: Model=%s, Provider=%s", modelName, providerName)

	maxRetries := len(availableKeys)
	for attempt := 0; attempt < maxRetries; attempt++ {
		apiKey, err := g.apiKeyManager.GetNextAPIKey(ctx, providerName, availableKeys)
		if err != nil {
			return nil, fmt.Errorf("failed to get API key: %w", err)
		}

		var client *genai.Client
		err = retryWithInternalBackoff(ctx, func() error {
			return retryWith503Backoff(ctx, func() error {
				var clientErr error
				client, clientErr = genai.NewClient(ctx, &genai.ClientConfig{
					APIKey: apiKey,
				})
				return clientErr
			})
		})

		if err != nil {
			if isAPIKeyError(err) {
				if err := g.apiKeyManager.MarkKeyAsBad(ctx, providerName, apiKey, err.Error()); err != nil {
					log.Printf("Failed to mark API key as bad: %v", err)
				}
				log.Printf("API key issue detected, trying next key: %v", err)
				continue
			}
			return nil, fmt.Errorf("failed to create Gemini client: %w", err)
		}

		response, err := client.Models.GenerateImages(ctx, modelName, prompt, imageConfig)
		if err != nil {
			if isAPIKeyError(err) || is503Error(err) || isInternalError(err) {
				log.Printf("Retriable error during image generation: %v", err)
				if isAPIKeyError(err) {
					if err := g.apiKeyManager.MarkKeyAsBad(ctx, providerName, apiKey, err.Error()); err != nil {
						log.Printf("Failed to mark API key as bad: %v", err)
					}
				}
				continue
			}
			return nil, fmt.Errorf("failed to generate images: %w", err)
		}
		return response.GeneratedImages, nil
	}
	return nil, fmt.Errorf("all API keys failed for provider: %s", providerName)
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

	if _, exists := g.config.Models[model]; !exists {
		return nil, fmt.Errorf("model parameters not found for: %s", model)
	}

	videoConfig := &genai.GenerateVideosConfig{
		PersonGeneration: "allow_all",
	}

	logging.LogToFile("Starting Gemini Video Generation: Model=%s, Provider=%s", modelName, providerName)

	maxRetries := len(availableKeys)
	for attempt := 0; attempt < maxRetries; attempt++ {
		apiKey, err := g.apiKeyManager.GetNextAPIKey(ctx, providerName, availableKeys)
		if err != nil {
			return nil, fmt.Errorf("failed to get API key: %w", err)
		}

		var client *genai.Client
		err = retryWithInternalBackoff(ctx, func() error {
			return retryWith503Backoff(ctx, func() error {
				var clientErr error
				client, clientErr = genai.NewClient(ctx, &genai.ClientConfig{
					APIKey: apiKey,
				})
				return clientErr
			})
		})

		if err != nil {
			if isAPIKeyError(err) {
				if err := g.apiKeyManager.MarkKeyAsBad(ctx, providerName, apiKey, err.Error()); err != nil {
					log.Printf("Failed to mark API key as bad: %v", err)
				}
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

// SupportsURLContext checks if a given Gemini model supports the URL context tool.
func (g *GeminiProvider) SupportsURLContext(modelName string) bool {
	supportedModels := map[string]bool{
		"gemini-2.5-pro":            true,
		"gemini-2.5-flash":          true,
		"gemini-2.5-flash-lite":     true,
		"gemini-2.0-flash":          true,
		"gemini-2.0-flash-live-001": true,
	}
	return supportedModels[modelName]
}
