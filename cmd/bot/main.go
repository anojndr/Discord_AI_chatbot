package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"DiscordAIChatbot/internal/bot"
	"DiscordAIChatbot/internal/config"
	"DiscordAIChatbot/internal/llm"
	"DiscordAIChatbot/internal/logging"
	"DiscordAIChatbot/internal/storage"
)

// main is the entry point for the Discord AI Chatbot application.
// It handles command-line flags, loads configuration, and manages the bot lifecycle.
func main() {
	// Parse command line flags
	var testConnectivity = flag.String("test-connectivity", "", "Test connectivity for a specific provider (e.g., 'openai') or 'all' for all providers")
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig("")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize console logging
	err = logging.InitializeLogging(cfg.Logging.LogLevel)
	if err != nil {
		log.Fatalf("Failed to initialize logging: %v", err)
	}

	// Handle test connectivity mode
	if *testConnectivity != "" {
		runConnectivityTest(cfg, *testConnectivity)
		return
	}

	// Validate required configuration
	if cfg.BotToken == "" {
		if logging.IsInitialized() {
			logging.Fatal("Bot token is required")
		} else {
			log.Fatal("Bot token is required")
		}
	}

	// Initialize and start bot
	if err := runBot(cfg); err != nil {
		if logging.IsInitialized() {
			logging.Fatal("Bot failed: %v", err)
		} else {
			log.Fatalf("Bot failed: %v", err)
		}
	}
}

// runBot initializes, starts, and manages the bot lifecycle
func runBot(cfg *config.Config) error {
	// Initialize bot with all dependencies
	discordBot, err := bot.NewBot(cfg)
	if err != nil {
		return fmt.Errorf("failed to create bot: %w", err)
	}

	// Start bot
	if err := discordBot.Start(); err != nil {
		return fmt.Errorf("failed to start bot: %w", err)
	}

	// Only show startup message if logging level is INFO or lower
	if logging.GetLogLevel() <= logging.GetINFOLevel() {
		logging.PrintfAndLog("Bot is now running. Press CTRL-C to exit.\n")
	}

	// Wait for interrupt signal to gracefully shut down
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	// Only show shutdown message if logging level is INFO or lower
	if logging.GetLogLevel() <= logging.GetINFOLevel() {
		logging.PrintlnAndLog("Shutting down...")
	}
	return discordBot.Stop()
}

// runConnectivityTest tests connectivity for specified provider(s)
func runConnectivityTest(cfg *config.Config, provider string) {
	discordBot, err := bot.NewBot(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize bot: %v", err)
	}
	defer func() {
		if err := discordBot.Stop(); err != nil {
			log.Printf("Failed to stop bot: %v", err)
		}
	}()

	// Only show test headers if logging level is INFO or lower
	if logging.GetLogLevel() <= logging.GetINFOLevel() {
		logging.PrintlnAndLog("🔍 Testing LLM Provider Connectivity...")
		logging.PrintlnAndLog("=====================================")
	}

	if provider == "all" {
		testAllProviders(cfg)
	} else {
		testSingleProvider(cfg, provider)
	}
}

// testAllProviders tests connectivity for all configured providers
func testAllProviders(cfg *config.Config) {
	hasErrors := false
	for providerName := range cfg.Providers {
		if logging.GetLogLevel() <= logging.GetINFOLevel() {
			logging.PrintfAndLog("\n📡 Testing provider: %s\n", providerName)
		}

		apiKeyManager := storage.NewAPIKeyManager(cfg.DatabaseURL)
		llmClient := llm.NewLLMClient(cfg, apiKeyManager)

		if err := llmClient.TestProviderConnectivity(providerName); err != nil {
			logging.PrintfAndLog("❌ %s: %v\n", providerName, err)
			hasErrors = true
		} else if logging.GetLogLevel() <= logging.GetINFOLevel() {
			logging.PrintfAndLog("✅ %s: Connection successful\n", providerName)
		}

		if err := apiKeyManager.Close(); err != nil {
			log.Printf("Failed to close API key manager: %v", err)
		}
	}

	if hasErrors {
		logging.PrintlnAndLog("\n⚠️  Some providers have connectivity issues. Please check the errors above.")
		os.Exit(1)
	} else if logging.GetLogLevel() <= logging.GetINFOLevel() {
		logging.PrintlnAndLog("\n🎉 All providers are working correctly!")
	}
}

// testSingleProvider tests connectivity for a specific provider
func testSingleProvider(cfg *config.Config, provider string) {
	if logging.GetLogLevel() <= logging.GetINFOLevel() {
		logging.PrintfAndLog("\n📡 Testing provider: %s\n", provider)
	}

	apiKeyManager := storage.NewAPIKeyManager(cfg.DatabaseURL)
	llmClient := llm.NewLLMClient(cfg, apiKeyManager)
	defer func() {
		if err := apiKeyManager.Close(); err != nil {
			log.Printf("Failed to close API key manager: %v", err)
		}
	}()

	if err := llmClient.TestProviderConnectivity(provider); err != nil {
		logging.PrintfAndLog("❌ %s: %v\n", provider, err)
		if logging.GetLogLevel() <= logging.GetINFOLevel() {
			printTroubleshootingTips()
		}
		os.Exit(1)
	} else if logging.GetLogLevel() <= logging.GetINFOLevel() {
		logging.PrintfAndLog("✅ %s: Connection successful\n", provider)
	}
}

// printTroubleshootingTips provides guidance for common connectivity issues
func printTroubleshootingTips() {
	logging.PrintlnAndLog("\n🔧 Troubleshooting Tips:")
	logging.PrintlnAndLog("1. Check if your local server is running")
	logging.PrintlnAndLog("2. Verify the base_url in your config.yaml")
	logging.PrintlnAndLog("3. Test the URL manually: curl http://localhost:4141/v1/models")
	logging.PrintlnAndLog("4. Check server logs for errors")
}
