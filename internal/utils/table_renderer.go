package utils

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"io"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
)

// TableRenderer handles converting markdown tables to images
type TableRenderer struct {
	initialized   bool
	mu            sync.Mutex // protects initialization
	fontFace      font.Face
	cellPadding   int
	borderWidth   int
	headerHeight  int
	rowHeight     int
	minColWidth   int
	useRod        bool // whether to use Rod for rendering
	rodConverter  *RodTableConverter
}

// NewTableRenderer creates a new table renderer instance
func NewTableRenderer() *TableRenderer {
	return &TableRenderer{
		fontFace:     basicfont.Face7x13,
		cellPadding:  12,
		borderWidth:  1,
		headerHeight: 40,
		rowHeight:    35,
		minColWidth:  80,
		useRod:       false, // default to gg graphics
	}
}

// NewTableRendererWithRod creates a new table renderer instance that uses Rod
func NewTableRendererWithRod() *TableRenderer {
	return &TableRenderer{
		fontFace:     basicfont.Face7x13,
		cellPadding:  12,
		borderWidth:  1,
		headerHeight: 40,
		rowHeight:    35,
		minColWidth:  80,
		useRod:       true,
		rodConverter: NewRodTableConverter(),
	}
}

// NewTableRendererWithRodConfig creates a new table renderer instance that uses Rod with specific configuration
func NewTableRendererWithRodConfig(timeout, quality int) *TableRenderer {
	return &TableRenderer{
		fontFace:     basicfont.Face7x13,
		cellPadding:  12,
		borderWidth:  1,
		headerHeight: 40,
		rowHeight:    35,
		minColWidth:  80,
		useRod:       true,
		rodConverter: NewRodTableConverterWithConfig(timeout, quality),
	}
}

// Initialize sets up the table renderer
func (tr *TableRenderer) Initialize() error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	if tr.initialized {
		return nil
	}

	// Initialize Rod converter if using Rod
	if tr.useRod && tr.rodConverter != nil {
		err := tr.rodConverter.Initialize()
		if err != nil {
			return fmt.Errorf("failed to initialize Rod converter: %w", err)
		}
	}

	tr.initialized = true
	return nil
}

// Close cleans up resources
func (tr *TableRenderer) Close() error {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	// Close Rod converter if using Rod
	if tr.useRod && tr.rodConverter != nil {
		err := tr.rodConverter.Close()
		if err != nil {
			log.Printf("Failed to close Rod converter: %v", err)
		}
	}

	tr.initialized = false
	return nil
}

// MarkdownTable represents a detected markdown table
type MarkdownTable struct {
	Content  string
	StartPos int
	EndPos   int
	RowCount int
	ColCount int
}

// TableImage represents a rendered table image
type TableImage struct {
	Data        []byte
	Filename    string
	ContentType string
	Width       int
	Height      int
}

// DetectTables finds markdown tables in text content
func (tr *TableRenderer) DetectTables(content string) []MarkdownTable {
	var tables []MarkdownTable

	// Regex to match markdown tables
	// Look for lines that contain pipe characters with headers and separator
	tableRegex := regexp.MustCompile(`(?m)^(\|[^\n]+\|\s*\n\|[\s\-\|:]+\|\s*\n(?:\|[^\n]+\|\s*\n?)+)`)

	matches := tableRegex.FindAllStringIndex(content, -1)
	for _, match := range matches {
		tableContent := content[match[0]:match[1]]

		// Count rows and columns
		lines := strings.Split(strings.TrimSpace(tableContent), "\n")
		rowCount := 0
		colCount := 0

		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}

			// Skip separator line (usually the second line)
			if i == 1 && strings.Contains(line, "---") {
				continue
			}

			rowCount++
			if colCount == 0 {
				// Count columns from first data row
				colCount = strings.Count(line, "|") - 1
				if colCount <= 0 {
					colCount = 1
				}
			}
		}

		// Only consider it a table if it has at least 2 rows and 2 columns
		if rowCount >= 2 && colCount >= 2 {
			tables = append(tables, MarkdownTable{
				Content:  tableContent,
				StartPos: match[0],
				EndPos:   match[1],
				RowCount: rowCount,
				ColCount: colCount,
			})
		}
	}

	return tables
}

// RenderTableToImage converts a markdown table to an image
func (tr *TableRenderer) RenderTableToImage(ctx context.Context, table MarkdownTable) (*TableImage, error) {
	// Ensure renderer is initialized
	if !tr.initialized {
		if err := tr.Initialize(); err != nil {
			return nil, err
		}
	}

	// Parse the markdown table
	tableData, err := tr.parseMarkdownTable(table.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse table: %w", err)
	}

	// Calculate dimensions
	width, height := tr.calculateTableDimensions(tableData)

	// Create image context
	dc := gg.NewContext(width, height)

	// Set background color
	dc.SetRGB(1, 1, 1) // white background
	dc.Clear()

	// Draw the table
	err = tr.drawTable(dc, tableData)
	if err != nil {
		return nil, fmt.Errorf("failed to draw table: %w", err)
	}

	// Convert to PNG bytes
	var buf bytes.Buffer
	err = png.Encode(&buf, dc.Image())
	if err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}

	filename := fmt.Sprintf("table_%d.png", time.Now().Unix())

	return &TableImage{
		Data:        buf.Bytes(),
		Filename:    filename,
		ContentType: "image/png",
		Width:       width,
		Height:      height,
	}, nil
}

// TableData represents parsed table data
type TableData struct {
	Headers []string
	Rows    [][]string
}

// parseMarkdownTable parses markdown table syntax into structured data
func (tr *TableRenderer) parseMarkdownTable(markdown string) (*TableData, error) {
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

// calculateTableDimensions calculates the required image dimensions
func (tr *TableRenderer) calculateTableDimensions(tableData *TableData) (int, int) {
	// Calculate column widths based on content
	colWidths := make([]int, len(tableData.Headers))

	// Check header widths
	for i, header := range tableData.Headers {
		width := tr.getTextWidth(header)
		if width > colWidths[i] {
			colWidths[i] = width
		}
	}

	// Check row data widths
	for _, row := range tableData.Rows {
		for i, cell := range row {
			if i < len(colWidths) {
				width := tr.getTextWidth(cell)
				if width > colWidths[i] {
					colWidths[i] = width
				}
			}
		}
	}

	// Apply minimum column width and padding
	totalWidth := 0
	for i := range colWidths {
		if colWidths[i] < tr.minColWidth {
			colWidths[i] = tr.minColWidth
		}
		colWidths[i] += tr.cellPadding * 2
		totalWidth += colWidths[i]
	}

	// Add border widths
	totalWidth += (len(colWidths) + 1) * tr.borderWidth

	// Calculate height
	totalHeight := tr.headerHeight + (len(tableData.Rows) * tr.rowHeight)
	totalHeight += (len(tableData.Rows) + 2) * tr.borderWidth // +2 for header and bottom border

	return totalWidth, totalHeight
}

// getTextWidth estimates text width in pixels
func (tr *TableRenderer) getTextWidth(text string) int {
	// Simple estimation: 7 pixels per character (based on basicfont.Face7x13)
	return len(text) * 7
}

// drawTable draws the table onto the graphics context
func (tr *TableRenderer) drawTable(dc *gg.Context, tableData *TableData) error {
	// Calculate column widths
	colWidths := make([]int, len(tableData.Headers))
	for i, header := range tableData.Headers {
		width := tr.getTextWidth(header)
		for _, row := range tableData.Rows {
			if i < len(row) {
				cellWidth := tr.getTextWidth(row[i])
				if cellWidth > width {
					width = cellWidth
				}
			}
		}
		if width < tr.minColWidth {
			width = tr.minColWidth
		}
		colWidths[i] = width + tr.cellPadding*2
	}

	// Draw borders and background
	dc.SetRGB(0.88, 0.89, 0.9) // Light gray for borders
	dc.SetLineWidth(float64(tr.borderWidth))

	// Draw header background
	dc.SetRGB(0.97, 0.98, 0.99) // Very light gray for header
	dc.DrawRectangle(0, 0, float64(dc.Width()), float64(tr.headerHeight))
	dc.Fill()

	// Draw alternating row backgrounds
	for i := 0; i < len(tableData.Rows); i++ {
		if i%2 == 0 {
			dc.SetRGB(0.97, 0.98, 0.99) // Light gray for even rows
		} else {
			dc.SetRGB(1, 1, 1) // White for odd rows
		}
		y := float64(tr.headerHeight + i*tr.rowHeight)
		dc.DrawRectangle(0, y, float64(dc.Width()), float64(tr.rowHeight))
		dc.Fill()
	}

	// Draw grid lines
	dc.SetRGB(0.88, 0.89, 0.9) // Border color
	dc.SetLineWidth(float64(tr.borderWidth))

	// Vertical lines
	x := 0
	for i := 0; i <= len(colWidths); i++ {
		dc.DrawLine(float64(x), 0, float64(x), float64(dc.Height()))
		dc.Stroke()
		if i < len(colWidths) {
			x += colWidths[i]
		}
	}

	// Horizontal lines
	dc.DrawLine(0, 0, float64(dc.Width()), 0) // Top
	dc.Stroke()
	dc.DrawLine(0, float64(tr.headerHeight), float64(dc.Width()), float64(tr.headerHeight)) // Header separator
	dc.Stroke()
	for i := 0; i <= len(tableData.Rows); i++ {
		y := float64(tr.headerHeight + i*tr.rowHeight)
		dc.DrawLine(0, y, float64(dc.Width()), y)
		dc.Stroke()
	}

	// Draw text
	dc.SetRGB(0.14, 0.16, 0.19) // Dark gray for text
	dc.SetFontFace(tr.fontFace)

	// Draw headers
	x = 0
	for i, header := range tableData.Headers {
		textX := float64(x + tr.cellPadding)
		textY := float64(tr.headerHeight/2 + 6) // Center vertically
		dc.DrawString(header, textX, textY)
		x += colWidths[i]
	}

	// Draw rows
	for rowIdx, row := range tableData.Rows {
		x = 0
		for colIdx, cell := range row {
			if colIdx < len(colWidths) {
				textX := float64(x + tr.cellPadding)
				textY := float64(tr.headerHeight + rowIdx*tr.rowHeight + tr.rowHeight/2 + 6)
				dc.DrawString(cell, textX, textY)
				x += colWidths[colIdx]
			}
		}
	}

	return nil
}

// ProcessResponse processes LLM response content and replaces tables with images
func (tr *TableRenderer) ProcessResponse(ctx context.Context, content string) (string, []TableImage, error) {
	// Ensure renderer is initialized
	if !tr.initialized {
		if err := tr.Initialize(); err != nil {
			return content, nil, fmt.Errorf("failed to initialize table renderer: %w", err)
		}
	}

	// If using Rod, delegate to Rod converter
	if tr.useRod && tr.rodConverter != nil {
		return tr.rodConverter.ProcessResponseWithRod(ctx, content)
	}

	// Otherwise use the original gg graphics implementation
	tables := tr.DetectTables(content)
	if len(tables) == 0 {
		return content, nil, nil
	}

	var tableImages []TableImage
	processedContent := content

	// Process tables in reverse order to maintain string positions
	for i := len(tables) - 1; i >= 0; i-- {
		table := tables[i]

		// Render table to image
		tableImage, err := tr.RenderTableToImage(ctx, table)
		if err != nil {
			log.Printf("Failed to render table to image: %v", err)
			continue
		}

		tableImages = append([]TableImage{*tableImage}, tableImages...) // Prepend to maintain order

		// Replace table markdown with a placeholder or remove it
		before := processedContent[:table.StartPos]
		after := processedContent[table.EndPos:]
		replacement := fmt.Sprintf("\n*[Table converted to image: %s]*\n", tableImage.Filename)

		processedContent = before + replacement + after
	}

	return processedContent, tableImages, nil
}

// ConvertToDiscordAttachment converts TableImage to Discord attachment format
func (tr *TableRenderer) ConvertToDiscordAttachment(tableImage TableImage) (*DiscordFile, error) {
	return &DiscordFile{
		Name:   tableImage.Filename,
		Reader: bytes.NewReader(tableImage.Data),
	}, nil
}

// DiscordFile represents a file attachment for Discord
type DiscordFile struct {
	Name   string
	Reader io.Reader
}
