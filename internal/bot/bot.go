package bot

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/fsnotify/fsnotify"

	"DiscordAIChatbot/internal/auth"
	"DiscordAIChatbot/internal/config"
	"DiscordAIChatbot/internal/llm"
	"DiscordAIChatbot/internal/llm/providers"
	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/net"
	"DiscordAIChatbot/internal/processors"
	"DiscordAIChatbot/internal/storage"
	"DiscordAIChatbot/internal/utils"
)

var ()

// Bot represents the Discord bot instance that handles all Discord interactions,
// manages conversations, processes messages, and integrates with LLM providers.
// It maintains the bot's configuration, session state, and various service clients.
type Bot struct {
	session          *discordgo.Session
	config           atomic.Pointer[config.Config]
	nodeManager      *messaging.MsgNodeManager
	permChecker      *auth.PermissionChecker
	llmClient        *llm.LLMClient
	webSearchClient  *processors.WebSearchClient
	googleLensClient *processors.GoogleLensClient
	geminiProvider   *providers.GeminiProvider
	userPrefs        *storage.UserPreferencesManager
	apiKeyManager    *storage.APIKeyManager
	tableRenderer    *utils.TableRenderer
	fileProcessor    *processors.FileProcessor
	chartProcessor   *processors.ChartProcessor
	channelProcessor *processors.ChannelProcessor
	httpClient       *http.Client
	lastTaskTime     time.Time
	mu               sync.RWMutex
	healthServer     *http.Server
	shutdownCtx      context.Context
	shutdownCancel   context.CancelFunc
	activeGoroutines sync.WaitGroup
	messageCache     *storage.MessageNodeCache
	paginationCache  *PaginationCache
	messageJobs      chan *discordgo.MessageCreate // Add this
}

// NewBot creates a new Discord bot instance
func NewBot(cfg *config.Config) (*Bot, error) {
	// Initialize database tables first (single connection, single transaction)
	if err := storage.InitializeAllTables(context.Background(), cfg.DatabaseURL); err != nil {
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
	httpClient := net.NewOptimizedClient(config.DefaultHTTPTimeout * time.Second)
	webSearchHTTPClient := net.NewOptimizedClientWithNoTimeout()
	bot := &Bot{
		session:          session,
		nodeManager:      messaging.NewMsgNodeManager(config.MaxMessageNodes),
		permChecker:      auth.NewPermissionChecker(cfg),
		llmClient:        llm.NewLLMClient(cfg, apiKeyManager, httpClient),
		webSearchClient:  processors.NewWebSearchClient(cfg, webSearchHTTPClient),
		googleLensClient: processors.NewGoogleLensClient(cfg, apiKeyManager, httpClient),
		geminiProvider:   providers.NewGeminiProvider(cfg, apiKeyManager),
		userPrefs:        storage.NewUserPreferencesManager(cfg.DatabaseURL),
		apiKeyManager:    apiKeyManager,
		tableRenderer:    createTableRenderer(cfg),
		fileProcessor:    processors.NewFileProcessor(),
		chartProcessor:   processors.NewChartProcessor(cfg.DatabaseURL),
		channelProcessor: processors.NewChannelProcessor(),
		messageCache:     storage.NewMessageNodeCache(cfg.DatabaseURL),
		shutdownCtx:      shutdownCtx,
		shutdownCancel:   shutdownCancel,
		httpClient:       httpClient,
		paginationCache:  NewPaginationCache(),
		messageJobs:      make(chan *discordgo.MessageCreate, 100), // Buffered channel
	}
	bot.config.Store(cfg)

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

// resolveUserModel returns a safe model name for the user, falling back to the configured default when needed.
func (b *Bot) resolveUserModel(ctx context.Context, userID string, cfg *config.Config) string {
	if cfg == nil {
		return ""
	}

	defaultModel := cfg.GetDefaultModel()
	if userID == "" || b.userPrefs == nil {
		return defaultModel
	}

	preferredModel := b.userPrefs.GetUserModel(ctx, userID, defaultModel)
	if preferredModel == "" {
		return defaultModel
	}

	if cfg.Models != nil {
		if _, exists := cfg.Models[preferredModel]; exists {
			return preferredModel
		}
	}

	if preferredModel != defaultModel {
		log.Printf("Preferred model %s for user %s not found in config. Using default model %s", preferredModel, userID, defaultModel)
	}

	if defaultModel != "" {
		return defaultModel
	}

	return preferredModel
}

// Start starts the Discord bot
func (b *Bot) Start() error {
	// Start worker pool
	cfg := b.config.Load()
	workerCount := cfg.WorkerCount
	for i := 0; i < workerCount; i++ {
		b.activeGoroutines.Add(1)
		go func(workerID int) {
			defer b.activeGoroutines.Done()
			log.Printf("Starting message worker %d", workerID)
			for {
				select {
				case job := <-b.messageJobs:
					b.handleMessage(b.session, job)
				case <-b.shutdownCtx.Done():
					log.Printf("Stopping message worker %d", workerID)
					return
				}
			}
		}(i)
	}

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
		log.Printf("Preinstalling common chart libraries in the background...")
		if err := b.chartProcessor.PreinstallCommonLibraries(b.shutdownCtx); err != nil {
			log.Printf("⚠️ Failed to preinstall common chart libraries: %v", err)
		} else {
			log.Printf("✅ Common chart libraries preinstalled successfully.")
		}
	}()

	// Start config file watcher
	go b.watchConfig()

	return b.session.Open()
}

// watchConfig watches the config file for changes and reloads it
func (b *Bot) watchConfig() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Println("ERROR: Could not create config watcher:", err)
		return
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			log.Println("ERROR: Could not close config watcher:", err)
		}
	}()

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Write == fsnotify.Write {
					log.Println("Config file modified. Reloading...")
					newCfg, err := config.LoadConfig("configs/config.yaml")
					if err != nil {
						log.Println("ERROR: Failed to reload config:", err)
					} else {
						b.config.Store(newCfg) // Atomically swap the pointer
						log.Println("Config reloaded successfully.")
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("ERROR: Config watcher error:", err)
			}
		}
	}()

	err = watcher.Add("configs/config.yaml")
	if err != nil {
		log.Println("ERROR: Could not add config to watcher:", err)
	}
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
		port = "8081"
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

// isBotActiveInThread checks if the bot has sent messages in the given thread
func (b *Bot) isBotActiveInThread(s *discordgo.Session, threadID string) bool {
	// Check recent messages in the thread to see if the bot has participated
	messages, err := s.ChannelMessages(threadID, 50, "", "", "")
	if err != nil {
		log.Printf("Failed to fetch thread messages for bot activity check: %v", err)
		return false
	}

	botUserID := s.State.User.ID
	for _, msg := range messages {
		if msg.Author.ID == botUserID {
			return true
		}
	}

	return false
}
