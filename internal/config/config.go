// Package config provides configuration management for the Discord AI chatbot.
// It handles loading YAML configuration files, setting defaults, and providing
// access to various bot settings including LLM models, Discord credentials,
// permissions, and API configurations.
package config

import (
	"fmt"
	"os"

	yaml "gopkg.in/yaml.v3"
)

// Config represents the main configuration structure
type Config struct {
	// Discord settings
	BotToken      string `yaml:"bot_token"`
	ClientID      string `yaml:"client_id"`
	StatusMessage string `yaml:"status_message"`

	// Default model for new users
	DefaultModel string `yaml:"default_model"`

	// Message limits
	MaxImages   int `yaml:"max_images"`
	MaxMessages int `yaml:"max_messages"`


	// Behavior settings
	UsePlainResponses bool `yaml:"use_plain_responses"`
	AllowDMs          bool `yaml:"allow_dms"`

	// Database settings
	DatabaseURL string `yaml:"database_url"`

	// Logging settings
	Logging struct {
		LogLevel string `yaml:"log_level"`
	} `yaml:"logging"`

	// Web Search settings
	WebSearch struct {
		BaseURL    string `yaml:"base_url"`
		MaxResults int    `yaml:"max_results"`
		MaxChars   int    `yaml:"max_chars_per_url"`
		Model      string `yaml:"model"`
	} `yaml:"web_search"`

	// SerpAPI settings
	SerpAPI struct {
		APIKey  string   `yaml:"api_key,omitempty"`  // Keep for backward compatibility
		APIKeys []string `yaml:"api_keys,omitempty"` // New field for multiple keys
	} `yaml:"serpapi"`

	// Permissions
	Permissions struct {
		Users struct {
			AdminIDs   []string `yaml:"admin_ids"`
			AllowedIDs []string `yaml:"allowed_ids"`
			BlockedIDs []string `yaml:"blocked_ids"`
		} `yaml:"users"`
		Roles struct {
			AllowedIDs []string `yaml:"allowed_ids"`
			BlockedIDs []string `yaml:"blocked_ids"`
		} `yaml:"roles"`
		Channels struct {
			AllowedIDs []string `yaml:"allowed_ids"`
			BlockedIDs []string `yaml:"blocked_ids"`
		} `yaml:"channels"`
	} `yaml:"permissions"`

	// LLM settings
	Providers    map[string]Provider    `yaml:"providers"`
	Models       map[string]ModelParams `yaml:"models"`
	SystemPrompt string                 `yaml:"system_prompt"`


	// Channel query settings
	Channel struct {
		// Token threshold ratio (0.0-1.0) for channel message fetching
		// Default: 0.7 (70% of model's token limit)
		TokenThreshold float64 `yaml:"token_threshold"`
	} `yaml:"channel"`

	// Context summarization settings
	ContextSummarization struct {
		// Enable context summarization when approaching token limit
		Enabled bool `yaml:"enabled"`
		// Token threshold ratio (0.0-1.0) to trigger summarization
		// Default: 0.8 (80% of model's token limit)
		TriggerThreshold float64 `yaml:"trigger_threshold"`
		// Model to use for summarization (should be fast and cheap)
		// Default: "gemini/gemini-2.5-flash"
		Model string `yaml:"model"`
		// Maximum number of conversation pairs to summarize in one batch
		// Default: 1 (summarize one pair at a time)
		MaxPairsPerBatch int `yaml:"max_pairs_per_batch"`
		// Minimum number of conversation pairs to keep unsummarized
		// Default: 0 (can summarize all pairs)
		MinUnsummarizedPairs int `yaml:"min_unsummarized_pairs"`
	} `yaml:"context_summarization"`


	// Table rendering settings
	TableRendering struct {
		// Method for table rendering: "gg" or "rod"
		Method string `yaml:"method"`
		// Rod-specific settings
		Rod struct {
			// Timeout for browser operations in seconds
			Timeout int `yaml:"timeout"`
			// PNG quality (0-100)
			Quality int `yaml:"quality"`
		} `yaml:"rod"`
	} `yaml:"table_rendering"`
}

// Provider represents an LLM provider configuration
type Provider struct {
	BaseURL string   `yaml:"base_url"`
	APIKey  string   `yaml:"api_key,omitempty"`  // Keep for backward compatibility
	APIKeys []string `yaml:"api_keys,omitempty"` // New field for multiple keys
}

// GetAPIKeys returns all available API keys for the provider
func (p *Provider) GetAPIKeys() []string {
	if len(p.APIKeys) > 0 {
		return p.APIKeys
	}
	if p.APIKey != "" {
		return []string{p.APIKey}
	}
	return []string{}
}

// GetSerpAPIKeys returns all available SerpAPI keys
func (c *Config) GetSerpAPIKeys() []string {
	if len(c.SerpAPI.APIKeys) > 0 {
		return c.SerpAPI.APIKeys
	}
	if c.SerpAPI.APIKey != "" {
		return []string{c.SerpAPI.APIKey}
	}
	return []string{}
}

// UseRodTableRendering returns true if Rod should be used for table rendering
func (c *Config) UseRodTableRendering() bool {
	return c.TableRendering.Method == "rod"
}

// GetRodTimeout returns the timeout for Rod operations in seconds
func (c *Config) GetRodTimeout() int {
	if c.TableRendering.Rod.Timeout > 0 {
		return c.TableRendering.Rod.Timeout
	}
	return DefaultRodTimeout
}

// GetRodQuality returns the PNG quality for Rod rendering
func (c *Config) GetRodQuality() int {
	if c.TableRendering.Rod.Quality > 0 {
		return c.TableRendering.Rod.Quality
	}
	return DefaultRodQuality
}

// GetChannelTokenThreshold returns the token threshold for channel queries
// Falls back to 0.7 (70%) if not specified
func (c *Config) GetChannelTokenThreshold() float64 {
	if c.Channel.TokenThreshold > 0 && c.Channel.TokenThreshold <= 1.0 {
		return c.Channel.TokenThreshold
	}
	return 0.7 // Default to 70%
}

// GetContextSummarizationEnabled returns whether context summarization is enabled
func (c *Config) GetContextSummarizationEnabled() bool {
	return c.ContextSummarization.Enabled
}

// GetContextSummarizationTriggerThreshold returns the token threshold to trigger summarization
// Falls back to DefaultContextSummarizationTriggerThreshold if not specified
func (c *Config) GetContextSummarizationTriggerThreshold() float64 {
	if c.ContextSummarization.TriggerThreshold > 0 && c.ContextSummarization.TriggerThreshold <= 1.0 {
		return c.ContextSummarization.TriggerThreshold
	}
	return DefaultContextSummarizationTriggerThreshold
}

// GetContextSummarizationModel returns the model to use for summarization
// Falls back to DefaultContextSummarizationModel if not specified
func (c *Config) GetContextSummarizationModel() string {
	if c.ContextSummarization.Model != "" {
		return c.ContextSummarization.Model
	}
	return DefaultContextSummarizationModel
}

// GetContextSummarizationMaxPairsPerBatch returns the maximum pairs to summarize per batch
// Falls back to DefaultContextSummarizationMaxPairsPerBatch if not specified
func (c *Config) GetContextSummarizationMaxPairsPerBatch() int {
	if c.ContextSummarization.MaxPairsPerBatch > 0 {
		return c.ContextSummarization.MaxPairsPerBatch
	}
	return DefaultContextSummarizationMaxPairsPerBatch
}

// GetContextSummarizationMinUnsummarizedPairs returns the minimum pairs to keep unsummarized
// Falls back to DefaultContextSummarizationMinUnsummarizedPairs if not specified
func (c *Config) GetContextSummarizationMinUnsummarizedPairs() int {
	if c.ContextSummarization.MinUnsummarizedPairs >= 0 {
		return c.ContextSummarization.MinUnsummarizedPairs
	}
	return DefaultContextSummarizationMinUnsummarizedPairs
}

// GetModelTokenLimit returns the token limit for a specific model
// Falls back to DefaultTokenLimit if not specified
func (c *Config) GetModelTokenLimit(modelName string) int {
	// Default token limit
	const DefaultTokenLimit = 128000
	
	if c.Models == nil {
		return DefaultTokenLimit
	}
	
	modelParams, exists := c.Models[modelName]
	if !exists {
		return DefaultTokenLimit
	}
	
	if modelParams.TokenLimit != nil && *modelParams.TokenLimit > 0 {
		return *modelParams.TokenLimit
	}
	
	return DefaultTokenLimit
}

// ModelParams represents model-specific parameters
type ModelParams struct {
	Temperature      *float32       `yaml:"temperature,omitempty"`
	ReasoningEffort  string         `yaml:"reasoning_effort,omitempty"`
	SearchParameters map[string]any `yaml:"search_parameters,omitempty"`
	ThinkingBudget   *int32         `yaml:"thinking_budget,omitempty"`
	TokenLimit       *int           `yaml:"token_limit,omitempty"`
	ExtraParams      map[string]any `yaml:",inline"`
}

// LoadConfig loads configuration from YAML file
// It supports both local development and Render deployment:
// 1. Local: reads from configs/config.yaml
// 2. Render: reads from /etc/secrets/config.yaml (Render secret files)
func LoadConfig(filename string) (*Config, error) {
	if filename == "" {
		filename = "configs/config.yaml"
	}

	// Try to load from Render secret files first (for production)
	renderSecretPath := "/etc/secrets/config.yaml"
	if _, err := os.Stat(renderSecretPath); err == nil {
		data, err := os.ReadFile(renderSecretPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read Render secret config file: %w", err)
		}
		return parseConfig(data)
	}

	// Fallback to local config file
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	return parseConfig(data)
}

// parseConfig parses YAML data into Config struct
func parseConfig(data []byte) (*Config, error) {

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set defaults
	if config.DefaultModel == "" {
		config.DefaultModel = DefaultModel
	}
	if config.MaxImages == 0 {
		config.MaxImages = DefaultMaxImages
	}
	if config.MaxMessages == 0 {
		config.MaxMessages = DefaultMaxMessages
	}
	if config.StatusMessage == "" {
		config.StatusMessage = DefaultStatusMessage
	}
	if !config.AllowDMs {
		config.AllowDMs = DefaultAllowDMs
	}

	// Set web search defaults
	if config.WebSearch.BaseURL == "" {
		config.WebSearch.BaseURL = DefaultWebSearchBaseURL
	}
	if config.WebSearch.MaxResults == 0 {
		config.WebSearch.MaxResults = DefaultWebSearchMaxResults
	}
	if config.WebSearch.MaxChars == 0 {
		config.WebSearch.MaxChars = DefaultWebSearchMaxChars
	}
	if config.WebSearch.Model == "" {
		config.WebSearch.Model = DefaultWebSearchModel
	}

	// Set logging defaults
	if config.Logging.LogLevel == "" {
		config.Logging.LogLevel = DefaultLogLevel
	}


	// Set table rendering defaults
	if config.TableRendering.Method == "" {
		config.TableRendering.Method = DefaultTableRenderingMethod
	}
	if config.TableRendering.Rod.Timeout == 0 {
		config.TableRendering.Rod.Timeout = DefaultRodTimeout
	}
	if config.TableRendering.Rod.Quality == 0 {
		config.TableRendering.Rod.Quality = DefaultRodQuality
	}

	// Set context summarization defaults
	if !config.ContextSummarization.Enabled {
		config.ContextSummarization.Enabled = DefaultContextSummarizationEnabled
	}
	if config.ContextSummarization.TriggerThreshold == 0 {
		config.ContextSummarization.TriggerThreshold = DefaultContextSummarizationTriggerThreshold
	}
	if config.ContextSummarization.Model == "" {
		config.ContextSummarization.Model = DefaultContextSummarizationModel
	}
	if config.ContextSummarization.MaxPairsPerBatch == 0 {
		config.ContextSummarization.MaxPairsPerBatch = DefaultContextSummarizationMaxPairsPerBatch
	}
	if config.ContextSummarization.MinUnsummarizedPairs == 0 {
		config.ContextSummarization.MinUnsummarizedPairs = DefaultContextSummarizationMinUnsummarizedPairs
	}


	return &config, nil
}

// GetFirstModel returns the first model from the config
func (c *Config) GetFirstModel() string {
	for model := range c.Models {
		return model
	}
	return ""
}

// GetDefaultModel returns the default model for new users
func (c *Config) GetDefaultModel() string {
	// Use the configured default model
	if c.DefaultModel != "" {
		// Check if the default model exists in config
		if _, exists := c.Models[c.DefaultModel]; exists {
			return c.DefaultModel
		}
	}

	// Fallback to the first available model from config
	return c.GetFirstModel()
}


