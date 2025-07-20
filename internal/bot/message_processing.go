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

// processMessage processes a Discord message and populates a message node.
// It uses errgroup to concurrently handle I/O-bound tasks like API calls and file processing.
func (b *Bot) processMessage(s *discordgo.Session, msg *discordgo.Message, node *messaging.MsgNode, isCurrentMessage bool, progressMgr *utils.ProgressManager) {
	var cleanedContent string
	var hasSkipDirective bool

	// 1. Clean and Prepare Initial Content
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

	// Early exit for commands that don't need the full processing pipeline
	if strings.HasPrefix(lc, "veo ") && isCurrentMessage {
		b.handleVeoGeneration(s, msg, cleanedContent)
		return
	}

	// Find parent message early for context
	parentMsg, fetchFailed, err := utils.FindParentMessage(s, &discordgo.MessageCreate{Message: msg}, s.State.User)
	if err != nil {
		log.Printf("Error finding parent message: %v", err)
	}

	// --- Concurrent Processing Stage ---
	var mu sync.Mutex
	var lensContent, channelContent, attachmentText, extractedURLContent, webSearchResults string
	var images []messaging.ImageContent
	var audioFiles []messaging.AudioContent
	var hasBadAttachments bool
	var urlExtractionErr, webSearchErr error
	webSearchRequired := false
	shouldProcessURLs := false
	webSearchResultCount := 0

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

	// Task 3: Process Current Message Attachments
	eg.Go(func() error {
		currentImages, currentAudio, currentText, currentBad, currentShouldProcessURLs, err := processors.ProcessAttachments(gctx, msg.Attachments, b.fileProcessor)
		mu.Lock()
		defer mu.Unlock()
		images = append(images, currentImages...)
		audioFiles = append(audioFiles, currentAudio...)
		if attachmentText != "" && currentText != "" {
			attachmentText = attachmentText + "\n\n" + "**📎 Current Attachments:**\n" + currentText
		} else if currentText != "" {
			attachmentText = "**📎 Current Attachments:**\n" + currentText
		}
		hasBadAttachments = hasBadAttachments || currentBad
		shouldProcessURLs = shouldProcessURLs || currentShouldProcessURLs
		if err != nil {
			return fmt.Errorf("failed to process current attachments: %w", err)
		}
		return nil
	})

	// Task 4: Process Parent Message Attachments
	isDirectReply := msg.MessageReference != nil && msg.MessageReference.MessageID != ""
	if isCurrentMessage && parentMsg != nil && len(parentMsg.Attachments) > 0 && !isDirectReply {
		eg.Go(func() error {
			log.Printf("Processing %d attachments from parent message for non-reply context", len(parentMsg.Attachments))
			parentImages, parentAudio, parentText, parentBad, parentShouldProcessURLs, err := processors.ProcessAttachments(gctx, parentMsg.Attachments, b.fileProcessor)
			mu.Lock()
			defer mu.Unlock()
			images = append(parentImages, images...)
			audioFiles = append(parentAudio, audioFiles...)
			if parentText != "" {
				attachmentText = "**📎 Referenced Files (from previous message):**\n" + parentText + "\n\n" + attachmentText
			}
			hasBadAttachments = hasBadAttachments || parentBad
			shouldProcessURLs = shouldProcessURLs || parentShouldProcessURLs
			if err != nil {
				return fmt.Errorf("failed to process parent attachments: %w", err)
			}
			return nil
		})
	}

	// Task 5: URL Content Extraction
	contentForURLExtraction := strings.Join([]string{cleanedContent, utils.ExtractEmbedText(msg.Embeds)}, "\n")
	if contentForURLExtraction != "" && !isAskChannelQuery && shouldProcessURLs {
		eg.Go(func() error {
			detectedURLs := processors.DetectURLs(contentForURLExtraction)
			if len(detectedURLs) > 0 {
				res, err := b.webSearchClient.ExtractURLs(gctx, detectedURLs)
				mu.Lock()
				extractedURLContent = res
				urlExtractionErr = err
				mu.Unlock()
			}
			return nil // Don't fail the whole group for a URL error
		})
	}

	// Task 6: Web Search Decision and Execution (concurrently)
	if isCurrentMessage && !isGoogleLensQuery && !isAskChannelQuery {
		eg.Go(func() error {
			// Combine preliminary content for the decision model
			mu.Lock()
			tempContentParts := []string{cleanedContent, utils.ExtractEmbedText(msg.Embeds), attachmentText, extractedURLContent}
			tempFullContent := strings.Join(tempContentParts, "\n\n")
			mu.Unlock()

			userModel := b.userPrefs.GetUserModel(gctx, msg.Author.ID, "")
			if userModel == "gemini/gemini-2.0-flash-preview-image-generation" {
				log.Printf("Skipping web search for image generation model: %s", userModel)
				return nil
			}

			chatHistory := b.buildChatHistoryForWebSearch(s, msg)
			b.configMutex.RLock()
			cfg := b.config
			b.configMutex.RUnlock()
			userSystemPrompt := b.userPrefs.GetUserSystemPrompt(gctx, msg.Author.ID)
			systemPrompt := cfg.SystemPrompt
			if userSystemPrompt != "" {
				systemPrompt = userSystemPrompt
			}

			contentForWebSearchDecision := tempFullContent
			if hasSkipDirective {
				contentForWebSearchDecision = "SKIP_WEB_SEARCH_DECIDER\n\n" + tempFullContent
			}

			decisionCtx, cancel := context.WithTimeout(gctx, 45*time.Second)
			defer cancel()

			decision, err := b.webSearchClient.DecideWebSearch(decisionCtx, b.llmClient, chatHistory, contentForWebSearchDecision, msg.Author.ID, b.userPrefs, systemPrompt, images)
			if err != nil {
				log.Printf("Web search decision failed: %v", err)
				return nil // Don't fail the group for a decision error
			}

			if decision.WebSearchRequired && len(decision.SearchQueries) > 0 {
				log.Printf("Web search required. Queries: %s", strings.Join(decision.SearchQueries, ", "))
				searchCtx, searchCancel := context.WithTimeout(gctx, 60*time.Second)
				defer searchCancel()

				results, err := b.webSearchClient.SearchMultiple(searchCtx, decision.SearchQueries)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					webSearchErr = err
				} else {
					webSearchResults = results
					webSearchRequired = true
					webSearchResultCount = len(decision.SearchQueries) * cfg.WebSearch.MaxResults
				}
			} else {
				log.Printf("Web search not required for this query")
			}
			return nil
		})
	}

	// Wait for all concurrent tasks to complete
	if err := eg.Wait(); err != nil {
		log.Printf("Error during concurrent message processing: %v", err)
		// Errors from attachments are now propagated. We can decide to notify the user.
		// For now, we log and continue, as some content might still be usable.
	}

	// --- Aggregation and Final Processing ---
	var finalContent string
	if isGoogleLensQuery {
		finalContent = lensContent
	} else if isAskChannelQuery {
		finalContent = channelContent
	} else {
		finalContent = cleanedContent
	}

	embedText := utils.ExtractEmbedText(msg.Embeds)
	textParts := []string{}
	if finalContent != "" {
		textParts = append(textParts, finalContent)
	}
	if embedText != "" {
		textParts = append(textParts, embedText)
	}
	if attachmentText != "" {
		textParts = append(textParts, attachmentText)
	}
	if extractedURLContent != "" {
		textParts = append(textParts, fmt.Sprintf("extracted url content: %s", extractedURLContent))
	}
	if urlExtractionErr != nil {
		textParts = append(textParts, fmt.Sprintf("\n\n⚠️ URL extraction failed: %v", urlExtractionErr))
	}
	if webSearchResults != "" {
		textParts = append(textParts, fmt.Sprintf("\n\nweb search api results: %s", webSearchResults))
	}
	if webSearchErr != nil {
		textParts = append(textParts, fmt.Sprintf("\n\n⚠️ Web search failed: %v", webSearchErr))
	}

	fullContent := strings.Join(textParts, "\n\n")

	// --- Set Final Node Properties ---
	node.SetText(fullContent)
	node.SetImages(images)
	node.SetAudioFiles(audioFiles)
	node.SetWebSearchInfo(webSearchRequired, webSearchResultCount)
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
