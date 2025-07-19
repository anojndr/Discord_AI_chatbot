package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
	"sync"

	"golang.org/x/sync/errgroup"

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
		cleanedContent = msg.Content
	} else {
		cleanedContent = msg.Content
		if strings.HasPrefix(cleanedContent, "SKIP_WEB_SEARCH_DECIDER\n\n") {
			hasSkipDirective = true
			cleanedContent = strings.TrimPrefix(cleanedContent, "SKIP_WEB_SEARCH_DECIDER\n\n")
		}
		botMention := s.State.User.Mention()
		isReply := msg.MessageReference != nil && msg.MessageReference.MessageID != ""
		cleanedContent = utils.RemoveMentionAndAtAIPrefix(cleanedContent, botMention, isReply)
	}

	lc := strings.ToLower(strings.TrimSpace(cleanedContent))
	isGoogleLensQuery := strings.HasPrefix(lc, "googlelens") && isCurrentMessage
	isAskChannelQuery, channelQuery := processors.IsAskChannelQuery(cleanedContent)

	// Early exit for Veo generation
	if strings.HasPrefix(lc, "veo ") && isCurrentMessage {
		b.handleVeoGeneration(s, msg, cleanedContent)
		return
	}

	// Find parent message early for attachment processing
	parentMsg, fetchFailed, err := utils.FindParentMessage(s, &discordgo.MessageCreate{Message: msg}, s.State.User)
	if err != nil {
		log.Printf("Error finding parent message: %v", err)
	}

	// --- Concurrent Processing Stage ---
	var mu sync.Mutex
	var lensContent string
	var channelContent string
	var images []messaging.ImageContent
	var audioFiles []messaging.AudioContent
	var attachmentText string
	var hasBadAttachments bool
	var extractedURLContent string
	var urlExtractionErr error

	eg, gctx := errgroup.WithContext(context.Background())

	// Task 1: Google Lens Search
	if isGoogleLensQuery {
		eg.Go(func() error {
			res, err := b.handleGoogleLensQuery(gctx, msg, cleanedContent)
			if err != nil {
				return fmt.Errorf("google Lens query failed: %w", err)
			}
			mu.Lock()
			lensContent = res
			mu.Unlock()
			return nil
		})
	}

	// Task 2: AskChannel Message Fetching
	if isAskChannelQuery && !isGoogleLensQuery && isCurrentMessage {
		eg.Go(func() error {
			res, err := b.handleAskChannelQuery(gctx, s, msg, channelQuery)
			if err != nil {
				return fmt.Errorf("askchannel query failed: %w", err)
			}
			mu.Lock()
			channelContent = res
			mu.Unlock()
			return nil
		})
	}

	// Task 3: Process Attachments (Current and Parent)
	eg.Go(func() error {
		// Process current message attachments
		currentImages, currentAudio, currentText, currentBad, err := processors.ProcessAttachments(gctx, msg.Attachments, b.fileProcessor)
		if err != nil {
			log.Printf("Failed to process attachments: %v", err)
			// Continue even if processing fails
		}

		// Process parent message attachments if applicable
		var parentImages []messaging.ImageContent
		var parentAudio []messaging.AudioContent
		var parentText string
		var parentBad bool
		isDirectReply := msg.MessageReference != nil && msg.MessageReference.MessageID != ""
		if isCurrentMessage && parentMsg != nil && len(parentMsg.Attachments) > 0 && !isDirectReply {
			log.Printf("Processing %d attachments from parent message for non-reply context", len(parentMsg.Attachments))
			parentImages, parentAudio, parentText, parentBad, err = processors.ProcessAttachments(gctx, parentMsg.Attachments, b.fileProcessor)
			if err != nil {
				log.Printf("Failed to process parent attachments: %v", err)
			}
		}

		mu.Lock()
		defer mu.Unlock()
		images = append(parentImages, currentImages...)
		audioFiles = append(parentAudio, currentAudio...)
		hasBadAttachments = currentBad || parentBad
		if parentText != "" {
			if currentText != "" {
				attachmentText = fmt.Sprintf("**📎 Referenced Files (from previous message):**\n%s\n\n**📎 Current Attachments:**\n%s", parentText, currentText)
			} else {
				attachmentText = fmt.Sprintf("**📎 Referenced Files (from previous message):**\n%s", parentText)
			}
		} else {
			attachmentText = currentText
		}
		return nil
	})

	// Task 4: URL Content Extraction
	embedTextForURL := utils.ExtractEmbedText(msg.Embeds)
	contentForURLExtraction := strings.Join([]string{cleanedContent, embedTextForURL}, "\n")
	if contentForURLExtraction != "" && !isAskChannelQuery {
		eg.Go(func() error {
			detectedURLs := processors.DetectURLs(contentForURLExtraction)
			if len(detectedURLs) > 0 {
				messageType := "current"
				if !isCurrentMessage {
					messageType = "historical"
				}
				log.Printf("Detected %d URL(s) in %s message: %v", len(detectedURLs), messageType, detectedURLs)

				ctx, cancel := context.WithTimeout(gctx, 60*time.Second)
				defer cancel()

				extracted, err := b.webSearchClient.ExtractURLs(ctx, detectedURLs)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					urlExtractionErr = err
					log.Printf("URL extraction failed for %s message: %v", messageType, err)
				} else {
					log.Printf("Successfully extracted content from URLs in %s message", messageType)
					extractedURLContent = extracted
				}
			}
			return nil
		})
	}

	// Wait for all concurrent tasks to complete
	if err := eg.Wait(); err != nil {
		log.Printf("Error during concurrent message processing: %v", err)
		// Decide how to handle partial failures, for now, we log and continue
	}

	// --- Aggregation and Sequential Processing Stage ---

	// Determine the base content after concurrent tasks
	var finalContent string
	if isGoogleLensQuery {
		finalContent = lensContent
	} else if isAskChannelQuery {
		finalContent = channelContent
	} else {
		finalContent = cleanedContent
	}

	// Combine all text parts
	embedText := utils.ExtractEmbedText(msg.Embeds)
	var textParts []string
	if finalContent != "" {
		textParts = append(textParts, finalContent)
	}
	if embedText != "" {
		textParts = append(textParts, embedText)
	}
	if attachmentText != "" {
		textParts = append(textParts, attachmentText)
	}

	// Handle URL extraction results
	if extractedURLContent != "" {
		if isCurrentMessage {
			textParts = append(textParts, fmt.Sprintf("extracted url content: %s", extractedURLContent))
		} else {
			textParts = append(textParts, fmt.Sprintf("historical message extracted url content: %s", extractedURLContent))
		}
	}
	if urlExtractionErr != nil {
		textParts = append(textParts, fmt.Sprintf("\n\n⚠️ URL extraction failed: %v", urlExtractionErr))
	}

	fullContent := strings.Join(textParts, "\n\n")

	// --- Web Search Decision and Execution ---
	if isCurrentMessage && !isGoogleLensQuery && !isAskChannelQuery {
		userModel := b.userPrefs.GetUserModel(context.Background(), msg.Author.ID, "")
		if userModel == "gemini/gemini-2.0-flash-preview-image-generation" {
			log.Printf("Skipping web search for image generation model: %s", userModel)
			node.SetWebSearchInfo(false, 0)
		} else {
			chatHistory := b.buildChatHistoryForWebSearch(s, msg)
			b.configMutex.RLock()
			cfg := b.config
			b.configMutex.RUnlock()
			userSystemPrompt := b.userPrefs.GetUserSystemPrompt(context.Background(), msg.Author.ID)
			systemPrompt := cfg.SystemPrompt
			if userSystemPrompt != "" {
				systemPrompt = userSystemPrompt
			}

			contentForWebSearchDecision := fullContent
			if hasSkipDirective {
				contentForWebSearchDecision = "SKIP_WEB_SEARCH_DECIDER\n\n" + fullContent
			}

			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()

			decision, err := b.webSearchClient.DecideWebSearch(ctx, b.llmClient, chatHistory, contentForWebSearchDecision, msg.Author.ID, b.userPrefs, systemPrompt, images)
			if err != nil {
				log.Printf("Web search decision failed: %v", err)
				node.SetWebSearchInfo(false, 0)
			} else if decision.WebSearchRequired && len(decision.SearchQueries) > 0 {
				log.Printf("Web search required. Queries: %s", strings.Join(decision.SearchQueries, ", "))
				searchResults, err := b.webSearchClient.SearchMultiple(ctx, decision.SearchQueries)
				if err != nil {
					log.Printf("Web search failed: %v", err)
					fullContent = fmt.Sprintf("%s\n\n⚠️ Web search failed: %v", fullContent, err)
					node.SetWebSearchInfo(true, 0)
				} else {
					b.configMutex.RLock()
					maxResults := b.config.WebSearch.MaxResults
					b.configMutex.RUnlock()
					resultCount := len(decision.SearchQueries) * maxResults
					fullContent = fmt.Sprintf("%s\n\nweb search api results: %s", fullContent, searchResults)
					node.SetWebSearchInfo(true, resultCount)
				}
			} else {
				log.Printf("Web search not required for this query")
				node.SetWebSearchInfo(false, 0)
			}
		}
	}

	// Set node properties
	node.SetText(fullContent)
	node.SetImages(images)
	node.SetAudioFiles(audioFiles)
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

	// Persist the processed node
	if b.messageCache != nil {
		if err := b.messageCache.SaveNode(context.Background(), msg.ID, node); err != nil {
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


// handleVeoGeneration handles the "veo" command to generate a video.
func (b *Bot) handleVeoGeneration(s *discordgo.Session, msg *discordgo.Message, cleanedContent string) {
	prompt := strings.TrimSpace(cleanedContent[len("veo"):])
	go func() {
		thinkingMsg, _ := s.ChannelMessageSend(msg.ChannelID, "Generating video with Veo, please wait...")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		videoData, err := b.llmClient.GenerateVideo(ctx, "gemini/veo-3.0-generate-preview", prompt)

		if thinkingMsg != nil {
			if err := s.ChannelMessageDelete(thinkingMsg.ChannelID, thinkingMsg.ID); err != nil {
				log.Printf("Failed to delete 'thinking' message: %v", err)
			}
		}

		if err != nil {
			log.Printf("Failed to generate video: %v", err)
			if _, err := s.ChannelMessageSend(msg.ChannelID, fmt.Sprintf("❌ Failed to generate video: %v", err)); err != nil {
				log.Printf("Failed to send video generation failure message: %v", err)
			}
			return
		}

		if _, err := s.ChannelMessageSendComplex(msg.ChannelID, &discordgo.MessageSend{
			Content: "✅ Video generated successfully!",
			Files: []*discordgo.File{
				{
					Name:        "video.mp4",
					ContentType: "video/mp4",
					Reader:      strings.NewReader(string(videoData)),
				},
			},
			Reference: msg.Reference(),
		}); err != nil {
			log.Printf("Failed to send video message: %v", err)
		}
	}()
}

// handleGoogleLensQuery handles a Google Lens query.
func (b *Bot) handleGoogleLensQuery(ctx context.Context, msg *discordgo.Message, cleanedContent string) (string, error) {
	remainder := strings.TrimSpace(cleanedContent[len("googlelens"):])
	var imageURL string
	var qParam string

	parts := strings.Fields(remainder)
	if len(parts) > 0 {
		firstPart := parts[0]
		if strings.HasPrefix(firstPart, "http://") || strings.HasPrefix(firstPart, "https://") {
			imageURL = firstPart
			qParam = strings.TrimSpace(strings.TrimPrefix(remainder, imageURL))
		} else {
			for _, attachment := range msg.Attachments {
				if strings.HasPrefix(attachment.ContentType, "image/") {
					imageURL = attachment.URL
					qParam = remainder
					break
				}
			}
		}
	} else {
		for _, attachment := range msg.Attachments {
			if strings.HasPrefix(attachment.ContentType, "image/") {
				imageURL = attachment.URL
				break
			}
		}
	}

	if imageURL == "" {
		return fmt.Sprintf("user query: %s\n\n⚠️ Google Lens requires an image URL or image attachment.", remainder), nil
	}

	log.Printf("Google Lens: Processing image URL: %s", imageURL)
	log.Printf("Google Lens: Query parameter: %s", qParam)

	lensCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	lensResults, err := b.googleLensClient.Search(lensCtx, imageURL, &processors.SearchOptions{})
	if err != nil {
		log.Printf("Google Lens search failed: %v", err)
		return fmt.Sprintf("user query: %s\n\n⚠️ Google Lens search failed: %v", remainder, err), nil
	}
	if lensResults == "" {
		return fmt.Sprintf("user query: %s\n\n⚠️ Google Lens found no visual matches.", remainder), nil
	}

	return fmt.Sprintf("user query: %s\n\ngoogle lens api results: %s", remainder, lensResults), nil
}

// handleAskChannelQuery handles an "askchannel" query.
func (b *Bot) handleAskChannelQuery(ctx context.Context, s *discordgo.Session, msg *discordgo.Message, channelQuery string) (string, error) {
	log.Printf("Detected askchannel query: %s", channelQuery)

	b.configMutex.RLock()
	cfg := b.config
	b.configMutex.RUnlock()

	userModel := b.userPrefs.GetUserModel(context.Background(), msg.Author.ID, cfg.GetDefaultModel())
	modelTokenLimit := cfg.GetModelTokenLimit(userModel)
	tokenThreshold := cfg.GetChannelTokenThreshold()

	channelResult, err := b.channelProcessor.FetchChannelMessages(ctx, s, msg.ChannelID, channelQuery, s.State.User.ID, modelTokenLimit, tokenThreshold, cfg)
	if err != nil {
		log.Printf("Failed to fetch channel messages: %v", err)
		return fmt.Sprintf("user query: %s\n\n⚠️ Failed to fetch channel messages: %v", channelQuery, err), nil
	}

	var contextParts []string
	contextParts = append(contextParts, fmt.Sprintf("user query: %s", channelQuery))

	if len(channelResult.UserMessageCounts) > 0 {
		contextParts = append(contextParts, fmt.Sprintf("\nmessage count summary (%d total messages):", channelResult.TotalMessages))
		for username, count := range channelResult.UserMessageCounts {
			contextParts = append(contextParts, fmt.Sprintf("%s: %d", username, count))
		}
	}

	contextParts = append(contextParts, "\nchannel history context:")

	for _, channelMsg := range channelResult.Messages {
		if content, ok := channelMsg.Content.(string); ok {
			contextParts = append(contextParts, content)
		}
	}

	log.Printf("Added %d channel messages to context from %d users", len(channelResult.Messages), len(channelResult.UserMessageCounts))
	return strings.Join(contextParts, "\n"), nil
}
