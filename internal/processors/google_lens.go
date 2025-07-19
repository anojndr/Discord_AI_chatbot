package processors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"DiscordAIChatbot/internal/config"
	"DiscordAIChatbot/internal/net"
	"DiscordAIChatbot/internal/storage"
)

// GoogleLensClient handles requests to the SerpAPI Google Lens endpoint.
type GoogleLensClient struct {
	config        *config.Config
	httpClient    *http.Client
	apiKeyManager *storage.APIKeyManager
}

// NewGoogleLensClient creates a new GoogleLensClient with reasonable defaults.
func NewGoogleLensClient(cfg *config.Config, apiKeyManager *storage.APIKeyManager) *GoogleLensClient {
	return &GoogleLensClient{
		config:        cfg,
		apiKeyManager: apiKeyManager,
		httpClient:    net.NewOptimizedClient(0),
	}
}

// GoogleLensResponse represents the JSON returned by SerpAPI Google Lens API.
// Updated to match the latest API documentation.
type GoogleLensResponse struct {
	VisualMatches []struct {
		Position   int     `json:"position"`
		Title      string  `json:"title"`
		Link       string  `json:"link"`
		Source     string  `json:"source"`
		SourceIcon string  `json:"source_icon"`
		Rating     float64 `json:"rating"`
		Reviews    int     `json:"reviews"`
		Price      *struct {
			Value          string  `json:"value"`
			ExtractedValue float64 `json:"extracted_value"`
			Currency       string  `json:"currency"`
		} `json:"price"`
		InStock         bool   `json:"in_stock"`
		Condition       string `json:"condition"`
		Thumbnail       string `json:"thumbnail"`
		ThumbnailWidth  int    `json:"thumbnail_width"`
		ThumbnailHeight int    `json:"thumbnail_height"`
		Image           string `json:"image"`
		ImageWidth      int    `json:"image_width"`
		ImageHeight     int    `json:"image_height"`
	} `json:"visual_matches"`
	RelatedContent []struct {
		Query       string `json:"query"`
		Link        string `json:"link"`
		Thumbnail   string `json:"thumbnail"`
		SerpapiLink string `json:"serpapi_link"`
	} `json:"related_content"`
	SearchMetadata struct {
		Status string `json:"status"`
		ID     string `json:"id"`
	} `json:"search_metadata"`
	Error string `json:"error"`
}

// SearchOptions holds optional parameters for Google Lens search
type SearchOptions struct {
	Query      string // q parameter - search query to refine results
	Type       string // type parameter - all, products, exact_matches, visual_matches (default: "all")
	Language   string // hl parameter - language code (e.g., "en", "es", "fr")
	Country    string // country parameter - country code (e.g., "us", "fr", "de")
	SafeSearch string // safe parameter - "active" or "off"
}

// Search performs a Google Lens search using SerpAPI and returns a human-readable string.
// imageURL is mandatory. opts contains optional parameters.
func (g *GoogleLensClient) Search(ctx context.Context, imageURL string, opts *SearchOptions) (string, error) {
	if imageURL == "" {
		return "", fmt.Errorf("image URL is required for Google Lens search")
	}

	// Validate that the supplied imageURL is a well-formed absolute URL.  An
	// invalid value will cause SerpAPI to stall and eventually time-out, which
	// manifests as a context-deadline-exceeded error on our side.  Failing
	// early saves a useless network round-trip and surfaces a clearer message
	// to the caller.
	if parsed, err := url.Parse(imageURL); err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid image URL: %s", imageURL)
	}

	// Additional validation: ensure the URL is from a trusted domain or Discord CDN
	// This prevents processing of arbitrary URLs that might not be user-provided images
	parsed, _ := url.Parse(imageURL)
	trustedDomains := []string{
		"cdn.discordapp.com",   // Discord CDN
		"media.discordapp.net", // Discord media CDN
		"discord.com",          // Discord domain
		"discordapp.com",       // Discord app domain
	}

	isDomainTrusted := false
	for _, domain := range trustedDomains {
		if parsed.Host == domain || strings.HasSuffix(parsed.Host, "."+domain) {
			isDomainTrusted = true
			break
		}
	}

	// Log the domain and URL format for debugging
	log.Printf("Google Lens: Image URL domain: %s, trusted: %t", parsed.Host, isDomainTrusted)

	// Test if the URL is accessible before sending to Google Lens, unless disabled
	if !g.config.SerpAPI.DisablePreflightCheck {
		testResp, err := http.Head(imageURL)
		if err != nil {
			log.Printf("Google Lens: Warning - Image URL not accessible via HEAD request: %v", err)
		} else {
			log.Printf("Google Lens: Image URL accessible, Content-Type: %s, Status: %d",
				testResp.Header.Get("Content-Type"), testResp.StatusCode)
			_ = testResp.Body.Close()
		}
	}

	// Allow explicit user-provided URLs (those starting with http/https that aren't from trusted domains)
	// but log them for monitoring purposes
	if !isDomainTrusted {
		log.Printf("Processing Google Lens search for user-provided URL: %s", imageURL)
	}

	// Get available SerpAPI keys
	availableKeys := g.config.GetSerpAPIKeys()
	if len(availableKeys) == 0 {
		return "", fmt.Errorf("no SerpAPI keys configured")
	}

	// Try API keys until one works or we run out
	maxRetries := len(availableKeys)
	var lastNoResultsError error

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Get next API key
		apiKey, err := g.apiKeyManager.GetNextAPIKey(ctx, "serpapi", availableKeys)
		if err != nil {
			return "", fmt.Errorf("failed to get SerpAPI key: %w", err)
		}

		// Try the search with this key
		result, err := g.searchWithKey(ctx, imageURL, opts, apiKey)
		if err != nil {
			// Check if this is a "no results" error that we should retry
			if strings.Contains(err.Error(), "- retryable") {
				lastNoResultsError = err
				// Don't mark key as bad for "no results" - it might work for other searches
				log.Printf("No results with SerpAPI key, trying next key: %v", err)
				continue
			}

			// Check if this is an API key related error
			if g.isSerpAPIKeyError(err) {
				// Mark this key as bad and try the next one
				markErr := g.apiKeyManager.MarkKeyAsBad(ctx, "serpapi", apiKey, err.Error())
				if markErr != nil {
					// Log the error but continue with the retry
					fmt.Printf("Failed to mark SerpAPI key as bad: %v\n", markErr)
				}
				continue
			}
			// For non-API key errors, return immediately
			return "", err
		}

		// Success! Return the result
		return result, nil
	}

	// If we exhausted all keys and the last errors were all "no results", return that
	if lastNoResultsError != nil {
		return "", nil // Return empty result instead of error for no visual matches
	}

	return "", fmt.Errorf("all SerpAPI keys failed")
}

// searchWithKey performs the actual search with a specific API key
func (g *GoogleLensClient) searchWithKey(ctx context.Context, imageURL string, opts *SearchOptions, apiKey string) (string, error) {
	values := url.Values{}
	values.Set("engine", "google_lens")
	values.Set("url", imageURL)
	values.Set("api_key", apiKey)

	// Set default type if not provided
	searchType := "all"
	safeSearch := "off" // Default to safe search off

	if opts != nil {
		// Note: Removed q parameter as it was causing "no results" issues
		// The query will be handled by the LLM after getting visual matches
		if opts.Type != "" {
			searchType = opts.Type
		}
		if opts.Language != "" {
			values.Set("hl", opts.Language)
		}
		if opts.Country != "" {
			values.Set("country", opts.Country)
		}
		if opts.SafeSearch != "" {
			safeSearch = opts.SafeSearch
		}
	}
	// type parameter is required per latest API docs
	values.Set("type", searchType)
	// Set safe search parameter
	values.Set("safe", safeSearch)

	endpoint := "https://serpapi.com/search.json?" + values.Encode()

	// Debug logging to see the actual API request
	log.Printf("Google Lens API request: %s", endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Failed to close response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10)) // read at most 2KB for error msg
		return "", fmt.Errorf("SerpAPI returned status %d: %s", resp.StatusCode, string(body))
	}

	var glResp GoogleLensResponse
	if err := json.NewDecoder(resp.Body).Decode(&glResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	// If API indicates an error
	if glResp.Error != "" {
		if strings.Contains(strings.ToLower(glResp.Error), "hasn't returned any results") {
			// Treat as retryable: no results found, might be due to API key limitations
			return "", fmt.Errorf("no results found - retryable")
		}
		return "", fmt.Errorf("SerpAPI error: %s", glResp.Error)
	}

	// Check if we have any visual matches - if not, treat as retryable
	if len(glResp.VisualMatches) == 0 {
		log.Printf("Google Lens: No visual matches found. Status: %s, Related content count: %d",
			glResp.SearchMetadata.Status, len(glResp.RelatedContent))
		return "", fmt.Errorf("no visual matches found - retryable")
	}

	// Build readable output (limit to first 5 visual matches for brevity)
	var builder strings.Builder
	builder.WriteString("Google Lens Results\n")
	builder.WriteString(fmt.Sprintf("Status: %s | Search ID: %s\n\n", glResp.SearchMetadata.Status, glResp.SearchMetadata.ID))

	maxMatches := 5
	for i, match := range glResp.VisualMatches {
		if i >= maxMatches {
			break
		}
		builder.WriteString(fmt.Sprintf("Match %d: %s\n", i+1, match.Title))
		builder.WriteString(fmt.Sprintf("Source: %s\n", match.Source))
		builder.WriteString(fmt.Sprintf("Link: %s\n", match.Link))

		// Add price information if available
		if match.Price != nil {
			builder.WriteString(fmt.Sprintf("Price: %s\n", match.Price.Value))
		}

		// Add rating and reviews if available
		if match.Rating > 0 {
			builder.WriteString(fmt.Sprintf("Rating: %.1f", match.Rating))
			if match.Reviews > 0 {
				builder.WriteString(fmt.Sprintf(" (%d reviews)", match.Reviews))
			}
			builder.WriteString("\n")
		}

		// Add availability and condition if available
		if match.Condition != "" {
			builder.WriteString(fmt.Sprintf("Condition: %s\n", match.Condition))
		}

		builder.WriteString("\n")
	}

	if len(glResp.RelatedContent) > 0 {
		builder.WriteString("Related Content:\n")
		maxRelated := 5
		for i, rc := range glResp.RelatedContent {
			if i >= maxRelated {
				break
			}
			builder.WriteString(fmt.Sprintf("- %s (%s)\n", rc.Query, rc.Link))
		}
	}

	return builder.String(), nil
}

// isSerpAPIKeyError checks if the error is related to SerpAPI key authentication/authorization
func (g *GoogleLensClient) isSerpAPIKeyError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// SerpAPI-specific error patterns
	serpAPIErrorPatterns := []string{
		"invalid api key",
		"unauthorized",
		"authentication",
		"invalid authentication",
		"api key",
		"401",
		"403",
		"quota exceeded",
		"rate limit",
		"credits",
		"billing",
		"payment required",
		"subscription",
		"limit exceeded",
		"monthly limit",
		"daily limit",
		"no results found - retryable",
		"no visual matches found - retryable",
	}

	for _, pattern := range serpAPIErrorPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}
