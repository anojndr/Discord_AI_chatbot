package processors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"DiscordAIChatbot/internal/auth"
	"DiscordAIChatbot/internal/config"
	"DiscordAIChatbot/internal/interfaces"
	"DiscordAIChatbot/internal/llm"
	"DiscordAIChatbot/internal/messaging"
	"DiscordAIChatbot/internal/utils"
)

// WebSearchClient handles web search API requests
type WebSearchClient struct {
	config    *config.Config
	httpClient *http.Client
	formatter *WebSearchResultFormatter
}

// WebSearchRequest represents the request to the web search API
type WebSearchRequest struct {
	Query         string `json:"query"`
	MaxResults    int    `json:"max_results,omitempty"`
	MaxCharPerURL int    `json:"max_char_per_url,omitempty"`
}

// ExtractedResult represents a single URL processing result
type ExtractedResult struct {
	URL                   string      `json:"url"`
	SourceType            string      `json:"source_type"`
	ProcessedSuccessfully bool        `json:"processed_successfully"`
	Data                  interface{} `json:"data"`
	Error                 *string     `json:"error"`
}

// WebSearchResponse represents the response from the web search API
type WebSearchResponse struct {
	QueryDetails struct {
		Query               string `json:"query"`
		MaxResultsRequested int    `json:"max_results_requested"`
		ActualResultsFound  int    `json:"actual_results_found"`
	} `json:"query_details"`
	Results []ExtractedResult `json:"results"`
	Error   *string           `json:"error"`
}

// WebSearchDecision represents the LLM decision response for web search
type WebSearchDecision struct {
	WebSearchRequired bool     `json:"web_search_required"`
	SearchQueries     []string `json:"search_queries,omitempty"`
}

// URLExtractRequest represents the request to the URL extract API
type URLExtractRequest struct {
	URLs          []string `json:"urls"`
	MaxCharPerURL int      `json:"max_char_per_url,omitempty"`
}

// URLExtractResponse represents the response from the URL extract API
type URLExtractResponse struct {
	RequestDetails struct {
		URLsRequested int `json:"urls_requested"`
		URLsProcessed int `json:"urls_processed"`
	} `json:"request_details"`
	Results []ExtractedResult `json:"results"`
	Error   *string           `json:"error"`
}

// HealthResponse represents the response from the health endpoint
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

// RedditComment represents a structured Reddit comment as per API docs
type RedditComment struct {
	Author  string          `json:"author"`
	Score   float64         `json:"score"`
	Text    string          `json:"text"`
	Replies []RedditComment `json:"replies,omitempty"`
}

// YouTubeComment represents a structured YouTube comment as per API docs
type YouTubeComment struct {
	Author  string           `json:"author"`
	Text    string           `json:"text"`
	Likes   int              `json:"likes,omitempty"`
	Replies []YouTubeComment `json:"replies,omitempty"`
}

// TwitterComment represents a structured Twitter comment as per API docs
type TwitterComment struct {
	Author    string `json:"author"`
	Username  string `json:"username"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Likes     string `json:"likes"`
	Replies   string `json:"replies"`
	Retweets  string `json:"retweets"`
}

// NewWebSearchClient creates a new web search client with optimized HTTP settings.
// It configures connection pooling, timeouts, and other performance optimizations
// for reliable web search API communication.
func NewWebSearchClient(cfg *config.Config) *WebSearchClient {
	return &WebSearchClient{
		config:    cfg,
		httpClient: createOptimizedHTTPClient(),
		formatter: NewWebSearchResultFormatter(),
	}
}

// createOptimizedHTTPClient creates an HTTP client with performance optimizations
func createOptimizedHTTPClient() *http.Client {
	return &http.Client{
		Timeout: config.DefaultHTTPTimeout * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:          config.MaxIdleConns,
			MaxIdleConnsPerHost:   config.MaxIdleConnsPerHost,
			IdleConnTimeout:       config.IdleConnTimeout * time.Second,
			TLSHandshakeTimeout:   config.TLSHandshakeTimeout * time.Second,
			ExpectContinueTimeout: config.ExpectContinueTimeout * time.Second,
		},
	}
}

// CheckHealth performs a health check on the web search API
func (w *WebSearchClient) CheckHealth(ctx context.Context) error {
	url := w.config.WebSearch.BaseURL + "/health"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Failed to close response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed with status %d", resp.StatusCode)
	}

	var healthResp HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&healthResp); err != nil {
		return fmt.Errorf("failed to decode health response: %w", err)
	}

	if healthResp.Status != "healthy" {
		return fmt.Errorf("API reported unhealthy status: %s", healthResp.Status)
	}

	return nil
}

// DecideWebSearch uses the user's preferred model to determine if web search is needed
func (w *WebSearchClient) DecideWebSearch(ctx context.Context, llmClient *llm.LLMClient, chatHistory []messaging.OpenAIMessage, latestQuery string, userID string, userPrefs interfaces.UserPreferences, systemPrompt string, images []messaging.ImageContent) (*WebSearchDecision, error) {
	// Check for skip web search directive
	if strings.HasPrefix(latestQuery, "SKIP_WEB_SEARCH_DECIDER\n\n") {
		// Remove the directive and return no web search
		return &WebSearchDecision{
			WebSearchRequired: false,
		}, nil
	}
	
	// Keep the original query including any "SEARCH THE NET" directive
	// so the Web Search Decider can see the user's explicit intent
	// Get current year dynamically
	currentYear := time.Now().Year()
	
	// Prepare the web search decider system prompt
	webSearchDeciderPrompt := fmt.Sprintf(`<task>
Analyze the latest query (and any attached images) to determine if web search is needed.

CRITICAL: EXPLICIT USER DIRECTIVES OVERRIDE ALL OTHER LOGIC
- If the query contains "SEARCH THE NET", this is an ABSOLUTE, EXPLICIT user request for web search
- ALWAYS honor this directive and enable web search, regardless of how simple or basic the query appears
- For basic prompts with "SEARCH THE NET" (like "Hi\n\nSEARCH THE NET"), use the basic prompt text itself as the search query (e.g., search for "Hi")

</task>

<criteria>
Use web search when responding requires up-to-date information from the web or location-specific data. The four main categories are:

1. **Local Information**: Questions requiring location-specific data (weather, local businesses, events)
2. **Freshness**: Questions about recent developments, current events, or topics where information changes frequently (stock prices, software releases, news, sports schedules)
3. **Niche Information**: Questions about specialized or obscure topics not widely documented (small companies, specific regulations, technical specifications)
4. **Accuracy-Critical**: Questions where outdated information could cause significant problems (software versions, medical information, legal requirements)

</criteria>

<instructions>
1. **CHECK FOR EXPLICIT DIRECTIVE FIRST**: Before applying any other logic, check if "SEARCH THE NET" is present in the query
   - If present: IMMEDIATELY enable web search
   - For basic prompts (like "Hi", "Hello", simple greetings): Use the basic prompt text itself as the search query without modifications
   - For complex prompts: Extract meaningful search terms from the content before "SEARCH THE NET"
2. **Apply criteria**: Determine if the query falls into one of the four categories listed above
3. **Use conversation context**: Review chat history to understand follow-up questions and include context in search queries
4. **Generate appropriate search queries**: Create specific, focused search queries in English. Always translate foreign language terms to English for optimal results. For complex queries involving multiple entities or concepts, generate separate queries for each entity plus one for their relationship/comparison. For comparative queries (e.g., "A vs B", "which is better A or B", "A compared to B"), generate multiple queries: one for each entity being compared plus one for the direct comparison. **IMPORTANT: For time-sensitive queries (news, releases, current events), append the current year (%d) to search queries to ensure fresh results**. **LIMIT: Generate a maximum of 3 search queries total**
5. **Handle images**: If images are attached, identify specific objects, people, places, or text content in the image and use those exact identifications in search queries. For text in images, extract and use the actual text content
6. **Return proper JSON**: Use exact format shown in examples
</instructions>

<examples>
<example>
<latest_query>What is 15 + 27?</latest_query>
<o>
{"web_search_required": false}
</o>
</example>

<example>
<latest_query>Write me a poem about the sunset</latest_query>
<o>
{"web_search_required": false}
</o>
</example>

<example>
<latest_query>What's the weather like in Paris today?</latest_query>
<o>
{
  "web_search_required": true,
  "search_queries": ["Paris weather today"]
}
</o>
</example>

<example>
<latest_query>Tell me about the new React 19 features</latest_query>
<o>
{
  "web_search_required": true,
  "search_queries": ["React 19 new features %d"]
}
</o>
</example>

<example>
<latest_query>Hi

SEARCH THE NET</latest_query>
<o>
{
  "web_search_required": true,
  "search_queries": ["Hi"]
}
</o>
</example>

<example>
<latest_query>Hello there

SEARCH THE NET</latest_query>
<o>
{
  "web_search_required": true,
  "search_queries": ["Hello there"]
}
</o>
</example>

<example>
<latest_query>Good morning

SEARCH THE NET</latest_query>
<o>
{
  "web_search_required": true,
  "search_queries": ["Good morning"]
}
</o>
</example>
</examples>

<output_format>
Return ONLY valid JSON in one of these formats:
- If no search needed: {"web_search_required": false}
- If search needed: {"web_search_required": true, "search_queries": ["query1", "query2", ...]}
</output_format>

/no_think`, currentYear, currentYear)

	// Use the configured web search model
	model := w.config.WebSearch.Model
	if _, exists := w.config.Models[model]; !exists {
		// Fallback to first available model if configured model not found
		model = w.config.GetFirstModel()
	}

	// Determine if the model supports usernames for system prompt processing
	acceptUsernames := auth.SupportsUsernames(model)

	// Create OpenAI messages using the same format as the main model
	messages := []messaging.OpenAIMessage{}

	// Add system prompt (use original system prompt, not web search decider prompt)
	messages = llmClient.AddSystemPrompt(messages, systemPrompt, acceptUsernames)

	// Add structured conversation history messages (images preserved per message like main model)
	if len(chatHistory) > 0 {
		messages = append(messages, chatHistory...)
	}

	// Prepare the final user message with web search decider prompt prepended to latest query
	promptWithQuery := webSearchDeciderPrompt + "\n\nlatest query: " + latestQuery

	// Prepare user message content with images if provided
	var userContent interface{}
	if len(images) > 0 {
		// Create multimodal content with text and images
		multiContent := []messaging.MessageContent{
			{
				Type: "text",
				Text: promptWithQuery,
			},
		}

		// Add all images to the content
		for _, img := range images {
			multiContent = append(multiContent, messaging.MessageContent{
				Type: "image_url",
				ImageURL: &messaging.ImageURL{
					URL: img.ImageURL.URL,
				},
			})
		}

		userContent = multiContent
	} else {
		// Text-only content
		userContent = promptWithQuery
	}

	// Add the latest query (with web search decider prompt prepended) as the final user message
	messages = append(messages, messaging.OpenAIMessage{
		Role:    "user",
		Content: userContent,
	})

	// Get response from LLM
	stream, err := llmClient.StreamChatCompletion(ctx, model, messages)
	if err != nil {
		return nil, fmt.Errorf("failed to get web search decision from LLM: %w", err)
	}

	// Collect the response
	var responseContent strings.Builder
	for response := range stream {
		if response.Error != nil {
			return nil, fmt.Errorf("stream error: %w", response.Error)
		}
		if response.Content != "" {
			responseContent.WriteString(response.Content)
		}
		if response.FinishReason != "" {
			break
		}
	}

	// Parse JSON response
	var decision WebSearchDecision
	responseText := strings.TrimSpace(responseContent.String())

	// Extract JSON from response (in case there's extra text)
	startIdx := strings.Index(responseText, "{")
	endIdx := strings.LastIndex(responseText, "}")
	if startIdx == -1 || endIdx == -1 {
		return nil, fmt.Errorf("no valid JSON found in response: %s", responseText)
	}

	jsonStr := responseText[startIdx : endIdx+1]

	// Clean common JSON formatting issues
	jsonStr = strings.ReplaceAll(jsonStr, ",]", "]") // Remove trailing commas in arrays
	jsonStr = strings.ReplaceAll(jsonStr, ",}", "}") // Remove trailing commas in objects

	if err := json.Unmarshal([]byte(jsonStr), &decision); err != nil {
		return nil, fmt.Errorf("failed to parse web search decision JSON: %w. JSON: %s", err, jsonStr)
	}

	return &decision, nil
}

// SearchMultiple performs multiple web searches and combines results
func (w *WebSearchClient) SearchMultiple(ctx context.Context, queries []string) (string, error) {
	if len(queries) == 0 {
		return "", fmt.Errorf("no search queries provided")
	}

	// Prepare a slice to hold results in the same order as the queries
	results := make([]string, len(queries))

	// Launch concurrent searches
	var wg sync.WaitGroup
	for idx, q := range queries {
		wg.Add(1)

		// Capture loop variables
		i := idx
		query := q

		go func() {
			defer wg.Done()

			res, err := w.Search(ctx, query)
			if err != nil {
				results[i] = fmt.Sprintf("Error searching for '%s': %v\n", query, err)
				return
			}
			results[i] = res
		}()
	}

	// Wait for all searches to complete
	wg.Wait()

	// Combine results in original order
	var allResults strings.Builder
	for i, res := range results {
		if i > 0 {
			allResults.WriteString("\n\n--- Search Query " + fmt.Sprintf("%d", i+1) + " ---\n")
		}
		allResults.WriteString(res)
	}

	return allResults.String(), nil
}

// Search performs a web search and returns formatted results
func (w *WebSearchClient) Search(ctx context.Context, query string) (string, error) {
	// Prepare request
	searchReq := WebSearchRequest{
		Query:         query,
		MaxResults:    w.config.WebSearch.MaxResults,
		MaxCharPerURL: w.config.WebSearch.MaxChars,
	}

	// Convert to JSON
	jsonData, err := json.Marshal(searchReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := w.config.WebSearch.BaseURL + "/search"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Make request
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Failed to close response body: %v", err)
		}
	}()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("web search API returned status %d", resp.StatusCode)
	}

	// Parse response
	var searchResp WebSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Check for API error
	if searchResp.Error != nil {
		return "", fmt.Errorf("web search API error: %s", *searchResp.Error)
	}

	// Format results using the formatter
	return w.formatter.FormatSearchResults(&searchResp), nil
}

// ExtractURLs extracts content from the provided URLs
func (w *WebSearchClient) ExtractURLs(ctx context.Context, urls []string) (string, error) {
	if len(urls) == 0 {
		return "", fmt.Errorf("no URLs provided")
	}

	// Validate URL limit as per API docs (maximum of 20 URLs per request)
	if len(urls) > 20 {
		return "", fmt.Errorf("too many URLs: maximum 20 URLs per request, got %d", len(urls))
	}

	// Prepare request
	extractReq := URLExtractRequest{
		URLs:          urls,
		MaxCharPerURL: w.config.WebSearch.MaxChars,
	}

	// Convert to JSON
	jsonData, err := json.Marshal(extractReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := w.config.WebSearch.BaseURL + "/extract"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Make request
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Failed to close response body: %v", err)
		}
	}()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("URL extract API returned status %d", resp.StatusCode)
	}

	// Parse response
	var extractResp URLExtractResponse
	if err := json.NewDecoder(resp.Body).Decode(&extractResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Check for API error
	if extractResp.Error != nil {
		return "", fmt.Errorf("URL extract API error: %s", *extractResp.Error)
	}

	// Format results using the formatter
	return w.formatter.FormatExtractResults(&extractResp), nil
}

// DetectURLs detects URLs in the given text with improved YouTube URL handling
func DetectURLs(text string) []string {
	// First, extract YouTube URLs using our specialized YouTube extractor
	youtubeURLs := utils.DetectYouTubeURLs(text)

	// URL regex pattern that matches http, https, and common domain patterns
	urlPattern := `(?i)\b(?:https?://(?:[-\w\.]+)+(?::[0-9]+)?(?:/(?:[\w/_\.-])*)?(?:\?(?:[\w&=%\.])*)?(?:#(?:[\w\.])*)?|(?:www\.)?(?:[-\w\.]+)+\.(?:[a-z]{2,})(?::[0-9]+)?(?:/(?:[\w/_\.-])*)?(?:\?(?:[\w&=%\.])*)?(?:#(?:[\w\.])*)?)`

	re := regexp.MustCompile(urlPattern)
	matches := re.FindAllString(text, -1)

	// Clean up and normalize URLs
	var urls []string
	seen := make(map[string]bool)

	// Add YouTube URLs first (they have priority and are already validated)
	for _, youtubeURL := range youtubeURLs {
		if !seen[youtubeURL] {
			urls = append(urls, youtubeURL)
			seen[youtubeURL] = true
		}
	}

	// Then process other URLs
	for _, match := range matches {
		url := strings.TrimSpace(match)

		// Skip if it looks like a standalone filename (e.g., "message.txt", "document.pdf")
		if !strings.HasPrefix(strings.ToLower(url), "http://") && !strings.HasPrefix(strings.ToLower(url), "https://") {
			// Check if it's just a filename by looking for common patterns
			if isLikelyFilename(url) {
				continue
			}
			url = "https://" + url
		}

		// Skip if it's a YouTube URL (already processed above)
		if utils.IsValidYouTubeURL(url) {
			continue
		}

		// Avoid duplicates
		if !seen[url] {
			urls = append(urls, url)
			seen[url] = true
		}
	}

	return urls
}

// isLikelyFilename checks if a string looks like a filename rather than a domain
func isLikelyFilename(s string) bool {
	// Check for common file extensions (but exclude common TLDs)
	fileExtensions := []string{".txt", ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".csv", ".json", ".xml", ".yml", ".yaml", ".md", ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".svg", ".mp4", ".avi", ".mov", ".mp3", ".wav", ".zip", ".rar", ".tar", ".gz", ".log", ".sql", ".py", ".js", ".html", ".css", ".php", ".java", ".cpp", ".c", ".h", ".go", ".rs", ".sh", ".bat", ".exe", ".dll", ".so", ".dmg", ".pkg", ".deb", ".rpm"}

	lowerS := strings.ToLower(s)
	for _, ext := range fileExtensions {
		if strings.HasSuffix(lowerS, ext) {
			return true
		}
	}

	// Common domain TLDs that should NOT be treated as filenames
	commonTlds := []string{".com", ".org", ".net", ".edu", ".gov", ".mil", ".int", ".co", ".io", ".me", ".tv", ".cc", ".tk", ".ml", ".ga", ".cf", ".uk", ".de", ".fr", ".it", ".es", ".ru", ".cn", ".jp", ".kr", ".in", ".au", ".ca", ".br", ".mx", ".us", ".info", ".biz", ".name", ".mobi", ".asia", ".tel", ".travel", ".museum", ".aero", ".coop", ".jobs", ".pro", ".cat", ".post", ".xxx", ".arpa", ".root", ".local", ".localhost", ".test", ".invalid", ".example", ".onion"}

	// If it ends with a common TLD, it's likely a domain, not a filename
	for _, tld := range commonTlds {
		if strings.HasSuffix(lowerS, tld) {
			return false
		}
	}

	return false
}