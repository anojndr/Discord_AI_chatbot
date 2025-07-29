package utils

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

var builderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
}

// RodTableConverter handles converting markdown tables to images using Rod browser automation
type RodTableConverter struct {
	browser *rod.Browser
	page    *rod.Page
	timeout int // timeout in seconds
	quality int // PNG quality (0-100)
}

// NewRodTableConverter creates a new Rod-based table converter with default settings
func NewRodTableConverter() *RodTableConverter {
	return &RodTableConverter{
		timeout: 10, // default timeout
		quality: 90, // default quality
	}
}

// NewRodTableConverterWithConfig creates a new Rod-based table converter with configuration
func NewRodTableConverterWithConfig(timeout, quality int) *RodTableConverter {
	return &RodTableConverter{
		timeout: timeout,
		quality: quality,
	}
}

// Initialize sets up the Rod browser instance
func (rtc *RodTableConverter) Initialize() error {
	// Launch browser in headless mode
	l := launcher.New().
		Headless(true).
		NoSandbox(true).
		Devtools(false)

	url, err := l.Launch()
	if err != nil {
		return fmt.Errorf("failed to launch browser: %w", err)
	}

	// Connect to browser
	rtc.browser = rod.New().ControlURL(url)
	err = rtc.browser.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to browser: %w", err)
	}

	// Create a new page
	rtc.page, err = rtc.browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return fmt.Errorf("failed to create page: %w", err)
	}

	return nil
}

// Close cleans up browser resources
func (rtc *RodTableConverter) Close() error {
	var err error
	if rtc.page != nil {
		if closeErr := rtc.page.Close(); closeErr != nil {
			err = closeErr
		}
	}
	if rtc.browser != nil {
		if closeErr := rtc.browser.Close(); closeErr != nil {
			err = closeErr
		}
	}
	return err
}

// ConvertMarkdownTableToImage converts a markdown table to a PNG image using Rod
func (rtc *RodTableConverter) ConvertMarkdownTableToImage(ctx context.Context, markdownTable string) (*TableImage, error) {
	if rtc.browser == nil || rtc.page == nil {
		return nil, fmt.Errorf("converter not initialized")
	}

	// Create a context with timeout based on configuration
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(rtc.timeout)*time.Second)
	defer cancel()

	// Parse the markdown table to extract data
	tableData, err := rtc.parseMarkdownTable(markdownTable)
	if err != nil {
		return nil, fmt.Errorf("failed to parse markdown table: %w", err)
	}

	// Generate HTML content for the table
	htmlContent := rtc.generateTableHTML(tableData)

	// Set page content
	err = rtc.page.Context(timeoutCtx).SetDocumentContent(htmlContent)
	if err != nil {
		return nil, fmt.Errorf("failed to set page content: %w", err)
	}

	// Wait for page to render
	err = rtc.page.Context(timeoutCtx).WaitLoad()
	if err != nil {
		// Continue anyway as this might not be critical
		log.Printf("Warning: WaitLoad failed: %v", err)
	}

	// Find the table element
	tableElement, err := rtc.page.Context(timeoutCtx).Element("table")
	if err != nil {
		return nil, fmt.Errorf("failed to find table element: %w", err)
	}

	// Take a screenshot of the table element
	screenshotBytes, err := tableElement.Screenshot(proto.PageCaptureScreenshotFormatPng, rtc.quality)
	if err != nil {
		return nil, fmt.Errorf("failed to take screenshot: %w", err)
	}

	filename := fmt.Sprintf("table_%d.png", time.Now().Unix())

	return &TableImage{
		Data:        screenshotBytes,
		Filename:    filename,
		ContentType: "image/png",
		Width:       0, // Rod doesn't provide dimensions directly
		Height:      0,
	}, nil
}

// parseMarkdownTable parses markdown table syntax into structured data
func (rtc *RodTableConverter) parseMarkdownTable(markdown string) (*TableData, error) {
	lines := strings.Split(strings.TrimSpace(markdown), "\n")
	if len(lines) < 3 {
		return nil, fmt.Errorf("invalid table format: need at least 3 lines")
	}

	// Parse header row
	headerLine := strings.Trim(lines[0], "|")
	headers := make([]string, 0)
	for _, cell := range strings.Split(headerLine, "|") {
		headers = append(headers, strings.TrimSpace(cell))
	}

	// Parse data rows (skip header and separator)
	var rows [][]string
	for i := 2; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		line = strings.Trim(line, "|")
		row := make([]string, 0)
		for _, cell := range strings.Split(line, "|") {
			row = append(row, strings.TrimSpace(cell))
		}
		rows = append(rows, row)
	}

	return &TableData{
		Headers: headers,
		Rows:    rows,
	}, nil
}

// generateTableHTML creates an HTML representation of the table with CSS styling
func (rtc *RodTableConverter) generateTableHTML(tableData *TableData) string {
	htmlBuilder := builderPool.Get().(*strings.Builder)
	defer func() {
		htmlBuilder.Reset()
		builderPool.Put(htmlBuilder)
	}()

	// Start HTML document with dark mode CSS styling
	htmlBuilder.WriteString(`<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
            margin: 20px;
            background-color: #2b2d31;
            color: #dbdee1;
        }
        table {
            border-collapse: collapse;
            margin: 0;
            border: 1px solid #4e5058;
            background-color: #313338;
            border-radius: 8px;
            overflow: hidden;
        }
        th, td {
            border: 1px solid #4e5058;
            padding: 12px 16px;
            text-align: left;
            vertical-align: top;
        }
        th {
            background-color: #404249;
            font-weight: 600;
            color: #f2f3f5;
            border-bottom: 2px solid #5865f2;
        }
        tbody tr:nth-child(even) {
            background-color: #383a40;
        }
        tbody tr:nth-child(odd) {
            background-color: #313338;
        }
        tbody tr:hover {
            background-color: #404249;
        }
        td {
            color: #dbdee1;
        }
    </style>
</head>
<body>
    <table>`)

	// Add header row
	if len(tableData.Headers) > 0 {
		htmlBuilder.WriteString("\n        <thead>\n            <tr>")
		for _, header := range tableData.Headers {
			_, _ = fmt.Fprintf(htmlBuilder, "\n                <th>%s</th>", rtc.escapeHTML(header))
		}
		htmlBuilder.WriteString("\n            </tr>\n        </thead>")
	}

	// Add data rows
	if len(tableData.Rows) > 0 {
		htmlBuilder.WriteString("\n        <tbody>")
		for _, row := range tableData.Rows {
			htmlBuilder.WriteString("\n            <tr>")
			for i, cell := range row {
				// Ensure we don't exceed the number of headers
				if i < len(tableData.Headers) {
					_, _ = fmt.Fprintf(htmlBuilder, "\n                <td>%s</td>", rtc.escapeHTML(cell))
				}
			}
			htmlBuilder.WriteString("\n            </tr>")
		}
		htmlBuilder.WriteString("\n        </tbody>")
	}

	// Close HTML
	htmlBuilder.WriteString(`
    </table>
</body>
</html>`)

	return htmlBuilder.String()
}

// escapeHTML escapes HTML special characters
func (rtc *RodTableConverter) escapeHTML(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	text = strings.ReplaceAll(text, "\"", "&quot;")
	text = strings.ReplaceAll(text, "'", "&#39;")
	return text
}

// ProcessResponseWithRod processes LLM response content and replaces tables with images using Rod
func (rtc *RodTableConverter) ProcessResponseWithRod(ctx context.Context, content string) (string, []TableImage, error) {
	// Use the existing table detection logic from TableRenderer
	tr := NewTableRenderer()
	tables := tr.DetectTables(content)

	if len(tables) == 0 {
		return content, nil, nil
	}

	var tableImages []TableImage
	processedContent := content

	// Process tables in reverse order to maintain string positions
	for i := len(tables) - 1; i >= 0; i-- {
		table := tables[i]

		// Convert table to image using Rod
		tableImage, err := rtc.ConvertMarkdownTableToImage(ctx, table.Content)
		if err != nil {
			log.Printf("Failed to convert table to image using Rod: %v", err)
			continue
		}

		tableImages = append([]TableImage{*tableImage}, tableImages...) // Prepend to maintain order

	}

	return processedContent, tableImages, nil
}

// ConvertToDiscordAttachment converts TableImage to Discord attachment format
func (rtc *RodTableConverter) ConvertToDiscordAttachment(tableImage TableImage) (*DiscordFile, error) {
	return &DiscordFile{
		Name:   tableImage.Filename,
		Reader: bytes.NewReader(tableImage.Data),
	}, nil
}
