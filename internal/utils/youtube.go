package utils

import (
	json "github.com/json-iterator/go"
	"fmt"
	"io"
	"net/http"
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
	// Regex for extracting playlist ID
	playlistIDRegex *regexp.Regexp
}

// NewYouTubeURLExtractor creates a new YouTube URL extractor with compiled regex patterns
func NewYouTubeURLExtractor() *YouTubeURLExtractor {
	// Comprehensive YouTube URL pattern that matches:
	// - youtube.com/watch?v=VIDEO_ID
	// - youtu.be/VIDEO_ID
	// - youtube.com/embed/VIDEO_ID
	// - youtube.com/v/VIDEO_ID
	// - m.youtube.com variants
	// - music.youtube.com variants
	// - youtube.com/playlist?list=PLAYLIST_ID
	// - youtube.com/watch?v=VIDEO_ID&list=PLAYLIST_ID
	youtubePattern := `(?i)(?:https?:\/\/)?(?:www\.|m\.|music\.)?(?:youtube\.com\/(?:watch\?v=|embed\/|v\/|playlist\?list=)|youtu\.be\/)([a-zA-Z0-9_-]+)(?:[&?][\w=&%-]*)?`

	// Pattern specifically for extracting video ID from various YouTube URL formats
	videoIDPattern := `(?:v=|v\/|embed\/|youtu\.be\/|\/v\/|watch\?v=)([a-zA-Z0-9_-]{11})`

	// Pattern for extracting playlist ID
	playlistIDPattern := `(?:list=)([a-zA-Z0-9_-]+)`

	return &YouTubeURLExtractor{
		youtubeRegex:    regexp.MustCompile(youtubePattern),
		videoIDRegex:    regexp.MustCompile(videoIDPattern),
		playlistIDRegex: regexp.MustCompile(playlistIDPattern),
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
		// Use the original match as it's more likely to be correct, especially for playlists
		normalizedURL := y.normalizeYouTubeURL(match)
		if normalizedURL != "" && !seen[normalizedURL] {
			if y.isValidYouTubeURL(normalizedURL) {
				urls = append(urls, normalizedURL)
				seen[normalizedURL] = true
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
		if len(videoID) == 11 {
			return videoID, true
		}
	}
	return "", false
}

// ExtractPlaylistID extracts the playlist ID from a YouTube URL
func (y *YouTubeURLExtractor) ExtractPlaylistID(youtubeURL string) (string, bool) {
	matches := y.playlistIDRegex.FindStringSubmatch(youtubeURL)
	if len(matches) >= 2 {
		playlistID := matches[1]
		// Playlist IDs can vary in length, but we can do a basic check
		if len(playlistID) > 11 { // Usually longer than video IDs
			return playlistID, true
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

	// If it's a playlist or music URL, return it as is to preserve all parameters
	if strings.Contains(input, "list=") || strings.Contains(input, "music.youtube.com") {
		return input
	}

	// For other URLs, extract video ID and create a standard URL
	videoID, found := y.ExtractVideoID(input)
	if !found {
		// Fallback for URLs that might not have a video ID but are valid (e.g., channel pages)
		// For now, we return empty if no video ID, to avoid processing non-video URLs
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
		"music.youtube.com",
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

	// A URL is valid if it has either a valid video ID or a valid playlist ID
	videoID, hasVideoID := y.ExtractVideoID(input)
	_, hasPlaylistID := y.ExtractPlaylistID(input)

	if !hasVideoID && !hasPlaylistID {
		return false
	}

	if hasVideoID {
		// YouTube video IDs are exactly 11 characters and contain only alphanumeric, underscore, and hyphen
		if len(videoID) != 11 {
			return false
		}
		validChars := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
		if !validChars.MatchString(videoID) {
			return false
		}
	}

	// No specific validation for playlist ID format for now, as they can vary.
	// The presence of a playlist ID is considered valid enough for this check.

	return true
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
// GetPlaylistVideoURLs is a convenience function to extract video URLs from a playlist URL
func GetPlaylistVideoURLs(playlistURL, apiKey string) ([]string, error) {
	extractor := NewYouTubeURLExtractor()
	playlistID, ok := extractor.ExtractPlaylistID(playlistURL)
	if !ok {
		return nil, fmt.Errorf("could not extract playlist ID from URL: %s", playlistURL)
	}
	return extractor.GetVideoURLsFromPlaylist(playlistID, apiKey)
}

// GetVideoURLsFromPlaylist fetches all video URLs from a given playlist ID.
func (y *YouTubeURLExtractor) GetVideoURLsFromPlaylist(playlistID, apiKey string) ([]string, error) {
	var videoURLs []string
	nextPageToken := ""

	for {
		apiURL := fmt.Sprintf("https://www.googleapis.com/youtube/v3/playlistItems?part=snippet&maxResults=50&playlistId=%s&key=%s&pageToken=%s", playlistID, apiKey, nextPageToken)
		
		resp, err := http.Get(apiURL)
		if err != nil {
			return nil, fmt.Errorf("failed to make request to YouTube API: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("YouTube API returned non-200 status code: %d", resp.StatusCode)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		var playlistResponse struct {
			NextPageToken string `json:"nextPageToken"`
			Items         []struct {
				Snippet struct {
					ResourceID struct {
						VideoID string `json:"videoId"`
					} `json:"resourceId"`
				} `json:"snippet"`
			} `json:"items"`
		}

		if err := json.Unmarshal(body, &playlistResponse); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON response: %w", err)
		}

		for _, item := range playlistResponse.Items {
			if item.Snippet.ResourceID.VideoID != "" {
				videoURLs = append(videoURLs, "https://www.youtube.com/watch?v="+item.Snippet.ResourceID.VideoID)
			}
		}

		if playlistResponse.NextPageToken == "" {
			break
		}
		nextPageToken = playlistResponse.NextPageToken
	}

	return videoURLs, nil
}
