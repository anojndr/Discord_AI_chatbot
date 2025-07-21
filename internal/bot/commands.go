package bot

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"DiscordAIChatbot/internal/storage"
	"DiscordAIChatbot/internal/uploader"
	"github.com/bwmarrin/discordgo"
)

// handleSlashCommand handles slash command execution
func (b *Bot) handleSlashCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	switch data.Name {
	case "model":
		b.handleModelCommand(s, i)
	case "systemprompt":
		b.handleSystemPromptCommand(s, i)
	case "apikeys":
		b.handleAPIKeysCommand(s, i)
	case "cleardatabase":
		b.handleClearDatabaseCommand(s, i)
	case "generatevideo":
		b.handleGenerateVideoCommand(s, i)
	case "generateimage":
		b.handleGenerateImageCommand(s, i)
	}
}

// handleModelCommand handles the /model slash command
func (b *Bot) handleModelCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	// Get user ID
	var userID string
	if i.User != nil {
		userID = i.User.ID
	} else if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	}

	// Thread-safe access to config
	b.configMutex.RLock()
	config := b.config
	b.configMutex.RUnlock()

	// Check if config is nil (safety check)
	if config == nil {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Configuration is not available",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	// Get user's current model
	var currentModel string
	if userID != "" && b.userPrefs != nil {
		currentModel = b.userPrefs.GetUserModel(context.Background(), userID, config.GetDefaultModel())
	} else {
		currentModel = config.GetDefaultModel()
	}

	if len(data.Options) == 0 {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("Current model: `%s`", currentModel),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	newModel := data.Options[0].StringValue()

	var response string

	if newModel == currentModel {
		response = fmt.Sprintf("Current model: `%s`", newModel)
	} else {
		// Save user's model preference
		if userID == "" || b.userPrefs == nil {
			response = "❌ Unable to save model preference (user ID not available)"
		} else {
			err := b.userPrefs.SetUserModel(context.Background(), userID, newModel)
			if err != nil {
				log.Printf("Failed to save user model preference: %v", err)
				response = "❌ Failed to save model preference"
			} else {
				response = fmt.Sprintf("Model switched to: `%s`", newModel)
				log.Printf("User %s switched model to: %s", userID, newModel)
			}
		}
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: response,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		log.Printf("Failed to respond to interaction: %v", err)
	}
}

// handleSystemPromptCommand handles the /systemprompt slash command
func (b *Bot) handleSystemPromptCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	// Get user ID
	var userID string
	if i.User != nil {
		userID = i.User.ID
	} else if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	}

	if len(data.Options) == 0 {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Please specify an action (view/set/clear)",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	action := data.Options[0].StringValue()
	var response string

	switch action {
	case "view":
		var currentPrompt string
		if userID != "" && b.userPrefs != nil {
			currentPrompt = b.userPrefs.GetUserSystemPrompt(context.Background(), userID)
		}
		if currentPrompt == "" {
			response = "You don't have a custom system prompt set. Using the default system prompt."
		} else {
			// Truncate prompt if too long for Discord message
			displayPrompt := currentPrompt
			if len(displayPrompt) > 1500 {
				displayPrompt = displayPrompt[:1500] + "..."
			}
			response = fmt.Sprintf("Your current system prompt:\n```\n%s\n```", displayPrompt)
		}

	case "set":
		if len(data.Options) < 2 {
			response = "❌ Please provide a prompt when using 'set'"
			break
		}

		newPrompt := data.Options[1].StringValue()
		if len(newPrompt) > 8000 {
			response = "❌ System prompt is too long (max 8000 characters)"
			break
		}

		if userID == "" || b.userPrefs == nil {
			response = "❌ Unable to save system prompt (user ID not available)"
		} else {
			err := b.userPrefs.SetUserSystemPrompt(context.Background(), userID, newPrompt)
			if err != nil {
				log.Printf("Failed to save user system prompt: %v", err)
				response = "❌ Failed to save system prompt"
			} else {
				response = "✅ Your custom system prompt has been set!"
				log.Printf("User %s set custom system prompt", userID)
			}
		}

	case "clear":
		if userID == "" || b.userPrefs == nil {
			response = "❌ Unable to clear system prompt (user ID not available)"
		} else {
			err := b.userPrefs.ClearUserSystemPrompt(context.Background(), userID)
			if err != nil {
				log.Printf("Failed to clear user system prompt: %v", err)
				response = "❌ Failed to clear system prompt"
			} else {
				response = "✅ Your custom system prompt has been cleared. You'll now use the default system prompt."
				log.Printf("User %s cleared custom system prompt", userID)
			}
		}

	default:
		response = "❌ Invalid action. Use 'view', 'set', or 'clear'"
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: response,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		log.Printf("Failed to respond to interaction: %v", err)
	}
}

// handleAPIKeysCommand handles the /apikeys slash command
func (b *Bot) handleAPIKeysCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	// Get user ID
	var userID string
	if i.User != nil {
		userID = i.User.ID
	} else if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	}

	// Thread-safe access to config
	b.configMutex.RLock()
	config := b.config
	b.configMutex.RUnlock()

	// Check if config is nil (safety check)
	if config == nil {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Configuration is not available",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	// Check if user is admin
	if userID == "" || !contains(config.Permissions.Users.AdminIDs, userID) {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ This command is only available to administrators",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	action := "status" // default action
	if len(data.Options) > 0 {
		action = data.Options[0].StringValue()
	}

	var response string

	switch action {
	case "status":
		// Get bad key statistics
		stats, err := b.apiKeyManager.GetBadKeyStats(context.Background())
		if err != nil {
			log.Printf("Failed to get API key stats: %v", err)
			response = "❌ Failed to get API key statistics"
			break
		}

		if len(stats) == 0 {
			response = "✅ No bad API keys currently recorded"
		} else {
			response = "📊 **API Key Status:**\n"
			for provider, badCount := range stats {
				var totalKeys int
				if provider == "serpapi" {
					totalKeys = len(config.GetSerpAPIKeys())
				} else {
					providerConfig := config.Providers[provider]
					totalKeys = len(providerConfig.GetAPIKeys())
				}
				goodKeys := totalKeys - badCount
				response += fmt.Sprintf("• **%s**: %d good, %d bad (total: %d)\n",
					provider, goodKeys, badCount, totalKeys)
			}
		}

	case "reset":
		var provider string
		if len(data.Options) > 1 {
			provider = data.Options[1].StringValue()
		}

		if provider == "" {
			response = "❌ Please specify a provider when using 'reset'"
			break
		}

		// Check if provider exists
		validProvider := false
		if provider == "serpapi" {
			validProvider = len(config.GetSerpAPIKeys()) > 0
		} else {
			_, validProvider = config.Providers[provider]
		}

		if !validProvider {
			response = fmt.Sprintf("❌ Unknown provider: %s", provider)
			break
		}

		// Reset bad keys for the provider
		err := b.apiKeyManager.ResetBadKeys(context.Background(), provider)
		if err != nil {
			log.Printf("Failed to reset bad keys for provider %s: %v", provider, err)
			response = fmt.Sprintf("❌ Failed to reset bad keys for provider: %s", provider)
		} else {
			response = fmt.Sprintf("✅ Reset bad API keys for provider: %s", provider)
			log.Printf("Admin %s reset bad API keys for provider: %s", userID, provider)
		}

	default:
		response = "❌ Invalid action. Use 'status' or 'reset'"
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: response,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		log.Printf("Failed to respond to interaction: %v", err)
	}
}

// Helper function to check if slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// handleAutocomplete handles autocomplete for slash commands
func (b *Bot) handleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	switch data.Name {
	case "model":
		b.handleModelAutocomplete(s, i)
	}
}

// handleModelAutocomplete handles autocomplete for the model command
func (b *Bot) handleModelAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	// Get the partial input
	var partial string
	if len(data.Options) > 0 && data.Options[0].Focused {
		partial = data.Options[0].StringValue()
	}

	// Thread-safe access to config
	b.configMutex.RLock()
	config := b.config
	b.configMutex.RUnlock()

	// Check if config is nil (safety check)
	if config == nil {
		log.Printf("Config is nil in handleModelAutocomplete")
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionApplicationCommandAutocompleteResult,
			Data: &discordgo.InteractionResponseData{
				Choices: []*discordgo.ApplicationCommandOptionChoice{},
			},
		}); err != nil {
			log.Printf("Failed to respond to autocomplete interaction: %v", err)
		}
		return
	}

	// Get user's current model
	var userID string
	if i.User != nil {
		userID = i.User.ID
	} else if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	}

	var currentModel string
	if userID != "" && b.userPrefs != nil {
		currentModel = b.userPrefs.GetUserModel(context.Background(), userID, config.GetDefaultModel())
	} else {
		currentModel = config.GetDefaultModel()
	}

	// Get all model names
	var models []string
	if config.Models != nil {
		for modelName := range config.Models {
			models = append(models, modelName)
		}
	}

	// Filter models based on partial input and exclude current model from regular list
	var filteredModels []string
	for _, model := range models {
		if model != currentModel && (partial == "" || strings.Contains(strings.ToLower(model), strings.ToLower(partial))) {
			filteredModels = append(filteredModels, model)
		}
	}

	// Sort and limit to 24 (leaving space for current model)
	sort.Strings(filteredModels)
	if len(filteredModels) > 24 {
		filteredModels = filteredModels[:24]
	}

	// Create choices - first add non-current models with ○ symbol
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(filteredModels)+1)
	for _, model := range filteredModels {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  fmt.Sprintf("○ %s", model),
			Value: model,
		})
	}

	// Add current model with ◉ symbol if it matches the partial input
	if partial == "" || strings.Contains(strings.ToLower(currentModel), strings.ToLower(partial)) {
		currentChoice := &discordgo.ApplicationCommandOptionChoice{
			Name:  fmt.Sprintf("◉ %s (current)", currentModel),
			Value: currentModel,
		}
		// Add current model to the end (as shown in llmcord)
		choices = append(choices, currentChoice)
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{
			Choices: choices,
		},
	}); err != nil {
		log.Printf("Failed to respond to autocomplete interaction: %v", err)
	}
}

// handleClearDatabaseCommand handles the /cleardatabase slash command
func (b *Bot) handleClearDatabaseCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Get user ID
	var userID string
	if i.User != nil {
		userID = i.User.ID
	} else if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	}

	// Check if user is the specific user allowed to use this command
	if userID != "676735636656357396" {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You do not have permission to use this command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	// Thread-safe access to config
	b.configMutex.RLock()
	config := b.config
	b.configMutex.RUnlock()

	// Check if config is nil (safety check)
	if config == nil {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Configuration is not available",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	err := storage.DropAllTables(context.Background(), config.DatabaseURL)
	var response string
	if err != nil {
		log.Printf("Failed to drop tables: %v", err)
		response = "❌ Failed to clear the database."
	} else {
		err = storage.InitializeAllTables(context.Background(), config.DatabaseURL)
		if err != nil {
			log.Printf("Failed to re-initialize tables: %v", err)
			response = "❌ Failed to re-initialize the database."
		} else {
			response = "✅ Database cleared and re-initialized successfully."
			log.Printf("User %s cleared and re-initialized the database.", userID)
		}
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: response,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}); err != nil {
		log.Printf("Failed to respond to interaction: %v", err)
	}
}

// handleGenerateVideoCommand handles the /generatevideo slash command
func (b *Bot) handleGenerateVideoCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	if len(data.Options) == 0 {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Please provide a prompt for video generation.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	prompt := data.Options[0].StringValue()

	// Acknowledge the interaction immediately
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Printf("Failed to send deferred response: %v", err)
		// Optionally, try to send an error message back to the user
		errorContent := "❌ An error occurred while processing your request."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &errorContent,
		})
		return
	}

	go func() {
		ctx := context.Background()

		b.configMutex.RLock()
		videoModel := b.config.VideoGenerationModel
		b.configMutex.RUnlock()

		if videoModel == "" {
			errorContent := "❌ Video generation model not configured."
			if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &errorContent,
			}); err != nil {
				log.Printf("Failed to edit interaction response with error: %v", err)
			}
			return
		}

		videoData, err := b.llmClient.GenerateVideo(ctx, videoModel, prompt)
		if err != nil {
			log.Printf("Failed to generate video: %v", err)
			errorContent := fmt.Sprintf("❌ Failed to generate video: %v", err)
			if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &errorContent,
			}); err != nil {
				log.Printf("Failed to edit interaction response with error: %v", err)
			}
			return
		}

		// Check video size
		if len(videoData) > 9*1024*1024 { // 9 MB
			// Upload to Catbox
			url, err := uploader.UploadToCatbox("video.mp4", videoData)
			if err != nil {
				log.Printf("Failed to upload video to Catbox: %v", err)
				errorContent := fmt.Sprintf("❌ Failed to upload video to Catbox: %v", err)
				if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
					Content: &errorContent,
				}); err != nil {
					log.Printf("Failed to edit interaction response with error: %v", err)
				}
				return
			}

			successContent := fmt.Sprintf("✅ Video generated and uploaded to Catbox: %s", url)
			if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &successContent,
			}); err != nil {
				log.Printf("Failed to edit interaction response with Catbox URL: %v", err)
			}
		} else {
			// Send directly to Discord
			successContent := "✅ Video generated successfully!"
			if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &successContent,
				Files: []*discordgo.File{
					{
						Name:        "video.mp4",
						ContentType: "video/mp4",
						Reader:      strings.NewReader(string(videoData)),
					},
				},
			}); err != nil {
				log.Printf("Failed to edit interaction response with video: %v", err)
			}
		}
	}()
}

// handleGenerateImageCommand handles the /generateimage slash command
func (b *Bot) handleGenerateImageCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()

	if len(data.Options) == 0 {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Please provide a prompt for image generation.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}); err != nil {
			log.Printf("Failed to respond to interaction: %v", err)
		}
		return
	}

	prompt := data.Options[0].StringValue()

	// Acknowledge the interaction immediately
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Printf("Failed to send deferred response: %v", err)
		errorContent := "❌ An error occurred while processing your request."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &errorContent,
		})
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		// Get the image generation model from config
		b.configMutex.RLock()
		imageModel := b.config.ImageGenerationModel
		b.configMutex.RUnlock()

		if imageModel == "" {
			errorContent := "❌ No image generation model found in configuration."
			if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &errorContent,
			}); err != nil {
				log.Printf("Failed to edit interaction response with error: %v", err)
			}
			return
		}

		images, err := b.llmClient.GenerateImage(ctx, imageModel, prompt)
		if err != nil {
			log.Printf("Failed to generate image: %v", err)
			errorContent := fmt.Sprintf("❌ Failed to generate image: %v", err)
			if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &errorContent,
			}); err != nil {
				log.Printf("Failed to edit interaction response with error: %v", err)
			}
			return
		}

		if len(images) == 0 {
			noImageContent := "❌ No images were generated."
			if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &noImageContent,
			}); err != nil {
				log.Printf("Failed to edit interaction response with no image message: %v", err)
			}
			return
		}

		var files []*discordgo.File
		for i, img := range images {
			if img.Image != nil && len(img.Image.ImageBytes) > 0 {
				files = append(files, &discordgo.File{
					Name:        fmt.Sprintf("image_%d.png", i+1),
					ContentType: "image/png",
					Reader:      strings.NewReader(string(img.Image.ImageBytes)),
				})
			}
		}

		successContent := fmt.Sprintf("✅ Generated %d images for prompt: `%s`", len(files), prompt)
		if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &successContent,
			Files:   files,
		}); err != nil {
			log.Printf("Failed to edit interaction response with image: %v", err)
		}
	}()
}
