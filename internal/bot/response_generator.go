package bot

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/llm"
	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/utils"
)

var builderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
}

// generateResponse generates and sends LLM response
func (b *Bot) generateResponse(s *discordgo.Session, originalMsg *discordgo.MessageCreate, model string, messages []messaging.OpenAIMessage, warnings []string, progressMgr *utils.ProgressManager, messageRef *discordgo.MessageReference, targetChannelID string, webSearchPerformed bool, searchResultCount int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// targetChannelID is now passed as a parameter from handleMessage
	// which handles thread creation logic

	// Initialize tracking variables
	actualModel := model
	fallbackAttempted := false
	
	// Helper function to attempt streaming with potential fallback
	attemptStream := func(attemptModel string, isFallback bool) (<-chan llm.StreamResponse, error) {
		if isFallback {
			log.Printf("Attempting fallback to model: %s", attemptModel)
		}
		
		fallbackModel := b.config.Load().FallbackModel
		stream, fallbackResult, err := b.llmClient.StreamChatCompletionWithFallback(ctx, attemptModel, messages, fallbackModel)
		if err != nil {
			return nil, err
		}
		
		// Update actualModel if fallback was used in the LLM client
		if fallbackResult != nil && fallbackResult.UsedFallback {
			actualModel = fallbackResult.FallbackModel
		} else if isFallback {
			actualModel = attemptModel
		}
		
		return stream, nil
	}

	// Start streaming with original model
	stream, err := attemptStream(model, false)
	if err != nil {
		log.Printf("Failed to create chat completion stream: %v", err)

		// Update progress message to show error instead of leaving it stuck
		if progressMgr != nil && progressMgr.GetMessageID() != "" {
			// Create error embed
			errorEmbed := &discordgo.MessageEmbed{
				Title:       "❌ Request Failed",
				Description: fmt.Sprintf("Failed to process your request:\n\n```\n%v\n```", err),
				Color:       0xFF0000, // Red color for errors
				Footer: &discordgo.MessageEmbedFooter{
					Text: fmt.Sprintf("🤖 Model: %s", actualModel),
				},
			}

			// Update the progress message to show the error
			_, updateErr := s.ChannelMessageEditEmbed(progressMgr.GetChannelID(), progressMgr.GetMessageID(), errorEmbed)
			if updateErr != nil {
				log.Printf("Failed to update progress message with error: %v", updateErr)

				// If we can't update the progress message, send a new error message
				_, sendErr := s.ChannelMessageSendComplex(targetChannelID, &discordgo.MessageSend{
					Embed:     errorEmbed,
					Reference: messageRef,
					AllowedMentions: &discordgo.MessageAllowedMentions{
						Parse:       []discordgo.AllowedMentionType{},
						RepliedUser: false,
					},
				})
				if sendErr != nil {
					log.Printf("Failed to send error message: %v", sendErr)
				}
			}
		} else {
			// No progress message to update, send error as new message
			errorEmbed := &discordgo.MessageEmbed{
				Title:       "❌ Request Failed",
				Description: fmt.Sprintf("Failed to process your request:\n\n```\n%v\n```", err),
				Color:       0xFF0000, // Red color for errors
				Footer: &discordgo.MessageEmbedFooter{
					Text: fmt.Sprintf("🤖 Model: %s", actualModel),
				},
			}

			_, sendErr := s.ChannelMessageSendComplex(targetChannelID, &discordgo.MessageSend{
				Embed:     errorEmbed,
				Reference: messageRef,
				AllowedMentions: &discordgo.MessageAllowedMentions{
					Parse:       []discordgo.AllowedMentionType{},
					RepliedUser: false,
				},
			})
			if sendErr != nil {
				log.Printf("Failed to send error message: %v", sendErr)
			}
		}
		return
	}

processStream:
	// Add fallback notification to warnings if we used fallback
	if actualModel != model {
		warnings = append(warnings, fmt.Sprintf("⚠️ Fallback to %s (original model failed)", actualModel))
		log.Printf("Using fallback model %s for response", actualModel)
	}

	// Atomically load config
	cfg := b.config.Load()
	usePlainResponses := cfg.UsePlainResponses

	maxLength := utils.MaxMessageLength
	if usePlainResponses {
		maxLength = utils.PlainMaxMessageLength
	} else {
		maxLength -= len(utils.StreamingIndicator)
	}

	var responseMessages []*discordgo.Message
	var responseContents []*strings.Builder
	defer func() {
		for _, builder := range responseContents {
			builder.Reset()
			builderPool.Put(builder)
		}
	}()
	var editTask *time.Timer
	var generatedImages [][]byte // Store generated images
	var imageMIMETypes []string  // Store MIME types for images
	lastEditTime := time.Now()
	firstContentReceived := false

	// Web search information is now passed as parameters

	// Create initial embed with warnings and footer info
	// Token usage info
	cfg = b.config.Load()

	tokenLimit := utils.DefaultTokenLimit
	if params, ok := cfg.Models[model]; ok && params.TokenLimit != nil {
		tokenLimit = *params.TokenLimit
	}

	// For streaming phase, omit token usage so it shows only at completion
	footerInfo := &utils.FooterInfo{
		Model:              actualModel,
		WebSearchPerformed: webSearchPerformed,
		SearchResultCount:  searchResultCount,
		// Token fields left zero; they will appear in final embed
	}

	// Add timeout monitoring
	timeoutTicker := time.NewTicker(30 * time.Second)
	defer timeoutTicker.Stop()

	// Channel to signal completion
	done := make(chan bool, 1)

	// Start a goroutine to monitor for timeout
	go func() {
		select {
		case <-ctx.Done():
			if !firstContentReceived {
				log.Printf("Context timeout reached, updating progress message")
				b.updateProgressWithError(s, progressMgr, "Request timed out after 5 minutes", actualModel)
			}
		case <-done:
			// Normal completion
			return
		}
	}()

	for response := range stream {
		if response.Error != nil {
			log.Printf("Stream error: %v", response.Error)

			// If the error suggests a fallback, and we haven't tried yet, attempt it.
			// This now also catches PrematureStreamFinishError.
			if b.llmClient.ShouldFallback(response.Error) && !fallbackAttempted {
				log.Printf("Fallback-triggering stream error, attempting fallback")
				fallbackAttempted = true

				// Try fallback model
				fallbackModel := b.config.Load().FallbackModel
				fallbackStream, fallbackErr := attemptStream(fallbackModel, true)
				if fallbackErr != nil {
					log.Printf("Fallback model also failed: %v", fallbackErr)
					// Continue with original error handling below
				} else {
					// Replace the stream with fallback stream and restart processing
					stream = fallbackStream
					goto processStream
				}
			}

			// Check if this is a quota exceeded error and provide user-friendly message
			var errorContent string
			errorStr := response.Error.Error()
			if strings.Contains(errorStr, "Error 429") && 
			   strings.Contains(errorStr, "You exceeded your current quota") &&
			   strings.Contains(errorStr, "GenerateContentInputTokensPerModelPerMinute-FreeTier") {
				errorContent = "❌ **Query Too Long**\n\nThis query exceeded the token limit. Please send a shorter version of your message."
			} else {
				// Show original error for other types of errors
				errorContent = fmt.Sprintf("❌ **Stream Error**\n\n```\n%v\n```", response.Error)
			}

			// If we haven't sent any content yet, update the progress message
			if !firstContentReceived && progressMgr != nil && progressMgr.GetMessageID() != "" {
				firstContentReceived = true // Mark as received to prevent redundant error message
				errorEmbed := &discordgo.MessageEmbed{
					Title:       "❌ Stream Error",
					Description: fmt.Sprintf("An error occurred while processing the response:\n\n```\n%v\n```", response.Error),
					Color:       0xFF0000, // Red color for errors
					Footer: &discordgo.MessageEmbedFooter{
						Text: fmt.Sprintf("🤖 Model: %s", actualModel),
					},
				}

				_, updateErr := s.ChannelMessageEditEmbed(progressMgr.GetChannelID(), progressMgr.GetMessageID(), errorEmbed)
				if updateErr != nil {
					log.Printf("Failed to update progress message with stream error: %v", updateErr)
				}
			} else if len(responseMessages) > 0 {
				// We already have response messages, append error to the last one
				lastMsg := responseMessages[len(responseMessages)-1]
				currentContent := ""
				if len(responseContents) > 0 {
					currentContent = responseContents[len(responseContents)-1].String()
				}

				// Create error embed with current content + error
				errorEmbed := utils.CreateEmbed(currentContent+"\n\n"+errorContent, warnings, true, footerInfo)

				_, err := s.ChannelMessageEditEmbed(lastMsg.ChannelID, lastMsg.ID, errorEmbed)
				if err != nil {
					log.Printf("Failed to edit message with stream error: %v", err)
				}
			} else {
				// No existing messages, send new error message
				errorEmbed := &discordgo.MessageEmbed{
					Title:       "❌ Stream Error",
					Description: fmt.Sprintf("An error occurred while processing the response:\n\n```\n%v\n```", response.Error),
					Color:       0xFF0000, // Red color for errors
					Footer: &discordgo.MessageEmbedFooter{
						Text: fmt.Sprintf("🤖 Model: %s", actualModel),
					},
				}

				_, sendErr := s.ChannelMessageSendComplex(targetChannelID, &discordgo.MessageSend{
					Embed:     errorEmbed,
					Reference: messageRef,
					AllowedMentions: &discordgo.MessageAllowedMentions{
						Parse:       []discordgo.AllowedMentionType{},
						RepliedUser: false,
					},
				})
				if sendErr != nil {
					log.Printf("Failed to send stream error message: %v", sendErr)
				}
			}
			break
		}

		if response.FinishReason != "" {
			// Stream finished
			break
		}

		// Handle image data if present
		if len(response.ImageData) > 0 {
			generatedImages = append(generatedImages, response.ImageData)
			imageMIMETypes = append(imageMIMETypes, response.ImageMIMEType)
			log.Printf("Received generated image: %d bytes, MIME type: %s", len(response.ImageData), response.ImageMIMEType)
		}

		// Skip empty content chunks
		if response.Content == "" && response.ImageData == nil {
			continue
		}

		// On first content, replace progress message
		if !firstContentReceived && progressMgr != nil {
			firstContentReceived = true
			// Clear progress message by updating to empty state
		}

		// Check if we need to start a new message
		needsNewMsg := len(responseContents) == 0 || (len(responseContents) > 0 && responseContents[len(responseContents)-1].Len()+len(response.Content) > maxLength)

		if needsNewMsg && len(responseContents) > 0 {
			// Finalize the current message before starting a new one
			if !usePlainResponses && len(responseMessages) > 0 {
				finalizeContent := responseContents[len(responseContents)-1].String()
				finalizeEmbed := utils.CreateEmbed(finalizeContent, warnings, false, footerInfo) // Still streaming, so incomplete

				lastMsg := responseMessages[len(responseMessages)-1]
				_, err := s.ChannelMessageEditEmbed(lastMsg.ChannelID, lastMsg.ID, finalizeEmbed)
				if err != nil {
					log.Printf("Failed to finalize message before split: %v", err)
				}
			}

			// Now start new message
			builder := builderPool.Get().(*strings.Builder)
			responseContents = append(responseContents, builder)
		} else if needsNewMsg {
			// First message
			builder := builderPool.Get().(*strings.Builder)
			responseContents = append(responseContents, builder)
		}

		responseContents[len(responseContents)-1].WriteString(response.Content)

		if !usePlainResponses {
			// Update embed more frequently
			readyToEdit := time.Since(lastEditTime) >= time.Duration(utils.EditDelaySeconds)*time.Second
			isGoodFinish := utils.IsGoodFinishReason(response.FinishReason)
			isFinalEdit := response.FinishReason != ""

			if needsNewMsg || readyToEdit || isFinalEdit {
				if editTask != nil {
					editTask.Stop()
				}

				content := responseContents[len(responseContents)-1].String()
				if !isFinalEdit {
					content += utils.StreamingIndicator
				}

				// Create updated embed with footer
				embed := utils.CreateEmbed(content, warnings, isFinalEdit && isGoodFinish, footerInfo)

				if needsNewMsg && len(responseContents) == 1 {
					// Replace progress message with actual response or send new message
					if progressMgr != nil && progressMgr.GetMessageID() != "" {
						// Update the progress message to show actual response
						_, err := s.ChannelMessageEditEmbed(progressMgr.GetChannelID(), progressMgr.GetMessageID(), embed)
						if err == nil {
							// Create a fake message object for tracking
							responseMsg := &discordgo.Message{
								ID:        progressMgr.GetMessageID(),
								ChannelID: progressMgr.GetChannelID(),
							}
							responseMessages = append(responseMessages, responseMsg)

							// Create node for response message
							responseNode := messaging.NewMsgNode()
							responseNode.ParentMsg = originalMsg.Message
							b.nodeManager.Set(responseMsg.ID, responseNode)
						} else {
							log.Printf("Failed to update progress message: %v", err)
						}
					} else {
						// Send new message if progress message update failed
						responseMsg, err := s.ChannelMessageSendComplex(targetChannelID, &discordgo.MessageSend{
							Embed:     embed,
							Reference: messageRef,
							AllowedMentions: &discordgo.MessageAllowedMentions{
								Parse:       []discordgo.AllowedMentionType{},
								RepliedUser: false,
							},
						})
						if err != nil {
							log.Printf("Failed to send message: %v", err)
							continue
						}

						responseMessages = append(responseMessages, responseMsg)

						// Create node for response message
						responseNode := messaging.NewMsgNode()
						responseNode.ParentMsg = originalMsg.Message
						b.nodeManager.Set(responseMsg.ID, responseNode)
					}
				} else if needsNewMsg && len(responseContents) > 1 {
					// Send new split message (previous message was already finalized above)
					var newRef *discordgo.MessageReference
					if len(responseMessages) > 0 {
						lastMsg := responseMessages[len(responseMessages)-1]
						newRef = &discordgo.MessageReference{
							MessageID: lastMsg.ID,
							ChannelID: lastMsg.ChannelID,
							GuildID:   originalMsg.GuildID,
						}
					} else {
						newRef = messageRef
					}
					responseMsg, err := s.ChannelMessageSendComplex(targetChannelID, &discordgo.MessageSend{
						Embed:     embed,
						Reference: newRef,
						AllowedMentions: &discordgo.MessageAllowedMentions{
							Parse:       []discordgo.AllowedMentionType{},
							RepliedUser: false,
						},
					})
					if err != nil {
						log.Printf("Failed to send split message: %v", err)
						continue
					}

					responseMessages = append(responseMessages, responseMsg)

					// Create node for response message
					responseNode := messaging.NewMsgNode()
					responseNode.ParentMsg = originalMsg.Message
					b.nodeManager.Set(responseMsg.ID, responseNode)
				} else if len(responseMessages) > 0 {
					// Edit existing message
					lastMsg := responseMessages[len(responseMessages)-1]
					_, err := s.ChannelMessageEditEmbed(lastMsg.ChannelID, lastMsg.ID, embed)
					if err != nil {
						log.Printf("Failed to edit message: %v", err)
					}
				}

				lastEditTime = time.Now()
				b.setLastTaskTime(time.Now())
			}
		}
	}

	// Final update to ensure completion

	if !usePlainResponses && len(responseMessages) > 0 && len(responseContents) > 0 {
		finalContent := responseContents[len(responseContents)-1].String()

		// Add token usage now that generation is complete
		finalCurrentTokens := utils.EstimateTokenCount(messages)
		finalFooterInfo := &utils.FooterInfo{
			Model:              actualModel,
			WebSearchPerformed: webSearchPerformed,
			SearchResultCount:  searchResultCount,
			CurrentTokens:      finalCurrentTokens,
			TokenLimit:         tokenLimit,
		}

		finalEmbed := utils.CreateEmbed(finalContent, warnings, true, finalFooterInfo)

		lastMsg := responseMessages[len(responseMessages)-1]

		// Add action buttons (download + view output better) to the final message
		actionButtons := utils.CreateActionButtons(lastMsg.ID, webSearchPerformed)

		if _, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
			Channel:    lastMsg.ChannelID,
			ID:         lastMsg.ID,
			Embeds:     &[]*discordgo.MessageEmbed{finalEmbed},
			Components: &actionButtons,
		}); err != nil {
			log.Printf("Failed to edit final message: %v", err)
		}
	}

	// Handle plain responses
	if usePlainResponses {
		currentRef := messageRef
		for i, contentBuilder := range responseContents {
			content := contentBuilder.String()
			// Add download button to the last plain response
			var components []discordgo.MessageComponent
			if i == len(responseContents)-1 {
				// This is the last message, add download button
				components = utils.CreateActionButtons("placeholder", webSearchPerformed)
			}

			responseMsg, err := s.ChannelMessageSendComplex(targetChannelID, &discordgo.MessageSend{
				Content:    content,
				Reference:  currentRef,
				Components: components,
				AllowedMentions: &discordgo.MessageAllowedMentions{
					Parse:       []discordgo.AllowedMentionType{},
					RepliedUser: false,
				},
			})
			if err != nil {
				log.Printf("Failed to send plain message: %v", err)
				continue
			}

			// Update message reference for subsequent messages
			currentRef = &discordgo.MessageReference{
				MessageID: responseMsg.ID,
				ChannelID: responseMsg.ChannelID,
				GuildID:   responseMsg.GuildID,
			}

			// Update button with actual message ID if this was the last message
			if i == len(responseContents)-1 {
				actionButtonsFinal := utils.CreateActionButtons(responseMsg.ID, webSearchPerformed)
				if _, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
					Channel:    responseMsg.ChannelID,
					ID:         responseMsg.ID,
					Content:    &content,
					Components: &actionButtonsFinal,
				}); err != nil {
					log.Printf("Failed to edit message with final buttons: %v", err)
				}
			}

			responseMessages = append(responseMessages, responseMsg)

			// Create node for response message
			responseNode := messaging.NewMsgNode()
			responseNode.ParentMsg = originalMsg.Message
			b.nodeManager.Set(responseMsg.ID, responseNode)
		}
	}

	// Update response nodes with full content
	fullContentBuilder := builderPool.Get().(*strings.Builder)
	defer func() {
		fullContentBuilder.Reset()
		builderPool.Put(fullContentBuilder)
	}()
	for _, sb := range responseContents {
		fullContentBuilder.WriteString(sb.String())
	}
	fullContent := fullContentBuilder.String()

	// Process tables and convert to images
	tableCtx := context.Background()
	processedContent, tableImages, err := b.tableRenderer.ProcessResponse(tableCtx, fullContent)
	if err != nil {
		log.Printf("Failed to process tables: %v", err)
		processedContent = fullContent // Fall back to original content
	}

	// Process charts and convert to images
	chartCtx := context.Background()
	chartImages, err := b.chartProcessor.ProcessResponse(chartCtx, fullContent)
	if err != nil {
		log.Printf("Failed to process charts: %v", err)
	}

	// Determine the correct message reference for replies (tables, charts, images).
	// We want to reply to the bot's own message that contained the content.
	var replyRef *discordgo.MessageReference
	if len(responseMessages) > 0 {
		lastBotMsg := responseMessages[len(responseMessages)-1]
		replyRef = &discordgo.MessageReference{
			MessageID: lastBotMsg.ID,
			ChannelID: lastBotMsg.ChannelID,
			GuildID:   originalMsg.GuildID,
		}
	} else {
		// Fallback to the original message reference if no bot messages were sent
		replyRef = messageRef
	}

	// Send table images as separate attachments if any were generated
	if len(tableImages) > 0 {
		for _, tableImage := range tableImages {
			// Convert to Discord file format
			file := &discordgo.File{
				Name:   tableImage.Filename,
				Reader: bytes.NewReader(tableImage.Data),
			}

			// Send table image as a separate message, replying to the bot's response
			_, err := s.ChannelMessageSendComplex(targetChannelID, &discordgo.MessageSend{
				Content:   fmt.Sprintf("📊 **Table:** %s", tableImage.Filename),
				Files:     []*discordgo.File{file},
				Reference: replyRef,
				AllowedMentions: &discordgo.MessageAllowedMentions{
					Parse:       []discordgo.AllowedMentionType{},
					RepliedUser: false,
				},
			})
			if err != nil {
				log.Printf("Failed to send table image: %v", err)
			}
		}
	}

	// Send chart images as separate attachments if any were generated
	if len(chartImages) > 0 {
		for _, chartImage := range chartImages {
			// Convert to Discord file format
			file := &discordgo.File{
				Name:   chartImage.Filename,
				Reader: bytes.NewReader(chartImage.Data),
			}

			// Send chart image as a separate message, replying to the bot's response
			_, err := s.ChannelMessageSendComplex(targetChannelID, &discordgo.MessageSend{
				Content:   "📈 **Generated Chart**",
				Files:     []*discordgo.File{file},
				Reference: replyRef,
				AllowedMentions: &discordgo.MessageAllowedMentions{
					Parse:       []discordgo.AllowedMentionType{},
					RepliedUser: false,
				},
			})
			if err != nil {
				log.Printf("Failed to send chart image: %v", err)
			}
		}
	}

	// Send generated images as separate attachments if any were generated
	if len(generatedImages) > 0 {
		for i, imageData := range generatedImages {
			// Determine file extension from MIME type
			var extension string
			if i < len(imageMIMETypes) {
				switch imageMIMETypes[i] {
				case "image/png":
					extension = "png"
				case "image/jpeg", "image/jpg":
					extension = "jpg"
				case "image/gif":
					extension = "gif"
				case "image/webp":
					extension = "webp"
				default:
					extension = "png" // Default to PNG
				}
			} else {
				extension = "png" // Default if MIME type not available
			}

			// Create filename with timestamp
			filename := fmt.Sprintf("gemini_generated_image_%d_%d.%s", time.Now().Unix(), i+1, extension)

			// Convert to Discord file format
			file := &discordgo.File{
				Name:   filename,
				Reader: bytes.NewReader(imageData),
			}

			// Send generated image as a separate message, replying to the bot's response
			_, err := s.ChannelMessageSendComplex(targetChannelID, &discordgo.MessageSend{
				Content:   "🎨 **Generated Image**",
				Files:     []*discordgo.File{file},
				Reference: replyRef,
				AllowedMentions: &discordgo.MessageAllowedMentions{
					Parse:       []discordgo.AllowedMentionType{},
					RepliedUser: false,
				},
			})
			if err != nil {
				log.Printf("Failed to send generated image: %v", err)
			}
		}
	}

	for _, responseMsg := range responseMessages {
		if node, exists := b.nodeManager.Get(responseMsg.ID); exists {
			node.SetText(processedContent)

			// Add generated images to the response node for conversation history
			if len(generatedImages) > 0 {
				for i, imageData := range generatedImages {
					var mimeType string
					if i < len(imageMIMETypes) {
						mimeType = imageMIMETypes[i]
					} else {
						mimeType = "image/png" // Default
					}

					generatedImg := messaging.GeneratedImageContent{
						Data:     imageData,
						MIMEType: mimeType,
					}
					node.AddGeneratedImage(generatedImg)
				}
			}

			// Persist the fully populated response node to the database cache so we don't re-process it after restart.
			if b.messageCache != nil {
				if err := b.messageCache.SaveNode(context.Background(), responseMsg.ID, node); err != nil {
					log.Printf("Failed to save response node to cache: %v", err)
				}
			}
		}
	}

	// Signal completion
	select {
	case done <- true:
	default:
		// Channel might be full, that's okay
	}

	// If we never received any content, attempt fallback before giving up
	if !firstContentReceived && !fallbackAttempted {
		log.Printf("No content received from %s, attempting fallback to GPT 4.1", actualModel)
		fallbackAttempted = true
		
		// Try fallback model
		fallbackModel := b.config.Load().FallbackModel
		fallbackStream, fallbackErr := attemptStream(fallbackModel, true)
		if fallbackErr != nil {
			log.Printf("Fallback model also failed: %v", fallbackErr)
			// Both models failed, show error
			if progressMgr != nil && progressMgr.GetMessageID() != "" {
				b.updateProgressWithError(s, progressMgr, "Both original and fallback models failed", actualModel)
			}
			return
		}
		
		// Replace the stream with fallback stream and restart processing
		stream = fallbackStream
		goto processStream
	}
	
	// If we still have no content after fallback attempt, clean up
	if !firstContentReceived && progressMgr != nil && progressMgr.GetMessageID() != "" {
		log.Printf("No content received even after fallback, cleaning up progress message")
		b.updateProgressWithError(s, progressMgr, "No response received from the model", actualModel)
	}
}
