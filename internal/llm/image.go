package llm

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// downloadImageFromURL downloads an image from a URL and returns the image data and MIME type
func (c *LLMClient) downloadImageFromURL(ctx context.Context, imageURL string) ([]byte, string, error) {
	// Check if it's a data URL
	if strings.HasPrefix(imageURL, "data:") {
		return c.parseDataURL(imageURL)
	}

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set User-Agent to avoid blocking
	req.Header.Set("User-Agent", "DiscordAI-Bot/1.0")

	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to download image: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Printf("Failed to close response body: %v", closeErr)
		}
	}()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("failed to download image: HTTP %d", resp.StatusCode)
	}

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", fmt.Errorf("URL does not point to an image: %s", contentType)
	}

	// Read image data
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read image data: %w", err)
	}

	// Limit image size (10MB max)
	maxSize := 10 * 1024 * 1024 // 10MB
	if len(imageData) > maxSize {
		return nil, "", fmt.Errorf("image too large: %d bytes (max %d bytes)", len(imageData), maxSize)
	}

	return imageData, contentType, nil
}

// parseDataURL parses a data URL and returns the image data and MIME type
func (c *LLMClient) parseDataURL(dataURL string) ([]byte, string, error) {
	// Data URL format: data:[<mediatype>][;base64],<data>
	if !strings.HasPrefix(dataURL, "data:") {
		return nil, "", fmt.Errorf("not a data URL")
	}

	// Remove "data:" prefix
	dataURL = dataURL[5:]

	// Find the comma that separates metadata from data
	commaIndex := strings.Index(dataURL, ",")
	if commaIndex == -1 {
		return nil, "", fmt.Errorf("invalid data URL format: missing comma")
	}

	// Extract metadata and data parts
	metadata := dataURL[:commaIndex]
	data := dataURL[commaIndex+1:]

	// Parse metadata
	var mimeType string
	var isBase64 bool

	if metadata == "" {
		mimeType = "text/plain" // Default MIME type
	} else {
		parts := strings.Split(metadata, ";")
		mimeType = parts[0]

		// Check for base64 encoding
		for _, part := range parts[1:] {
			if part == "base64" {
				isBase64 = true
				break
			}
		}
	}

	// Ensure it's an image MIME type
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, "", fmt.Errorf("data URL does not contain an image: %s", mimeType)
	}

	// Decode the data
	var imageData []byte
	var err error

	if isBase64 {
		imageData, err = base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, "", fmt.Errorf("failed to decode base64 data: %w", err)
		}
	} else {
		// URL-encoded data (less common for images)
		imageData = []byte(data)
	}

	// Limit image size (10MB max)
	maxSize := 10 * 1024 * 1024 // 10MB
	if len(imageData) > maxSize {
		return nil, "", fmt.Errorf("image too large: %d bytes (max %d bytes)", len(imageData), maxSize)
	}

	return imageData, mimeType, nil
}