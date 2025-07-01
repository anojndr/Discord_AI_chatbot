package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/processors"
	"DiscordAIChatbot/internal/utils"
)

// processMessage processes a Discord message and populates a message node
func (b *Bot) processMessage(s *discordgo.Session, msg *discordgo.Message, node *messaging.MsgNode, isCurrentMessage bool, progressMgr *utils.ProgressManager) {
	var cleanedContent string

	var hasSkipDirective bool
	if msg.Author.ID == s.State.User.ID {
		// This is an assistant message, use content as-is
		cleanedContent = msg.Content
	} else {
		// This is a user message
		cleanedContent = msg.Content

		// Check for SKIP_WEB_SEARCH_DECIDER directive but don't remove it yet
		// We need to preserve it for the web search decider call
		if strings.HasPrefix(cleanedContent, "SKIP_WEB_SEARCH_DECIDER\n\n") {
			hasSkipDirective = true
			// Only remove it for display/processing purposes, but keep original for web search decision
			cleanedContent = strings.TrimPrefix(cleanedContent, "SKIP_WEB_SEARCH_DECIDER\n\n")
		}

		// Remove bot mention and "at ai" prefix
		botMention := s.State.User.Mention()
		isReply := msg.MessageReference != nil && msg.MessageReference.MessageID != ""
		cleanedContent = utils.RemoveMentionAndAtAIPrefix(cleanedContent, botMention, isReply)
	}

	// Detect explicit Google Lens invocation (query must start with "googlelens ")
	lc := strings.ToLower(strings.TrimSpace(cleanedContent))
	isGoogleLensQuery := false
	if strings.HasPrefix(lc, "googlelens") && isCurrentMessage {
		isGoogleLensQuery = true
		// Extract the remainder after the keyword
		remainder := strings.TrimSpace(cleanedContent[len("googlelens"):])

		// Check if user provided an image URL or if there are image attachments in the message
		var imageURL string
		var qParam string

		// First check if user provided an explicit image URL
		parts := strings.Fields(remainder)
		if len(parts) > 0 {
			// Check if first part looks like a URL
			firstPart := parts[0]
			if strings.HasPrefix(firstPart, "http://") || strings.HasPrefix(firstPart, "https://") {
				imageURL = firstPart
				// Optional refinement query: everything after the URL
				qParam = strings.TrimSpace(strings.TrimPrefix(remainder, imageURL))
			} else {
				// No URL provided, check for image attachments in the current message
				for _, attachment := range msg.Attachments {
					if strings.HasPrefix(attachment.ContentType, "image/") {
						imageURL = attachment.URL
						qParam = remainder // Use entire remainder as query since no URL was provided
						break
					}
				}
			}
		} else {
			// No parameters provided, check for image attachments in the current message
			for _, attachment := range msg.Attachments {
				if strings.HasPrefix(attachment.ContentType, "image/") {
					imageURL = attachment.URL
					break
				}
			}
		}

		if imageURL != "" {
			// Debug logging to see what URL we're actually sending
			log.Printf("Google Lens: Processing image URL: %s", imageURL)
			log.Printf("Google Lens: Query parameter: %s", qParam)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			lensResults, err := b.googleLensClient.Search(ctx, imageURL, &processors.SearchOptions{
				// Note: Not passing Query to Google Lens API to avoid "no results" issues
				// The query will be included in the final message to the LLM
			})
			if err != nil {
				log.Printf("Google Lens search failed: %v", err)
				cleanedContent = fmt.Sprintf("user query: %s\n\n⚠️ Google Lens search failed: %v", remainder, err)
			} else if lensResults == "" {
				cleanedContent = fmt.Sprintf("user query: %s\n\n⚠️ Google Lens found no visual matches.", remainder)
			} else {
				// Append the Lens results to the user's query (sans the keyword)
				// The user's query will be used by the LLM to interpret the visual matches
				cleanedContent = fmt.Sprintf("user query: %s\n\ngoogle lens api results: %s", remainder, lensResults)
			}
		} else {
			cleanedContent = fmt.Sprintf("user query: %s\n\n⚠️ Google Lens requires an image URL or image attachment.", remainder)
		}
	}

	// Find parent message early so we can process its attachments if needed
	parentMsg, fetchFailed, err := utils.FindParentMessage(s, &discordgo.MessageCreate{Message: msg}, s.State.User)
	if err != nil {
		log.Printf("Error finding parent message: %v", err)
	}

	// Process attachments from current message early so they can be included in web search decision
	images, attachmentText, hasBadAttachments, err := processors.ProcessAttachments(context.Background(), msg.Attachments, b.fileProcessor)
	if err != nil {
		log.Printf("Failed to process attachments: %v", err)
	}

	// Don't process parent message attachments when this is a direct reply, since the parent message
	// will already be included in the conversation chain as a separate message.
	// Only process parent attachments for non-reply contexts where parent wouldn't be included otherwise.
	if isCurrentMessage && parentMsg != nil && len(parentMsg.Attachments) > 0 {
		// Simple check: if this message has a MessageReference, it's a direct reply
		isDirectReply := msg.MessageReference != nil && msg.MessageReference.MessageID != ""

		if !isDirectReply {
			// This is not a direct reply, so parent attachments won't be in conversation history
			// Process them for context (e.g., thread conversations, implicit continuations)
			log.Printf("Processing %d attachments from parent message for non-reply context", len(parentMsg.Attachments))
			parentImages, parentAttachmentText, parentHasBadAttachments, err := processors.ProcessAttachments(context.Background(), parentMsg.Attachments, b.fileProcessor)
			if err != nil {
				log.Printf("Failed to process parent attachments: %v", err)
			} else {
				// Combine parent and current attachments
				if len(parentImages) > 0 {
					images = append(parentImages, images...)
				}

				if parentAttachmentText != "" {
					if attachmentText != "" {
						attachmentText = fmt.Sprintf("**📎 Referenced Files (from previous message):**\n%s\n\n**📎 Current Attachments:**\n%s", parentAttachmentText, attachmentText)
					} else {
						attachmentText = fmt.Sprintf("**📎 Referenced Files (from previous message):**\n%s", parentAttachmentText)
					}
				}

				// Combine bad attachment flags
				hasBadAttachments = hasBadAttachments || parentHasBadAttachments
			}
		} else {
			// This is a direct reply, parent message will be in conversation history
			log.Printf("Skipping parent message attachment processing for direct reply (parent message will be in conversation history)")
		}
	}

	// Extract embed text
	embedText := utils.ExtractEmbedText(msg.Embeds)

	// Combine all text for web search decision (including attachments)
	var fullContent string
	var textParts []string
	if cleanedContent != "" {
		textParts = append(textParts, cleanedContent)
	}
	if embedText != "" {
		textParts = append(textParts, embedText)
	}
	if attachmentText != "" {
		textParts = append(textParts, attachmentText)
	}
	fullContent = strings.Join(textParts, "\n")

	// Combine only message text and embeds for URL extraction (excluding attachment text)
	var contentForURLExtraction string
	var urlTextParts []string
	if cleanedContent != "" {
		urlTextParts = append(urlTextParts, cleanedContent)
	}
	if embedText != "" {
		urlTextParts = append(urlTextParts, embedText)
	}
	contentForURLExtraction = strings.Join(urlTextParts, "\n")

	// Perform URL extraction only on message text and embeds, not file content
	if contentForURLExtraction != "" {
		messageType := "current"
		if !isCurrentMessage {
			messageType = "historical"
		}
		log.Printf("Analyzing %s message: %s", messageType, contentForURLExtraction)

		// Check for URLs in the message (excluding file content)
		detectedURLs := processors.DetectURLs(contentForURLExtraction)
		if len(detectedURLs) > 0 {
			log.Printf("Detected %d URL(s) in %s message: %v", len(detectedURLs), messageType, detectedURLs)

			// Extract content from URLs
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			extractedContent, err := b.webSearchClient.ExtractURLs(ctx, detectedURLs)
			if err != nil {
				log.Printf("URL extraction failed for %s message: %v", messageType, err)
				// Continue with original content and note the failure
				fullContent = fmt.Sprintf("%s\n\n⚠️ URL extraction failed: %v", fullContent, err)
			} else {
				log.Printf("Successfully extracted content from URLs in %s message", messageType)
				// Append extracted content to the original query
				if isCurrentMessage {
					fullContent = fmt.Sprintf("user query: %s\n\nextracted url content: %s", fullContent, extractedContent)
				} else {
					fullContent = fmt.Sprintf("historical message: %s\n\nextracted url content: %s", fullContent, extractedContent)
				}
			}
		}

		// Only perform web search for the current message, not parent messages
		if isCurrentMessage {
			userModel := b.userPrefs.GetUserModel(msg.Author.ID, "")
			if userModel == "gemini/gemini-2.0-flash-preview-image-generation" {
				log.Printf("Skipping web search for image generation model: %s", userModel)
				node.SetWebSearchInfo(false, 0)
			} else if isGoogleLensQuery {
				log.Printf("Skipping web search decider for Google Lens query")
				node.SetWebSearchInfo(false, 0)
			} else {
				// Build chat history for context with images preserved per message
				chatHistory := b.buildChatHistoryForWebSearch(s, msg)

				// Get user's custom system prompt or fall back to default for web search decision
				b.mu.RLock()
				cfg := b.config
				b.mu.RUnlock()
				userSystemPrompt := b.userPrefs.GetUserSystemPrompt(msg.Author.ID)
				systemPrompt := cfg.SystemPrompt
				if userSystemPrompt != "" {
					systemPrompt = userSystemPrompt
				}

				// Use user's preferred model to decide if web search is needed (now includes attachment content)
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()

				// For web search decision, use original content with directive preserved if present
				contentForWebSearchDecision := fullContent
				if hasSkipDirective {
					contentForWebSearchDecision = "SKIP_WEB_SEARCH_DECIDER\n\n" + fullContent
				}

				decision, err := b.webSearchClient.DecideWebSearch(ctx, b.llmClient, chatHistory, contentForWebSearchDecision, msg.Author.ID, b.userPrefs, systemPrompt, images)
				if err != nil {
					log.Printf("Web search decision failed: %v", err)
					// Continue without web search if decision fails
					node.SetWebSearchInfo(false, 0)
				} else if decision.WebSearchRequired && len(decision.SearchQueries) > 0 {
					log.Printf("Web search required. Queries: %s", strings.Join(decision.SearchQueries, ", "))

					// Perform web searches
					searchResults, err := b.webSearchClient.SearchMultiple(ctx, decision.SearchQueries)
					if err != nil {
						log.Printf("Web search failed: %v", err)
						// If search fails, continue with current content (which may include URL extracts)
						fullContent = fmt.Sprintf("%s\n\n⚠️ Web search failed: %v", fullContent, err)
						node.SetWebSearchInfo(true, 0) // Search was attempted but failed
					} else {
						// Thread-safe access to config for result count estimation
						b.mu.RLock()
						maxResults := b.config.WebSearch.MaxResults
						b.mu.RUnlock()

						// Count results - estimate from search response
						resultCount := len(decision.SearchQueries) * maxResults
						// Append search results to the current content (which may include URL extracts)
						fullContent = fmt.Sprintf("%s\n\nweb search api results: %s", fullContent, searchResults)
						node.SetWebSearchInfo(true, resultCount)
					}
				} else {
					log.Printf("Web search not required for this query")
					node.SetWebSearchInfo(false, 0)
				}
			}
		}
	} else if fullContent != "" && !isCurrentMessage {
		log.Printf("Processing historical message (with URL extraction but no web search): %s", fullContent)
	}

	// Set node properties using the already processed content
	node.SetText(fullContent)
	node.SetImages(images)
	node.HasBadAttachments = hasBadAttachments

	if msg.Author.ID == s.State.User.ID {
		node.Role = "assistant"
		node.UserID = ""
	} else {
		node.Role = "user"
		node.UserID = msg.Author.ID
	}

	node.ParentMsg = parentMsg
	node.FetchParentFailed = fetchFailed

	// Persist the processed node so we don't have to redo this work after restart.
	if b.messageCache != nil {
		if err := b.messageCache.SaveNode(msg.ID, node); err != nil {
			log.Printf("Failed to save message node to cache: %v", err)
		}
	}
}

// buildChatHistoryForWebSearch builds structured chat history for web search decision context
// Uses the EXACT same buildConversationChain function as the main model to ensure 100% consistency
// Excludes the current message since it's passed separately as latestQuery
func (b *Bot) buildChatHistoryForWebSearch(s *discordgo.Session, msg *discordgo.Message) []messaging.OpenAIMessage {
	// If there's no parent message, return empty history
	if msg.MessageReference == nil || msg.MessageReference.MessageID == "" {
		return []messaging.OpenAIMessage{}
	}

	// Get the parent message
	parentMsg, err := s.ChannelMessage(msg.ChannelID, msg.MessageReference.MessageID)
	if err != nil {
		log.Printf("Failed to get parent message for web search history: %v", err)
		return []messaging.OpenAIMessage{}
	}

	// Create a fake MessageCreate for the parent message to use with buildConversationChain
	parentMsgCreate := &discordgo.MessageCreate{
		Message: parentMsg,
	}

	// Create a minimal progress manager for web search context
	progressMgr := utils.NewProgressManager(s, msg.ChannelID)

	// Use the exact same buildConversationChain function as the main model but with web search disabled
	// This ensures 100% identical behavior including image handling, etc.
	// but prevents infinite recursion and duplicate web search analysis
	messages, _ := b.buildConversationChainWithWebSearch(s, parentMsgCreate, true, false, false, progressMgr)
	// Note: We ignore warnings since they're not displayed in web search context

	// buildConversationChain returns messages in "newest first" order, but for web search context
	// we want chronological order (oldest first) so the conversation flows naturally
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages
}
