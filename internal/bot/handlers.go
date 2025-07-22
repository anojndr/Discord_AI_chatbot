package bot

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/auth"
	"DiscordAIChatbot/internal/config"
	contextmgr "DiscordAIChatbot/internal/context"
	"DiscordAIChatbot/internal/utils"
)

// onReady handles the ready event
func (b *Bot) onReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Printf("Bot is ready! Logged in as %s", event.User.String())

	// Atomically load config
	cfg := b.config.Load()
	clientID := cfg.ClientID
	statusMessage := cfg.StatusMessage

	// Set custom activity (now that websocket connection is established)
	if statusMessage == "" {
		statusMessage = "i love you"
	}
	if len(statusMessage) > config.MaxStatusMessageLength {
		statusMessage = statusMessage[:config.MaxStatusMessageLength]
	}

	if err := s.UpdateGameStatus(0, statusMessage); err != nil {
		// Non-fatal error, just log it
		log.Printf("Failed to update game status: %v", err)
	}

	// Print invite URL if client ID is configured
	if clientID != "" {
		inviteURL := fmt.Sprintf("https://discord.com/oauth2/authorize?client_id=%s&permissions=412317273088&scope=bot", clientID)
		log.Printf("\nBOT INVITE URL:\n%s\n", inviteURL)
	}

	// Register slash commands
	err := b.registerCommands()
	if err != nil {
		log.Printf("Failed to register commands: %v", err)
	}
}

// registerCommands registers slash commands
func (b *Bot) registerCommands() error {
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "model",
			Description: "View or switch the current model",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:         discordgo.ApplicationCommandOptionString,
					Name:         "model",
					Description:  "The model to switch to",
					Required:     true,
					Autocomplete: true,
				},
			},
		},
		{
			Name:        "systemprompt",
			Description: "View, set, or clear your personal system prompt",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "action",
					Description: "Action to perform",
					Required:    true,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{
							Name:  "view",
							Value: "view",
						},
						{
							Name:  "set",
							Value: "set",
						},
						{
							Name:  "clear",
							Value: "clear",
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "prompt",
					Description: "The system prompt to set (required when action is 'set')",
					Required:    false,
				},
			},
		},
		{
			Name:        "apikeys",
			Description: "View API key status and manage bad keys (admin only)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "action",
					Description: "Action to perform",
					Required:    false,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{
							Name:  "status",
							Value: "status",
						},
						{
							Name:  "reset",
							Value: "reset",
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "provider",
					Description: "Provider to reset bad keys for (required when action is 'reset')",
					Required:    false,
				},
			},
		},
		{
			Name:        "cleardatabase",
			Description: "Clear the database",
		},
		{
			Name:        "generatevideo",
			Description: "Generate a video using Veo",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "prompt",
					Description: "The prompt for the video",
					Required:    true,
				},
			},
		},
		{
			Name:        "generateimage",
			Description: "Generate an image using Imagen",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "prompt",
					Description: "The prompt for the image",
					Required:    true,
				},
			},
		},
	}

	for _, cmd := range commands {
		_, err := b.session.ApplicationCommandCreate(b.session.State.User.ID, "", cmd)
		if err != nil {
			return fmt.Errorf("failed to create command %s: %w", cmd.Name, err)
		}
	}

	return nil
}

// onInteractionCreate handles slash command interactions
func (b *Bot) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		b.handleSlashCommand(s, i)
	case discordgo.InteractionApplicationCommandAutocomplete:
		b.handleAutocomplete(s, i)
	case discordgo.InteractionMessageComponent:
		b.handleButtonInteraction(s, i)
	}
}

// onMessageCreate handles new messages
func (b *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore bot messages
	if m.Author.Bot {
		return
	}

	// Check if this is a DM or if bot is mentioned
	isDM := m.GuildID == ""
	isMentioned := false

	for _, user := range m.Mentions {
		if user.ID == s.State.User.ID {
			isMentioned = true
			break
		}
	}

	// Also check for "at ai" prefix (case insensitive)
	isAtAI := strings.HasPrefix(strings.ToLower(strings.TrimSpace(m.Content)), "at ai")

	// Check if this message is in a thread created by the bot
	isInBotThread := false
	if m.Member != nil && m.Member.GuildID != "" {
		// Get channel info to check if it's a thread
		channel, err := s.Channel(m.ChannelID)
		if err == nil && channel.Type == discordgo.ChannelTypeGuildPublicThread {
			// This is a thread, check if the bot participated in its creation
			// We'll consider it a bot thread if the bot has sent messages in it
			isInBotThread = b.isBotActiveInThread(s, m.ChannelID)
		}
	}

	if !isDM && !isMentioned && !isAtAI && !isInBotThread {
		return
	}

	// Check permissions
	if !b.permChecker.CheckPermissions(m) {
		return
	}

	// Instead of `go b.handleMessage(s, m)`, submit to the job channel
	select {
	case b.messageJobs <- m:
	// Job successfully submitted
	default:
		// Pool is busy, log and drop the message to prevent blocking the Discord handler.
		log.Printf("Message processing pool is full. Dropping message from user %s", m.Author.ID)
	}
}

// getProperMessageReference returns the appropriate MessageReference for a message
// For synthetic retry messages, it returns the MessageReference that was set
// For normal messages, it returns the result of calling Reference()
func (b *Bot) getProperMessageReference(m *discordgo.MessageCreate) *discordgo.MessageReference {
	// For synthetic retry messages (identified by timestamp-based IDs),
	// use the explicit MessageReference that was set during retry creation
	if b.isSyntheticMessage(m) {
		if m.MessageReference != nil {
			// Validate that the referenced message exists before using it
			if b.validateMessageReference(m.MessageReference) {
				return m.MessageReference
			}
		}
		// If synthetic message has invalid reference, don't use any reference
		return nil
	}

	// For real user messages we *always* want to reply directly to that message
	// instead of following the user-supplied MessageReference (which usually
	// points at the bot's previous answer). This prevents the bot from
	// accidentally replying to its own message and breaking the conversation
	// thread.
	return m.Reference()
}

// isSyntheticMessage checks if a message is a synthetic retry message
func (b *Bot) isSyntheticMessage(m *discordgo.MessageCreate) bool {
	// Synthetic messages have timestamp-based IDs (Unix nanoseconds)
	// Real Discord snowflake IDs are much larger and follow a different pattern
	// Timestamp IDs are typically 19 digits, while Discord snowflakes are 18-19 digits
	// but start with specific patterns based on Discord's epoch
	if len(m.ID) >= 19 {
		// Check if this looks like a nanosecond timestamp (very large number)
		// Discord snowflakes for 2025 start around 14-15, but timestamps start around 17
		if m.ID[0] >= '1' && m.ID[1] >= '7' {
			return true
		}
	}
	return false
}

// validateMessageReference checks if a MessageReference points to a valid message
func (b *Bot) validateMessageReference(ref *discordgo.MessageReference) bool {
	if ref == nil || ref.MessageID == "" || ref.ChannelID == "" {
		return false
	}
	
	// Basic format validation - Discord IDs should be numeric and properly sized
	if len(ref.MessageID) < 10 || len(ref.ChannelID) < 10 {
		return false
	}
	
	// Try to fetch the message to see if it exists using the session from the bot
	if b.session != nil {
		_, err := b.session.ChannelMessage(ref.ChannelID, ref.MessageID)
		if err != nil {
			log.Printf("MessageReference validation failed: %v", err)
			return false
		}
	}
	
	return true
}

// createThreadForResponse creates a thread for the bot's response
func (b *Bot) createThreadForResponse(s *discordgo.Session, originalMsg *discordgo.MessageCreate) (string, error) {
	// Generate a thread name based on the user's message content
	threadName := "AI Chat"
	if len(originalMsg.Content) > 0 {
		// Use first 50 characters of the message as thread name
		name := strings.TrimSpace(originalMsg.Content)
		if len(name) > 50 {
			name = name[:50] + "..."
		}
		if name != "" {
			threadName = name
		}
	}

	// Create the thread
	thread, err := s.MessageThreadStart(originalMsg.ChannelID, originalMsg.ID, threadName, 60) // Auto-archive after 60 minutes
	if err != nil {
		return "", fmt.Errorf("failed to create thread: %w", err)
	}

	log.Printf("Created thread %s for message %s", thread.ID, originalMsg.ID)
	return thread.ID, nil
}

// handleMessage processes a message and generates LLM response
func (b *Bot) handleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Atomically load config
	cfg := b.config.Load()
	useThreads := cfg.UseThreads

	targetChannelID := m.ChannelID
	messageRef := b.getProperMessageReference(m)

	// Check if we're already in a thread
	isAlreadyInThread := false
	if m.GuildID != "" {
		channel, err := s.Channel(m.ChannelID)
		if err == nil && channel.Type == discordgo.ChannelTypeGuildPublicThread {
			isAlreadyInThread = true
		}
	}

	// Create thread if enabled and we're not already in one
	if useThreads && !isAlreadyInThread {
		threadID, err := b.createThreadForResponse(s, m)
		if err != nil {
			log.Printf("Failed to create thread, falling back to regular response: %v", err)
			// Continue with regular response in the original channel
		} else {
			targetChannelID = threadID
			log.Printf("Created thread %s for response", threadID)
			// For thread responses, we don't need a message reference since the thread itself provides context
			messageRef = nil
		}
	} else if isAlreadyInThread {
		log.Printf("Already in thread %s, responding directly", m.ChannelID)
	}

	// Create progress manager with the correct channel ID (thread or original)
	progressMgr := utils.NewProgressManager(s, targetChannelID)

	// Show simple progress message once (as a reply)
	if err := progressMgr.UpdateProgress(utils.ProgressProcessing, nil, messageRef); err != nil {
		log.Printf("Failed to update progress: %v", err)
	}

	// Defer cleanup function to ensure progress message is always handled
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Panic in handleMessage: %v", r)
			b.updateProgressWithError(s, progressMgr, fmt.Sprintf("Internal error: %v", r), "unknown")
		}
	}()

	// Get user's preferred model
	cfg = b.config.Load()
	currentModel := b.userPrefs.GetUserModel(context.Background(), m.Author.ID, cfg.GetDefaultModel())

	// Parse provider and model
	parts := strings.SplitN(currentModel, "/", 2)
	if len(parts) != 2 {
		log.Printf("Invalid model format: %s", currentModel)
		b.updateProgressWithError(s, progressMgr, fmt.Sprintf("Invalid model format: %s", currentModel), currentModel)
		return
	}

	modelName := parts[1]

	// Check if model supports images and usernames
	acceptImages := auth.IsVisionModel(modelName)
	acceptUsernames := auth.SupportsUsernames(currentModel)

	// Calculate total attachment count (current + parent if applicable)
	totalAttachments := len(m.Attachments)

	// Check if this is a reply to a message with attachments
	if m.MessageReference != nil && m.MessageReference.MessageID != "" {
		parentMsg, err := s.ChannelMessage(m.ChannelID, m.MessageReference.MessageID)
		if err == nil && len(parentMsg.Attachments) > 0 {
			totalAttachments += len(parentMsg.Attachments)
		}
	}

	// Build conversation chain
	messages, warnings := b.buildConversationChainWithWebSearch(s, m, acceptImages, acceptUsernames, true, progressMgr)

	// Get user's custom system prompt or fall back to default
	userSystemPrompt := b.userPrefs.GetUserSystemPrompt(context.Background(), m.Author.ID)
	cfg = b.config.Load()
	systemPrompt := cfg.SystemPrompt
	if userSystemPrompt != "" {
		systemPrompt = userSystemPrompt
	}

	// Add system prompt
	messages = b.llmClient.AddSystemPrompt(messages, systemPrompt, acceptUsernames)

	// Apply context management before sending to LLM
	ctx := context.Background()
	cfg = b.config.Load()
	contextManager := contextmgr.NewContextManager(b.llmClient, cfg)
	
	managedResult, err := contextManager.ManageContext(ctx, messages, currentModel)
	if err != nil {
		log.Printf("Context management failed: %v", err)
		b.updateProgressWithError(s, progressMgr, fmt.Sprintf("Context management error: %v", err), currentModel)
		return
	}
	
	// Use managed messages
	messages = managedResult.Messages
	
	// Add context management information to warnings if needed
	if managedResult.WasSummarized {
		warnings = append(warnings, fmt.Sprintf("📝 Summarized %d conversation pairs to fit within token limit", managedResult.SummariesCount))
	}
	if managedResult.WasTruncated {
		warnings = append(warnings, "✂️ Latest message truncated to fit within token limit")
	}
	
	log.Printf("Context management: %d tokens used, summarized: %v, truncated: %v", 
		managedResult.TokensUsed, managedResult.WasSummarized, managedResult.WasTruncated)

	// Reverse messages (newest first for API)
	messages = utils.ReverseMessages(messages)

	log.Printf("Message received (user ID: %s, attachments: %d, conversation length: %d):\n%s",
		m.Author.ID, totalAttachments, len(messages), m.Content)

	// Get web search information from the original message node
	node, exists := b.nodeManager.Get(m.ID)
	var webSearchPerformed bool
	var searchResultCount int
	if exists {
		webSearchPerformed, searchResultCount = node.GetWebSearchInfo()
	}

	// Generate response with web search information
	b.generateResponse(s, m, currentModel, messages, warnings, progressMgr, messageRef, targetChannelID, webSearchPerformed, searchResultCount)
}

// updateProgressWithError updates the progress message with an error
func (b *Bot) updateProgressWithError(s *discordgo.Session, progressMgr *utils.ProgressManager, errorMsg string, model string) {
	if progressMgr != nil && progressMgr.GetMessageID() != "" {
		errorEmbed := &discordgo.MessageEmbed{
			Title:       "❌ Processing Failed",
			Description: fmt.Sprintf("An error occurred while processing your request:\n\n```\n%s\n```", errorMsg),
			Color:       0xFF0000, // Red color for errors
		}

		// Add model info to footer if available
		if model != "" && model != "unknown" {
			errorEmbed.Footer = &discordgo.MessageEmbedFooter{
				Text: fmt.Sprintf("🤖 Model: %s", model),
			}
		}

		// Update the progress message to show the error
		if _, err := s.ChannelMessageEditEmbed(progressMgr.GetChannelID(), progressMgr.GetMessageID(), errorEmbed); err != nil {
			log.Printf("Failed to update progress message with error: %v", err)
		}
	}
}
