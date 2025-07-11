package bot

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"DiscordAIChatbot/internal/auth"
	"DiscordAIChatbot/internal/config"
	"DiscordAIChatbot/internal/llm"
	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/processors"
	"DiscordAIChatbot/internal/storage"
	"DiscordAIChatbot/internal/utils"
)

// Bot represents the Discord bot instance that handles all Discord interactions,
// manages conversations, processes messages, and integrates with LLM providers.
// It maintains the bot's configuration, session state, and various service clients.
type Bot struct {
	session          *discordgo.Session
	config           *config.Config
	nodeManager      *messaging.MsgNodeManager
	permChecker      *auth.PermissionChecker
	llmClient        *llm.LLMClient
	webSearchClient  *processors.WebSearchClient
	googleLensClient *processors.GoogleLensClient
	userPrefs        *storage.UserPreferencesManager
	apiKeyManager    *storage.APIKeyManager
	tableRenderer    *utils.TableRenderer
	fileProcessor    *processors.FileProcessor
	chartProcessor   *processors.ChartProcessor
	lastTaskTime     time.Time
	mu               sync.RWMutex
	healthServer     *http.Server
	shutdownCtx      context.Context
	shutdownCancel   context.CancelFunc
	activeGoroutines sync.WaitGroup
	messageCache     *storage.MessageNodeCache
}

// NewBot creates a new Discord bot instance
func NewBot(cfg *config.Config) (*Bot, error) {
	// Initialize database tables first (single connection, single transaction)
	if err := storage.InitializeAllTables(cfg.DatabaseURL); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	// Create Discord session
	session, err := discordgo.New("Bot " + cfg.BotToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	// Initialize managers (now using shared connection)
	apiKeyManager := storage.NewAPIKeyManager(cfg.DatabaseURL)

	// Set up bot
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	bot := &Bot{
		session:          session,
		config:           cfg,
		nodeManager:      messaging.NewMsgNodeManager(config.MaxMessageNodes),
		permChecker:      auth.NewPermissionChecker(cfg),
		llmClient:        llm.NewLLMClient(cfg, apiKeyManager),
		webSearchClient:  processors.NewWebSearchClient(cfg),
		googleLensClient: processors.NewGoogleLensClient(cfg, apiKeyManager),
		userPrefs:        storage.NewUserPreferencesManager(cfg.DatabaseURL),
		apiKeyManager:    apiKeyManager,
		tableRenderer:    createTableRenderer(cfg),
		fileProcessor:    processors.NewFileProcessor(),
		chartProcessor:   processors.NewChartProcessor(cfg.DatabaseURL),
		messageCache:     storage.NewMessageNodeCache(cfg.DatabaseURL),
		shutdownCtx:      shutdownCtx,
		shutdownCancel:   shutdownCancel,
	}

	// Configure Discord session
	session.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent

	// Register event handlers
	session.AddHandler(bot.onReady)
	session.AddHandler(bot.onMessageCreate)
	session.AddHandler(bot.onInteractionCreate)

	// Setup health check server
	bot.setupHealthServer()

	return bot, nil
}

// Start starts the Discord bot
func (b *Bot) Start() error {
	// Start health check server
	go func() {
		if err := b.healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Health server error: %v", err)
		}
	}()

	// Initialize table renderer
	if err := b.tableRenderer.Initialize(); err != nil {
		log.Printf("⚠️ Failed to initialize table renderer: %v", err)
		log.Printf("Table rendering functionality may not be available")
	} else {
		log.Printf("✅ Table renderer initialized successfully")
	}

	// Start background health checks and initialization
	b.activeGoroutines.Add(2)
	
	// Check web search API health in background (non-blocking)
	go func() {
		defer b.activeGoroutines.Done()
		log.Printf("Checking web search API health...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := b.webSearchClient.CheckHealth(ctx); err != nil {
			log.Printf("⚠️ Web search API health check failed: %v", err)
			log.Printf("Web search functionality may not be available")
		} else {
			log.Printf("✅ Web search API is healthy")
		}
	}()

	// Preinstall common chart libraries in background (only when chart generation is used)
	go func() {
		defer b.activeGoroutines.Done()
		// Chart libraries will be installed lazily when first needed
		log.Printf("Chart processor initialized with lazy loading")
	}()

	return b.session.Open()
}

// Stop stops the Discord bot
func (b *Bot) Stop() error {
	// Cancel ongoing operations
	if b.shutdownCancel != nil {
		b.shutdownCancel()
	}

	// Wait for background goroutines with timeout
	done := make(chan struct{})
	go func() {
		b.activeGoroutines.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Printf("All background operations completed")
	case <-time.After(30 * time.Second):
		log.Printf("Timeout waiting for background operations, proceeding with shutdown")
	}

	// Stop health check server
	if b.healthServer != nil {
		if err := b.healthServer.Close(); err != nil {
			log.Printf("Failed to close health server: %v", err)
		}
	}

	// Close table renderer
	if b.tableRenderer != nil {
		if err := b.tableRenderer.Close(); err != nil {
			log.Printf("Failed to close table renderer: %v", err)
		}
	}
	// Close chart processor (includes library manager database)
	if b.chartProcessor != nil {
		if err := b.chartProcessor.Cleanup(); err != nil {
			log.Printf("Failed to cleanup chart processor: %v", err)
		}
	}
	// Close user preferences database
	if b.userPrefs != nil {
		if err := b.userPrefs.Close(); err != nil {
			log.Printf("Failed to close user preferences: %v", err)
		}
	}
	// Close message cache
	if b.messageCache != nil {
		if err := b.messageCache.Close(); err != nil {
			log.Printf("Failed to close message cache: %v", err)
		}
	}
	// Close API key manager database
	if b.apiKeyManager != nil {
		if err := b.apiKeyManager.Close(); err != nil {
			log.Printf("Failed to close API key manager: %v", err)
		}
	}

	// Close shared database connection
	if err := storage.CloseDatabase(); err != nil {
		log.Printf("Failed to close shared database connection: %v", err)
	}

	return b.session.Close()
}

// setLastTaskTime sets the last task time (thread-safe)
func (b *Bot) setLastTaskTime(t time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastTaskTime = t
}

// setupHealthServer sets up the health check HTTP server
func (b *Bot) setupHealthServer() {
	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/health", b.healthCheckHandler)
	mux.HandleFunc("/", b.healthCheckHandler)

	// Get port from environment variable, default to 8080
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	b.healthServer = &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}
}

// healthCheckHandler handles health check requests
func (b *Bot) healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := fmt.Sprintf(`{
		"status": "healthy",
		"timestamp": "%s",
		"discord_connected": %t,
		"uptime": "%s"
	}`,
		time.Now().UTC().Format(time.RFC3339),
		b.session != nil && b.session.DataReady,
		time.Since(b.lastTaskTime).String(),
	)

	_, _ = w.Write([]byte(response))
}

// createTableRenderer creates the appropriate table renderer based on configuration
func createTableRenderer(cfg *config.Config) *utils.TableRenderer {
	if cfg.UseRodTableRendering() {
		timeout := cfg.GetRodTimeout()
		quality := cfg.GetRodQuality()
		log.Printf("Initializing Rod table renderer (method: %s, timeout: %ds, quality: %d)", 
			cfg.TableRendering.Method, timeout, quality)
		return utils.NewTableRendererWithRodConfig(timeout, quality)
	} else {
		log.Printf("Initializing gg graphics table renderer (method: %s)", cfg.TableRendering.Method)
		return utils.NewTableRenderer()
	}
}
