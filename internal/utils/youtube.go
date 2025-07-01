package utils

import (
	"net/url"
	"regexp"
	"strings"
)

// YouTubeURLExtractor provides methods for extracting and validating YouTube URLs
type YouTubeURLExtractor struct {
	// Comprehensive regex pattern for YouTube URLs
	youtubeRegex *regexp.Regexp
	// Regex for extracting video ID
	videoIDRegex *regexp.Regexp
}

// NewYouTubeURLExtractor creates a new YouTube URL extractor with compiled regex patterns
func NewYouTubeURLExtractor() *YouTubeURLExtractor {
	// Comprehensive YouTube URL pattern that matches:
	// - youtube.com/watch?v=VIDEO_ID
	// - youtu.be/VIDEO_ID
	// - youtube.com/embed/VIDEO_ID
	// - youtube.com/v/VIDEO_ID
	// - m.youtube.com variants
	// - youtube.com/watch?v=VIDEO_ID&other_params
	youtubePattern := `(?i)(?:https?:\/\/)?(?:www\.|m\.)?(?:youtube\.com\/(?:watch\?v=|embed\/|v\/)|youtu\.be\/)([a-zA-Z0-9_-]{11})(?:[&?][\w=&%-]*)?`

	// Pattern specifically for extracting video ID from various YouTube URL formats
	videoIDPattern := `(?:youtube\.com\/(?:watch\?v=|embed\/|v\/)|youtu\.be\/)([a-zA-Z0-9_-]{11})`

	return &YouTubeURLExtractor{
		youtubeRegex: regexp.MustCompile(youtubePattern),
		videoIDRegex: regexp.MustCompile(videoIDPattern),
	}
}

// ExtractYouTubeURLs extracts all YouTube URLs from the given text
func (y *YouTubeURLExtractor) ExtractYouTubeURLs(text string) []string {
	matches := y.youtubeRegex.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}

	var urls []string
	seen := make(map[string]bool)

	for _, match := range matches {
		cleanURL := y.normalizeYouTubeURL(match)
		if cleanURL != "" && !seen[cleanURL] {
			// Additional validation using net/url
			if y.isValidYouTubeURL(cleanURL) {
				urls = append(urls, cleanURL)
				seen[cleanURL] = true
			}
		}
	}

	if len(urls) == 0 {
		return nil
	}
	return urls
}

// ExtractVideoID extracts the video ID from a YouTube URL
func (y *YouTubeURLExtractor) ExtractVideoID(youtubeURL string) (string, bool) {
	matches := y.videoIDRegex.FindStringSubmatch(youtubeURL)
	if len(matches) >= 2 {
		videoID := matches[1]
		// YouTube video IDs are exactly 11 characters long
		if len(videoID) == 11 {
			return videoID, true
		}
	}
	return "", false
}

// IsYouTubeURL checks if a given URL is a valid YouTube URL
func (y *YouTubeURLExtractor) IsYouTubeURL(input string) bool {
	// First check with regex
	if !y.youtubeRegex.MatchString(input) {
		return false
	}

	// Additional validation
	return y.isValidYouTubeURL(input)
}

// normalizeYouTubeURL normalizes a YouTube URL to a standard format
func (y *YouTubeURLExtractor) normalizeYouTubeURL(input string) string {
	input = strings.TrimSpace(input)

	// Add https:// if missing
	if !strings.HasPrefix(strings.ToLower(input), "http://") && !strings.HasPrefix(strings.ToLower(input), "https://") {
		input = "https://" + input
	}

	// Extract video ID and create normalized URL
	videoID, found := y.ExtractVideoID(input)
	if !found {
		return ""
	}

	return "https://www.youtube.com/watch?v=" + videoID
}

// isValidYouTubeURL validates a YouTube URL using net/url package
func (y *YouTubeURLExtractor) isValidYouTubeURL(input string) bool {
	parsedURL, err := url.Parse(input)
	if err != nil {
		return false
	}

	// Check if it's a valid URL with proper scheme and host
	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return false
	}

	// Check if it's a YouTube domain
	host := strings.ToLower(parsedURL.Host)
	validHosts := []string{
		"youtube.com",
		"www.youtube.com",
		"m.youtube.com",
		"youtu.be",
		"www.youtu.be",
	}

	isValidHost := false
	for _, validHost := range validHosts {
		if host == validHost {
			isValidHost = true
			break
		}
	}

	if !isValidHost {
		return false
	}

	// Extract and validate video ID
	videoID, found := y.ExtractVideoID(input)
	if !found {
		return false
	}

	// YouTube video IDs are exactly 11 characters and contain only alphanumeric, underscore, and hyphen
	if len(videoID) != 11 {
		return false
	}

	validChars := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	return validChars.MatchString(videoID)
}

// DetectYouTubeURLs is a convenience function that creates an extractor and extracts URLs
func DetectYouTubeURLs(text string) []string {
	extractor := NewYouTubeURLExtractor()
	result := extractor.ExtractYouTubeURLs(text)
	if result == nil {
		return nil
	}
	return result
}

// GetVideoIDFromURL is a convenience function to extract video ID from a URL
func GetVideoIDFromURL(youtubeURL string) (string, bool) {
	extractor := NewYouTubeURLExtractor()
	return extractor.ExtractVideoID(youtubeURL)
}

// IsValidYouTubeURL is a convenience function to check if a URL is a valid YouTube URL
func IsValidYouTubeURL(input string) bool {
	extractor := NewYouTubeURLExtractor()
	return extractor.IsYouTubeURL(input)
}
