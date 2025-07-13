package bot

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/utils"
)

// generateResponse generates and sends LLM response
func (b *Bot) generateResponse(s *discordgo.Session, originalMsg *discordgo.MessageCreate, model string, messages []messaging.OpenAIMessage, warnings []string, progressMgr *utils.ProgressManager, messageRef *discordgo.MessageReference, webSearchPerformed bool, searchResultCount int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Start streaming
	stream, err := b.llmClient.StreamChatCompletion(ctx, model, messages)
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
					Text: fmt.Sprintf("🤖 Model: %s", model),
				},
			}

			// Update the progress message to show the error
			_, updateErr := s.ChannelMessageEditEmbed(progressMgr.GetChannelID(), progressMgr.GetMessageID(), errorEmbed)
			if updateErr != nil {
				log.Printf("Failed to update progress message with error: %v", updateErr)

				// If we can't update the progress message, send a new error message
				_, sendErr := s.ChannelMessageSendComplex(originalMsg.ChannelID, &discordgo.MessageSend{
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
					Text: fmt.Sprintf("🤖 Model: %s", model),
				},
			}

			_, sendErr := s.ChannelMessageSendComplex(originalMsg.ChannelID, &discordgo.MessageSend{
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

	// Thread-safe access to config
	b.mu.RLock()
	usePlainResponses := b.config.UsePlainResponses
	b.mu.RUnlock()

	maxLength := utils.MaxMessageLength
	if usePlainResponses {
		maxLength = utils.PlainMaxMessageLength
	} else {
		maxLength -= len(utils.StreamingIndicator)
	}

	var responseMessages []*discordgo.Message
	var responseContents []string
	var editTask *time.Timer
	var totalContent string
	var generatedImages [][]byte // Store generated images
	var imageMIMETypes []string  // Store MIME types for images
	lastEditTime := time.Now()
	firstContentReceived := false

	// Web search information is now passed as parameters

	// Create initial embed with warnings and footer info
	// Token usage info
	b.mu.RLock()
	cfg := b.config
	b.mu.RUnlock()

	tokenLimit := utils.DefaultTokenLimit
	if params, ok := cfg.Models[model]; ok && params.TokenLimit != nil {
		tokenLimit = *params.TokenLimit
	}

	// For streaming phase, omit token usage so it shows only at completion
	footerInfo := &utils.FooterInfo{
		Model:              model,
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
				b.updateProgressWithError(s, progressMgr, "Request timed out after 5 minutes", model)
			}
		case <-done:
			// Normal completion
			return
		}
	}()

	for response := range stream {
		if response.Error != nil {
			log.Printf("Stream error: %v", response.Error)

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
						Text: fmt.Sprintf("🤖 Model: %s", model),
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
					currentContent = responseContents[len(responseContents)-1]
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
						Text: fmt.Sprintf("🤖 Model: %s", model),
					},
				}

				_, sendErr := s.ChannelMessageSendComplex(originalMsg.ChannelID, &discordgo.MessageSend{
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

		totalContent += response.Content

		// Check if we need to start a new message
		needsNewMsg := len(responseContents) == 0 || (len(responseContents) > 0 && len(responseContents[len(responseContents)-1]+response.Content) > maxLength)

		if needsNewMsg && len(responseContents) > 0 {
			// Finalize the current message before starting a new one
			if !usePlainResponses && len(responseMessages) > 0 {
				finalizeContent := responseContents[len(responseContents)-1]
				finalizeEmbed := utils.CreateEmbed(finalizeContent, warnings, false, footerInfo) // Still streaming, so incomplete

				lastMsg := responseMessages[len(responseMessages)-1]
				_, err := s.ChannelMessageEditEmbed(lastMsg.ChannelID, lastMsg.ID, finalizeEmbed)
				if err != nil {
					log.Printf("Failed to finalize message before split: %v", err)
				}
			}

			// Now start new message
			responseContents = append(responseContents, "")
		} else if needsNewMsg {
			// First message
			responseContents = append(responseContents, "")
		}

		responseContents[len(responseContents)-1] += response.Content

		if !usePlainResponses {
			// Update embed more frequently
			readyToEdit := time.Since(lastEditTime) >= time.Duration(utils.EditDelaySeconds)*time.Second
			isGoodFinish := utils.IsGoodFinishReason(response.FinishReason)
			isFinalEdit := response.FinishReason != ""

			if needsNewMsg || readyToEdit || isFinalEdit {
				if editTask != nil {
					editTask.Stop()
				}

				content := responseContents[len(responseContents)-1]
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
						responseMsg, err := s.ChannelMessageSendComplex(originalMsg.ChannelID, &discordgo.MessageSend{
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
					responseMsg, err := s.ChannelMessageSendComplex(originalMsg.ChannelID, &discordgo.MessageSend{
						Embed:     embed,
						Reference: messageRef,
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
		finalContent := responseContents[len(responseContents)-1]

		// Add token usage now that generation is complete
		finalCurrentTokens := utils.EstimateTokenCount(messages, cfg)
		finalFooterInfo := &utils.FooterInfo{
			Model:              model,
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
		for i, content := range responseContents {
			// Add download button to the last plain response
			var components []discordgo.MessageComponent
			if i == len(responseContents)-1 {
				// This is the last message, add download button
				components = utils.CreateActionButtons("placeholder", webSearchPerformed)
			}

			responseMsg, err := s.ChannelMessageSendComplex(originalMsg.ChannelID, &discordgo.MessageSend{
				Content:    content,
				Reference:  messageRef,
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
	fullContent := strings.Join(responseContents, "")

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

	// Send table images as separate attachments if any were generated
	if len(tableImages) > 0 {
		for _, tableImage := range tableImages {
			// Convert to Discord file format
			file := &discordgo.File{
				Name:   tableImage.Filename,
				Reader: bytes.NewReader(tableImage.Data),
			}

			// Send table image as a separate message
			_, err := s.ChannelMessageSendComplex(originalMsg.ChannelID, &discordgo.MessageSend{
				Content:   fmt.Sprintf("📊 **Table:** %s", tableImage.Filename),
				Files:     []*discordgo.File{file},
				Reference: messageRef,
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

			// Send chart image as a separate message
			_, err := s.ChannelMessageSendComplex(originalMsg.ChannelID, &discordgo.MessageSend{
				Content:   "📈 **Generated Chart**",
				Files:     []*discordgo.File{file},
				Reference: messageRef,
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

			// Send generated image as a separate message
			_, err := s.ChannelMessageSendComplex(originalMsg.ChannelID, &discordgo.MessageSend{
				Content:   "🎨 **Generated Image**",
				Files:     []*discordgo.File{file},
				Reference: messageRef,
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
				if err := b.messageCache.SaveNode(responseMsg.ID, node); err != nil {
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

	// If we never received any content and still have a progress message, ensure it's cleaned up
	if !firstContentReceived && progressMgr != nil && progressMgr.GetMessageID() != "" {
		log.Printf("No content received, cleaning up progress message")
		b.updateProgressWithError(s, progressMgr, "No response received from the model", model)
	}
}
