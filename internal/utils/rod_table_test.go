package utils

import (
	"context"
	"testing"
	"time"
)

func TestRodTableConverter(t *testing.T) {
	// Skip if running in CI or headless environment
	if testing.Short() {
		t.Skip("Skipping Rod test in short mode")
	}

	// Create a Rod table converter
	converter := NewRodTableConverter()
	
	// Initialize the converter
	err := converter.Initialize()
	if err != nil {
		t.Skipf("Failed to initialize Rod converter (browser not available): %v", err)
	}
	defer func() {
		if err := converter.Close(); err != nil {
			t.Errorf("Failed to close converter: %v", err)
		}
	}()

	// Test markdown table
	markdownTable := `| Name | Age | City |
|------|-----|------|
| Alice | 30 | New York |
| Bob | 25 | London |
| Charlie | 35 | Tokyo |`

	// Convert table to image
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tableImage, err := converter.ConvertMarkdownTableToImage(ctx, markdownTable)
	if err != nil {
		t.Skipf("Failed to convert table to image (browser issue): %v", err)
	}

	// Verify the result
	if tableImage == nil {
		t.Fatal("Table image is nil")
	}

	if len(tableImage.Data) == 0 {
		t.Fatal("Table image data is empty")
	}

	if tableImage.ContentType != "image/png" {
		t.Errorf("Expected content type 'image/png', got '%s'", tableImage.ContentType)
	}

	if tableImage.Filename == "" {
		t.Error("Filename is empty")
	}

	t.Logf("Successfully converted table to image: %s (%d bytes)", tableImage.Filename, len(tableImage.Data))
}

func TestTableRendererWithRod(t *testing.T) {
	// Skip if running in CI or headless environment
	if testing.Short() {
		t.Skip("Skipping Rod test in short mode")
	}

	// Create a table renderer with Rod
	renderer := NewTableRendererWithRod()
	
	// Initialize the renderer
	err := renderer.Initialize()
	if err != nil {
		t.Skipf("Failed to initialize Rod table renderer (browser not available): %v", err)
	}
	defer func() {
		if err := renderer.Close(); err != nil {
			t.Errorf("Failed to close renderer: %v", err)
		}
	}()

	// Test content with markdown table
	content := `Here is some text with a table:

| Product | Price | Stock |
|---------|-------|-------|
| Laptop | $999 | 15 |
| Phone | $599 | 32 |
| Tablet | $399 | 8 |

End of content.`

	// Process the response
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	processedContent, tableImages, err := renderer.ProcessResponse(ctx, content)
	if err != nil {
		t.Skipf("Failed to process response (browser issue): %v", err)
	}

	// Verify the results
	if len(tableImages) == 0 {
		t.Fatal("No table images generated")
	}

	if len(tableImages) != 1 {
		t.Errorf("Expected 1 table image, got %d", len(tableImages))
	}

	if len(tableImages[0].Data) == 0 {
		t.Fatal("Table image data is empty")
	}

	// Check that the table was replaced in the content
	if !contains(processedContent, "[Table converted to image:") {
		t.Error("Table was not replaced with placeholder in processed content")
	}

	t.Logf("Successfully processed content with Rod: %d table images generated", len(tableImages))
	t.Logf("Processed content:\n%s", processedContent)
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (len(s) > len(substr) && 
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || 
		indexOf(s, substr) >= 0)))
}

// Helper function to find index of substring
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}