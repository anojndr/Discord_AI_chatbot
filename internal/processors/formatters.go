package processors

import (
	"fmt"
	"strings"
)

// WebSearchResultFormatter handles formatting of web search results
type WebSearchResultFormatter struct{}

// NewWebSearchResultFormatter creates a new formatter instance
func NewWebSearchResultFormatter() *WebSearchResultFormatter {
	return &WebSearchResultFormatter{}
}

// FormatSearchResults formats the search results into a readable string
func (f *WebSearchResultFormatter) FormatSearchResults(resp *FinalResponsePayload) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("Found %d results for query: %s\n\n",
		resp.QueryDetails.ActualResultsFound, resp.QueryDetails.Query))

	for i, result := range resp.Results {
		builder.WriteString(fmt.Sprintf("--- Result %d ---\n", i+1))
		builder.WriteString(fmt.Sprintf("URL: %s\n", result.URL))
		builder.WriteString(fmt.Sprintf("Source Type: %s\n", result.SourceType))

		if !result.ProcessedSuccessfully {
			if result.Error != nil {
				builder.WriteString(fmt.Sprintf("Error: %s\n", *result.Error))
			} else {
				builder.WriteString("Error: Processing failed\n")
			}
			builder.WriteString("\n")
			continue
		}

		// Format content based on source type
		if result.Data != nil {
			content := f.ExtractContentFromData(result.Data, result.SourceType)
			if content != "" {
				builder.WriteString(fmt.Sprintf("Content:\n%s\n", content))
			}
		}

		builder.WriteString("\n")
	}

	return builder.String()
}

// FormatExtractResults formats the extract results into a readable string
func (f *WebSearchResultFormatter) FormatExtractResults(resp *ExtractResponsePayload) string {
	var builder strings.Builder

	builder.WriteString(fmt.Sprintf("📄 **Extracted content from %d URL(s):**\n\n", resp.RequestDetails.URLsProcessed))

	for i, result := range resp.Results {
		builder.WriteString(fmt.Sprintf("--- URL %d ---\n", i+1))
		builder.WriteString(fmt.Sprintf("URL: %s\n", result.URL))
		builder.WriteString(fmt.Sprintf("Source Type: %s\n", result.SourceType))

		if !result.ProcessedSuccessfully {
			if result.Error != nil {
				builder.WriteString(fmt.Sprintf("Error: %s\n", *result.Error))
			} else {
				builder.WriteString("Error: Processing failed\n")
			}
			builder.WriteString("\n")
			continue
		}

		// Format content based on source type
		if result.Data != nil {
			content := f.ExtractContentFromData(result.Data, result.SourceType)
			if content != "" {
				builder.WriteString(fmt.Sprintf("Content:\n%s\n", content))
			}
		}

		builder.WriteString("\n")
	}

	return builder.String()
}

// ExtractContentFromData extracts readable content from the data field based on source type
func (f *WebSearchResultFormatter) ExtractContentFromData(data interface{}, sourceType string) string {
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return fmt.Sprintf("%v", data)
	}

	switch sourceType {
	case "youtube":
		return f.formatYouTubeData(dataMap)
	case "reddit":
		return f.formatRedditData(dataMap)
	case "pdf":
		return f.formatPDFData(dataMap)
	case "webpage", "webpage_js":
		return f.formatWebpageData(dataMap)
	case "twitter":
		return f.formatTwitterData(dataMap)
	case "twitter_profile":
		return f.formatTwitterProfileData(dataMap)
	default:
		return f.formatGenericData(dataMap)
	}
}

// formatYouTubeData formats YouTube video data according to API documentation
func (f *WebSearchResultFormatter) formatYouTubeData(data map[string]interface{}) string {
	var parts []string

	if title, ok := data["title"].(string); ok && title != "" {
		parts = append(parts, fmt.Sprintf("Title: %s", title))
	}

	if channel, ok := data["channel_name"].(string); ok && channel != "" {
		parts = append(parts, fmt.Sprintf("Channel: %s", channel))
	}

	if transcript, ok := data["transcript"].(string); ok && transcript != "" {
		parts = append(parts, fmt.Sprintf("Transcript: %s", transcript))
	}

	// Handle structured comments array as per API docs
	if comments, ok := data["comments"].([]interface{}); ok && len(comments) > 0 {
		parts = append(parts, "Comments:")
		for _, comment := range comments {
			if commentMap, ok := comment.(map[string]interface{}); ok {
				parts = append(parts, f.formatYouTubeComment(commentMap, 1))
			}
		}
	}

	return strings.Join(parts, "\n")
}

// formatRedditData formats Reddit post data according to API documentation
func (f *WebSearchResultFormatter) formatRedditData(data map[string]interface{}) string {
	var parts []string

	if title, ok := data["post_title"].(string); ok && title != "" {
		parts = append(parts, fmt.Sprintf("Post Title: %s", title))
	}

	if body, ok := data["post_body"].(string); ok && body != "" {
		parts = append(parts, fmt.Sprintf("Post Body: %s", body))
	}

	if author, ok := data["author"].(string); ok && author != "" {
		parts = append(parts, fmt.Sprintf("Author: %s", author))
	}

	if score, ok := data["score"].(float64); ok {
		parts = append(parts, fmt.Sprintf("Score: %.0f", score))
	}

	// Handle posts from subreddit/user pages
	if posts, ok := data["posts"].([]interface{}); ok && len(posts) > 0 {
		parts = append(parts, "Posts:")
		for i, post := range posts {
			if postMap, ok := post.(map[string]interface{}); ok {
				var postParts []string
				if postTitle, exists := postMap["title"].(string); exists {
					postParts = append(postParts, fmt.Sprintf("%d. %s", i+1, postTitle))
				}
				if postAuthor, exists := postMap["author"].(string); exists {
					postParts = append(postParts, fmt.Sprintf("(u/%s)", postAuthor))
				}
				if postScore, exists := postMap["score"].(float64); exists {
					postParts = append(postParts, fmt.Sprintf("- %.0f points", postScore))
				}
				if len(postParts) > 0 {
					parts = append(parts, fmt.Sprintf("  - %s", strings.Join(postParts, " ")))
				}
			}
		}
	}

	// Handle structured comments array as per API docs
	if comments, ok := data["comments"].([]interface{}); ok && len(comments) > 0 {
		parts = append(parts, "Comments:")
		for _, comment := range comments {
			if commentStr, ok := comment.(string); ok && commentStr != "" {
				// Filter out Reddit pagination objects that show as "... and X more comments"
				if !strings.Contains(commentStr, "... and") || !strings.Contains(commentStr, "more comments") {
					parts = append(parts, fmt.Sprintf("  - %s", commentStr))
				}
			} else if commentMap, ok := comment.(map[string]interface{}); ok {
				var commentParts []string
				if author, exists := commentMap["author"].(string); exists && author != "" {
					commentParts = append(commentParts, fmt.Sprintf("u/%s", author))
				}
				if score, exists := commentMap["score"].(float64); exists {
					commentParts = append(commentParts, fmt.Sprintf("(%.0f)", score))
				}
				if text, exists := commentMap["text"].(string); exists && text != "" {
					// Filter out pagination objects
					if !strings.Contains(text, "... and") || !strings.Contains(text, "more comments") {
						commentParts = append(commentParts, text)
					}
				}
				if len(commentParts) > 0 {
					parts = append(parts, fmt.Sprintf("  - %s", strings.Join(commentParts, " ")))
				}

				// Handle nested replies if present
				if replies, exists := commentMap["replies"].([]interface{}); exists && len(replies) > 0 {
					for j, reply := range replies {
						// Remove reply limit - include all replies
						_ = j // Keep variable to avoid unused variable error
						if replyMap, ok := reply.(map[string]interface{}); ok {
							if replyText, exists := replyMap["text"].(string); exists && replyText != "" {
								if replyAuthor, exists := replyMap["author"].(string); exists && replyAuthor != "" {
									parts = append(parts, fmt.Sprintf("    ↳ u/%s: %s", replyAuthor, replyText))
								} else {
									parts = append(parts, fmt.Sprintf("    ↳ %s", replyText))
								}
							}
						}
					}
				}
			}
		}
	}

	return strings.Join(parts, "\n")
}

// formatPDFData formats PDF document data
func (f *WebSearchResultFormatter) formatPDFData(data map[string]interface{}) string {
	if textContent, ok := data["text_content"].(string); ok {
		return textContent
	}
	return ""
}

// formatWebpageData formats webpage data
func (f *WebSearchResultFormatter) formatWebpageData(data map[string]interface{}) string {
	var parts []string

	if title, ok := data["title"].(string); ok && title != "" {
		parts = append(parts, fmt.Sprintf("Title: %s", title))
	}

	if textContent, ok := data["text_content"].(string); ok && textContent != "" {
		parts = append(parts, textContent)
	}

	return strings.Join(parts, "\n")
}

// formatSingleTweetData formats a single Twitter/X post, making it reusable
func (f *WebSearchResultFormatter) formatSingleTweetData(data map[string]interface{}) string {
	var parts []string

	if tweetContent, ok := data["tweet_content"].(string); ok && tweetContent != "" {
		parts = append(parts, fmt.Sprintf("Tweet: %s", tweetContent))
	}

	if tweetAuthor, ok := data["tweet_author"].(string); ok && tweetAuthor != "" {
		parts = append(parts, fmt.Sprintf("Author: %s", tweetAuthor))
	}

	if totalComments, ok := data["total_comments"].(float64); ok {
		parts = append(parts, fmt.Sprintf("Total Comments: %.0f", totalComments))
	}

	// Handle structured comments array as per API docs
	if comments, ok := data["comments"].([]interface{}); ok && len(comments) > 0 {
		parts = append(parts, "Comments:")
		for _, comment := range comments {
			if commentMap, ok := comment.(map[string]interface{}); ok {
				var commentParts []string
				if author, exists := commentMap["author"].(string); exists && author != "" {
					commentParts = append(commentParts, fmt.Sprintf("@%s", author))
				}
				if username, exists := commentMap["username"].(string); exists && username != "" {
					commentParts = append(commentParts, fmt.Sprintf("(%s)", username))
				}
				if content, exists := commentMap["content"].(string); exists && content != "" {
					commentParts = append(commentParts, content)
				}
				if timestamp, exists := commentMap["timestamp"].(string); exists && timestamp != "" {
					commentParts = append(commentParts, fmt.Sprintf("[%s]", timestamp))
				}
				if likes, exists := commentMap["likes"].(string); exists && likes != "" {
					commentParts = append(commentParts, fmt.Sprintf("♥ %s", likes))
				}
				if replies, exists := commentMap["replies"].(string); exists && replies != "" {
					commentParts = append(commentParts, fmt.Sprintf("↩ %s", replies))
				}
				if retweets, exists := commentMap["retweets"].(string); exists && retweets != "" {
					commentParts = append(commentParts, fmt.Sprintf("🔄 %s", retweets))
				}
				if len(commentParts) > 0 {
					parts = append(parts, fmt.Sprintf("  - %s", strings.Join(commentParts, " ")))
				}
			}
		}
	}

	return strings.Join(parts, "\n")
}

// formatTwitterData formats Twitter/X data according to API documentation
func (f *WebSearchResultFormatter) formatTwitterData(data map[string]interface{}) string {
	return f.formatSingleTweetData(data)
}

// formatTwitterProfileData formats Twitter/X profile data
func (f *WebSearchResultFormatter) formatTwitterProfileData(data map[string]interface{}) string {
	var parts []string

	if profileURL, ok := data["profile_url"].(string); ok && profileURL != "" {
		parts = append(parts, fmt.Sprintf("Profile: %s", profileURL))
	}

	if tweets, ok := data["latest_tweets"].([]interface{}); ok && len(tweets) > 0 {
		parts = append(parts, "\nLatest Tweets:")
		for i, tweet := range tweets {
			if tweetMap, ok := tweet.(map[string]interface{}); ok {
				parts = append(parts, fmt.Sprintf("\n--- Tweet %d ---", i+1))
				if tweetURL, ok := tweetMap["url"].(string); ok && tweetURL != "" {
					parts = append(parts, fmt.Sprintf("URL: %s", tweetURL))
				}
				if tweetData, ok := tweetMap["data"].(map[string]interface{}); ok {
					parts = append(parts, f.formatSingleTweetData(tweetData))
				}
			}
		}
	}

	return strings.Join(parts, "\n")
}

// formatGenericData formats generic data
func (f *WebSearchResultFormatter) formatGenericData(data map[string]interface{}) string {
	var parts []string
	for key, value := range data {
		if str, ok := value.(string); ok && str != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", key, str))
		}
	}
	return strings.Join(parts, "\n")
}

// formatYouTubeComment formats a single YouTube comment and its replies recursively
func (f *WebSearchResultFormatter) formatYouTubeComment(comment map[string]interface{}, depth int) string {
	var builder strings.Builder
	indent := strings.Repeat("    ", depth-1)

	var commentParts []string
	if author, ok := comment["author"].(string); ok && author != "" {
		commentParts = append(commentParts, fmt.Sprintf("@%s", author))
	}
	if text, ok := comment["text"].(string); ok && text != "" {
		commentParts = append(commentParts, text)
	}
	if likes, ok := comment["likes"].(float64); ok && likes > 0 {
		commentParts = append(commentParts, fmt.Sprintf("(%d likes)", int(likes)))
	}

	builder.WriteString(fmt.Sprintf("%s- %s\n", indent, strings.Join(commentParts, " ")))

	if replies, ok := comment["replies"].([]interface{}); ok && len(replies) > 0 {
		for _, reply := range replies {
			if replyMap, ok := reply.(map[string]interface{}); ok {
				builder.WriteString(f.formatYouTubeComment(replyMap, depth+1))
			}
		}
	}

	return builder.String()
}