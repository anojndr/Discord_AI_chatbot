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
	builder := builderPool.Get().(*strings.Builder)
	defer func() {
		builder.Reset()
		builderPool.Put(builder)
	}()

	_, _ = fmt.Fprintf(builder, "Found %d results for query: %s\n\n",
		resp.QueryDetails.ActualResultsFound, resp.QueryDetails.Query)

	for i, result := range resp.Results {
		_, _ = fmt.Fprintf(builder, "--- Result %d ---\n", i+1)
		_, _ = fmt.Fprintf(builder, "URL: %s\n", result.URL)
		_, _ = fmt.Fprintf(builder, "Source Type: %s\n", result.SourceType)

		if !result.ProcessedSuccessfully {
			if result.Error != nil {
				_, _ = fmt.Fprintf(builder, "Error: %s\n", *result.Error)
			} else {
				_, _ = fmt.Fprint(builder, "Error: Processing failed\n")
			}
			builder.WriteString("\n")
			continue
		}

		// Format content based on source type
		if result.Data != nil {
			content := f.ExtractContentFromData(result.Data, result.SourceType)
			if content != "" {
				_, _ = fmt.Fprintf(builder, "Content:\n%s\n", content)
			}
		}

		builder.WriteString("\n")
	}

	return builder.String()
}

// FormatExtractResults formats the extract results into a readable string
func (f *WebSearchResultFormatter) FormatExtractResults(resp *ExtractResponsePayload) string {
	builder := builderPool.Get().(*strings.Builder)
	defer func() {
		builder.Reset()
		builderPool.Put(builder)
	}()

	_, _ = fmt.Fprintf(builder, "📄 **Extracted content from %d URL(s):**\n\n", resp.RequestDetails.URLsProcessed)

	for i, result := range resp.Results {
		_, _ = fmt.Fprintf(builder, "--- URL %d ---\n", i+1)
		_, _ = fmt.Fprintf(builder, "URL: %s\n", result.URL)
		_, _ = fmt.Fprintf(builder, "Source Type: %s\n", result.SourceType)

		if !result.ProcessedSuccessfully {
			if result.Error != nil {
				_, _ = fmt.Fprintf(builder, "Error: %s\n", *result.Error)
			} else {
				_, _ = fmt.Fprint(builder, "Error: Processing failed\n")
			}
			builder.WriteString("\n")
			continue
		}

		// Format content based on source type
		if result.Data != nil {
			content := f.ExtractContentFromData(result.Data, result.SourceType)
			if content != "" {
				_, _ = fmt.Fprintf(builder, "Content:\n%s\n", content)
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
	builder := builderPool.Get().(*strings.Builder)
	defer func() {
		builder.Reset()
		builderPool.Put(builder)
	}()

	if title, ok := data["title"].(string); ok && title != "" {
		_, _ = fmt.Fprintf(builder, "Title: %s\n", title)
	}

	if channel, ok := data["channel_name"].(string); ok && channel != "" {
		_, _ = fmt.Fprintf(builder, "Channel: %s\n", channel)
	}

	if transcript, ok := data["transcript"].(string); ok && transcript != "" {
		_, _ = fmt.Fprintf(builder, "Transcript: %s\n", transcript)
	}

	// Handle structured comments array as per API docs
	if comments, ok := data["comments"].([]interface{}); ok && len(comments) > 0 {
		_, _ = fmt.Fprint(builder, "Comments:\n")
		for _, comment := range comments {
			if commentMap, ok := comment.(map[string]interface{}); ok {
				_, _ = fmt.Fprint(builder, f.formatYouTubeComment(commentMap, 1))
			}
		}
	}

	return builder.String()
}

// formatRedditData formats Reddit post data according to API documentation
func (f *WebSearchResultFormatter) formatRedditData(data map[string]interface{}) string {
	builder := builderPool.Get().(*strings.Builder)
	defer func() {
		builder.Reset()
		builderPool.Put(builder)
	}()

	if title, ok := data["post_title"].(string); ok && title != "" {
		_, _ = fmt.Fprintf(builder, "Post Title: %s\n", title)
	}

	if body, ok := data["post_body"].(string); ok && body != "" {
		_, _ = fmt.Fprintf(builder, "Post Body: %s\n", body)
	}

	if author, ok := data["author"].(string); ok && author != "" {
		_, _ = fmt.Fprintf(builder, "Author: %s\n", author)
	}

	if score, ok := data["score"].(float64); ok {
		_, _ = fmt.Fprintf(builder, "Score: %.0f\n", score)
	}

	// Handle posts from subreddit/user pages
	if posts, ok := data["posts"].([]interface{}); ok && len(posts) > 0 {
		_, _ = fmt.Fprint(builder, "Posts:\n")
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
					_, _ = fmt.Fprintf(builder, "  - %s\n", strings.Join(postParts, " "))
				}
			}
		}
	}

	// Handle structured comments array as per API docs
	if comments, ok := data["comments"].([]interface{}); ok && len(comments) > 0 {
		_, _ = fmt.Fprint(builder, "Comments:\n")
		for _, comment := range comments {
			if commentStr, ok := comment.(string); ok && commentStr != "" {
				// Filter out Reddit pagination objects that show as "... and X more comments"
				if !strings.Contains(commentStr, "... and") || !strings.Contains(commentStr, "more comments") {
					_, _ = fmt.Fprintf(builder, "  - %s\n", commentStr)
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
					_, _ = fmt.Fprintf(builder, "  - %s\n", strings.Join(commentParts, " "))
				}

				// Handle nested replies if present
				if replies, exists := commentMap["replies"].([]interface{}); exists && len(replies) > 0 {
					for j, reply := range replies {
						// Remove reply limit - include all replies
						_ = j // Keep variable to avoid unused variable error
						if replyMap, ok := reply.(map[string]interface{}); ok {
							if replyText, exists := replyMap["text"].(string); exists && replyText != "" {
								if replyAuthor, exists := replyMap["author"].(string); exists && replyAuthor != "" {
									_, _ = fmt.Fprintf(builder, "    ↳ u/%s: %s\n", replyAuthor, replyText)
								} else {
									_, _ = fmt.Fprintf(builder, "    ↳ %s\n", replyText)
								}
							}
						}
					}
				}
			}
		}
	}

	return builder.String()
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
	builder := builderPool.Get().(*strings.Builder)
	defer func() {
		builder.Reset()
		builderPool.Put(builder)
	}()

	if title, ok := data["title"].(string); ok && title != "" {
		_, _ = fmt.Fprintf(builder, "Title: %s\n", title)
	}

	if textContent, ok := data["text_content"].(string); ok && textContent != "" {
		_, _ = fmt.Fprint(builder, textContent)
	}

	return builder.String()
}

// formatSingleTweetData formats a single Twitter/X post, making it reusable
func (f *WebSearchResultFormatter) formatSingleTweetData(data map[string]interface{}) string {
	builder := builderPool.Get().(*strings.Builder)
	defer func() {
		builder.Reset()
		builderPool.Put(builder)
	}()

	if tweetContent, ok := data["tweet_content"].(string); ok && tweetContent != "" {
		_, _ = fmt.Fprintf(builder, "Tweet: %s\n", tweetContent)
	}

	if tweetAuthor, ok := data["tweet_author"].(string); ok && tweetAuthor != "" {
		_, _ = fmt.Fprintf(builder, "Author: %s\n", tweetAuthor)
	}

	if totalComments, ok := data["total_comments"].(float64); ok {
		_, _ = fmt.Fprintf(builder, "Total Comments: %.0f\n", totalComments)
	}

	// Handle structured comments array as per API docs
	if comments, ok := data["comments"].([]interface{}); ok && len(comments) > 0 {
		_, _ = fmt.Fprint(builder, "Comments:\n")
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
					_, _ = fmt.Fprintf(builder, "  - %s\n", strings.Join(commentParts, " "))
				}
			}
		}
	}

	return builder.String()
}

// formatTwitterData formats Twitter/X data according to API documentation
func (f *WebSearchResultFormatter) formatTwitterData(data map[string]interface{}) string {
	return f.formatSingleTweetData(data)
}

// formatTwitterProfileData formats Twitter/X profile data
func (f *WebSearchResultFormatter) formatTwitterProfileData(data map[string]interface{}) string {
	builder := builderPool.Get().(*strings.Builder)
	defer func() {
		builder.Reset()
		builderPool.Put(builder)
	}()

	if profileURL, ok := data["profile_url"].(string); ok && profileURL != "" {
		_, _ = fmt.Fprintf(builder, "Profile: %s\n", profileURL)
	}

	if tweets, ok := data["latest_tweets"].([]interface{}); ok && len(tweets) > 0 {
		_, _ = fmt.Fprint(builder, "\nLatest Tweets:\n")
		for i, tweet := range tweets {
			if tweetMap, ok := tweet.(map[string]interface{}); ok {
				_, _ = fmt.Fprintf(builder, "\n--- Tweet %d ---\n", i+1)
				if tweetURL, ok := tweetMap["url"].(string); ok && tweetURL != "" {
					_, _ = fmt.Fprintf(builder, "URL: %s\n", tweetURL)
				}
				if tweetData, ok := tweetMap["data"].(map[string]interface{}); ok {
					_, _ = fmt.Fprint(builder, f.formatSingleTweetData(tweetData))
				}
			}
		}
	}

	return builder.String()
}

// formatGenericData formats generic data
func (f *WebSearchResultFormatter) formatGenericData(data map[string]interface{}) string {
	builder := builderPool.Get().(*strings.Builder)
	defer func() {
		builder.Reset()
		builderPool.Put(builder)
	}()

	for key, value := range data {
		if str, ok := value.(string); ok && str != "" {
			_, _ = fmt.Fprintf(builder, "%s: %s\n", key, str)
		}
	}
	return builder.String()
}

// formatYouTubeComment formats a single YouTube comment and its replies recursively
func (f *WebSearchResultFormatter) formatYouTubeComment(comment map[string]interface{}, depth int) string {
	builder := builderPool.Get().(*strings.Builder)
	defer func() {
		builder.Reset()
		builderPool.Put(builder)
	}()
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

	_, _ = fmt.Fprintf(builder, "%s- %s\n", indent, strings.Join(commentParts, " "))

	if replies, ok := comment["replies"].([]interface{}); ok && len(replies) > 0 {
		for _, reply := range replies {
			if replyMap, ok := reply.(map[string]interface{}); ok {
				_, _ = fmt.Fprint(builder, f.formatYouTubeComment(replyMap, depth+1))
			}
		}
	}

	return builder.String()
}